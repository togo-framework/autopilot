package autopilot

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Issue is the human/agent-facing unit of work.
type Issue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Status    string `json:"status"`
	Kind      string `json:"kind"`
	Assignee  string `json:"assignee"`
	ClaimedBy string `json:"claimed_by"`
	ClaimedAt string `json:"claimed_at"`
	Branch    string `json:"branch"`
	PRURL     string `json:"pr_url"`
	Result    string `json:"result"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Comment is one message in the human<->agent thread on an issue.
type Comment struct {
	ID         string `json:"id"`
	IssueID    string `json:"issue_id"`
	Author     string `json:"author"`
	AuthorKind string `json:"author_kind"` // human | agent
	Body       string `json:"body"`
	CreatedAt  string `json:"created_at"`
}

const issueCols = "id,title,body,status,kind,assignee,claimed_by,claimed_at,branch,pr_url,result,created_by,created_at,updated_at"

func scanIssue(rs interface{ Scan(...any) error }) (Issue, error) {
	var i Issue
	err := rs.Scan(&i.ID, &i.Title, &i.Body, &i.Status, &i.Kind, &i.Assignee, &i.ClaimedBy,
		&i.ClaimedAt, &i.Branch, &i.PRURL, &i.Result, &i.CreatedBy, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

// ---- store ----

func (s *server) getIssueByID(ctx context.Context, id string) (Issue, bool) {
	db, ph := s.db(ctx)
	if db == nil {
		return Issue{}, false
	}
	row := db.QueryRowContext(ctx, "SELECT "+issueCols+" FROM autopilot_issues WHERE id="+ph(1), id)
	i, err := scanIssue(row)
	return i, err == nil
}

func (s *server) listIssuesBy(ctx context.Context, status string) []Issue {
	db, ph := s.db(ctx)
	out := []Issue{}
	if db == nil {
		return out
	}
	q := "SELECT " + issueCols + " FROM autopilot_issues"
	args := []any{}
	if status != "" {
		q += " WHERE status=" + ph(1)
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC LIMIT 500"
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		if i, err := scanIssue(rows); err == nil {
			out = append(out, i)
		}
	}
	return out
}

func (s *server) insertIssue(ctx context.Context, i Issue) error {
	db, ph := s.db(ctx)
	if db == nil {
		return sql.ErrConnDone
	}
	_, err := db.ExecContext(ctx,
		"INSERT INTO autopilot_issues ("+issueCols+") VALUES ("+
			ph(1)+","+ph(2)+","+ph(3)+","+ph(4)+","+ph(5)+","+ph(6)+","+ph(7)+","+ph(8)+","+ph(9)+","+ph(10)+","+ph(11)+","+ph(12)+","+ph(13)+","+ph(14)+")",
		i.ID, i.Title, i.Body, i.Status, i.Kind, i.Assignee, i.ClaimedBy, i.ClaimedAt,
		i.Branch, i.PRURL, i.Result, i.CreatedBy, i.CreatedAt, i.UpdatedAt)
	return err
}

// setIssueStatus updates status (+ optional fields) and stamps updated_at.
func (s *server) setIssueStatus(ctx context.Context, id, status string, fields map[string]string) error {
	db, ph := s.db(ctx)
	if db == nil {
		return sql.ErrConnDone
	}
	set := "status=" + ph(1) + ", updated_at=" + ph(2)
	args := []any{status, nowStr()}
	n := 3
	for k, v := range fields {
		set += ", " + k + "=" + ph(n)
		args = append(args, v)
		n++
	}
	args = append(args, id)
	_, err := db.ExecContext(ctx, "UPDATE autopilot_issues SET "+set+" WHERE id="+ph(n), args...)
	return err
}

func (s *server) insertComment(ctx context.Context, c Comment) error {
	db, ph := s.db(ctx)
	if db == nil {
		return sql.ErrConnDone
	}
	_, err := db.ExecContext(ctx,
		"INSERT INTO autopilot_comments (id,issue_id,author,author_kind,body,created_at) VALUES ("+
			ph(1)+","+ph(2)+","+ph(3)+","+ph(4)+","+ph(5)+","+ph(6)+")",
		c.ID, c.IssueID, c.Author, c.AuthorKind, c.Body, c.CreatedAt)
	return err
}

func (s *server) commentsFor(ctx context.Context, issueID string) []Comment {
	db, ph := s.db(ctx)
	out := []Comment{}
	if db == nil {
		return out
	}
	rows, err := db.QueryContext(ctx,
		"SELECT id,issue_id,author,author_kind,body,created_at FROM autopilot_comments WHERE issue_id="+ph(1)+" ORDER BY created_at ASC", issueID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.IssueID, &c.Author, &c.AuthorKind, &c.Body, &c.CreatedAt); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// ---- handlers ----

func (s *server) listIssues(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.listIssuesBy(r.Context(), r.URL.Query().Get("status")))
}

func (s *server) getIssue(w http.ResponseWriter, r *http.Request) {
	i, ok := s.getIssueByID(r.Context(), chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, 404, "issue not found")
		return
	}
	writeJSON(w, 200, map[string]any{"issue": i, "comments": s.commentsFor(r.Context(), i.ID)})
}

func (s *server) createIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title, Body, Kind, Assignee, CreatedBy, Status string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeErr(w, 400, "title is required")
		return
	}
	i := Issue{
		ID: genID(), Title: strings.TrimSpace(body.Title), Body: body.Body,
		Status: firstNonEmpty(body.Status, StatusBacklog), Kind: firstNonEmpty(body.Kind, "feature"),
		Assignee: body.Assignee, CreatedBy: firstNonEmpty(body.CreatedBy, "human"),
		CreatedAt: nowStr(), UpdatedAt: nowStr(),
	}
	if !validStatus(i.Status) {
		writeErr(w, 400, "invalid status")
		return
	}
	if err := s.insertIssue(r.Context(), i); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, i)
}

func (s *server) patchIssue(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cur, ok := s.getIssueByID(r.Context(), id)
	if !ok {
		writeErr(w, 404, "issue not found")
		return
	}
	var body struct {
		Title, Body, Kind, Assignee *string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	fields := map[string]string{}
	if body.Title != nil {
		fields["title"] = *body.Title
	}
	if body.Body != nil {
		fields["body"] = *body.Body
	}
	if body.Kind != nil {
		fields["kind"] = *body.Kind
	}
	if body.Assignee != nil {
		fields["assignee"] = *body.Assignee
	}
	if err := s.setIssueStatus(r.Context(), id, cur.Status, fields); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	updated, _ := s.getIssueByID(r.Context(), id)
	writeJSON(w, 200, updated)
}

// setStatus is the human control: move an issue between workflow states
// (e.g. backlog -> ready hands it to the agents; in_review -> done accepts it).
func (s *server) setStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.getIssueByID(r.Context(), id); !ok {
		writeErr(w, 404, "issue not found")
		return
	}
	var body struct{ Status string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !validStatus(body.Status) {
		writeErr(w, 400, "invalid status")
		return
	}
	// Moving back to ready clears any prior claim so an agent can re-pick it.
	fields := map[string]string{}
	if body.Status == StatusReady {
		fields["claimed_by"] = ""
		fields["claimed_at"] = ""
	}
	if err := s.setIssueStatus(r.Context(), id, body.Status, fields); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	i, _ := s.getIssueByID(r.Context(), id)
	writeJSON(w, 200, i)
}

func (s *server) listComments(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.commentsFor(r.Context(), chi.URLParam(r, "id")))
}

// addComment appends to the thread. Human comment on a BLOCKED issue unblocks it
// (-> ready), which is exactly how a human answers an agent's question and lets
// the loop resume.
func (s *server) addComment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := s.getIssueByID(r.Context(), id)
	if !ok {
		writeErr(w, 404, "issue not found")
		return
	}
	var body struct{ Author, AuthorKind, Body string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeErr(w, 400, "comment body is required")
		return
	}
	kind := firstNonEmpty(body.AuthorKind, "human")
	c := Comment{ID: genID(), IssueID: id, Author: firstNonEmpty(body.Author, "human"),
		AuthorKind: kind, Body: body.Body, CreatedAt: nowStr()}
	if err := s.insertComment(r.Context(), c); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	unblocked := false
	if kind == "human" && issue.Status == StatusBlocked {
		if err := s.setIssueStatus(r.Context(), id, StatusReady, map[string]string{"claimed_by": "", "claimed_at": ""}); err == nil {
			unblocked = true
		}
	}
	writeJSON(w, 201, map[string]any{"comment": c, "unblocked": unblocked})
}

// submitFeedback is the Feedback SDK ingress: a lightweight, unauthenticated
// endpoint that files a feedback issue (backlog) from anywhere in the product.
func (s *server) submitFeedback(w http.ResponseWriter, r *http.Request) {
	var body struct{ Message, Page, User string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		writeErr(w, 400, "message is required")
		return
	}
	title := body.Message
	if len(title) > 72 {
		title = title[:72] + "…"
	}
	desc := body.Message
	if body.Page != "" {
		desc += "\n\n— from " + body.Page
	}
	i := Issue{ID: genID(), Title: title, Body: desc, Status: StatusBacklog, Kind: "feedback",
		CreatedBy: firstNonEmpty(body.User, "feedback"), CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.insertIssue(r.Context(), i); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "id": i.ID})
}

// ---- small helpers ----

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func validStatus(s string) bool {
	switch s {
	case StatusBacklog, StatusReady, StatusInProgress, StatusBlocked, StatusInReview, StatusDone:
		return true
	}
	return false
}
