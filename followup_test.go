package autopilot

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/togo-framework/auth"
)

func uploadFrom(h http.Handler, path, origin string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "a.txt")
	fw.Write([]byte("hello"))
	mw.Close()
	req := httptest.NewRequest("POST", path, &buf) // Host is example.com
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-User", "op")
	req.Header.Set("X-Roles", "autopilot:operator")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// The upload is multipart, so jsonOnly cannot protect it. A browser request from
// another origin (including a sibling subdomain, which SameSite=Lax allows) must
// be refused and must not store anything.
func TestUploadRefusesCrossOrigin(t *testing.T) {
	s, h := rolesHarness(t)
	i := seedIssue(t, s, "op", StatusBacklog)
	path := "/api/autopilot/issues/" + i.ID + "/attachments"

	for _, origin := range []string{"https://evil.example", "https://evil.example.com", "http://app.example.com", "null", "::bad::"} {
		if w := uploadFrom(h, path, origin); w.Code != 403 {
			t.Errorf("Origin %q = %d, want 403", origin, w.Code)
		}
	}
	if n := len(s.attachmentsFor(context.Background(), i.ID)); n != 0 {
		t.Fatalf("a refused upload stored %d attachment(s)", n)
	}
	for _, origin := range []string{"http://example.com", "https://example.com", ""} {
		if w := uploadFrom(h, path, origin); w.Code != 201 && w.Code != 200 {
			t.Errorf("Origin %q = %d, want success: %s", origin, w.Code, w.Body)
		}
	}
	if n := len(s.attachmentsFor(context.Background(), i.ID)); n != 3 {
		t.Fatalf("stored %d attachments, want 3", n)
	}
}

// An open event stream ends when the account loses its Autopilot role, instead of
// streaming until the client leaves.
func TestWebSocketClosesWhenRoleIsRevoked(t *testing.T) {
	t.Setenv("AUTOPILOT_WS_ORIGINS", "")
	old := wsRecheckEvery
	wsRecheckEvery = 40 * time.Millisecond
	t.Cleanup(func() { wsRecheckEvery = old })

	s, h := rolesHarness(t)
	var revoked atomic.Bool
	s.identity = func(r *http.Request) (*auth.Identity, bool) {
		if revoked.Load() {
			return &auth.Identity{ID: "v", Roles: []string{"member"}}, true // signed in, role gone
		}
		return &auth.Identity{ID: "v", Roles: []string{"autopilot:viewer"}}, true
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/autopilot/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, _, err := c.Read(ctx); err != nil { // hello
		t.Fatal(err)
	}

	// still allowed after several re-checks: events keep flowing
	time.Sleep(200 * time.Millisecond)
	s.emit("ping.test", nil)
	if _, msg, err := c.Read(ctx); err != nil || !strings.Contains(string(msg), "ping.test") {
		t.Fatalf("stream should stay open while the role is held: %v %s", err, msg)
	}

	revoked.Store(true)
	done := make(chan error, 1)
	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case err := <-done:
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("closed with %v, want policy violation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream is still open after the role was revoked")
	}
}
