package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

// Client represents a connected WebSocket dashboard client.
type Client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub manages WebSocket client connections and broadcasts DaemonEvents.
type Hub struct {
	clients    map[*Client]bool
	mu         sync.RWMutex
	broadcast  chan DaemonEvent
	register   chan *Client
	unregister chan *Client
	log        *slog.Logger
}

// New creates a new Hub ready to accept connections and broadcast events.
func New(log *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan DaemonEvent, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		log:        log,
	}
}

// Run starts the hub's main loop. It blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			h.log.Info("client connected", "clients", h.ClientCount())

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				close(client.send)
				delete(h.clients, client)
			}
			h.mu.Unlock()
			h.log.Info("client disconnected", "clients", h.ClientCount())

		case event := <-h.broadcast:
			data, err := json.Marshal(event)
			if err != nil {
				h.log.Error("failed to marshal event", "error", err)
				continue
			}

			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- data:
				default:
					// Client too slow — drop it
					go func(c *Client) { h.unregister <- c }(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Publish sends an event to all connected clients. Implements Publisher.
func (h *Hub) Publish(event DaemonEvent) {
	select {
	case h.broadcast <- event:
	default:
		h.log.Warn("broadcast channel full, dropping event", "type", event.Type)
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// RegisterClient adds a new WebSocket connection to the hub.
func (h *Hub) RegisterClient(conn *websocket.Conn) *Client {
	client := &Client{
		conn: conn,
		send: make(chan []byte, 64),
	}
	h.register <- client
	return client
}

// UnregisterClient removes a client from the hub.
func (h *Hub) UnregisterClient(client *Client) {
	h.unregister <- client
}

// WritePump sends queued messages to the WebSocket connection.
// Call this in a goroutine per client.
func (h *Hub) WritePump(ctx context.Context, client *Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-client.send:
			if !ok {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := client.conn.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				h.unregister <- client
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := client.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				h.unregister <- client
				return
			}
		}
	}
}
