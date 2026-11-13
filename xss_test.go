package autopilot

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

const xssPayload = `"><img src=x onerror=alert(1)>`

// Bypass tests: values the board renders as markup or class names are fixed
// allow-lists on the server, never free text.
func TestFreeTextKindIsRejected(t *testing.T) {
	s := newTestServer(t)

	body, _ := json.Marshal(map[string]any{"title": "x", "kind": xssPayload})
	w := httptest.NewRecorder()
	s.createIssue(w, httptest.NewRequest("POST", "/api/autopilot/issues", bytes.NewReader(body)))
	if w.Code != 400 {
		t.Fatalf("createIssue with a script-bearing kind = %d, want 400", w.Code)
	}

	// patch
	i := Issue{ID: genID(), Title: "ok", Status: StatusBacklog, Kind: "bug", CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.insertIssue(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	pb, _ := json.Marshal(map[string]any{"kind": xssPayload})
	preq := httptest.NewRequest("PATCH", "/api/autopilot/issues/"+i.ID, bytes.NewReader(pb))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", i.ID)
	preq = preq.WithContext(context.WithValue(preq.Context(), chi.RouteCtxKey, rc))
	pw := httptest.NewRecorder()
	s.patchIssue(pw, preq)
	if pw.Code != 400 {
		t.Fatalf("patchIssue with a script-bearing kind = %d, want 400", pw.Code)
	}
	got, _ := s.getIssueByID(context.Background(), i.ID)
	if got.Kind != "bug" {
		t.Fatalf("kind changed to %q", got.Kind)
	}

	// valid kinds still work
	for _, k := range []string{"feature", "bug", "chore", "question", "discussion", "feedback"} {
		b, _ := json.Marshal(map[string]any{"title": "x", "kind": k})
		cw := httptest.NewRecorder()
		s.createIssue(cw, httptest.NewRequest("POST", "/api/autopilot/issues", bytes.NewReader(b)))
		if cw.Code != 201 {
			t.Errorf("kind %q = %d, want 201", k, cw.Code)
		}
	}
}

func TestFreeTextAuthorKindIsRejected(t *testing.T) {
	s := newTestServer(t)
	i := Issue{ID: genID(), Title: "ok", Status: StatusBacklog, Kind: "bug", CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.insertIssue(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	post := func(kind string) int {
		b, _ := json.Marshal(map[string]string{"author": "a", "AuthorKind": kind, "body": "hi"})
		req := httptest.NewRequest("POST", "/api/autopilot/issues/"+i.ID+"/comments", bytes.NewReader(b))
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", i.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
		w := httptest.NewRecorder()
		s.addComment(w, req)
		return w.Code
	}
	if c := post(xssPayload); c != 400 {
		t.Fatalf("script-bearing author_kind = %d, want 400", c)
	}
	for _, k := range []string{"human", "agent", "system"} {
		if c := post(k); c != 201 {
			t.Errorf("author_kind %q = %d, want 201", k, c)
		}
	}
}

// The embedded UI escapes quotes too, so stale rows or future fields cannot
// break out of an attribute.
func TestEmbeddedUIEscapesQuotes(t *testing.T) {
	for _, f := range []string{"web/board.html", "web/sdk.js"} {
		b, err := webFS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, `&quot;`) || !strings.Contains(src, `&#39;`) {
			t.Errorf("%s: esc() must escape double and single quotes", f)
		}
		if !strings.Contains(src, "safeUrl(") || !strings.Contains(src, "cls(") {
			t.Errorf("%s: class names and URLs must be sanitized", f)
		}
		for _, bad := range []string{`${i.kind}`, `${c.author_kind}`, `" + c.author_kind`, `' + c.author_kind +`} {
			if strings.Contains(src, bad) {
				t.Errorf("%s still interpolates %s unescaped", f, bad)
			}
		}
	}
}
