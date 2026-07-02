package autopilot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	db, err := sql.Open("sqlite", "file:aloop_"+genID()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s := &server{testDB: db}
	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t.io")
	run("config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0644)
	run("add", "-A")
	run("commit", "-qm", "seed")
	return dir
}

type fakeImpl struct {
	needsInput bool
	writeFile  bool
	reason     string
}

func (f fakeImpl) Implement(ctx context.Context, workdir string, issue Issue) ImplementResult {
	if f.writeFile {
		_ = os.WriteFile(filepath.Join(workdir, "AGENT_OUT.txt"), []byte(issue.Title), 0644)
	}
	return ImplementResult{
		Summary:    "fake did the work",
		NeedsInput: f.needsInput,
		Reason:     f.reason,
		Changed:    workdirDirty(ctx, workdir),
	}
}

func testRunner(s *server, workdir string, impl Implementer) *runner {
	return &runner{s: s, workdir: workdir, agentID: "test-agent", gitName: "a", gitEmail: "a@a.io", impl: impl, push: false}
}

func seedReady(t *testing.T, s *server, title string) Issue {
	t.Helper()
	i := Issue{ID: genID(), Title: title, Status: StatusReady, Kind: "feature", CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.insertIssue(context.Background(), i); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return i
}

func TestRunnerImplementsReadyIssue(t *testing.T) {
	s := newTestServer(t)
	repo := gitRepo(t)
	i := seedReady(t, s, "add a hello file")
	r := testRunner(s, repo, fakeImpl{writeFile: true})

	r.tick(context.Background())

	got, ok := s.getIssueByID(context.Background(), i.ID)
	if !ok {
		t.Fatal("issue vanished")
	}
	if got.Status != StatusInReview {
		t.Fatalf("status = %q, want in_review", got.Status)
	}
	if got.Branch == "" {
		t.Error("branch not recorded")
	}
	// the change is on the agent branch
	if out, _ := exec.Command("git", "-C", repo, "log", "--oneline", got.Branch).CombinedOutput(); !strings.Contains(string(out), "add a hello file") {
		t.Errorf("commit not on branch: %s", out)
	}
	// agent posted a result comment
	found := false
	for _, c := range s.commentsFor(context.Background(), i.ID) {
		if c.AuthorKind == "agent" && strings.Contains(c.Body, "Implemented") {
			found = true
		}
	}
	if !found {
		t.Error("no agent 'Implemented' comment")
	}
}

func TestRunnerBlocksWhenAgentNeedsInput(t *testing.T) {
	s := newTestServer(t)
	i := seedReady(t, s, "ambiguous thing")
	r := testRunner(s, gitRepo(t), fakeImpl{needsInput: true, reason: "which database?"})

	r.tick(context.Background())

	got, _ := s.getIssueByID(context.Background(), i.ID)
	if got.Status != StatusBlocked {
		t.Fatalf("status = %q, want blocked", got.Status)
	}
	last := s.commentsFor(context.Background(), i.ID)
	if len(last) == 0 || !strings.Contains(last[len(last)-1].Body, "which database?") {
		t.Errorf("blocked comment missing the question: %+v", last)
	}
}

func TestClaimIsAtomicSingleProcessing(t *testing.T) {
	s := newTestServer(t)
	seedReady(t, s, "only once")
	r := testRunner(s, gitRepo(t), fakeImpl{needsInput: true})

	if _, ok := r.claimNextReady(context.Background()); !ok {
		t.Fatal("first claim should succeed")
	}
	// already in_progress → nothing left to claim
	if _, ok := r.claimNextReady(context.Background()); ok {
		t.Error("second claim should find nothing (issue already claimed)")
	}
}

func TestHumanCommentUnblocks(t *testing.T) {
	s := newTestServer(t)
	// a blocked issue
	i := Issue{ID: genID(), Title: "blocked one", Status: StatusBlocked, CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.insertIssue(context.Background(), i); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"author": "fady", "author_kind": "human", "body": "use postgres"})
	req := httptest.NewRequest("POST", "/api/autopilot/issues/"+i.ID+"/comments", bytes.NewReader(body))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", i.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()

	s.addComment(w, req)

	if w.Code != 201 {
		t.Fatalf("addComment code = %d", w.Code)
	}
	got, _ := s.getIssueByID(context.Background(), i.ID)
	if got.Status != StatusReady {
		t.Fatalf("human comment on blocked issue should unblock -> ready, got %q", got.Status)
	}
}

func TestPriorityAndHumanOnlyPersist(t *testing.T) {
	s := newTestServer(t)

	// create with priority + human_only (regression: underscore JSON keys need tags)
	body, _ := json.Marshal(map[string]any{"title": "guarded", "kind": "bug", "priority": "high", "human_only": true})
	req := httptest.NewRequest("POST", "/api/autopilot/issues", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.createIssue(w, req)
	if w.Code != 201 {
		t.Fatalf("createIssue code = %d", w.Code)
	}
	var created Issue
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Priority != "high" || !created.HumanOnly {
		t.Fatalf("create did not persist priority/human_only: %+v", created)
	}

	// patch priority -> critical, human_only -> false
	pb, _ := json.Marshal(map[string]any{"priority": "critical", "human_only": false})
	preq := httptest.NewRequest("PATCH", "/api/autopilot/issues/"+created.ID, bytes.NewReader(pb))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", created.ID)
	preq = preq.WithContext(context.WithValue(preq.Context(), chi.RouteCtxKey, rc))
	pw := httptest.NewRecorder()
	s.patchIssue(pw, preq)
	if pw.Code != 200 {
		t.Fatalf("patchIssue code = %d", pw.Code)
	}
	got, _ := s.getIssueByID(context.Background(), created.ID)
	if got.Priority != "critical" || got.HumanOnly {
		t.Fatalf("patch did not persist priority/human_only: %+v", got)
	}
}

func TestRunnerSkipsHumanOnly(t *testing.T) {
	s := newTestServer(t)
	// a human-only ready issue (must be skipped) + a normal ready issue
	guarded := Issue{ID: genID(), Title: "human only", Status: StatusReady, Kind: "bug", HumanOnly: true, CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.insertIssue(context.Background(), guarded); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Millisecond)
	normal := seedReady(t, s, "agent ok")

	r := testRunner(s, t.TempDir(), fakeImpl{})
	claimed, ok := r.claimNextReady(context.Background())
	if !ok {
		t.Fatal("expected to claim the non-human-only issue")
	}
	if claimed.ID != normal.ID {
		t.Fatalf("runner claimed the wrong issue: got %q, want the non-human-only %q", claimed.ID, normal.ID)
	}
}
