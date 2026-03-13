package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/lancekrogers/agent-coordinator/internal/hedera/hcs"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/hts"
	"github.com/lancekrogers/agent-coordinator/internal/integration"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := integration.LoadTestnetConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	coordClient, err := integration.NewClientForAccount(cfg.CoordinatorAccount)
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer coordClient.Close()

	// Create HCS topics.
	topicSvc := hcs.NewTopicService(coordClient)

	taskTopicID, err := topicSvc.CreateTopic(ctx, "agent-tasks")
	if err != nil {
		log.Fatalf("create task topic: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Created task topic:   %s\n", taskTopicID)

	statusTopicID, err := topicSvc.CreateTopic(ctx, "agent-status")
	if err != nil {
		log.Fatalf("create status topic: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Created status topic: %s\n", statusTopicID)

	// Create HTS payment token.
	tokenSvc := hts.NewTokenService(coordClient)
	tokenCfg := hts.DefaultTokenConfig()
	tokenCfg.TreasuryAccountID = cfg.CoordinatorAccount.AccountID

	paymentTokenID, err := tokenSvc.CreateFungibleToken(ctx, tokenCfg)
	if err != nil {
		log.Fatalf("create payment token: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Created payment token: %s\n", paymentTokenID)

	// Associate token with agent accounts.
	agent1Client, err := integration.NewClientForAccount(cfg.Agent1Account)
	if err != nil {
		log.Fatalf("create agent1 client: %v", err)
	}
	defer agent1Client.Close()

	agent1Transfer := hts.NewTransferService(agent1Client)
	if err := agent1Transfer.AssociateToken(ctx, paymentTokenID, cfg.Agent1Account.AccountID); err != nil {
		log.Fatalf("associate token with agent1: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Associated token with agent1: %s\n", cfg.Agent1Account.AccountID)

	agent2Client, err := integration.NewClientForAccount(cfg.Agent2Account)
	if err != nil {
		log.Fatalf("create agent2 client: %v", err)
	}
	defer agent2Client.Close()

	agent2Transfer := hts.NewTransferService(agent2Client)
	if err := agent2Transfer.AssociateToken(ctx, paymentTokenID, cfg.Agent2Account.AccountID); err != nil {
		log.Fatalf("associate token with agent2: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Associated token with agent2: %s\n", cfg.Agent2Account.AccountID)

	// Output env vars to stdout for sourcing.
	fmt.Printf("HCS_TASK_TOPIC_ID=%s\n", taskTopicID)
	fmt.Printf("HCS_STATUS_TOPIC_ID=%s\n", statusTopicID)
	fmt.Printf("HTS_PAYMENT_TOKEN_ID=%s\n", paymentTokenID)
}
