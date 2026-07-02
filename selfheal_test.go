package autopilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func bugTitles(s *server) []string {
	var out []string
	for _, i := range s.listIssuesBy(context.Background(), "") {
		if i.Kind == "bug" {
			out = append(out, i.Title)
		}
	}
	return out
}

func TestSelfHealFilesBugOnPanic(t *testing.T) {
	s := newTestServer(t)
	h := s.selfHealMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }))

	req := httptest.NewRequest("GET", "/api/widgets", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Errorf("panic should yield 500, got %d", w.Code)
	}
	bugs := bugTitles(s)
	if len(bugs) != 1 || !strings.Contains(bugs[0], "panic") {
		t.Fatalf("expected 1 panic bug, got %v", bugs)
	}
}

func TestSelfHealFilesBugOn5xx(t *testing.T) {
	s := newTestServer(t)
	h := s.selfHealMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "kaboom", 503)
	}))
	req := httptest.NewRequest("POST", "/api/orders", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	bugs := bugTitles(s)
	if len(bugs) != 1 || !strings.Contains(bugs[0], "503") {
		t.Fatalf("expected 1 5xx bug mentioning 503, got %v", bugs)
	}
}

func TestSelfHealDedupsAndSkipsOwnRoutes(t *testing.T) {
	s := newTestServer(t)
	mw := s.selfHealMiddleware()
	fail := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) }))

	// same fingerprint twice -> filed once (cooldown)
	fail.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/z", nil))
	fail.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/z", nil))
	if got := len(bugTitles(s)); got != 1 {
		t.Errorf("dedup failed: %d bugs, want 1", got)
	}

	// autopilot's own 5xx must NOT self-file (no reference loop)
	fail.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/autopilot/issues", nil))
	if got := len(bugTitles(s)); got != 1 {
		t.Errorf("should skip own routes: %d bugs, want 1", got)
	}
}
