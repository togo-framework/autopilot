// Package autopilot is togo's Issues -> Agent -> Code -> Deploy loop.
//
// Humans (and agents) file issues; an in-app runner claims "ready" issues,
// invokes Claude Code headless in the project working tree to implement them,
// pushes a branch / opens a PR, and moves the issue to review. When an agent
// needs a human decision it moves the issue to "blocked" and comments; a human
// reply flips it back to "ready" to unblock. Issues + comments live in the app
// DB (hybrid: an optional one-way GitHub mirror can be layered on top).
//
// Mounted under /api/autopilot/*. The runner only starts when AUTOPILOT_RUNNER=1
// so the API can be used (and the board browsed) without an agent running.
package autopilot

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"
	auth "github.com/togo-framework/auth"
)

// Status workflow. Transitions are enforced in api.go / runner.go.
const (
	StatusBacklog    = "backlog"
	StatusReady      = "ready"        // handed to the agents
	StatusInProgress = "in_progress"  // an agent has claimed it
	StatusBlocked    = "blocked"      // agent needs a human decision (see comments)
	StatusInReview   = "in_review"    // branch/PR ready for human review
	StatusDone       = "done"
)

func init() {
	// PriorityLate+30: mount after every plugin + auth middleware (chi requires
	// middleware before routes).
	togo.RegisterProviderFunc("autopilot", togo.PriorityLate+30, func(k *togo.Kernel) error {
		if k.Router == nil {
			return nil
		}
		s := &server{k: k, hub: newHub()}
		if a, ok := auth.FromKernel(k); ok {
			s.auth = a
		}
		if err := s.migrate(context.Background()); err != nil {
			if k.Log != nil {
				k.Log.Error("autopilot: migrate failed", "err", err)
			}
			return nil // don't take the app down; the board just won't work
		}
		s.mount(k.Router)

		// Self-healing (opt-in): an outer middleware that auto-files a bug issue on
		// panic/5xx. Uses UseMiddleware (togo >= v0.21.0), applied at serve time so
		// chi's "middleware before routes" rule is never violated.
		if envBool("AUTOPILOT_SELF_HEAL") {
			k.UseMiddleware(s.selfHealMiddleware())
			if k.Log != nil {
				k.Log.Info("autopilot: self-healing enabled (panics/5xx -> bug issues)")
			}
		}

		// The runner is opt-in (it shells out to Claude Code + git). Off by default.
		if os.Getenv("AUTOPILOT_RUNNER") == "1" {
			r := newRunner(s)
			go r.loop(context.Background())
			if k.Log != nil {
				k.Log.Info("autopilot: runner started", "workdir", r.workdir, "push", r.push)
			}
		}
		return nil
	})
}

type server struct {
	k    *togo.Kernel
	auth *auth.Service
	// testDB, when set, bypasses the kernel so the store/runner are unit-testable
	// against an in-memory SQLite DB (uses "?" placeholders).
	testDB *sql.DB
	// hub fans real-time events (issue/comment changes) to WebSocket clients.
	hub *hub
}

func (s *server) mount(r chi.Router) {
	r.Route("/api/autopilot", func(r chi.Router) {
		// Guard with the auth session when the auth plugin is present. The
		// Feedback SDK's public submit endpoint is mounted separately below.
		if s.auth != nil {
			r.Use(s.auth.Middleware)
		}
		r.Get("/issues", s.listIssues)
		r.Post("/issues", s.createIssue)
		r.Get("/issues/{id}", s.getIssue)
		r.Patch("/issues/{id}", s.patchIssue)
		r.Post("/issues/{id}/status", s.setStatus)
		r.Get("/issues/{id}/comments", s.listComments)
		r.Post("/issues/{id}/comments", s.addComment)
		// Media attachments (image/video/file) for an issue.
		r.Post("/issues/{id}/attachments", s.uploadAttachment)
		r.Get("/issues/{id}/attachments", s.listAttachments)
		r.Get("/uploads/{uid}", s.serveUpload)
		// Real-time event stream (new issue, status move, new comment, …).
		r.Get("/ws", s.serveWS)
	})

	// Feedback SDK ingress — intentionally unauthenticated so a "feedback button
	// everywhere" (including logged-out surfaces) can file an issue. Rate/spam
	// controls are a later hardening step.
	r.Post("/api/autopilot/feedback", s.submitFeedback)

	// Self-contained Mission Control board + the drop-in Feedback SDK widget.
	// Served at the root (not /api) so the HTML shell + script load without a
	// session; their data calls remain auth-guarded above.
	r.Get("/autopilot", s.serveBoard)
	r.Get("/autopilot/feedback.js", s.serveFeedbackJS)
	r.Get("/autopilot/sdk.js", s.serveSDK)
}

// ---- schema (idempotent; dialect-portable TEXT/INTEGER) ----

func (s *server) migrate(ctx context.Context) error {
	db, _ := s.db(ctx)
	if db == nil {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS autopilot_issues (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			body TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'backlog',
			kind TEXT NOT NULL DEFAULT 'feature',
			assignee TEXT NOT NULL DEFAULT '',
			claimed_by TEXT NOT NULL DEFAULT '',
			claimed_at TEXT NOT NULL DEFAULT '',
			branch TEXT NOT NULL DEFAULT '',
			pr_url TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			priority TEXT NOT NULL DEFAULT 'normal',
			human_only INTEGER NOT NULL DEFAULT 0,
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS autopilot_comments (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL,
			author TEXT NOT NULL DEFAULT '',
			author_kind TEXT NOT NULL DEFAULT 'human',
			body TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		// Attachments (image/video/file). Bytes are base64 in a TEXT column so the
		// schema is dialect-portable (no BLOB/BYTEA divergence) — fine for an in-app
		// tool with a sane size cap.
		`CREATE TABLE IF NOT EXISTS autopilot_uploads (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
			size INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	// Best-effort ALTERs so tables created before priority/human_only pick them up.
	// (Errors — e.g. "column already exists", or SQLite's lack of IF NOT EXISTS — are
	// expected and ignored.)
	for _, alter := range []string{
		`ALTER TABLE autopilot_issues ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'`,
		`ALTER TABLE autopilot_issues ADD COLUMN human_only INTEGER NOT NULL DEFAULT 0`,
	} {
		_, _ = db.ExecContext(ctx, alter)
	}
	return nil
}

// ---- helpers ----

func (s *server) db(ctx context.Context) (*sql.DB, func(int) string) {
	if s.testDB != nil {
		return s.testDB, func(int) string { return "?" }
	}
	db, _ := s.k.SQL(ctx)
	return db, s.k.Dialect().Placeholder
}

func genID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowStr() string { return time.Now().UTC().Format(time.RFC3339) }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envBool(k string) bool { return os.Getenv(k) == "1" }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
