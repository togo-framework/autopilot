package autopilot

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Self-healing: an outer middleware that watches every response, and when a
// request panics or returns 5xx it auto-files a `bug` issue (deduplicated) so
// the agent runner can pick it up and fix it — closing the loop from "prod
// broke" to "agent opened a fix PR" with no human in the middle.
//
// Opt-in via AUTOPILOT_SELF_HEAL=1 (and AUTOPILOT_SELF_HEAL_READY=1 to file the
// bug straight to `ready` so an agent starts immediately instead of `backlog`).

type healer struct {
	s        *server
	mu       sync.Mutex
	seen     map[string]time.Time // fingerprint -> last filed
	cooldown time.Duration
	ready    bool
}

func (s *server) selfHealMiddleware() func(http.Handler) http.Handler {
	h := &healer{s: s, seen: map[string]time.Time{}, cooldown: time.Hour, ready: envBool("AUTOPILOT_SELF_HEAL_READY")}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Never self-reference: skip autopilot's own surfaces.
			if strings.HasPrefix(r.URL.Path, "/api/autopilot") || strings.HasPrefix(r.URL.Path, "/autopilot") {
				next.ServeHTTP(w, r)
				return
			}
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			defer func() {
				if p := recover(); p != nil {
					h.file(r, fmt.Sprintf("panic: %v", p), string(debug.Stack()))
					if !rec.wrote {
						http.Error(rec, "internal server error", http.StatusInternalServerError)
					}
				}
			}()
			next.ServeHTTP(rec, r)
			if rec.status >= 500 {
				h.file(r, fmt.Sprintf("HTTP %d — %s %s", rec.status, r.Method, r.URL.Path), "")
			}
		})
	}
}

// file records a bug issue for a fingerprint, at most once per cooldown.
func (h *healer) file(r *http.Request, summary, stack string) {
	fp := r.Method + " " + r.URL.Path + " :: " + firstLine(summary)
	h.mu.Lock()
	if last, ok := h.seen[fp]; ok && time.Since(last) < h.cooldown {
		h.mu.Unlock()
		return
	}
	h.seen[fp] = time.Now()
	h.mu.Unlock()

	status := StatusBacklog
	if h.ready {
		status = StatusReady
	}
	body := "Auto-filed by self-healing.\n\n" +
		"- Request: `" + r.Method + " " + r.URL.Path + "`\n" +
		"- Detail: " + summary + "\n"
	if stack != "" {
		body += "\n```\n" + trim(stack, 4000) + "\n```\n"
	}
	title := "🔧 " + trim(summary, 120)
	_ = h.s.insertIssue(r.Context(), Issue{
		ID: genID(), Title: title, Body: body, Status: status, Kind: "bug",
		CreatedBy: "self-heal", CreatedAt: nowStr(), UpdatedAt: nowStr(),
	})
}

// statusRecorder captures the response status (and whether the header was sent)
// so the middleware can detect 5xx and avoid double-writing after a panic.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
