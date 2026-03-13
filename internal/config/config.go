package config

import (
	"fmt"
	"os"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator/internal/coordinator"
)

// Env holds parsed environment variables for the coordinator.
type Env struct {
	CoordinatorAccountID hiero.AccountID
	CoordinatorKey       hiero.PrivateKey
	Agent1AccountID      string
	Agent2AccountID      string
	Coordinator          coordinator.Config
}

// Load reads coordinator configuration from environment variables.
func Load() (*Env, error) {
	coordAcctStr := os.Getenv("HEDERA_COORDINATOR_ACCOUNT_ID")
	if coordAcctStr == "" {
		return nil, fmt.Errorf("config: HEDERA_COORDINATOR_ACCOUNT_ID is required")
	}
	coordAcct, err := hiero.AccountIDFromString(coordAcctStr)
	if err != nil {
		return nil, fmt.Errorf("config: parse HEDERA_COORDINATOR_ACCOUNT_ID: %w", err)
	}

	coordKeyStr := os.Getenv("HEDERA_COORDINATOR_PRIVATE_KEY")
	if coordKeyStr == "" {
		return nil, fmt.Errorf("config: HEDERA_COORDINATOR_PRIVATE_KEY is required")
	}
	coordKey, err := hiero.PrivateKeyFromString(coordKeyStr)
	if err != nil {
		return nil, fmt.Errorf("config: parse HEDERA_COORDINATOR_PRIVATE_KEY: %w", err)
	}

	taskTopicStr := os.Getenv("HCS_TASK_TOPIC_ID")
	if taskTopicStr == "" {
		return nil, fmt.Errorf("config: HCS_TASK_TOPIC_ID is required")
	}
	taskTopic, err := hiero.TopicIDFromString(taskTopicStr)
	if err != nil {
		return nil, fmt.Errorf("config: parse HCS_TASK_TOPIC_ID: %w", err)
	}

	statusTopicStr := os.Getenv("HCS_STATUS_TOPIC_ID")
	if statusTopicStr == "" {
		return nil, fmt.Errorf("config: HCS_STATUS_TOPIC_ID is required")
	}
	statusTopic, err := hiero.TopicIDFromString(statusTopicStr)
	if err != nil {
		return nil, fmt.Errorf("config: parse HCS_STATUS_TOPIC_ID: %w", err)
	}

	paymentTokenStr := os.Getenv("HTS_PAYMENT_TOKEN_ID")
	if paymentTokenStr == "" {
		return nil, fmt.Errorf("config: HTS_PAYMENT_TOKEN_ID is required")
	}
	paymentToken, err := hiero.TokenIDFromString(paymentTokenStr)
	if err != nil {
		return nil, fmt.Errorf("config: parse HTS_PAYMENT_TOKEN_ID: %w", err)
	}

	agent1 := os.Getenv("HEDERA_AGENT1_ACCOUNT_ID")
	if agent1 == "" {
		return nil, fmt.Errorf("config: HEDERA_AGENT1_ACCOUNT_ID is required")
	}
	if _, err := hiero.AccountIDFromString(agent1); err != nil {
		return nil, fmt.Errorf("config: parse HEDERA_AGENT1_ACCOUNT_ID: %w", err)
	}

	agent2 := os.Getenv("HEDERA_AGENT2_ACCOUNT_ID")
	if agent2 == "" {
		return nil, fmt.Errorf("config: HEDERA_AGENT2_ACCOUNT_ID is required")
	}
	if _, err := hiero.AccountIDFromString(agent2); err != nil {
		return nil, fmt.Errorf("config: parse HEDERA_AGENT2_ACCOUNT_ID: %w", err)
	}

	cfg := coordinator.DefaultConfig()
	cfg.TaskTopicID = taskTopic
	cfg.StatusTopicID = statusTopic
	cfg.PaymentTokenID = paymentToken
	cfg.TreasuryAccountID = coordAcct

	return &Env{
		CoordinatorAccountID: coordAcct,
		CoordinatorKey:       coordKey,
		Agent1AccountID:      agent1,
		Agent2AccountID:      agent2,
		Coordinator:          cfg,
	}, nil
}
