package autopilot

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"
)

// rolesHarness mounts the real routes. The account comes from two test headers:
// X-User (id; empty means no session) and X-Roles (comma separated auth roles).
func rolesHarness(t *testing.T) (*server, http.Handler) {
	t.Helper()
	s := newTestServer(t)
	s.hub = newHub()
	s.identity = func(r *http.Request) (*auth.Identity, bool) {
		u := r.Header.Get("X-User")
		if u == "" {
			return nil, false
		}
		var roles []string
		for _, x := range strings.Split(r.Header.Get("X-Roles"), ",") {
			if x != "" {
				roles = append(roles, x)
			}
		}
		return &auth.Identity{ID: u, Email: u + "@example.test", Roles: roles}, true
	}
	r := chi.NewRouter()
	s.mount(r)
	return s, r
}

func call(h http.Handler, method, path, user, roles string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		req.Header.Set("X-User", user)
	}
	req.Header.Set("X-Roles", roles)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func upload(h http.Handler, path, user, roles string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "a.txt")
	fw.Write([]byte("hello"))
	mw.Close()
	req := httptest.NewRequest("POST", path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if user != "" {
		req.Header.Set("X-User", user)
	}
	req.Header.Set("X-Roles", roles)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func seedIssue(t *testing.T, s *server, createdBy, status string) Issue {
	t.Helper()
	i := Issue{ID: genID(), Title: "t", Status: status, Kind: "bug", CreatedBy: createdBy, CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.insertIssue(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	return i
}

// Every route, as seen by: no session, a signed-in user without an Autopilot
// role, and a user whose roles only look similar.
func TestRolesNoSessionAndNoRoleAreRefused(t *testing.T) {
	s, h := rolesHarness(t)
	i := seedIssue(t, s, "u1", StatusBacklog)
	id := i.ID
	routes := []struct{ method, path string }{
		{"GET", "/api/autopilot/issues"},
		{"POST", "/api/autopilot/issues"},
		{"GET", "/api/autopilot/issues/" + id},
		{"PATCH", "/api/autopilot/issues/" + id},
		{"POST", "/api/autopilot/issues/" + id + "/status"},
		{"GET", "/api/autopilot/issues/" + id + "/comments"},
		{"POST", "/api/autopilot/issues/" + id + "/comments"},
		{"POST", "/api/autopilot/issues/" + id + "/attachments"},
		{"GET", "/api/autopilot/issues/" + id + "/attachments"},
		{"GET", "/api/autopilot/uploads/x"},
		{"GET", "/api/autopilot/ws"},
	}
	for _, rt := range routes {
		if w := call(h, rt.method, rt.path, "", "", map[string]string{"title": "x", "body": "x"}); w.Code != 401 {
			t.Errorf("%s %s without a session = %d, want 401", rt.method, rt.path, w.Code)
		}
		// signed in, but with no role, with the bare names, or with another prefix
		for _, roles := range []string{"", "operator", "admin,viewer,reporter", "other:operator", "autopilot-operator", "Autopilot:Operator"} {
			if w := call(h, rt.method, rt.path, "u1", roles, map[string]string{"title": "x", "body": "x"}); w.Code != 403 {
				t.Errorf("%s %s with roles %q = %d, want 403", rt.method, rt.path, roles, w.Code)
			}
		}
	}
}

func TestViewerReadsButCannotWrite(t *testing.T) {
	s, h := rolesHarness(t)
	i := seedIssue(t, s, "u1", StatusBacklog)
	base := "/api/autopilot/issues/" + i.ID
	for _, p := range []string{"/api/autopilot/issues", base, base + "/comments", base + "/attachments"} {
		if w := call(h, "GET", p, "v", "autopilot:viewer", nil); w.Code != 200 {
			t.Errorf("viewer GET %s = %d, want 200", p, w.Code)
		}
	}
	writes := []struct{ method, path string }{
		{"POST", "/api/autopilot/issues"}, {"PATCH", base}, {"POST", base + "/status"},
		{"POST", base + "/comments"}, {"POST", base + "/attachments"},
	}
	for _, rt := range writes {
		if w := call(h, rt.method, rt.path, "v", "autopilot:viewer", map[string]string{"title": "x", "body": "x", "status": "ready"}); w.Code != 403 {
			t.Errorf("viewer %s %s = %d, want 403", rt.method, rt.path, w.Code)
		}
	}
	if w := upload(h, base+"/attachments", "v", "autopilot:viewer"); w.Code != 403 {
		t.Errorf("viewer upload = %d, want 403", w.Code)
	}
}

func TestReporterCannotSteerTheAgent(t *testing.T) {
	s, h := rolesHarness(t)
	const rep = "autopilot:reporter"

	// status, assignee, human_only and created_by from the client are ignored
	w := call(h, "POST", "/api/autopilot/issues", "r1", rep, map[string]any{
		"title": "mine", "status": "ready", "assignee": "agent", "human_only": true, "created_by": "someone-else"})
	if w.Code != 201 {
		t.Fatalf("reporter create = %d: %s", w.Code, w.Body)
	}
	var created Issue
	json.Unmarshal(w.Body.Bytes(), &created)
	got, _ := s.getIssueByID(context.Background(), created.ID)
	if got.Status != StatusBacklog || got.Assignee != "" || got.HumanOnly || got.CreatedBy != "r1" {
		t.Fatalf("reporter issue stored as status=%q assignee=%q human_only=%v created_by=%q",
			got.Status, got.Assignee, got.HumanOnly, got.CreatedBy)
	}
	base := "/api/autopilot/issues/" + created.ID

	// own issue, backlog: title and body only
	if w := call(h, "PATCH", base, "r1", rep, map[string]string{"title": "reworded"}); w.Code != 200 {
		t.Errorf("reporter edits own backlog title = %d, want 200", w.Code)
	}
	for _, f := range []map[string]any{{"priority": "high"}, {"assignee": "agent"}, {"kind": "bug"}, {"human_only": false}} {
		if w := call(h, "PATCH", base, "r1", rep, f); w.Code != 403 {
			t.Errorf("reporter patch %v = %d, want 403", f, w.Code)
		}
	}
	// someone else's issue
	other := seedIssue(t, s, "u2", StatusBacklog)
	if w := call(h, "PATCH", "/api/autopilot/issues/"+other.ID, "r1", rep, map[string]string{"title": "x"}); w.Code != 403 {
		t.Errorf("reporter edits another's issue = %d, want 403", w.Code)
	}
	if w := call(h, "POST", "/api/autopilot/issues/"+other.ID+"/comments", "r1", rep, map[string]string{"body": "x"}); w.Code != 403 {
		t.Errorf("reporter comments on another's issue = %d, want 403", w.Code)
	}
	// once an operator has moved it, the reporter can no longer edit it
	s.setIssueStatus(context.Background(), created.ID, StatusReady, nil)
	if w := call(h, "PATCH", base, "r1", rep, map[string]string{"title": "late"}); w.Code != 403 {
		t.Errorf("reporter edits own non-backlog issue = %d, want 403", w.Code)
	}
	// no status change, no upload
	if w := call(h, "POST", base+"/status", "r1", rep, map[string]string{"Status": "ready"}); w.Code != 403 {
		t.Errorf("reporter status = %d, want 403", w.Code)
	}
	if w := upload(h, base+"/attachments", "r1", rep); w.Code != 403 {
		t.Errorf("reporter upload = %d, want 403", w.Code)
	}
}

// A reporter's comment is stored but does not hand a blocked issue back to the
// agent, and the author fields in the body are ignored.
func TestReporterCommentDoesNotUnblockAndAuthorIsFromSession(t *testing.T) {
	s, h := rolesHarness(t)
	i := seedIssue(t, s, "r1", StatusBlocked)
	path := "/api/autopilot/issues/" + i.ID + "/comments"

	w := call(h, "POST", path, "r1", "autopilot:reporter", map[string]string{
		"body": "more info", "author": "boss", "AuthorKind": "agent", "author_kind": "agent"})
	if w.Code != 201 {
		t.Fatalf("reporter comment on own issue = %d: %s", w.Code, w.Body)
	}
	cur, _ := s.getIssueByID(context.Background(), i.ID)
	if cur.Status != StatusBlocked {
		t.Errorf("reporter comment moved the issue to %q, want it to stay blocked", cur.Status)
	}
	cs := s.commentsFor(context.Background(), i.ID)
	if len(cs) != 1 || cs[0].AuthorKind != "human" || cs[0].Author != "r1@example.test" {
		t.Fatalf("stored comment = %+v, want human by r1@example.test", cs)
	}

	// an operator's comment does unblock
	if w := call(h, "POST", path, "op", "autopilot:operator", map[string]string{"body": "use postgres"}); w.Code != 201 {
		t.Fatalf("operator comment = %d", w.Code)
	}
	cur, _ = s.getIssueByID(context.Background(), i.ID)
	if cur.Status != StatusReady {
		t.Errorf("operator comment left the issue %q, want ready", cur.Status)
	}
}

func TestOperatorFullFlow(t *testing.T) {
	s, h := rolesHarness(t)
	const op = "autopilot:operator"
	w := call(h, "POST", "/api/autopilot/issues", "op", op, map[string]any{"title": "do it", "status": "ready", "assignee": "agent", "human_only": true})
	if w.Code != 201 {
		t.Fatalf("operator create = %d: %s", w.Code, w.Body)
	}
	var i Issue
	json.Unmarshal(w.Body.Bytes(), &i)
	got, _ := s.getIssueByID(context.Background(), i.ID)
	if got.Status != StatusReady || got.Assignee != "agent" || !got.HumanOnly || got.CreatedBy != "op" {
		t.Fatalf("operator issue stored as %+v", got)
	}
	base := "/api/autopilot/issues/" + i.ID
	if w := call(h, "PATCH", base, "op", op, map[string]string{"priority": "high"}); w.Code != 200 {
		t.Errorf("operator patch = %d", w.Code)
	}
	if w := call(h, "POST", base+"/status", "op", op, map[string]string{"Status": "done"}); w.Code != 200 {
		t.Errorf("operator status = %d", w.Code)
	}
	if w := upload(h, base+"/attachments", "op", op); w.Code != 201 {
		t.Errorf("operator upload = %d: %s", w.Code, w.Body)
	}
	// a higher level includes the lower ones: an operator needs no viewer role
	if w := call(h, "GET", base, "op", op, nil); w.Code != 200 {
		t.Errorf("operator read = %d", w.Code)
	}
}

// The feedback ingress stays public, and its creator is a fixed value, not the
// caller-supplied "user".
func TestFeedbackCreatorIsFixed(t *testing.T) {
	s, h := rolesHarness(t)
	w := call(h, "POST", "/api/autopilot/feedback", "", "", map[string]string{"message": "broken", "user": "admin"})
	if w.Code != 201 {
		t.Fatalf("feedback = %d: %s", w.Code, w.Body)
	}
	for _, i := range s.listIssuesBy(context.Background(), "") {
		if i.CreatedBy != "feedback" || i.Status != StatusBacklog {
			t.Errorf("feedback issue created_by=%q status=%q", i.CreatedBy, i.Status)
		}
	}
}

// Without the auth plugin (development opt-out) the caller is a local operator,
// so the existing single-user flow keeps working; with the plugin there is no
// such fallback and a missing session is 401.
func TestRolesDevOptOutIsLocalOperator(t *testing.T) {
	s := newTestServer(t)
	r := chi.NewRouter()
	s.mount(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/autopilot/issues", nil))
	if w.Code != 200 {
		t.Fatalf("dev opt-out GET = %d, want 200", w.Code)
	}
}

// C1: the prefix is not configurable. No environment setting can turn a plain
// "viewer", "admin" or any other role into Autopilot access.
func TestRolePrefixIsNotConfigurable(t *testing.T) {
	for _, v := range []string{"", "x:", "board."} {
		t.Setenv("AUTOPILOT_ROLE_PREFIX", v)
		_, h := rolesHarness(t)
		if w := call(h, "GET", "/api/autopilot/issues", "u", "viewer,admin,operator,reporter", nil); w.Code != 403 {
			t.Errorf("AUTOPILOT_ROLE_PREFIX=%q: plain roles = %d, want 403", v, w.Code)
		}
		if v != "" {
			if w := call(h, "GET", "/api/autopilot/issues", "u", v+"viewer", nil); w.Code != 403 {
				t.Errorf("AUTOPILOT_ROLE_PREFIX=%q: %sviewer = %d, want 403", v, v, w.Code)
			}
		}
		if w := call(h, "GET", "/api/autopilot/issues", "u", "autopilot:viewer", nil); w.Code != 200 {
			t.Errorf("AUTOPILOT_ROLE_PREFIX=%q: autopilot:viewer = %d, want 200", v, w.Code)
		}
	}
}

// T1 mass assignment: a reporter sending every steering field on create, and
// each of them on PATCH, changes none of them.
func TestReporterMassAssignmentChangesNothing(t *testing.T) {
	s, h := rolesHarness(t)
	const rep = "autopilot:reporter"
	all := map[string]any{"title": "t", "body": "b", "status": "ready", "assignee": "agent", "human_only": true,
		"priority": "high", "created_by": "op", "kind": "bug", "author": "op", "author_kind": "agent"}
	w := call(h, "POST", "/api/autopilot/issues", "r1", rep, all)
	if w.Code != 201 {
		t.Fatalf("create = %d: %s", w.Code, w.Body)
	}
	var c Issue
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	got, _ := s.getIssueByID(context.Background(), c.ID)
	if got.Status != StatusBacklog || got.Assignee != "" || got.HumanOnly || got.Priority != "normal" || got.CreatedBy != "r1" {
		t.Fatalf("create stored %+v", got)
	}
	for k, v := range map[string]any{"assignee": "agent", "human_only": true,
		"priority": "high", "kind": "chore"} {
		pw := call(h, "PATCH", "/api/autopilot/issues/"+c.ID, "r1", rep, map[string]any{k: v})
		if pw.Code != 403 {
			t.Errorf("PATCH {%s: %v} = %d, want 403", k, v, pw.Code)
		}
	}
	// keys the PATCH handler does not know are ignored, never applied
	call(h, "PATCH", "/api/autopilot/issues/"+c.ID, "r1", rep, map[string]any{"title": "new", "status": "ready", "created_by": "op"})
	after, _ := s.getIssueByID(context.Background(), c.ID)
	if after.Title != "new" || after.Status != StatusBacklog || after.CreatedBy != "r1" ||
		after.Assignee != got.Assignee || after.HumanOnly != got.HumanOnly || after.Priority != got.Priority || after.Kind != got.Kind {
		t.Errorf("PATCH changed more than title: %+v", after)
	}
}

// C2: the reporter's edit is one conditional UPDATE, so it cannot touch an
// issue that is no longer in backlog or is not theirs.
func TestOwnEditIsAtomicAgainstReady(t *testing.T) {
	s, h := rolesHarness(t)
	ctx := context.Background()
	i := seedIssue(t, s, "r1", StatusBacklog)

	ok, err := s.updateOwnBacklog(ctx, i.ID, "r1", map[string]string{"body": "edited"})
	if err != nil || !ok {
		t.Fatalf("edit in backlog: ok=%v err=%v", ok, err)
	}
	if ok, _ := s.updateOwnBacklog(ctx, i.ID, "someone-else", map[string]string{"body": "x"}); ok {
		t.Fatal("edit by a non-owner must not apply")
	}
	// the operator moves it to ready between the reporter's read and write
	if err := s.setIssueStatus(ctx, i.ID, StatusReady, nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.updateOwnBacklog(ctx, i.ID, "r1", map[string]string{"body": "after ready"}); ok {
		t.Fatal("edit after the issue became ready must not apply")
	}
	if w := call(h, "PATCH", "/api/autopilot/issues/"+i.ID, "r1", "autopilot:reporter", map[string]string{"body": "after ready"}); w.Code != 403 {
		t.Errorf("HTTP edit of a ready issue = %d, want 403", w.Code)
	}
	got, _ := s.getIssueByID(ctx, i.ID)
	if got.Body != "edited" {
		t.Fatalf("body = %q, want %q", got.Body, "edited")
	}
}

// T2: the live stream checks the role AND the Origin at the handshake.
func TestWebSocketNeedsRoleAndSameOrigin(t *testing.T) {
	t.Setenv("AUTOPILOT_WS_ORIGINS", "")
	_, h := rolesHarness(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	dial := func(user, roles, origin string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hdr := http.Header{}
		if user != "" {
			hdr.Set("X-User", user)
		}
		hdr.Set("X-Roles", roles)
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		c, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):]+"/api/autopilot/ws", &websocket.DialOptions{HTTPHeader: hdr})
		if err == nil {
			c.CloseNow()
		}
		return err
	}
	if err := dial("", "", ""); err == nil {
		t.Error("no session must be refused")
	}
	if err := dial("u", "", ""); err == nil {
		t.Error("signed in without a role must be refused")
	}
	if err := dial("u", "viewer,admin", ""); err == nil {
		t.Error("plain viewer/admin roles must be refused")
	}
	if err := dial("v", "autopilot:viewer", "https://evil.example"); err == nil {
		t.Error("a viewer from a foreign Origin must be refused")
	}
	if err := dial("v", "autopilot:viewer", srv.URL); err != nil {
		t.Errorf("a viewer from the same origin must connect: %v", err)
	}
}

// T3: public feedback and self-heal never reach ready without an operator.
func TestFeedbackAndSelfHealLandInBacklog(t *testing.T) {
	t.Setenv("AUTOPILOT_SELF_HEAL_READY", "1")
	s, h := rolesHarness(t)
	if w := call(h, "POST", "/api/autopilot/feedback", "", "", map[string]string{"message": "x", "status": "ready", "user": "op"}); w.Code != 201 {
		t.Fatalf("feedback = %d: %s", w.Code, w.Body)
	}
	hl := &healer{s: s, seen: map[string]time.Time{}, cooldown: time.Hour}
	hl.file(httptest.NewRequest("GET", "/boom", nil), "HTTP 500", "")
	n := 0
	for _, i := range s.listIssuesBy(context.Background(), "") {
		n++
		if i.Status != StatusBacklog {
			t.Errorf("issue %q landed as %q, want backlog", i.Title, i.Status)
		}
	}
	if n != 2 {
		t.Fatalf("expected a feedback and a self-heal issue, got %d", n)
	}
}

// A cross-site form post (text/plain or urlencoded) must not reach the handlers
// even with a valid session.
func TestMutationsRequireJSONContentType(t *testing.T) {
	s, h := rolesHarness(t)
	i := seedIssue(t, s, "op", StatusBacklog)
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		for _, p := range []struct{ m, path string }{
			{"POST", "/api/autopilot/issues"},
			{"PATCH", "/api/autopilot/issues/" + i.ID},
			{"POST", "/api/autopilot/issues/" + i.ID + "/status"},
			{"POST", "/api/autopilot/issues/" + i.ID + "/comments"},
		} {
			req := httptest.NewRequest(p.m, p.path, strings.NewReader(`{"title":"x","status":"ready","body":"x"}`))
			req.Header.Set("X-User", "op")
			req.Header.Set("X-Roles", "autopilot:operator")
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 415 {
				t.Errorf("%s %s with Content-Type %q = %d, want 415", p.m, p.path, ct, w.Code)
			}
		}
	}
	if got, _ := s.getIssueByID(context.Background(), i.ID); got.Status != StatusBacklog {
		t.Errorf("a refused request changed the issue: %+v", got)
	}
}
