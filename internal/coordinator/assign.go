package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/hedera/hcs"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/pkg/creclient"
)

// TaskAssignmentPayload is the payload for a task assignment message.
// Fields are a superset: inference agents use ModelID/Input/MaxTokens,
// defi agents use TaskType. Both use Priority.
type TaskAssignmentPayload struct {
	TaskID       string              `json:"task_id"`
	TaskName     string              `json:"task_name"`
	TaskType     string              `json:"task_type,omitempty"`
	AgentID      string              `json:"agent_id"`
	ModelID      string              `json:"model_id,omitempty"`
	Input        string              `json:"input,omitempty"`
	Priority     int                 `json:"priority,omitempty"`
	MaxTokens    int                 `json:"max_tokens,omitempty"`
	Dependencies []string            `json:"dependencies,omitempty"`
	CREDecision  *CREDecisionPayload `json:"cre_decision,omitempty"`
}

// CREDecisionPayload captures the risk constraints approved by CRE.
type CREDecisionPayload struct {
	Approved          bool   `json:"approved"`
	MaxPositionUSD    uint64 `json:"max_position_usd"`
	MaxSlippageBps    uint64 `json:"max_slippage_bps"`
	TTLSeconds        uint64 `json:"ttl_seconds"`
	DecisionTimestamp int64  `json:"decision_timestamp"`
	Reason            string `json:"reason"`
}

// Assigner implements the TaskAssigner interface.
type Assigner struct {
	publisher hcs.MessagePublisher
	topicID   hiero.TopicID
	agentIDs  []string
	creClient *creclient.Client // optional CRE Risk Router client
	logger    *slog.Logger

	mu          sync.RWMutex
	assignments map[string]string // taskID -> agentID
	seqNum      uint64
}

// NewAssigner creates a new task assigner.
func NewAssigner(publisher hcs.MessagePublisher, topicID hiero.TopicID, agentIDs []string) *Assigner {
	return &Assigner{
		publisher:   publisher,
		topicID:     topicID,
		agentIDs:    agentIDs,
		assignments: make(map[string]string),
		logger:      slog.Default(),
	}
}

// SetCREClient configures the optional CRE Risk Router client for DeFi task risk checks.
func (a *Assigner) SetCREClient(client *creclient.Client) {
	a.creClient = client
}

// AssignTasks publishes task assignments for all tasks in the plan.
func (a *Assigner) AssignTasks(ctx context.Context, plan Plan) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("assign tasks for plan %s: %w", plan.FestivalID, err)
	}

	var assignedIDs []string
	agentIdx := 0

	for _, seq := range plan.Sequences {
		for _, task := range seq.Tasks {
			if err := ctx.Err(); err != nil {
				return assignedIDs, fmt.Errorf("assign tasks: cancelled during assignment: %w", err)
			}

			agentID := task.AssignTo
			if agentID == "" && len(a.agentIDs) > 0 {
				agentID = a.agentIDs[agentIdx%len(a.agentIDs)]
				agentIdx++
			}

			assigned, err := a.assignPlanTask(ctx, task, agentID)
			if err != nil {
				return assignedIDs, fmt.Errorf("assign tasks: task %s: %w", task.ID, err)
			}
			if assigned {
				assignedIDs = append(assignedIDs, task.ID)
			}
		}
	}

	return assignedIDs, nil
}

// AssignTask assigns a single task to a specific agent via HCS.
func (a *Assigner) AssignTask(ctx context.Context, taskID string, agentID string) error {
	_, err := a.assignPlanTask(ctx, PlanTask{ID: taskID}, agentID)
	return err
}

