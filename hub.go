package autopilot

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// hub fans real-time events out to every connected WebSocket client. Events are
// emitted whenever an issue is created/updated/moved or a comment is added, so
// the board + the floating launcher update live instead of polling.
type hub struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
}

type wsClient struct {
	send chan []byte
}

func newHub() *hub { return &hub{clients: map[*wsClient]struct{}{}} }

func (h *hub) add(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(c *wsClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

func (h *hub) broadcast(msg []byte) {
	h.mu.Lock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default: // slow client — drop this event rather than block the whole hub
		}
	}
	h.mu.Unlock()
}

// emit marshals a typed event and broadcasts it. Safe to call with a nil hub.
func (s *server) emit(event string, fields map[string]any) {
	if s.hub == nil {
		return
	}
	m := map[string]any{"type": event, "at": nowStr()}
	for k, v := range fields {
		m[k] = v
	}
	if b, err := json.Marshal(m); err == nil {
		s.hub.broadcast(b)
	}
}

// serveWS upgrades to a WebSocket and streams events until the client leaves.
func (s *server) serveWS(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		http.Error(w, "realtime not available", http.StatusServiceUnavailable)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer c.CloseNow()

	client := &wsClient{send: make(chan []byte, 32)}
	s.hub.add(client)
	defer s.hub.remove(client)

	ctx := r.Context()
	// reader: detect close/keepalive; we don't accept inbound messages.
	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()

	// greet, then pump events with a periodic ping so idle conns stay alive.
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"hello"}`))
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-client.send:
			if !ok {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
