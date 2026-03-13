//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/lancekrogers/agent-coordinator/internal/coordinator"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/hcs"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/hts"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/schedule"
)

func TestE2E_FullFestivalCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Phase 1: Load testnet configuration.
	cfg, err := LoadTestnetConfig()
	if err != nil {
		t.Fatalf("load testnet config: %v", err)
	}

	coordClient, err := NewClientForAccount(cfg.CoordinatorAccount)
	if err != nil {
		t.Fatalf("create coordinator client: %v", err)
	}
	defer coordClient.Close()

	// Phase 2: Create HCS topics.
	topicSvc := hcs.NewTopicService(coordClient)

	taskTopicID, err := topicSvc.CreateTopic(ctx, "e2e-festival-tasks")
	if err != nil {
		t.Fatalf("create task topic: %v", err)
	}
	t.Logf("Created task topic: %s", taskTopicID)

	statusTopicID, err := topicSvc.CreateTopic(ctx, "e2e-agent-status")
	if err != nil {
		t.Fatalf("create status topic: %v", err)
	}
	t.Logf("Created status topic: %s", statusTopicID)

	// Phase 3: Create HTS payment token.
	tokenSvc := hts.NewTokenService(coordClient)
	tokenCfg := hts.DefaultTokenConfig()
	tokenCfg.TreasuryAccountID = cfg.CoordinatorAccount.AccountID

	paymentTokenID, err := tokenSvc.CreateFungibleToken(ctx, tokenCfg)
	if err != nil {
		t.Fatalf("create payment token: %v", err)
	}
	t.Logf("Created payment token: %s", paymentTokenID)

	// Associate token with agent accounts (requires agent signing).
	agent1Client, err := NewClientForAccount(cfg.Agent1Account)
	if err != nil {
		t.Fatalf("create agent1 client: %v", err)
	}
	defer agent1Client.Close()

	agent1TransferSvc := hts.NewTransferService(agent1Client)
	if err := agent1TransferSvc.AssociateToken(ctx, paymentTokenID, cfg.Agent1Account.AccountID); err != nil {
		t.Fatalf("associate token with agent1: %v", err)
	}

	agent2Client, err := NewClientForAccount(cfg.Agent2Account)
	if err != nil {
		t.Fatalf("create agent2 client: %v", err)
	}
	defer agent2Client.Close()

	agent2TransferSvc := hts.NewTransferService(agent2Client)
	if err := agent2TransferSvc.AssociateToken(ctx, paymentTokenID, cfg.Agent2Account.AccountID); err != nil {
		t.Fatalf("associate token with agent2: %v", err)
	}

	// Phase 4: Create plan and assign tasks via HCS.
	plan := coordinator.Plan{
		FestivalID: "e2e-test",
		Sequences: []coordinator.PlanSequence{
			{
				ID: "test-sequence",
				Tasks: []coordinator.PlanTask{
					{ID: "task-1", Name: "First Task", AssignTo: cfg.Agent1Account.AccountID.String()},
					{ID: "task-2", Name: "Second Task", AssignTo: cfg.Agent2Account.AccountID.String()},
				},
			},
		},
	}

	publisher := hcs.NewPublisher(coordClient, hcs.DefaultPublishConfig())
	assigner := coordinator.NewAssigner(publisher, taskTopicID, nil)

	assignedIDs, err := assigner.AssignTasks(ctx, plan)
	if err != nil {
		t.Fatalf("assign tasks: %v", err)
	}
	t.Logf("Assigned %d tasks: %v", len(assignedIDs), assignedIDs)

	if len(assignedIDs) != 2 {
		t.Fatalf("expected 2 assigned tasks, got %d", len(assignedIDs))
	}

	// Phase 5: Subscribe to task topic and verify message delivery.
	subscriber := hcs.NewSubscriber(coordClient, hcs.DefaultSubscribeConfig())
	subCtx, subCancel := context.WithTimeout(ctx, 30*time.Second)
	defer subCancel()

	msgCh, errCh := subscriber.Subscribe(subCtx, taskTopicID)

	receivedCount := 0
	timeout := time.After(30 * time.Second)

	for receivedCount < 2 {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatal("message channel closed before receiving all messages")
			}
			t.Logf("Received HCS message: type=%s task=%s", msg.Type, msg.TaskID)
			receivedCount++
		case subErr := <-errCh:
			t.Logf("HCS subscription error (non-fatal): %v", subErr)
		case <-timeout:
			t.Fatalf("timed out waiting for HCS messages, received %d of 2", receivedCount)
		}
	}
	subCancel()
	t.Log("HCS message delivery verified")

	// Phase 6: Execute payment for task-1.
	coordConfig := coordinator.Config{
		TaskTopicID:          taskTopicID,
		StatusTopicID:        statusTopicID,
		PaymentTokenID:       paymentTokenID,
		TreasuryAccountID:    cfg.CoordinatorAccount.AccountID,
		DefaultPaymentAmount: 100,
		MonitorPollInterval:  5 * time.Second,
		QualityGateTimeout:   30 * time.Second,
	}

	transferSvc := hts.NewTransferService(coordClient)
	paymentMgr := coordinator.NewPayment(transferSvc, publisher, coordConfig)

	err = paymentMgr.PayForTask(ctx, "task-1", cfg.Agent1Account.AccountID.String(), 100)
	if err != nil {
		t.Fatalf("pay for task-1: %v", err)
	}
	t.Log("Payment for task-1 completed")

	payStatus, err := paymentMgr.PaymentStatus("task-1")
	if err != nil {
		t.Fatalf("payment status: %v", err)
	}
	if payStatus != coordinator.PaymentProcessed {
		t.Fatalf("expected payment processed, got %s", payStatus)
	}

	// Phase 7: Heartbeat verification.
	schedulerSvc := schedule.NewScheduleService(agent1Client)
	hbConfig := schedule.HeartbeatConfig{
		Interval:  10 * time.Second,
		Memo:      "e2e-heartbeat",
		AgentID:   "agent-1",
		AccountID: cfg.Agent1Account.AccountID,
	}
	hb, err := schedule.NewHeartbeat(agent1Client, schedulerSvc, hbConfig)
	if err != nil {
		t.Fatalf("create heartbeat: %v", err)
	}

	hbCtx, hbCancel := context.WithTimeout(ctx, 15*time.Second)
	hbErrCh := hb.Start(hbCtx)

	time.Sleep(12 * time.Second)
	hbCancel()

	// Drain error channel.
	for err := range hbErrCh {
		t.Logf("heartbeat error (non-fatal): %v", err)
	}

	if hb.LastHeartbeat().IsZero() {
		t.Error("expected at least one successful heartbeat")
	} else {
		t.Logf("Heartbeat last fired at: %s", hb.LastHeartbeat())
	}

	t.Log("E2E test passed: plan -> assign -> HCS verify -> payment -> heartbeat -> done")
}
