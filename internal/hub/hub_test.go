package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestHubBroadcast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log := slog.Default()
	h := New(log)
	go h.Run(ctx)

	srv := httptest.NewServer(nil)
	defer srv.Close()

	// Start WebSocket server
	wsSrv := NewServer(ctx, h, ":0", log)
	go wsSrv.ListenAndServe()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Publish an event
	event := DaemonEvent{
		Type:      "heartbeat",
		AgentID:   "test-001",
		AgentName: "test",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload:   map[string]interface{}{"status": "running"},
	}

	h.Publish(event)

	// Verify client count starts at 0
	if h.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", h.ClientCount())
	}
}

func TestDaemonEventJSON(t *testing.T) {
	event := DaemonEvent{
		Type:      "task_result",
		AgentID:   "defi-001",
		AgentName: "defi",
		Timestamp: "2026-03-22T12:00:00Z",
		Payload: map[string]interface{}{
			"txHash": "0xabc123",
			"pair":   "ETH/USDC",
			"pnl":    42.5,
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"agentId"`) {
		t.Error("JSON should use agentId (camelCase)")
	}
	if !strings.Contains(jsonStr, `"agentName"`) {
		t.Error("JSON should use agentName (camelCase)")
	}
	if !strings.Contains(jsonStr, `"defi-001"`) {
		t.Error("JSON should contain agent ID value")
	}
}

func TestPublisherInterface(t *testing.T) {
	// Verify Hub implements Publisher
	var _ Publisher = (*Hub)(nil)
}

// Suppress unused import warning for websocket
var _ = websocket.StatusNormalClosure
