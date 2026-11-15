package autopilot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

func writeWith(h http.Handler, method, path, token, body string) int {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

// A write re-reads the account's CURRENT roles, so an operator who is demoted or
// deleted stops writing at once although the token (24 h) still lists the role.
// A read may still use the roles in the token.
func TestRealAuthWritesReadRolesFromTheDatabase(t *testing.T) {
	svc, s, h := realAuth(t)
	ctx := context.Background()
	id, tok := bearer(t, svc, "w@example.com", "autopilot:operator")
	i := seedIssue(t, s, "w", StatusBacklog)
	base := "/api/autopilot/issues/" + i.ID
	writes := func() []int {
		return []int{
			writeWith(h, "POST", base+"/status", tok, `{"status":"ready"}`),
			writeWith(h, "POST", base+"/comments", tok, `{"body":"x"}`),
			writeWith(h, "PATCH", base, tok, `{"title":"new"}`),
			writeWith(h, "POST", "/api/autopilot/issues", tok, `{"title":"n","kind":"bug"}`),
		}
	}

	for n, code := range writes() {
		if code != 200 && code != 201 {
			t.Fatalf("operator write #%d = %d, want success", n, code)
		}
	}

	if err := svc.SetRoles(ctx, id, []string{"autopilot:viewer"}); err != nil {
		t.Fatal(err)
	}
	for n, code := range writes() {
		if code != 403 {
			t.Errorf("demoted to viewer, write #%d = %d, want 403", n, code)
		}
	}
	if code := writeWith(h, "GET", base, tok, ""); code != 200 {
		t.Errorf("read with a token role = %d, want 200", code)
	}

	if err := svc.SetRoles(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	for n, code := range writes() {
		if code != 403 {
			t.Errorf("roles removed, write #%d = %d, want 403", n, code)
		}
	}

	// a valid token for an account that no longer exists
	ghost, err := svc.IssueToken(auth.Identity{ID: "ghost", Email: "g@example.com", Roles: []string{"autopilot:operator"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := writeWith(h, "POST", base+"/status", ghost, `{"status":"ready"}`); code != 401 {
		t.Errorf("deleted account write = %d, want 401", code)
	}
}


// realDB opens a second handle on the in-memory database that realAuth booted, so
// a test can write rows the auth plugin reads (a PAT) or break the store.
func realDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// patFor stores a personal access token for the account the way the auth plugin
// does (only the sha256 is kept) and returns the token to send.
func patFor(t *testing.T, db *sql.DB, userID, abilities string) string {
	t.Helper()
	tok := "togo_pat_" + strings.Repeat("ab12", 10)
	sum := sha256.Sum256([]byte(tok))
	_, err := db.Exec(`INSERT INTO personal_access_tokens (id, user_id, name, token_hash, abilities, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"pat1", userID, "ci", hex.EncodeToString(sum[:]), abilities, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// A personal access token is scoped to its abilities and carries no Autopilot
// role. The write path must NOT swap it for the account's full roles: a PAT of an
// operator account stays refused, on writes and on reads, and cannot hold a stream.
func TestRealAuthPATNeverGetsTheAccountRoles(t *testing.T) {
	svc, s, h := realAuth(t)
	id, jwt := bearer(t, svc, "pat-owner@example.com", "autopilot:operator")
	pat := patFor(t, realDB(t), id, "read:issues")
	i := seedIssue(t, s, "pat-owner", StatusBacklog)
	base := "/api/autopilot/issues/" + i.ID

	// control: the same account's session token still works
	if code := writeWith(h, "POST", base+"/comments", jwt, `{"body":"from the session"}`); code != 200 && code != 201 {
		t.Fatalf("operator session write = %d, want success", code)
	}

	for _, c := range []struct{ name, method, path, body string }{
		{"status", "POST", base + "/status", `{"status":"ready"}`},
		{"comment", "POST", base + "/comments", `{"body":"from the pat"}`},
		{"edit", "PATCH", base, `{"title":"hijacked"}`},
		{"create", "POST", "/api/autopilot/issues", `{"title":"n","kind":"bug"}`},
		{"read", "GET", base, ""},
	} {
		if code := writeWith(h, c.method, c.path, pat, c.body); code != 403 {
			t.Errorf("PAT %s = %d, want 403", c.name, code)
		}
	}

	got, ok := s.getIssueByID(context.Background(), i.ID)
	if !ok {
		t.Fatal("issue vanished")
	}
	if got.Status != StatusBacklog || got.Title == "hijacked" {
		t.Fatalf("a PAT changed the issue: status %q title %q", got.Status, got.Title)
	}
	if s.stillAllowed(context.Background(), withToken("GET", "/api/autopilot/ws", pat)) {
		t.Fatal("a PAT must not keep an open stream")
	}
}

// A store error while the account is re-read is a 503 (retry later), not a 401: the
// caller is not unauthenticated. Only an account the store no longer has is a 401.
func TestRealAuthStoreErrorIs503DeletedAccountIs401(t *testing.T) {
	svc, s, h := realAuth(t)
	_, tok := bearer(t, svc, "store@example.com", "autopilot:operator")
	i := seedIssue(t, s, "store", StatusBacklog)
	base := "/api/autopilot/issues/" + i.ID

	ghost, err := svc.IssueToken(auth.Identity{ID: "ghost", Email: "g@example.com", Roles: []string{"autopilot:operator"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := writeWith(h, "POST", base+"/status", ghost, `{"status":"ready"}`); code != 401 {
		t.Errorf("deleted account = %d, want 401", code)
	}

	if _, err := realDB(t).Exec(`ALTER TABLE users RENAME TO users_offline`); err != nil {
		t.Fatal(err)
	}
	if code := writeWith(h, "POST", base+"/status", tok, `{"status":"ready"}`); code != 503 {
		t.Errorf("store error on a write = %d, want 503", code)
	}
	if code := writeWith(h, "GET", base, tok, ""); code != 200 {
		t.Errorf("a read uses the token roles and needs no lookup = %d, want 200", code)
	}
	if got, ok := s.getIssueByID(context.Background(), i.ID); !ok || got.Status != StatusBacklog {
		t.Fatalf("a failed lookup must not change the issue: %v %v", got, ok)
	}
}
