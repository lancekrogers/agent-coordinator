package coordinator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/hedera/hcs"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/pkg/creclient"
)

func TestAssigner_ContextCancellation(t *testing.T) {
	a := NewAssigner(nil, hiero.TopicID{}, []string{"agent-1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.AssignTasks(ctx, Plan{})
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestAssignTask_ContextCancellation(t *testing.T) {
	a := NewAssigner(nil, hiero.TopicID{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := a.AssignTask(ctx, "task-1", "agent-1")
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestAssigner_AssignmentTracking(t *testing.T) {
	a := NewAssigner(nil, hiero.TopicID{}, nil)

	if a.AssignmentCount() != 0 {
		t.Errorf("initial count = %d, want 0", a.AssignmentCount())
	}

	if got := a.Assignment("task-1"); got != "" {
		t.Errorf("Assignment(unassigned) = %q, want empty", got)
	}
}

func TestPlan_TaskCount(t *testing.T) {
	tests := []struct {
		name string
		plan Plan
		want int
	}{
		{
			name: "empty plan",
			plan: Plan{},
			want: 0,
		},
		{
			name: "single sequence with 3 tasks",
			plan: Plan{
				Sequences: []PlanSequence{
					{ID: "seq-1", Tasks: []PlanTask{{ID: "t1"}, {ID: "t2"}, {ID: "t3"}}},
				},
			},
			want: 3,
		},
		{
			name: "multiple sequences",
			plan: Plan{
				Sequences: []PlanSequence{
					{ID: "seq-1", Tasks: []PlanTask{{ID: "t1"}, {ID: "t2"}}},
					{ID: "seq-2", Tasks: []PlanTask{{ID: "t3"}}},
				},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.plan.TaskCount(); got != tt.want {
				t.Errorf("TaskCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPlan_TaskByID(t *testing.T) {
	plan := Plan{
		Sequences: []PlanSequence{
			{
				ID: "seq-1",
				Tasks: []PlanTask{
					{ID: "task-1", Name: "First"},
					{ID: "task-2", Name: "Second"},
				},
			},
		},
	}

	tests := []struct {
		name   string
		taskID string
		found  bool
	}{
		{"existing task", "task-1", true},
		{"another existing", "task-2", true},
		{"non-existent", "task-99", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := plan.TaskByID(tt.taskID)
			if (result != nil) != tt.found {
				t.Errorf("TaskByID(%q) found = %v, want %v", tt.taskID, result != nil, tt.found)
			}
		})
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to TaskStatus
		want     bool
	}{
		{StatusPending, StatusAssigned, true},
		{StatusPending, StatusFailed, true},
		{StatusPending, StatusPaid, false},
		{StatusAssigned, StatusInProgress, true},
		{StatusInProgress, StatusReview, true},
		{StatusReview, StatusComplete, true},
		{StatusReview, StatusInProgress, true},
		{StatusComplete, StatusPaid, true},
		{StatusPaid, StatusPending, false},
		{StatusFailed, StatusPending, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestTransition_Invalid(t *testing.T) {
	err := Transition(StatusPending, StatusPaid)
	if err == nil {
		t.Error("expected error for invalid transition")
	}
}

func TestIsTerminal(t *testing.T) {
	if !IsTerminal(StatusPaid) {
		t.Error("StatusPaid should be terminal")
	}
	if IsTerminal(StatusComplete) {
		t.Error("StatusComplete should not be terminal")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultPaymentAmount != 100 {
		t.Errorf("DefaultPaymentAmount = %d, want 100", cfg.DefaultPaymentAmount)
	}
	if cfg.MonitorPollInterval != 5*time.Second {
		t.Errorf("MonitorPollInterval = %v, want 5s", cfg.MonitorPollInterval)
	}
}

func TestIsDeFiTask(t *testing.T) {
	tests := []struct {
		taskType string
		want     bool
	}{
		{"defi", true},
		{"trade", true},
		{"execute_trade", true},
		{"inference", false},
		{"", false},
		{"DEFI", false},
	}

	for _, tt := range tests {
		t.Run(tt.taskType, func(t *testing.T) {
			task := PlanTask{TaskType: tt.taskType}
			if got := isDeFiTask(task); got != tt.want {
				t.Errorf("isDeFiTask(%q) = %v, want %v", tt.taskType, got, tt.want)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				TaskTopicID:          hiero.TopicID{Topic: 1},
				StatusTopicID:        hiero.TopicID{Topic: 2},
				PaymentTokenID:       hiero.TokenID{Token: 1},
				TreasuryAccountID:    hiero.AccountID{Account: 100},
				DefaultPaymentAmount: 100,
			},
			wantErr: false,
		},
		{
			name: "missing task topic",
			config: Config{
				StatusTopicID:        hiero.TopicID{Topic: 2},
				PaymentTokenID:       hiero.TokenID{Token: 1},
				TreasuryAccountID:    hiero.AccountID{Account: 100},
				DefaultPaymentAmount: 100,
			},
			wantErr: true,
		},
		{
			name: "zero payment amount",
			config: Config{
				TaskTopicID:          hiero.TopicID{Topic: 1},
				StatusTopicID:        hiero.TopicID{Topic: 2},
				PaymentTokenID:       hiero.TokenID{Token: 1},
				TreasuryAccountID:    hiero.AccountID{Account: 100},
				DefaultPaymentAmount: 0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// mockPublisher records Publish calls for testing.
type mockPublisher struct {
	calls []hcs.Envelope
}

func (m *mockPublisher) Publish(_ context.Context, _ hiero.TopicID, msg hcs.Envelope) error {
	m.calls = append(m.calls, msg)
	return nil
}

func TestAssignTasks_CREDeniedTaskNotInAssignedIDs(t *testing.T) {
	// CRE server: deny task-defi-1, approve task-defi-2.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req creclient.RiskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		decision := creclient.RiskDecision{
			Approved:       req.TaskID != "task-defi-1",
			MaxPositionUSD: 1000_000000,
			MaxSlippageBps: 50,
			TTLSeconds:     300,
			Reason:         "test",
		}
		if !decision.Approved {
			decision.Reason = "denied by test"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(decision)
	}))
	defer srv.Close()

	pub := &mockPublisher{}
	a := NewAssigner(pub, hiero.TopicID{Topic: 1}, []string{"agent-1"})
	a.SetCREClient(creclient.New(srv.URL, 5*time.Second))

	plan := Plan{
		FestivalID: "test-fest",
		Sequences: []PlanSequence{
			{
				ID: "seq-1",
				Tasks: []PlanTask{
					{ID: "task-defi-1", TaskType: "defi", Name: "denied trade"},
					{ID: "task-defi-2", TaskType: "defi", Name: "approved trade"},
				},
			},
		},
	}

	assignedIDs, err := a.AssignTasks(context.Background(), plan)
	if err != nil {
		t.Fatalf("AssignTasks returned error: %v", err)
	}

	// Only the approved task should appear in assignedIDs.
	if len(assignedIDs) != 1 {
		t.Fatalf("expected 1 assigned ID, got %d: %v", len(assignedIDs), assignedIDs)
	}
	if assignedIDs[0] != "task-defi-2" {
		t.Errorf("expected assigned ID task-defi-2, got %s", assignedIDs[0])
	}

	// Only the approved task should be tracked in assignments.
	if a.AssignmentCount() != 1 {
		t.Errorf("expected AssignmentCount() == 1, got %d", a.AssignmentCount())
	}
	if got := a.Assignment("task-defi-1"); got != "" {
		t.Errorf("denied task should not be assigned, got agent %q", got)
	}
	if got := a.Assignment("task-defi-2"); got != "agent-1" {
		t.Errorf("approved task should be assigned to agent-1, got %q", got)
	}
}
