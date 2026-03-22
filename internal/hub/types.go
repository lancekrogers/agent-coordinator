package hub

// DaemonEvent matches the dashboard's TypeScript DaemonEvent type exactly.
// See: projects/dashboard/src/lib/data/types.ts
type DaemonEvent struct {
	Type      string                 `json:"type"`
	AgentID   string                 `json:"agentId"`
	AgentName string                 `json:"agentName"`
	Timestamp string                 `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload"`
}

// Publisher is the interface agents use to emit events to the WebSocket hub.
// The hub implements this; agents receive it via dependency injection.
type Publisher interface {
	Publish(event DaemonEvent)
}
