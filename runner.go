package autopilot

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/togo-framework/providers"
)

// ImplementResult is what an Implementer reports back to the runner.
type ImplementResult struct {
	Summary    string // human-readable summary of what happened
	NeedsInput bool   // agent needs a human decision -> issue goes BLOCKED
	Reason     string // question (if NeedsInput) or error detail
	Changed    bool   // did it leave file changes in the working tree
}

// Implementer turns an issue into working-tree changes — the `impl` capability.
// ClaudeExecutor is the default (invokes Claude Code headless); togo-framework/
// omnigent registers an alternative; tests inject a fake.
type Implementer interface {
	Implement(ctx context.Context, workdir string, issue Issue) ImplementResult
}

// Executor provisions the environment an issue is implemented in — the `exec`
// capability. localExecutor (default) uses the configured working directory in
// place; togo-framework/coder provisions an isolated workspace instead.
type Executor interface {
	// Acquire returns a working directory to implement in and a release func to
	// tear it down afterwards (a no-op for local).
	Acquire(ctx context.Context, issue Issue) (workdir string, release func(), err error)
}

// localExecutor runs in the configured working directory in place (today's
// behavior). It is registered as the default `exec` backend.
type localExecutor struct{ workdir string }

func (l *localExecutor) Acquire(context.Context, Issue) (string, func(), error) {
	return l.workdir, func() {}, nil
}

// resolveImpl / resolveExec read the active backend from the kernel container
// (populated by providers.Use), falling back to the built-in defaults so
// autopilot works even if no provider was explicitly selected.
func resolveImpl(s *server) Implementer {
	if s.k != nil {
		if v, ok := s.k.Get(providers.CapImplement); ok {
			if im, ok := v.(Implementer); ok {
				return im
			}
		}
	}
	return &ClaudeExecutor{bin: env("AUTOPILOT_CLAUDE_BIN", "claude")}
}

func resolveExec(s *server) Executor {
	if s.k != nil {
		if v, ok := s.k.Get(providers.CapExecute); ok {
			if ex, ok := v.(Executor); ok {
				return ex
			}
		}
	}
	return &localExecutor{workdir: env("AUTOPILOT_WORKDIR", ".")}
}

type runner struct {
	s         *server
	workdir   string
	poll      time.Duration
	push      bool
	agentID   string
	gitName   string
	gitEmail  string
	impl      Implementer
	exec      Executor
}

func newRunner(s *server) *runner {
	poll, _ := strconv.Atoi(env("AUTOPILOT_POLL_SECONDS", "15"))
	if poll < 3 {
		poll = 3
	}
	return &runner{
		s:        s,
		workdir:  env("AUTOPILOT_WORKDIR", "."),
		poll:     time.Duration(poll) * time.Second,
		push:     os.Getenv("AUTOPILOT_PUSH") == "1",
		agentID:  env("AUTOPILOT_AGENT_ID", "togo-agent"),
		gitName:  env("AUTOPILOT_COMMIT_NAME", "togo agent"),
		gitEmail: env("AUTOPILOT_COMMIT_EMAIL", "agent@togo.dev"),
		// impl + exec resolve the active provider (claude/local by default), so
		// installing togo-framework/omnigent or /coder swaps them via config.
		impl: resolveImpl(s),
		exec: resolveExec(s),
	}
}

