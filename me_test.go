package autopilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/togo-framework/auth"
)

func getMe(h http.Handler, token string) (int, map[string]any, http.Header) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, withToken("GET", "/api/autopilot/me", token))
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body, w.Header()
}

// GET /me answers the caller's level from the database (UserByID), 200 for every
// signed-in account, "none" instead of 403, and never names a role or an e-mail.
func TestMeReportsTheCurrentLevel(t *testing.T) {
	svc, _, h := realAuth(t)
	for _, c := range []struct {
		email string
		roles []string
		level string
	}{
		{"op@example.com", []string{"autopilot:operator"}, "operator"},
		{"rep@example.com", []string{"autopilot:reporter"}, "reporter"},
		{"view@example.com", []string{"autopilot:viewer"}, "viewer"},
		{"none@example.com", nil, "none"},
		{"admin@example.com", []string{"admin", "operator", "viewer"}, "none"}, // names without the prefix grant nothing
	} {
		id, tok := bearer(t, svc, c.email, c.roles...)
		code, body, hdr := getMe(h, tok)
		if code != 200 {
			t.Fatalf("%s: /me = %d, want 200", c.email, code)
		}
		if body["level"] != c.level || body["source"] != "database" || body["id"] != id {
			t.Errorf("%s: got %v, want level %q source database id %q", c.email, body, c.level, id)
		}
		if len(body) != 3 {
			t.Errorf("%s: response has %d fields (%v), want only id, level, source", c.email, len(body), body)
		}
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "@") || strings.Contains(string(raw), "autopilot:") || strings.Contains(string(raw), "roles") {
			t.Errorf("%s: response leaks an e-mail or a role: %s", c.email, raw)
		}
		if hdr.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", c.email, hdr.Get("Cache-Control"))
		}
	}
}

// The token still lists the old role after a demotion; /me must follow the
// database within one request (the same as a write), in both directions.
func TestMeFollowsARoleChangeAtOnce(t *testing.T) {
	svc, _, h := realAuth(t)
	ctx := context.Background()
	id, tok := bearer(t, svc, "chg@example.com", "autopilot:operator")

	step := func(want string) {
		t.Helper()
		code, body, _ := getMe(h, tok)
		if code != 200 || body["level"] != want {
			t.Fatalf("/me = %d %v, want 200 level %q", code, body, want)
		}
	}
	step("operator")
	if err := svc.SetRoles(ctx, id, []string{"autopilot:viewer"}); err != nil {
		t.Fatal(err)
	}
	step("viewer")
	if err := svc.SetRoles(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	step("none") // 200 with level none, not 403
	if err := svc.SetRoles(ctx, id, []string{"autopilot:reporter"}); err != nil {
		t.Fatal(err)
	}
	step("reporter")
}

// A personal access token is never mapped to the account's roles: level none,
// read from the token. The same account's session token still reports operator.
func TestMePATIsNoneEvenForAnOperatorAccount(t *testing.T) {
	svc, _, h := realAuth(t)
	id, jwt := bearer(t, svc, "pat-me@example.com", "autopilot:operator")
	pat := patFor(t, realDB(t), id, "read:issues")

	code, body, _ := getMe(h, pat)
	if code != 200 || body["level"] != "none" || body["source"] != "token" {
		t.Fatalf("PAT /me = %d %v, want 200 level none source token", code, body)
	}
	if code, body, _ := getMe(h, jwt); code != 200 || body["level"] != "operator" {
		t.Fatalf("session /me = %d %v, want operator", code, body)
	}
}

// 401 without a usable session or for a deleted account, 503 when the account
// store cannot be read (not an authentication failure). Every one of them carries
// Cache-Control: no-store, like the 200, so a shared cache cannot keep an error.
func TestMeErrorCodes(t *testing.T) {
	svc, _, h := realAuth(t)
	_, tok := bearer(t, svc, "codes@example.com", "autopilot:operator")

	for name, token := range map[string]string{"no token": "", "garbage token": "not-a-jwt"} {
		code, _, hdr := getMe(h, token)
		if code != 401 {
			t.Errorf("%s: /me = %d, want 401", name, code)
		}
		wantNoStore(t, name, hdr)
	}
	// a valid token for an account that no longer exists
	ghost, err := svc.IssueToken(auth.Identity{ID: "ghost", Email: "g@example.com", Roles: []string{"autopilot:operator"}})
	if err != nil {
		t.Fatal(err)
	}
	code, _, hdr := getMe(h, ghost)
	if code != 401 {
		t.Errorf("deleted account: /me = %d, want 401", code)
	}
	wantNoStore(t, "deleted account", hdr)
	if _, err := realDB(t).Exec(`ALTER TABLE users RENAME TO users_offline`); err != nil {
		t.Fatal(err)
	}
	code, _, hdr = getMe(h, tok)
	if code != 503 {
		t.Errorf("store error: /me = %d, want 503", code)
	}
	wantNoStore(t, "store error", hdr)
}

func wantNoStore(t *testing.T, name string, hdr http.Header) {
	t.Helper()
	if got := hdr.Get("Cache-Control"); got != "no-store" {
		t.Errorf("%s: Cache-Control = %q, want no-store", name, got)
	}
}

// Without the auth plugin (development-only opt-out) the caller is a local operator.
func TestMeDevOptOutIsLocalOperator(t *testing.T) {
	s := newTestServer(t)
	w := httptest.NewRecorder()
	s.me(w, httptest.NewRequest("GET", "/api/autopilot/me", nil))
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 200 || body["level"] != "operator" {
		t.Fatalf("dev /me = %d %v, want 200 operator", w.Code, body)
	}
}
