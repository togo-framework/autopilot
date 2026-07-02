package autopilot

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// maxUpload caps a single attachment. Bytes are stored base64 in a TEXT column,
// so keep it modest (in-app tool, not a media host).
const maxUpload = 25 << 20 // 25 MB

// Attachment is a stored media/file attachment on an issue. Data lives in the DB
// (base64); URL points at the serve route.
type Attachment struct {
	ID          string `json:"id"`
	IssueID     string `json:"issue_id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	URL         string `json:"url"`
	CreatedAt   string `json:"created_at"`
}

func (a *Attachment) fillURL() { a.URL = "/api/autopilot/uploads/" + a.ID }

// uploadAttachment accepts a multipart "file" and stores it against the issue.
func (s *server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeErr(w, 400, "file too large or malformed (max 25MB)")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "missing 'file'")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil || len(data) == 0 {
		writeErr(w, 400, "empty or unreadable file")
		return
	}
	if len(data) > maxUpload {
		writeErr(w, 413, "file exceeds 25MB")
		return
	}
	ct := hdr.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	a := Attachment{ID: genID(), IssueID: issueID, Name: hdr.Filename, ContentType: ct, Size: len(data), CreatedAt: nowStr()}
	db, ph := s.db(r.Context())
	if db == nil {
		writeErr(w, 500, "no db")
		return
	}
	_, err = db.ExecContext(r.Context(),
		"INSERT INTO autopilot_uploads (id,issue_id,name,content_type,size,data,created_at) VALUES ("+
			ph(1)+","+ph(2)+","+ph(3)+","+ph(4)+","+ph(5)+","+ph(6)+","+ph(7)+")",
		a.ID, a.IssueID, a.Name, a.ContentType, a.Size, base64.StdEncoding.EncodeToString(data), a.CreatedAt)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.fillURL()
	s.emit("issue.updated", map[string]any{"id": issueID})
	writeJSON(w, 201, a)
}

// listAttachments returns an issue's attachments (metadata + URLs, no bytes).
func (s *server) listAttachments(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	db, ph := s.db(r.Context())
	if db == nil {
		writeJSON(w, 200, []Attachment{})
		return
	}
	rows, err := db.QueryContext(r.Context(),
		"SELECT id,issue_id,name,content_type,size,created_at FROM autopilot_uploads WHERE issue_id="+ph(1)+" ORDER BY created_at ASC", issueID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.IssueID, &a.Name, &a.ContentType, &a.Size, &a.CreatedAt); err == nil {
			a.fillURL()
			out = append(out, a)
		}
	}
	writeJSON(w, 200, out)
}

// serveUpload streams the stored bytes with the right Content-Type (rendered
// inline by <img>/<video>/markdown in the dashboard).
func (s *server) serveUpload(w http.ResponseWriter, r *http.Request) {
	uid := chi.URLParam(r, "uid")
	db, ph := s.db(r.Context())
	if db == nil {
		http.Error(w, "no db", 500)
		return
	}
	var name, ct, b64 string
	err := db.QueryRowContext(r.Context(),
		"SELECT name,content_type,data FROM autopilot_uploads WHERE id="+ph(1), uid).Scan(&name, &ct, &b64)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		http.Error(w, "corrupt", 500)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Disposition", "inline; filename=\""+name+"\"")
	_, _ = w.Write(data)
}

// (helper so other files can list attachments if needed)
func (s *server) attachmentsFor(ctx context.Context, issueID string) []Attachment {
	db, ph := s.db(ctx)
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx,
		"SELECT id,issue_id,name,content_type,size,created_at FROM autopilot_uploads WHERE issue_id="+ph(1)+" ORDER BY created_at ASC", issueID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.IssueID, &a.Name, &a.ContentType, &a.Size, &a.CreatedAt); err == nil {
			a.fillURL()
			out = append(out, a)
		}
	}
	return out
}