func (a *Assigner) assignPlanTask(ctx context.Context, task PlanTask, agentID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("assign task %s to %s: %w", task.ID, agentID, err)
	}

	var creDecision *CREDecisionPayload

	// CRE risk check for DeFi tasks (fail-closed).
	if isDeFiTask(task) {
		if a.creClient == nil {
			a.logger.Warn("CRE not configured, denying DeFi task", "task_id", task.ID)
			a.publishRiskEvent(ctx, task.ID, agentID, hcs.MessageTypeRiskCheckDenied, "cre_not_configured")
			return false, nil
		}

		decision, err := a.creClient.EvaluateRisk(ctx, creclient.RiskRequest{
			AgentID:           agentID,
			TaskID:            task.ID,
			Signal:            "buy",
			SignalConfidence:  0.85, // default until inference populates this
			RiskScore:         task.Priority,
			MarketPair:        "ETH/USD",
			RequestedPosition: 1000_000000, // $1000 default in 6-decimal
			Timestamp:         time.Now().Unix(),
		})
		if err != nil {
			reason := classifyCREError(err)
			a.logger.Warn("CRE risk check failed, denying task",
				"task_id", task.ID, "reason", reason, "error", err)
			a.publishRiskEvent(ctx, task.ID, agentID, hcs.MessageTypeRiskCheckDenied, reason)
			return false, nil
		}

		if !decision.Approved {
			a.logger.Info("CRE denied task, skipping assignment",
				"task_id", task.ID, "reason", decision.Reason)
			a.publishRiskEvent(ctx, task.ID, agentID, hcs.MessageTypeRiskCheckDenied, decision.Reason)
			return false, nil // skip this task, don't abort the loop
		}

		a.logger.Info("CRE approved task",
			"task_id", task.ID, "max_position", decision.MaxPositionUSD)
		a.publishRiskEvent(ctx, task.ID, agentID, hcs.MessageTypeRiskCheckApproved, "approved")

		decisionTS := decision.Timestamp
		if decisionTS == 0 {
			decisionTS = time.Now().Unix()
		}
		creDecision = &CREDecisionPayload{
			Approved:          decision.Approved,
			MaxPositionUSD:    decision.MaxPositionUSD,
			MaxSlippageBps:    decision.MaxSlippageBps,
			TTLSeconds:        decision.TTLSeconds,
			DecisionTimestamp: decisionTS,
			Reason:            decision.Reason,
		}
	}

	payload := TaskAssignmentPayload{
		TaskID:       task.ID,
		TaskName:     task.Name,
		TaskType:     task.TaskType,
		AgentID:      agentID,
		ModelID:      task.ModelID,
		Input:        task.Input,
		Priority:     task.Priority,
		MaxTokens:    task.MaxTokens,
		Dependencies: task.Dependencies,
		CREDecision:  creDecision,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("assign task %s to %s: marshal payload: %w", task.ID, agentID, err)
	}

	a.mu.Lock()
	a.seqNum++
	seqNum := a.seqNum
	a.mu.Unlock()

	env := hcs.Envelope{
		Type:        hcs.MessageTypeTaskAssignment,
		Sender:      "coordinator",
		Recipient:   agentID,
		TaskID:      task.ID,
		SequenceNum: seqNum,
		Timestamp:   time.Now(),
		Payload:     payloadBytes,
	}

	if err := a.publisher.Publish(ctx, a.topicID, env); err != nil {
		return false, fmt.Errorf("assign task %s to %s: publish: %w", task.ID, agentID, err)
	}

	a.mu.Lock()
	a.assignments[task.ID] = agentID
	a.mu.Unlock()

	return true, nil
}

// Assignment returns the agent ID assigned to a task, or empty string if unassigned.
func (a *Assigner) Assignment(taskID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.assignments[taskID]
}

// AssignmentCount returns the number of tasks that have been assigned.
func (a *Assigner) AssignmentCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.assignments)
}

// publishRiskEvent emits an HCS message for CRE risk check lifecycle events.
func (a *Assigner) publishRiskEvent(ctx context.Context, taskID, agentID string, msgType hcs.MessageType, reason string) {
	a.mu.Lock()
	a.seqNum++
	seqNum := a.seqNum
	a.mu.Unlock()

	payload, _ := json.Marshal(map[string]string{"reason": reason})
	env := hcs.Envelope{
		Type:        msgType,
		Sender:      "coordinator",
		Recipient:   agentID,
		TaskID:      taskID,
		SequenceNum: seqNum,
		Timestamp:   time.Now(),
		Payload:     payload,
	}
	if err := a.publisher.Publish(ctx, a.topicID, env); err != nil {
		a.logger.Warn("failed to publish risk event", "type", msgType, "task_id", taskID, "error", err)
	}
}

// isDeFiTask returns true if the task involves DeFi trade execution.
func isDeFiTask(task PlanTask) bool {
	return task.TaskType == "defi" || task.TaskType == "trade" || task.TaskType == "execute_trade"
}

func classifyCREError(err error) string {
	switch {
	case errors.Is(err, creclient.ErrUnexpectedStatus), errors.Is(err, creclient.ErrDecodeFailed):
		return "cre_invalid_response"
	case errors.Is(err, creclient.ErrRequestFailed):
		return "cre_unreachable"
	default:
		return "cre_unreachable"
	}
}

// Compile-time interface compliance check.
var _ TaskAssigner = (*Assigner)(nil)
