package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/config"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/coordinator"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/hedera/hcs"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/hedera/hts"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/hedera/schedule"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/pkg/creclient"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/pkg/daemon"
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

	// Connect to daemon runtime (optional — agent works standalone if unavailable).
	daemonClient := connectDaemon(ctx, log, cfg.CoordinatorAccountID.String())
	defer daemonClient.Close()

	// Create Hedera client with coordinator credentials.
	hederaClient := hiero.ClientForTestnet()
	hederaClient.SetOperator(cfg.CoordinatorAccountID, cfg.CoordinatorKey)

	// Initialize HCS publisher and subscriber.
	publisher := hcs.NewPublisher(hederaClient, hcs.DefaultPublishConfig())
	subscriber := hcs.NewSubscriber(hederaClient, hcs.DefaultSubscribeConfig())

	// Initialize HTS transfer service.
	transferSvc := hts.NewTransferService(hederaClient)

	// Schedule service — 4th Hedera native service.
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

	// Create coordinator components.
	agentIDs := []string{"inference-001", "defi-001"}
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

	// Build and execute integration plan.
	plan := coordinator.IntegrationCyclePlan("inference-001", "defi-001")
	log.Info("coordinator starting",
		"version", "0.2.0",
		"task_topic", cfg.Coordinator.TaskTopicID,
		"status_topic", cfg.Coordinator.StatusTopicID,
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
