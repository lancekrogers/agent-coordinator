package coordinator

import (
	"context"
	"encoding/json"
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
	TaskID       string   `json:"task_id"`
	TaskName     string   `json:"task_name"`
	TaskType     string   `json:"task_type,omitempty"`
	AgentID      string   `json:"agent_id"`
	ModelID      string   `json:"model_id,omitempty"`
	Input        string   `json:"input,omitempty"`
	Priority     int      `json:"priority,omitempty"`
	MaxTokens    int      `json:"max_tokens,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
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

			if err := a.assignPlanTask(ctx, task, agentID); err != nil {
				return assignedIDs, fmt.Errorf("assign tasks: task %s: %w", task.ID, err)
			}

			assignedIDs = append(assignedIDs, task.ID)
		}
	}

	return assignedIDs, nil
}

// AssignTask assigns a single task to a specific agent via HCS.
func (a *Assigner) AssignTask(ctx context.Context, taskID string, agentID string) error {
	return a.assignPlanTask(ctx, PlanTask{ID: taskID}, agentID)
}

func (a *Assigner) assignPlanTask(ctx context.Context, task PlanTask, agentID string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("assign task %s to %s: %w", task.ID, agentID, err)
	}

	// CRE risk check for DeFi tasks
	if isDeFiTask(task) && a.creClient != nil {
		decision, err := a.creClient.EvaluateRisk(ctx, creclient.RiskRequest{
			AgentID:   agentID,
			TaskID:    task.ID,
			Signal:    "buy",
			RiskScore: task.Priority, // use priority as risk proxy
			Timestamp: time.Now().Unix(),
		})
		if err != nil {
			a.logger.Warn("CRE risk check failed, blocking task", "task_id", task.ID, "error", err)
			return fmt.Errorf("assign task %s: CRE check failed: %w", task.ID, err)
		}
		if !decision.Approved {
			a.logger.Info("CRE denied task", "task_id", task.ID, "reason", decision.Reason)
			return fmt.Errorf("assign task %s: CRE denied: %s", task.ID, decision.Reason)
		}
		a.logger.Info("CRE approved task", "task_id", task.ID, "max_position", decision.MaxPositionUSD)
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
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("assign task %s to %s: marshal payload: %w", task.ID, agentID, err)
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
		return fmt.Errorf("assign task %s to %s: publish: %w", task.ID, agentID, err)
	}

	a.mu.Lock()
	a.assignments[task.ID] = agentID
	a.mu.Unlock()

	return nil
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

// isDeFiTask returns true if the task involves DeFi trade execution.
func isDeFiTask(task PlanTask) bool {
	return task.TaskType == "defi" || task.TaskType == "trade"
}

// Compile-time interface compliance check.
var _ TaskAssigner = (*Assigner)(nil)
