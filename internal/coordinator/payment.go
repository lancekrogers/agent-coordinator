package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator/internal/hedera/hcs"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/hts"
)

// PaymentSettledPayload is the HCS message payload for a payment settlement.
type PaymentSettledPayload struct {
	TaskID   string `json:"task_id"`
	AgentID  string `json:"agent_id"`
	Amount   int64  `json:"amount"`
	TokenID  string `json:"token_id"`
	TxStatus string `json:"tx_status"`
}

// Payment implements the PaymentManager interface.
type Payment struct {
	transferSvc hts.TokenTransfer
	publisher   hcs.MessagePublisher
	config      Config

	mu       sync.RWMutex
	payments map[string]PaymentState // taskID -> payment state
	seqNum   uint64
}

// NewPayment creates a new payment manager.
func NewPayment(transferSvc hts.TokenTransfer, publisher hcs.MessagePublisher, config Config) *Payment {
	return &Payment{
		transferSvc: transferSvc,
		publisher:   publisher,
		config:      config,
		payments:    make(map[string]PaymentState),
	}
}

// PayForTask triggers a token transfer to the agent that completed the task.
func (p *Payment) PayForTask(ctx context.Context, taskID string, agentID string, amount int64) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pay for task %s to %s amount %d: %w", taskID, agentID, amount, err)
	}

	if amount <= 0 {
		return fmt.Errorf("pay for task %s to %s: amount must be positive, got %d", taskID, agentID, amount)
	}

	// Atomically check for double-payment and mark as pending.
	p.mu.Lock()
	existing := p.payments[taskID]
	if existing == PaymentProcessed || existing == PaymentPending {
		p.mu.Unlock()
		return fmt.Errorf("pay for task %s to %s amount %d: already %s", taskID, agentID, amount, existing)
	}
	p.payments[taskID] = PaymentPending
	p.mu.Unlock()

	// Parse agent account ID.
	agentAccountID, err := hiero.AccountIDFromString(agentID)
	if err != nil {
		p.setPaymentState(taskID, PaymentFailed)
		return fmt.Errorf("pay for task %s to %s amount %d: parse agent account: %w", taskID, agentID, amount, err)
	}

	// Execute the token transfer.
	receipt, err := p.transferSvc.Transfer(ctx, hts.TransferRequest{
		TokenID:       p.config.PaymentTokenID,
		FromAccountID: p.config.TreasuryAccountID,
		ToAccountID:   agentAccountID,
		Amount:        amount,
		Memo:          fmt.Sprintf("payment:task:%s", taskID),
	})
	if err != nil {
		p.setPaymentState(taskID, PaymentFailed)
		return fmt.Errorf("pay for task %s to %s amount %d: transfer: %w", taskID, agentID, amount, err)
	}

	// Mark as processed.
	p.setPaymentState(taskID, PaymentProcessed)

	// Publish settlement notification via HCS.
	if err := p.publishSettlement(ctx, taskID, agentID, amount, receipt.Status); err != nil {
		return fmt.Errorf("pay for task %s to %s amount %d: publish settlement: %w", taskID, agentID, amount, err)
	}

	return nil
}

// PaymentStatus returns the payment status for a task.
func (p *Payment) PaymentStatus(taskID string) (PaymentState, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	state, exists := p.payments[taskID]
	if !exists {
		return "", fmt.Errorf("payment status for task %s: not tracked", taskID)
	}
	return state, nil
}

func (p *Payment) publishSettlement(ctx context.Context, taskID string, agentID string, amount int64, txStatus string) error {
	payload := PaymentSettledPayload{
		TaskID:   taskID,
		AgentID:  agentID,
		Amount:   amount,
		TokenID:  p.config.PaymentTokenID.String(),
		TxStatus: txStatus,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal settlement payload: %w", err)
	}

	p.mu.Lock()
	p.seqNum++
	seqNum := p.seqNum
	p.mu.Unlock()

	env := hcs.Envelope{
		Type:        hcs.MessageTypePaymentSettled,
		Sender:      "coordinator",
		Recipient:   agentID,
		TaskID:      taskID,
		SequenceNum: seqNum,
		Timestamp:   time.Now(),
		Payload:     payloadBytes,
	}

	return p.publisher.Publish(ctx, p.config.TaskTopicID, env)
}

func (p *Payment) setPaymentState(taskID string, state PaymentState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.payments[taskID] = state
}

// Compile-time interface compliance check.
var _ PaymentManager = (*Payment)(nil)
