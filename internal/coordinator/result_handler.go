package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator/internal/hedera/hcs"
)

// TaskResultPayload is the payload agents send when completing a task.
type TaskResultPayload struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	TxHash     string `json:"tx_hash,omitempty"`
}

// ResultHandler subscribes to the status topic and processes task_result
// and pnl_report messages from agents.
type ResultHandler struct {
	subscriber hcs.MessageSubscriber
	topicID    hiero.TopicID
	payment    PaymentManager
	config     Config
	log        *slog.Logger

	// agentAccounts maps agent ID → Hedera account ID string for payments.
	agentAccounts map[string]string

	mu      sync.RWMutex
	results map[string]TaskResultPayload
}

// ResultHandlerConfig holds configuration for the result handler.
type ResultHandlerConfig struct {
	Subscriber    hcs.MessageSubscriber
	TopicID       hiero.TopicID
	Payment       PaymentManager
	Config        Config
	Log           *slog.Logger
	AgentAccounts map[string]string
}

// NewResultHandler creates a handler that processes agent results from the status topic.
func NewResultHandler(cfg ResultHandlerConfig) *ResultHandler {
	return &ResultHandler{
		subscriber:    cfg.Subscriber,
		topicID:       cfg.TopicID,
		payment:       cfg.Payment,
		config:        cfg.Config,
		log:           cfg.Log,
		agentAccounts: cfg.AgentAccounts,
		results:       make(map[string]TaskResultPayload),
	}
}

// Start begins listening for results on the status topic. Blocks until ctx is cancelled.
func (rh *ResultHandler) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("result handler start: %w", err)
	}

	msgCh, errCh := rh.subscriber.Subscribe(ctx, rh.topicID)

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			rh.processMessage(ctx, msg)
		case err, ok := <-errCh:
			if !ok {
				errCh = nil // prevent spin on closed channel
				continue
			}
			rh.log.Warn("result handler subscription error", "error", err)
		}
	}
}

// Result returns the stored result for a task, if any.
func (rh *ResultHandler) Result(taskID string) (TaskResultPayload, bool) {
	rh.mu.RLock()
	defer rh.mu.RUnlock()
	r, ok := rh.results[taskID]
	return r, ok
}

func (rh *ResultHandler) processMessage(ctx context.Context, msg hcs.Envelope) {
	switch msg.Type {
	case hcs.MessageTypeTaskResult:
		rh.handleTaskResult(ctx, msg)
	case hcs.MessageTypePnLReport:
		rh.handlePnLReport(msg)
	}
}

func (rh *ResultHandler) handleTaskResult(ctx context.Context, msg hcs.Envelope) {
	var result TaskResultPayload
	if err := json.Unmarshal(msg.Payload, &result); err != nil {
		rh.log.Warn("failed to unmarshal task result", "error", err)
		return
	}

	rh.mu.Lock()
	rh.results[result.TaskID] = result
	rh.mu.Unlock()

	rh.log.Info("task result received",
		"task_id", result.TaskID,
		"status", result.Status,
		"sender", msg.Sender,
		"duration_ms", result.DurationMs)

	if result.Status != "completed" {
		return
	}

	// Resolve agent account ID for payment.
	agentAccountID, ok := rh.agentAccounts[msg.Sender]
	if !ok {
		rh.log.Warn("no account mapping for agent, skipping payment", "agent_id", msg.Sender)
		return
	}

	amount := rh.config.DefaultPaymentAmount
	if err := rh.payment.PayForTask(ctx, result.TaskID, agentAccountID, amount); err != nil {
		rh.log.Error("payment failed",
			"task_id", result.TaskID,
			"agent_id", msg.Sender,
			"amount", amount,
			"error", err)
	} else {
		rh.log.Info("payment settled",
			"task_id", result.TaskID,
			"agent_id", msg.Sender,
			"amount", amount)
	}
}

// PnLReportPayload is the payload agents send with P&L data.
type PnLReportPayload struct {
	AgentID          string  `json:"agent_id"`
	NetPnL           float64 `json:"net_pnl"`
	TradeCount       int     `json:"trade_count"`
	IsSelfSustaining bool    `json:"is_self_sustaining"`
	ActiveStrategy   string  `json:"active_strategy"`
}

func (rh *ResultHandler) handlePnLReport(msg hcs.Envelope) {
	var report PnLReportPayload
	if err := json.Unmarshal(msg.Payload, &report); err != nil {
		rh.log.Warn("failed to unmarshal pnl report", "error", err)
		return
	}

	rh.log.Info("pnl report received",
		"sender", msg.Sender,
		"agent_id", report.AgentID,
		"net_pnl", report.NetPnL,
		"trades", report.TradeCount,
		"self_sustaining", report.IsSelfSustaining,
		"strategy", report.ActiveStrategy)
}
