package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"nhooyr.io/websocket"
)

// NewServer creates an HTTP server with WebSocket (/ws) and health (/health) endpoints.
func NewServer(ctx context.Context, h *Hub, addr string, log *slog.Logger) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			log.Error("websocket accept failed", "error", err)
			return
		}

		client := h.RegisterClient(conn)
		defer h.UnregisterClient(client)

		// WritePump handles outgoing messages and pings.
		go h.WritePump(ctx, client)

		// ReadPump: keep connection alive by reading (handles pong internally).
		for {
			_, _, err := conn.Read(ctx)
			if err != nil {
				return
			}
		}
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"status":  "ok",
			"clients": h.ClientCount(),
		}
		json.NewEncoder(w).Encode(resp)
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	return srv
}

// ListenAndServe starts the WebSocket server. Blocks until error or shutdown.
func ListenAndServe(ctx context.Context, h *Hub, addr string, log *slog.Logger) error {
	srv := NewServer(ctx, h, addr, log)
	log.Info("websocket server starting", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("websocket server: %w", err)
	}
	return nil
}
