package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator/internal/config"
	"github.com/lancekrogers/agent-coordinator/internal/coordinator"
	"github.com/lancekrogers/agent-coordinator/internal/festival"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/hcs"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/hts"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/schedule"
	"github.com/lancekrogers/agent-coordinator/internal/hub"
	"github.com/lancekrogers/agent-coordinator/pkg/creclient"
	"github.com/lancekrogers/agent-coordinator/pkg/daemon"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if err := cfg.Coordinator.Validate(); err != nil {
		log.Error("invalid coordinator config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start WebSocket hub for dashboard connections.
	wsHub := hub.New(log)
	go wsHub.Run(ctx)

	wsPort := os.Getenv("WS_PORT")
	if wsPort == "" {
		wsPort = ":8080"
	} else if wsPort[0] != ':' {
		wsPort = ":" + wsPort
	}
	go func() {
		if err := hub.ListenAndServe(ctx, wsHub, wsPort, log); err != nil {
			log.Error("websocket server failed", "error", err)
		}
	}()

	// Connect to daemon runtime (optional — agent works standalone if unavailable).
	daemonClient := connectDaemon(ctx, log, cfg.CoordinatorAccountID.String())
	defer daemonClient.Close()

	// Initialize HCS publisher/subscriber and Hedera services.
	// In mock mode, use in-memory implementations and skip HTS/schedule.
	mockHCS := envBool("MOCK_HCS", false)

	var publisher hcs.MessagePublisher
	var subscriber hcs.MessageSubscriber
	var transferSvc *hts.TransferService

	if mockHCS {
		mockPub := hcs.NewMockPublisher()
		publisher = mockPub
		subscriber = hcs.NewMockSubscriber(mockPub)
		log.Info("HCS mock mode enabled — no Hedera connection")
	} else {
		hederaClient := hiero.ClientForTestnet()
		hederaClient.SetOperator(cfg.CoordinatorAccountID, cfg.CoordinatorKey)
		publisher = hcs.NewPublisher(hederaClient, hcs.DefaultPublishConfig())
		subscriber = hcs.NewSubscriber(hederaClient, hcs.DefaultSubscribeConfig())
		transferSvc = hts.NewTransferService(hederaClient)

		scheduleSvc := schedule.NewScheduleService(hederaClient)
		heartbeatCfg := schedule.DefaultHeartbeatConfig()
		heartbeatCfg.AgentID = "coordinator"
		heartbeatCfg.AccountID = cfg.CoordinatorAccountID

		heartbeat, err := schedule.NewHeartbeat(hederaClient, scheduleSvc, heartbeatCfg)
		if err != nil {
			log.Error("failed to create heartbeat runner", "error", err)
			os.Exit(1)
		}
		heartbeatErrs := heartbeat.Start(ctx)
		go func() {
			for err := range heartbeatErrs {
				log.Warn("schedule heartbeat error", "error", err)
			}
		}()
	}

	// Create coordinator components.
	inferenceAgentID := "inference-001"
	defiAgentID := "defi-001"
	agentIDs := []string{inferenceAgentID, defiAgentID}
	assigner := coordinator.NewAssigner(publisher, cfg.Coordinator.TaskTopicID, agentIDs)

	// Wire CRE Risk Router client (optional — skipped if CRE_ENDPOINT not set).
	if creEndpoint := os.Getenv("CRE_ENDPOINT"); creEndpoint != "" {
		creTimeout := 10 * time.Second
		creClient := creclient.New(creEndpoint, creTimeout)
		assigner.SetCREClient(creClient)
		log.Info("CRE Risk Router enabled", "endpoint", creEndpoint)
	} else {
		log.Warn("CRE Risk Router not configured, DeFi tasks will be denied (fail-closed)")
	}
	monitor := coordinator.NewMonitor(subscriber, cfg.Coordinator.StatusTopicID, nil)
	monitor.SetWSPublisher(hubAdapter{wsHub})
	payment := coordinator.NewPayment(transferSvc, publisher, cfg.Coordinator)

	// Agent ID → Hedera account ID for payments.
	agentAccounts := map[string]string{
		"inference-001": cfg.Agent1AccountID,
		"defi-001":      cfg.Agent2AccountID,
	}

	resultHandler := coordinator.NewResultHandler(coordinator.ResultHandlerConfig{
		Subscriber:    subscriber,
		TopicID:       cfg.Coordinator.StatusTopicID,
		Payment:       payment,
		Config:        cfg.Coordinator,
		Log:           log,
		AgentAccounts: agentAccounts,
	})

	// Start monitor, result handler, and daemon heartbeat in background.
	go func() {
		if err := monitor.Start(ctx); err != nil {
			log.Error("monitor stopped", "error", err)
		}
	}()
	go func() {
		if err := resultHandler.Start(ctx); err != nil {
			log.Error("result handler stopped", "error", err)
		}
	}()
	go daemonHeartbeatLoop(ctx, log, daemonClient)

	// Build runtime fest adapter and derive an execution plan.
	festReader := festival.NewReader(festival.ReaderConfig{
		RootDir:        os.Getenv("FEST_ROOT_DIR"),
		Selector:       os.Getenv("FEST_SELECTOR"),
		AllowCompleted: envBool("FEST_ALLOW_COMPLETED", false),
		CommandTimeout: envDurationSeconds("FEST_COMMAND_TIMEOUT_SECONDS", 8*time.Second),
	}, log)
	festRuntime := coordinator.NewFestRuntime(
		festReader,
		inferenceAgentID,
		defiAgentID,
		envInt("FEST_STALE_AFTER_SECONDS", 30),
		log,
	)

	allowSynthetic := envBool("FEST_FALLBACK_ALLOW_SYNTHETIC", false)
	pollInterval := envDurationSeconds("FEST_POLL_INTERVAL_SECONDS", 10*time.Second)
	planSource := "fest"
	initialSource := "fest"
	festSelector := os.Getenv("FEST_SELECTOR")

	plan, selectedSelector, snapshot, err := festRuntime.LoadPlan(ctx)
	if err != nil {
		if !allowSynthetic {
			log.Error("failed to load fest runtime plan", "error", err)
			os.Exit(1)
		}
		planSource = "synthetic_fallback_static_plan"
		initialSource = "synthetic"
		plan = coordinator.IntegrationCyclePlan(inferenceAgentID, defiAgentID)
		log.Warn("fest runtime unavailable, using static fallback plan", "error", err)
	} else {
		festSelector = selectedSelector
		initialSource = snapshot.Source
		log.Info("fest runtime plan loaded",
			"selector", selectedSelector,
			"progress_pct", snapshot.FestivalProgress.OverallCompletionPercent)
	}

	// Publish periodic festival progress updates for dashboard consumption.
	progressPublisher := coordinator.NewFestProgressPublisher(
		festRuntime,
		publisher,
		cfg.Coordinator.StatusTopicID,
		pollInterval,
		allowSynthetic,
		log,
	)
	progressPublisher.SetWSPublisher(hubAdapter{wsHub})
	progressErrs := progressPublisher.Start(ctx)
	go func() {
		for err := range progressErrs {
			log.Warn("festival progress publisher error", "error", err)
		}
	}()

	log.Info("coordinator starting",
		"version", "0.2.0",
		"task_topic", cfg.Coordinator.TaskTopicID,
		"status_topic", cfg.Coordinator.StatusTopicID,
		"plan_source", planSource,
		"initial_source", initialSource,
		"fest_selector", festSelector,
		"allow_synthetic", allowSynthetic,
		"poll_interval_seconds", int(pollInterval.Seconds()),
		"tasks", plan.TaskCount())

	assignedIDs, err := assigner.AssignTasks(ctx, plan)
	if err != nil {
		log.Error("failed to assign tasks", "error", err)
		os.Exit(1)
	}
	log.Info("tasks assigned", "count", len(assignedIDs), "task_ids", assignedIDs)

	// Block until shutdown signal.
	<-ctx.Done()
	log.Info("coordinator shutting down")
}

func connectDaemon(ctx context.Context, log *slog.Logger, hederaAccountID string) daemon.DaemonClient {
	daemonAddr := os.Getenv("DAEMON_ADDRESS")
	if daemonAddr == "" {
		daemonAddr = "localhost:50051"
	}

	daemonCfg := daemon.DefaultConfig()
	daemonCfg.Address = daemonAddr

	client, err := daemon.NewGRPCClient(ctx, daemonCfg)
	if err != nil {
		log.Warn("daemon connection failed, running standalone", "error", err)
		return daemon.Noop()
	}

	resp, err := client.Register(ctx, daemon.RegisterRequest{
		AgentName:       "coordinator",
		AgentType:       "coordinator",
		Capabilities:    []string{"hcs", "hts", "scheduling"},
		HederaAccountID: hederaAccountID,
	})
	if err != nil {
		log.Warn("daemon registration failed, running standalone", "error", err)
		client.Close()
		return daemon.Noop()
	}

	log.Info("registered with daemon",
		"agent_id", resp.AgentID,
		"session_id", resp.SessionID)
	return client
}

func daemonHeartbeatLoop(ctx context.Context, log *slog.Logger, client daemon.DaemonClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := client.Heartbeat(ctx, daemon.HeartbeatRequest{
				Timestamp: time.Now(),
			}); err != nil {
				log.Warn("daemon heartbeat failed", "error", err)
			}
		}
	}
}

func envBool(name string, defaultVal bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return defaultVal
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return defaultVal
	}
}

func envInt(name string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// hubAdapter bridges the coordinator's WSPublisher interface to the hub's Publish method.
type hubAdapter struct {
	h *hub.Hub
}

func (a hubAdapter) Publish(event coordinator.WSEvent) {
	a.h.Publish(hub.DaemonEvent{
		Type:      event.Type,
		AgentID:   event.AgentID,
		AgentName: event.AgentName,
		Timestamp: event.Timestamp,
		Payload:   event.Payload,
	})
}

func envDurationSeconds(name string, defaultVal time.Duration) time.Duration {
	seconds := envInt(name, int(defaultVal.Seconds()))
	if seconds <= 0 {
		return defaultVal
	}
	return time.Duration(seconds) * time.Second
}
