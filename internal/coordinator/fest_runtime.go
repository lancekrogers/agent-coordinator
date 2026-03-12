package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/festival"
	"github.com/lancekrogers/agent-coordinator-ethden-2026/internal/hedera/hcs"
)

// FestRuntime builds coordinator plans and dashboard snapshots from fest CLI output.
type FestRuntime struct {
	Reader            *festival.Reader
	InferenceAgentID  string
	DeFiAgentID       string
	StaleAfterSeconds int
	Logger            *slog.Logger
}

func NewFestRuntime(reader *festival.Reader, inferenceAgentID, defiAgentID string, staleAfterSeconds int, logger *slog.Logger) *FestRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	if staleAfterSeconds <= 0 {
		staleAfterSeconds = 30
	}
	return &FestRuntime{
		Reader:            reader,
		InferenceAgentID:  inferenceAgentID,
		DeFiAgentID:       defiAgentID,
		StaleAfterSeconds: staleAfterSeconds,
		Logger:            logger,
	}
}

func (r *FestRuntime) LoadPlan(ctx context.Context) (Plan, string, festival.ProgressSnapshot, error) {
	selector, roadmap, err := r.Reader.Load(ctx)
	if err != nil {
		return Plan{}, "", festival.ProgressSnapshot{}, err
	}

	execPlan := festival.BuildExecutionPlan(roadmap)
	plan := mapExecutionPlan(execPlan, r.InferenceAgentID, r.DeFiAgentID)
	snapshot := festival.BuildProgressSnapshot(roadmap, selector, r.StaleAfterSeconds)

	if plan.TaskCount() == 0 {
		return Plan{}, selector, snapshot, fmt.Errorf("fest plan has no executable non-gate tasks")
	}
	return plan, selector, snapshot, nil
}

func mapExecutionPlan(execPlan festival.ExecutionPlan, inferenceAgentID, defiAgentID string) Plan {
	plan := Plan{FestivalID: execPlan.FestivalID}
	for _, seq := range execPlan.Sequences {
		normSeq := PlanSequence{ID: seq.ID}
		for _, task := range seq.Tasks {
			if task.IsGate || task.Status == "completed" {
				continue
			}

			normTask := PlanTask{
				ID:            chooseTaskID(task),
				Name:          task.Name,
				Priority:      1,
				MaxTokens:     512,
				PaymentAmount: 100,
				Dependencies:  task.Dependencies,
			}

			if looksLikeDeFiTask(task) {
				normTask.TaskType = "execute_trade"
				normTask.AssignTo = defiAgentID
			} else {
				normTask.TaskType = "inference_job"
				normTask.AssignTo = inferenceAgentID
				normTask.ModelID = "qwen/qwen-2.5-7b-instruct"
				normTask.Input = fmt.Sprintf("Execute festival task: %s", task.Name)
			}
			normSeq.Tasks = append(normSeq.Tasks, normTask)
		}
		if len(normSeq.Tasks) > 0 {
			plan.Sequences = append(plan.Sequences, normSeq)
		}
	}
	return plan
}

func chooseTaskID(task festival.ExecutionTask) string {
	if strings.TrimSpace(task.ID) != "" {
		return task.ID
	}
	return task.Name
}

func looksLikeDeFiTask(task festival.ExecutionTask) bool {
	name := strings.ToLower(task.Name + " " + task.ID)
	return strings.Contains(name, "defi") ||
		strings.Contains(name, "trade") ||
		strings.Contains(name, "swap") ||
		strings.Contains(name, "pnl")
}

// FestProgressPublisher emits canonical festival progress snapshots to HCS.
type FestProgressPublisher struct {
	runtime        *FestRuntime
	publisher      hcs.MessagePublisher
	topicID        hiero.TopicID
	allowSynthetic bool
	pollInterval   time.Duration
	logger         *slog.Logger

	mu  sync.Mutex
	seq uint64
}

func NewFestProgressPublisher(runtime *FestRuntime, publisher hcs.MessagePublisher, topicID hiero.TopicID, pollInterval time.Duration, allowSynthetic bool, logger *slog.Logger) *FestProgressPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	return &FestProgressPublisher{
		runtime:        runtime,
		publisher:      publisher,
		topicID:        topicID,
		allowSynthetic: allowSynthetic,
		pollInterval:   pollInterval,
		logger:         logger,
	}
}

func (p *FestProgressPublisher) Start(ctx context.Context) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)

		if err := p.publishOnce(ctx); err != nil {
			errCh <- err
			return
		}

		ticker := time.NewTicker(p.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.publishOnce(ctx); err != nil {
					errCh <- err
					if !p.allowSynthetic {
						return
					}
				}
			}
		}
	}()
	return errCh
}

func (p *FestProgressPublisher) publishOnce(ctx context.Context) error {
	started := time.Now()
	outcome := "fest"

	plan, selector, snapshot, err := p.runtime.LoadPlan(ctx)
	if err != nil {
		if !p.allowSynthetic {
			return fmt.Errorf("load fest runtime state: %w", err)
		}
		outcome = "synthetic_fallback"
		snapshot = syntheticProgressSnapshot(err.Error(), p.runtime.StaleAfterSeconds)
		selector = snapshot.Selector
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal festival progress payload: %w", err)
	}

	env := hcs.Envelope{
		Type:        hcs.MessageTypeFestivalProgress,
		Sender:      "coordinator",
		SequenceNum: p.nextSeq(),
		Timestamp:   time.Now().UTC(),
		Payload:     payload,
	}

	if err := p.publisher.Publish(ctx, p.topicID, env); err != nil {
		return fmt.Errorf("publish festival progress: %w", err)
	}

	p.logger.Info("festival progress published",
		"outcome", outcome,
		"source", snapshot.Source,
		"selector", selector,
		"tasks", plan.TaskCount(),
		"fallback_reason", snapshot.FallbackReason,
		"duration_ms", time.Since(started).Milliseconds(),
	)
	return nil
}

func (p *FestProgressPublisher) nextSeq() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	return p.seq
}

func syntheticProgressSnapshot(reason string, staleAfterSeconds int) festival.ProgressSnapshot {
	now := time.Now().UTC()
	return festival.ProgressSnapshot{
		Version:           "v1",
		Source:            "synthetic",
		Selector:          "synthetic-fallback",
		SnapshotTime:      now,
		StaleAfterSeconds: staleAfterSeconds,
		FallbackReason:    reason,
		FestivalProgress: festival.FestivalProgress{
			FestivalID:               "synthetic",
			FestivalName:             "synthetic-fallback",
			OverallCompletionPercent: 0,
			Phases: []festival.FestivalPhase{
				{
					ID:                "001_IMPLEMENT",
					Name:              "IMPLEMENT",
					Status:            "active",
					CompletionPercent: 0,
					Sequences: []festival.FestivalSequence{
						{
							ID:                "01_fallback",
							Name:              "fallback",
							Status:            "active",
							CompletionPercent: 0,
							Tasks: []festival.FestivalTask{
								{ID: "01", Name: "synthetic_fallback_active", Status: "active", Autonomy: "medium"},
							},
						},
					},
				},
			},
		},
	}
}
