package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator/internal/festival"
	"github.com/lancekrogers/agent-coordinator/internal/hedera/hcs"
)

type festCommandResult struct {
	stdout []byte
	stderr []byte
	err    error
}

type festCommandRunner struct {
	responses map[string]festCommandResult
}

func (m festCommandRunner) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
	key := strings.Join(args, " ")
	res, ok := m.responses[key]
	if !ok {
		return nil, nil, errors.New("unexpected command: " + key)
	}
	return res.stdout, res.stderr, res.err
}

type notFoundExecError struct{}

func (notFoundExecError) Error() string { return "executable file not found in $PATH" }

type capturePublisher struct {
	messages []hcs.Envelope
	err      error
}

func (p *capturePublisher) Publish(_ context.Context, _ hiero.TopicID, msg hcs.Envelope) error {
	if p.err != nil {
		return p.err
	}
	p.messages = append(p.messages, msg)
	return nil
}

func festFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fest", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func testTopicID(t *testing.T) hiero.TopicID {
	t.Helper()
	id, err := hiero.TopicIDFromString("0.0.123")
	if err != nil {
		t.Fatalf("parse topic id: %v", err)
	}
	return id
}

func testRuntime(t *testing.T, runner festCommandRunner) *FestRuntime {
	t.Helper()
	reader := festival.NewReader(festival.ReaderConfig{}, nil)
	reader.SetRunner(runner)
	return NewFestRuntime(reader, "inference-001", "defi-001", 30, nil)
}

func TestFestRuntimeLoadPlan(t *testing.T) {
	runtime := testRuntime(t, festCommandRunner{
		responses: map[string]festCommandResult{
			"show all --json": {stdout: festFixture(t, "show_all_no_active.json")},
			"show --festival fest-ready --json --roadmap": {stdout: festFixture(t, "show_roadmap_valid.json")},
		},
	})

	plan, selector, snapshot, err := runtime.LoadPlan(context.Background())
	if err != nil {
		t.Fatalf("LoadPlan error: %v", err)
	}
	if selector != "fest-ready" {
		t.Fatalf("selector = %q, want fest-ready", selector)
	}
	if snapshot.Source != "fest" {
		t.Fatalf("snapshot source = %q, want fest", snapshot.Source)
	}
	if plan.TaskCount() != 1 {
		t.Fatalf("task count = %d, want 1", plan.TaskCount())
	}

	task := plan.Sequences[0].Tasks[0]
	if task.AssignTo != "inference-001" {
		t.Fatalf("assign_to = %q, want inference-001", task.AssignTo)
	}
	if task.TaskType != "inference_job" {
		t.Fatalf("task_type = %q, want inference_job", task.TaskType)
	}
}

func TestFestProgressPublisherPublishOnce_UsesFestSource(t *testing.T) {
	runtime := testRuntime(t, festCommandRunner{
		responses: map[string]festCommandResult{
			"show all --json": {stdout: festFixture(t, "show_all_no_active.json")},
			"show --festival fest-ready --json --roadmap": {stdout: festFixture(t, "show_roadmap_valid.json")},
		},
	})
	pub := &capturePublisher{}
	publisher := NewFestProgressPublisher(runtime, pub, testTopicID(t), 10*time.Second, false, nil)

	if err := publisher.publishOnce(context.Background()); err != nil {
		t.Fatalf("publishOnce error: %v", err)
	}
	if len(pub.messages) != 1 {
		t.Fatalf("published messages = %d, want 1", len(pub.messages))
	}
	if pub.messages[0].Type != hcs.MessageTypeFestivalProgress {
		t.Fatalf("message type = %q, want %q", pub.messages[0].Type, hcs.MessageTypeFestivalProgress)
	}

	var payload festival.ProgressSnapshot
	if err := json.Unmarshal(pub.messages[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Source != "fest" {
		t.Fatalf("payload source = %q, want fest", payload.Source)
	}
	if payload.Selector != "fest-ready" {
		t.Fatalf("payload selector = %q, want fest-ready", payload.Selector)
	}
	if payload.FallbackReason != "" {
		t.Fatalf("fallback_reason = %q, want empty", payload.FallbackReason)
	}
}

func TestFestProgressPublisherPublishOnce_UsesSyntheticFallback(t *testing.T) {
	runtime := testRuntime(t, festCommandRunner{
		responses: map[string]festCommandResult{
			"show all --json": {err: notFoundExecError{}},
		},
	})
	pub := &capturePublisher{}
	publisher := NewFestProgressPublisher(runtime, pub, testTopicID(t), 10*time.Second, true, nil)

	if err := publisher.publishOnce(context.Background()); err != nil {
		t.Fatalf("publishOnce error: %v", err)
	}
	if len(pub.messages) != 1 {
		t.Fatalf("published messages = %d, want 1", len(pub.messages))
	}

	var payload festival.ProgressSnapshot
	if err := json.Unmarshal(pub.messages[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Source != "synthetic" {
		t.Fatalf("payload source = %q, want synthetic", payload.Source)
	}
	if payload.FallbackReason == "" {
		t.Fatalf("fallback_reason is empty, want non-empty")
	}
}

func TestFestProgressPublisherPublishOnce_FailsWithoutSyntheticFallback(t *testing.T) {
	runtime := testRuntime(t, festCommandRunner{
		responses: map[string]festCommandResult{
			"show all --json": {err: notFoundExecError{}},
		},
	})
	pub := &capturePublisher{}
	publisher := NewFestProgressPublisher(runtime, pub, testTopicID(t), 10*time.Second, false, nil)

	err := publisher.publishOnce(context.Background())
	if err == nil {
		t.Fatalf("publishOnce error = nil, want error")
	}
	if !strings.Contains(err.Error(), "load fest runtime state") {
		t.Fatalf("error = %q, want load fest runtime state", err.Error())
	}
	if len(pub.messages) != 0 {
		t.Fatalf("published messages = %d, want 0", len(pub.messages))
	}
}
