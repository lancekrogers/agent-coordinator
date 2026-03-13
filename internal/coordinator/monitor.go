package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator/internal/hedera/hcs"
)

// StatusUpdatePayload is the payload for agent status update messages.
type StatusUpdatePayload struct {
	TaskID    string     `json:"task_id"`
	AgentID   string     `json:"agent_id"`
	NewStatus TaskStatus `json:"new_status"`
	Message   string     `json:"message,omitempty"`
}

// Monitor implements the ProgressMonitor interface.
type Monitor struct {
	subscriber   hcs.MessageSubscriber
	topicID      hiero.TopicID
	gateEnforcer QualityGateEnforcer

	mu     sync.RWMutex
	states map[string]TaskStatus
}

// NewMonitor creates a new progress monitor.
func NewMonitor(subscriber hcs.MessageSubscriber, topicID hiero.TopicID, gate QualityGateEnforcer) *Monitor {
	return &Monitor{
		subscriber:   subscriber,
		topicID:      topicID,
		gateEnforcer: gate,
		states:       make(map[string]TaskStatus),
	}
}

// Start begins monitoring the HCS topic for status updates. Blocks until context is cancelled.
func (m *Monitor) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("monitor start: %w", err)
	}

	msgCh, errCh := m.subscriber.Subscribe(ctx, m.topicID)

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			m.processMessage(ctx, msg)
		case _, ok := <-errCh:
			if !ok {
				errCh = nil // prevent spin on closed channel
				continue
			}
		}
	}
}

// TaskState returns the current state of a task.
func (m *Monitor) TaskState(taskID string) (TaskStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status, exists := m.states[taskID]
	if !exists {
		return "", fmt.Errorf("task %s: not tracked by monitor", taskID)
	}
	return status, nil
}

// AllTaskStates returns a snapshot of all tracked tasks and their states.
func (m *Monitor) AllTaskStates() map[string]TaskStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]TaskStatus, len(m.states))
	for k, v := range m.states {
		result[k] = v
	}
	return result
}

// InitTask registers a task with the monitor in pending state.
func (m *Monitor) InitTask(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[taskID] = StatusPending
}

func (m *Monitor) processMessage(ctx context.Context, msg hcs.Envelope) {
	if msg.Type != hcs.MessageTypeStatusUpdate {
		return
	}

	var payload StatusUpdatePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	currentStatus, exists := m.states[payload.TaskID]
	if !exists {
		currentStatus = StatusPending
	}

	if payload.NewStatus == StatusComplete && m.gateEnforcer != nil {
		passed, err := m.gateEnforcer.Evaluate(ctx, payload.TaskID)
		if err != nil || !passed {
			m.states[payload.TaskID] = StatusInProgress
			return
		}
	}

	if err := Transition(currentStatus, payload.NewStatus); err != nil {
		return
	}

	m.states[payload.TaskID] = payload.NewStatus
}

// Compile-time interface compliance check.
var _ ProgressMonitor = (*Monitor)(nil)
