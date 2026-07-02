package autopilot

import (
	"embed"
	"net/http"
)

//go:embed web/board.html web/feedback.js web/sdk.js
var webFS embed.FS

// serveBoard serves the self-contained Mission Control board (no build step).
// The HTML shell is public; its data calls to /api/autopilot are auth-guarded.
func (s *server) serveBoard(w http.ResponseWriter, _ *http.Request) {
	b, err := webFS.ReadFile("web/board.html")
	if err != nil {
		http.Error(w, "board not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// serveFeedbackJS serves the drop-in Feedback SDK widget (embed anywhere).
func (s *server) serveFeedbackJS(w http.ResponseWriter, _ *http.Request) {
	s.serveJS(w, "web/feedback.js")
}

// serveSDK serves the drop-in Autopilot SDK — a floating launcher that slides
// the board open as an overlay (like the Feedback button).
func (s *server) serveSDK(w http.ResponseWriter, _ *http.Request) {
	s.serveJS(w, "web/sdk.js")
}

func (s *server) serveJS(w http.ResponseWriter, path string) {
	b, err := webFS.ReadFile(path)
	if err != nil {
		http.Error(w, "not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(b)
}
