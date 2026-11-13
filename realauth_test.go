package autopilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"
	"github.com/togo-framework/togo"
)

// realAuth boots a real kernel with the auth plugin (in-memory SQLite) and mounts
// Autopilot on it WITHOUT the identity test seam: requests go through
// auth.Service.Middleware / Authenticate / UserByID exactly as in production.
func realAuth(t *testing.T) (*auth.Service, *server, http.Handler) {
	t.Helper()
	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("DATABASE_URL", "file:"+strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())+"?mode=memory&cache=shared")
	t.Setenv("AUTH_SECRET", "a-sufficiently-long-test-secret-for-autopilot!")
	t.Setenv("APP_ENV", "development")
	k := togo.New()
	t.Cleanup(func() { k.Close() })
	svc, ok := auth.FromKernel(k)
	if !ok {
		t.Fatal("auth provider did not register")
	}
	s := newTestServer(t)
	s.hub = newHub()
	s.auth = svc
	r := chi.NewRouter()
	s.mount(r)
	return svc, s, r
}

func bearer(t *testing.T, svc *auth.Service, email string, roles ...string) (id string, token string) {
	t.Helper()
	u, err := svc.CreateUser(context.Background(), email, "correct-horse-battery-1", roles)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := svc.IssueToken(*u)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID, tok
}

func withToken(method, path, token string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// The route guards against the real plugin: no token 401, no Autopilot role 403,
// role 200.
func TestRealAuthRouteGuards(t *testing.T) {
	svc, _, h := realAuth(t)
	_, viewer := bearer(t, svc, "v@example.com", "autopilot:viewer")
	_, plain := bearer(t, svc, "p@example.com", "admin", "viewer")

	for _, c := range []struct {
		name, token string
		want        int
	}{{"no token", "", 401}, {"garbage token", "not-a-jwt", 401}, {"admin/viewer without prefix", plain, 403}, {"autopilot:viewer", viewer, 200}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, withToken("GET", "/api/autopilot/issues", c.token))
		if w.Code != c.want {
			t.Errorf("%s = %d, want %d", c.name, w.Code, c.want)
		}
	}
}

// stillAllowed uses Authenticate (token validity) and UserByID (CURRENT roles).
// The token still carries the old role; the database decides.
func TestRealAuthStillAllowedReadsCurrentRoles(t *testing.T) {
	svc, s, _ := realAuth(t)
	ctx := context.Background()
	id, tok := bearer(t, svc, "op@example.com", "autopilot:operator")
	req := func() *http.Request { return withToken("GET", "/api/autopilot/ws", tok) }

	if !s.stillAllowed(ctx, req()) {
		t.Fatal("operator must be allowed")
	}
	if err := svc.SetRoles(ctx, id, []string{"member"}); err != nil {
		t.Fatal(err)
	}
	if s.stillAllowed(ctx, req()) {
		t.Fatal("role revoked in the database must end access although the token still lists it")
	}
	if err := svc.SetRoles(ctx, id, []string{"autopilot:viewer"}); err != nil {
		t.Fatal(err)
	}
	if !s.stillAllowed(ctx, req()) {
		t.Fatal("a viewer role granted later must be allowed")
	}
	if s.stillAllowed(ctx, withToken("GET", "/", "")) {
		t.Fatal("no token must not be allowed")
	}
	// a valid token for an account that does not exist (deleted)
	ghost, err := svc.IssueToken(auth.Identity{ID: "ghost", Email: "g@example.com", Roles: []string{"autopilot:operator"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.stillAllowed(ctx, withToken("GET", "/", ghost)) {
		t.Fatal("a token for a missing account must not be allowed")
	}
}

// End to end: an open stream authenticated with a real token is closed after the
// role is removed in the database.
func TestRealAuthWebSocketClosesOnRevoke(t *testing.T) {
	t.Setenv("AUTOPILOT_WS_ORIGINS", "")
	old := wsRecheckEvery
	wsRecheckEvery = 40 * time.Millisecond
	t.Cleanup(func() { wsRecheckEvery = old })

	svc, _, h := realAuth(t)
	id, tok := bearer(t, svc, "ws@example.com", "autopilot:viewer")
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/autopilot/ws",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if _, _, err := c.Read(ctx); err != nil { // hello
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // several re-checks while the role is held
	if err := svc.SetRoles(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
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
		t.Fatal("stream still open after the role was removed in the database")
	}
}
