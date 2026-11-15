package autopilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dialWS(t *testing.T, srv *httptest.Server, origin string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{}
	if origin != "" {
		opts.HTTPHeader = http.Header{"Origin": []string{origin}}
	}
	c, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):], opts)
	if err == nil {
		c.CloseNow()
	}
	return err
}

// Bypass test: a page on another origin must not be able to open the event stream.
func TestWebSocketRejectsCrossOrigin(t *testing.T) {
	t.Setenv("AUTOPILOT_WS_ORIGINS", "")
	s := &server{hub: newHub()}
	srv := httptest.NewServer(http.HandlerFunc(s.serveWS))
	defer srv.Close()

	if err := dialWS(t, srv, "https://evil.example"); err == nil {
		t.Fatal("cross-origin WebSocket handshake must be refused")
	}
	if err := dialWS(t, srv, srv.URL); err != nil {
		t.Fatalf("same-origin handshake must work: %v", err)
	}
	if err := dialWS(t, srv, ""); err != nil {
		t.Fatalf("non-browser client without Origin must work: %v", err)
	}
}

func TestWebSocketExtraOriginsAreExplicit(t *testing.T) {
	t.Setenv("AUTOPILOT_WS_ORIGINS", "*, app.example.com")
	s := &server{hub: newHub()}
	srv := httptest.NewServer(http.HandlerFunc(s.serveWS))
	defer srv.Close()

	if err := dialWS(t, srv, "https://app.example.com"); err != nil {
		t.Fatalf("listed origin must work: %v", err)
	}
	if err := dialWS(t, srv, "https://evil.example"); err == nil {
		t.Fatal("a bare * must be ignored; other origins stay refused")
	}
}
