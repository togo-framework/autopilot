package autopilot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveClaudeImplements runs the REAL Claude Code executor end to end:
// ready issue -> runner claims it -> `claude -p` edits the working tree ->
// runner commits a branch -> issue moves to in_review. Gated behind
// AUTOPILOT_LIVE=1 because it spends tokens + needs the claude CLI authed.
//
//	AUTOPILOT_LIVE=1 go test ./internal/autopilot/ -run TestLiveClaude -v -timeout 25m
func TestLiveClaudeImplements(t *testing.T) {
	if os.Getenv("AUTOPILOT_LIVE") != "1" {
		t.Skip("set AUTOPILOT_LIVE=1 to run the live Claude Code loop")
	}
	s := newTestServer(t)
	repo := gitRepo(t)
	i := seedReady(t, s, "Create HELLO_FROM_AGENT.md")
	// give the agent a concrete, tiny task so the run is fast + deterministic
	_ = s.setIssueStatus(context.Background(), i.ID, StatusReady, map[string]string{
		"body": "Create a new file named HELLO_FROM_AGENT.md at the repo root containing a short, friendly one-paragraph greeting that says this repository is a togo autopilot demo and that this file was written by the autonomous agent. Do not modify any other files.",
	})
	i, _ = s.getIssueByID(context.Background(), i.ID)

	bin := env("AUTOPILOT_CLAUDE_BIN", "claude")
	r := testRunner(s, repo, &ClaudeExecutor{bin: bin})

	r.tick(context.Background())

	got, _ := s.getIssueByID(context.Background(), i.ID)
	t.Logf("final status=%s branch=%s", got.Status, got.Branch)
	for _, c := range s.commentsFor(context.Background(), i.ID) {
		t.Logf("[%s] %s", c.AuthorKind, strings.Split(c.Body, "\n")[0])
	}
	if got.Status != StatusInReview {
		t.Fatalf("status = %q, want in_review", got.Status)
	}
	// the file exists on the agent branch
	out, err := exec.Command("git", "-C", repo, "show", got.Branch+":HELLO_FROM_AGENT.md").CombinedOutput()
	if err != nil {
		t.Fatalf("file not on branch %s: %v: %s", got.Branch, err, out)
	}
	t.Logf("HELLO_FROM_AGENT.md on branch:\n%s", out)
	if _, err := os.Stat(filepath.Join(repo, "HELLO_FROM_AGENT.md")); err != nil {
		t.Errorf("file missing in working tree: %v", err)
	}
}