func (r *runner) loop(ctx context.Context) {
	t := time.NewTicker(r.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

// tick claims at most one ready issue and processes it. Claiming is atomic so
// multiple agents (processes) can share the same queue without double-work.
func (r *runner) tick(ctx context.Context) {
	issue, ok := r.claimNextReady(ctx)
	if !ok {
		return
	}
	r.process(ctx, issue)
}

func (r *runner) claimNextReady(ctx context.Context) (Issue, bool) {
	db, ph := r.s.db(ctx)
	if db == nil {
		return Issue{}, false
	}
	// pick oldest ready
	var id string
	// human_only issues are skipped — agents never touch them.
	row := db.QueryRowContext(ctx, "SELECT id FROM autopilot_issues WHERE status="+ph(1)+" AND human_only=0 ORDER BY created_at ASC LIMIT 1", StatusReady)
	if err := row.Scan(&id); err != nil {
		return Issue{}, false
	}
	// atomic claim: only succeeds if still ready
	res, err := db.ExecContext(ctx,
		"UPDATE autopilot_issues SET status="+ph(1)+", claimed_by="+ph(2)+", claimed_at="+ph(3)+", updated_at="+ph(4)+" WHERE id="+ph(5)+" AND status="+ph(6),
		StatusInProgress, r.agentID, nowStr(), nowStr(), id, StatusReady)
	if err != nil {
		return Issue{}, false
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Issue{}, false // someone else grabbed it
	}
	r.s.emit("issue.status", map[string]any{"id": id, "status": StatusInProgress, "agent": r.agentID})
	return r.s.getIssueByID(ctx, id)
}

func (r *runner) process(ctx context.Context, issue Issue) {
	r.comment(ctx, issue.ID, fmt.Sprintf("Claimed by **%s** — implementing…", r.agentID))

	// Acquire an environment from the exec provider (local dir by default; an
	// isolated Coder workspace when togo-framework/coder is selected).
	wd, release, err := r.exec.Acquire(ctx, issue)
	if err != nil {
		r.setStatus(ctx, issue.ID, StatusBlocked, issue.Title, nil)
		r.comment(ctx, issue.ID, "🚧 **Blocked — could not acquire an environment:** "+err.Error())
		return
	}
	defer release()

	result := r.impl.Implement(ctx, wd, issue)

	if result.NeedsInput {
		r.setStatus(ctx, issue.ID, StatusBlocked, issue.Title, nil)
		r.comment(ctx, issue.ID, "🚧 **Blocked — need a human decision:**\n\n"+result.Reason+"\n\n_Reply with a comment to unblock; I'll pick it back up._")
		return
	}
	if !result.Changed {
		r.setStatus(ctx, issue.ID, StatusBlocked, issue.Title, nil)
		r.comment(ctx, issue.ID, "🚧 **Blocked — no changes produced.**\n\n"+result.Reason+result.Summary)
		return
	}

	branch := "autopilot/issue-" + shortID(issue.ID)
	if err := r.commitBranch(ctx, branch, issue); err != nil {
		r.setStatus(ctx, issue.ID, StatusBlocked, issue.Title, nil)
		r.comment(ctx, issue.ID, "🚧 **Blocked — git failed:** "+err.Error())
		return
	}

	fields := map[string]string{"branch": branch, "result": trim(result.Summary, 2000)}
	prMsg := ""
	if r.push {
		if url, err := r.pushAndPR(ctx, branch, issue); err == nil {
			fields["pr_url"] = url
			prMsg = "\n\nPR: " + url
		} else {
			prMsg = "\n\n_(push/PR failed: " + err.Error() + " — branch is local: " + branch + ")_"
		}
	} else {
		prMsg = "\n\n_(local branch only — set AUTOPILOT_PUSH=1 to open a PR)_"
	}
	r.setStatus(ctx, issue.ID, StatusInReview, issue.Title, fields)
	r.comment(ctx, issue.ID, "✅ **Implemented on branch `"+branch+"`.**\n\n"+result.Summary+prMsg+"\n\n_Moved to review._")
}

func (r *runner) comment(ctx context.Context, issueID, body string) {
	_ = r.s.insertComment(ctx, Comment{ID: genID(), IssueID: issueID, Author: r.agentID, AuthorKind: "agent", Body: body, CreatedAt: nowStr()})
	r.s.emit("comment.added", map[string]any{"issue_id": issueID, "author_kind": "agent", "author": r.agentID})
}

// setStatus updates the issue status and broadcasts the move (with the title so
// clients can render a notification without a lookup).
func (r *runner) setStatus(ctx context.Context, id, status, title string, fields map[string]string) {
	_ = r.s.setIssueStatus(ctx, id, status, fields)
	r.s.emit("issue.status", map[string]any{"id": id, "status": status, "title": title})
}

// ---- git ----

func (r *runner) git(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-C", r.workdir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func (r *runner) commitBranch(ctx context.Context, branch string, issue Issue) error {
	// fresh branch off current HEAD (delete a stale one of the same name first)
	_, _ = r.git(ctx, "branch", "-D", branch)
	if _, err := r.git(ctx, "checkout", "-b", branch); err != nil {
		return err
	}
	if _, err := r.git(ctx, "add", "-A"); err != nil {
		return err
	}
	msg := issue.Title + "\n\nautopilot issue " + issue.ID
	_, err := r.git(ctx, "-c", "user.name="+r.gitName, "-c", "user.email="+r.gitEmail, "commit", "-m", msg)
	return err
}

func (r *runner) pushAndPR(ctx context.Context, branch string, issue Issue) (string, error) {
	if _, err := r.git(ctx, "push", "-u", "origin", branch); err != nil {
		return "", err
	}
	// open a PR via gh (hybrid GitHub mirror). Best-effort.
	cmd := exec.CommandContext(ctx, "gh", "pr", "create", "--head", branch,
		"--title", issue.Title, "--body", "Implements autopilot issue `"+issue.ID+"`.\n\n"+issue.Body)
	cmd.Dir = r.workdir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// ---- Claude Code executor ----

// ClaudeExecutor implements an issue by invoking Claude Code headless in the
// working tree. It asks Claude to either edit files or, if a human decision is
// needed, make no changes and reply with a line starting "BLOCKED:".
type ClaudeExecutor struct{ bin string }

func (c *ClaudeExecutor) Implement(ctx context.Context, workdir string, issue Issue) ImplementResult {
	prompt := buildPrompt(issue)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, c.bin, "-p", prompt, "--permission-mode", "acceptEdits")
	cmd.Dir = workdir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	stdout := out.String()

	// Did it change anything?
	changed := workdirDirty(cctx, workdir)

	// Explicit block signal from the agent.
	if strings.Contains(stdout, "BLOCKED:") {
		reason := stdout[strings.Index(stdout, "BLOCKED:")+len("BLOCKED:"):]
		return ImplementResult{NeedsInput: true, Reason: trim(strings.TrimSpace(reason), 1500), Changed: changed}
	}
	if runErr != nil && !changed {
		return ImplementResult{Reason: "claude: " + runErr.Error() + " " + trim(strings.TrimSpace(errb.String()), 500), Changed: false}
	}
	return ImplementResult{Summary: trim(strings.TrimSpace(stdout), 2000), Changed: changed}
}

func buildPrompt(issue Issue) string {
	return "You are an autonomous engineering agent working in this repository. " +
		"Implement the following issue end to end by editing files in the working tree. Keep changes minimal and focused.\n\n" +
		"Issue " + issue.ID + " — " + issue.Title + "\n" + issue.Body + "\n\n" +
		"Rules:\n" +
		"- Make the code changes directly; do NOT run git commit/push (the runner handles git).\n" +
		"- If you cannot proceed without a human decision, do NOT guess: make no changes and reply with one line starting 'BLOCKED: <your question>'.\n" +
		"- When done, briefly summarize what you changed."
}

func workdirDirty(ctx context.Context, workdir string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", workdir, "status", "--porcelain")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
