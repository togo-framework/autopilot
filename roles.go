package autopilot

import (
	"context"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/togo-framework/auth"
)

// Roles. Autopilot does not keep its own user table: it maps to the roles of
// the auth plugin (auth.Identity.Roles): "autopilot:viewer", "autopilot:reporter"
// and "autopilot:operator". The names are fixed and matched exactly. There is no
// configurable prefix, so no setting can turn a plain "viewer" or "admin" role
// into Autopilot access.
// Each level includes the ones below it. A signed-in user with none of them is
// refused with 403: no role is granted implicitly, and the host app's own roles
// (admin, ...) mean nothing here.
//
//	viewer   read issues, comments, attachments and the live stream
//	reporter viewer + create issues (always as backlog) and work on the issues they created
//	operator reporter + change status, hand to the agent, edit any issue, upload
type level int

const (
	levelNone level = iota
	levelViewer
	levelReporter
	levelOperator
)

func (l level) String() string {
	switch l {
	case levelViewer:
		return "viewer"
	case levelReporter:
		return "reporter"
	case levelOperator:
		return "operator"
	}
	return "none"
}

// actor is the authenticated caller, derived from the session only. Nothing in
// a request body can change it.
type actor struct {
	ID    string // stable id of the account (stored as created_by)
	Name  string // shown as the comment author
	Level level
}

const rolePrefix = "autopilot:"

// levelOf returns the highest Autopilot level the identity holds. Role names
// are matched exactly, so "operator" or "admin" without the prefix grant nothing.
func levelOf(id *auth.Identity) level {
	switch {
	case id.HasRole(rolePrefix + "operator"):
		return levelOperator
	case id.HasRole(rolePrefix + "reporter"):
		return levelReporter
	case id.HasRole(rolePrefix + "viewer"):
		return levelViewer
	}
	return levelNone
}

// actorFor resolves the caller. ok is false when there is no authenticated
// session (the caller gets 401). Without the auth plugin (only possible with the
// development-only opt-out) the caller is a local operator.
func (s *server) actorFor(r *http.Request) (actor, bool) {
	if s.identity != nil {
		id, ok := s.identity(r)
		if !ok || id == nil {
			return actor{}, false
		}
		return newActor(id), true
	}
	if s.auth == nil {
		return actor{ID: "dev", Name: "dev", Level: levelOperator}, true
	}
	id, ok := auth.IdentityFrom(r.Context())
	if !ok || id == nil {
		return actor{}, false
	}
	return newActor(id), true
}

func newActor(id *auth.Identity) actor {
	return actor{ID: id.ID, Name: firstNonEmpty(id.Email, id.ID), Level: levelOf(id)}
}

// need guards a route: 401 without a session, 403 below the required level.
// Denials are logged with the actor, the route and the level that was required;
// the response body never names a role.
func (s *server) need(min level) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := s.actorFor(r)
			if !ok {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if a.Level < min {
				s.denied(a, r, min)
				writeErr(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// jsonOnly rejects a request body that is not application/json (415). A browser
// cannot send that type cross-site without a CORS preflight, so this closes the
// "text/plain form post" route to a session cookie that does not carry a CSRF
// token (the auth plugin's double-submit guard covers only its own routes).
func jsonOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				writeErr(w, http.StatusUnsupportedMediaType, "content type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginOnly refuses a browser request that comes from another origin. The
// upload is multipart/form-data, which a page can send cross-site as a plain form
// post, so jsonOnly cannot cover it, and SameSite=Lax does not stop a sibling
// subdomain. Browsers always send Origin on a POST, so a present Origin whose
// host differs from ours (or is "null") is refused. A request without Origin
// (curl, a server-side client) is not a browser with ambient cookies and passes.
func sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
				writeErr(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// stillAllowed re-reads the caller's access for a connection that is already
// open. The role in a session token is a snapshot from sign-in, so it asks the
// auth plugin for the account's CURRENT roles (UserByID) and also re-validates
// the token itself (expiry). It reports false when the account is gone, has no
// Autopilot role any more, or the token has expired.
func (s *server) stillAllowed(ctx context.Context, r *http.Request) bool {
	if s.identity != nil {
		id, ok := s.identity(r)
		return ok && id != nil && levelOf(id) >= levelViewer
	}
	if s.auth == nil {
		return true // development opt-out: no accounts to revoke
	}
	id, err := s.auth.Authenticate(r)
	if err != nil || id == nil {
		return false
	}
	cur, err := s.auth.UserByID(ctx, id.ID)
	if err != nil || cur == nil {
		return false
	}
	return levelOf(cur) >= levelViewer
}

// deny answers 403 for a rule that depends on the data (for example "own
// issues only") and logs it the same way as need.
func (s *server) deny(w http.ResponseWriter, r *http.Request, a actor, min level) {
	s.denied(a, r, min)
	writeErr(w, http.StatusForbidden, "forbidden")
}

func (s *server) denied(a actor, r *http.Request, min level) {
	if s.k != nil && s.k.Log != nil {
		s.k.Log.Warn("autopilot: denied", "actor", a.ID, "has", a.Level.String(),
			"needs", min.String(), "method", r.Method, "path", r.URL.Path)
	}
}

// owns reports whether the actor created the issue.
func (a actor) owns(createdBy string) bool {
	return a.ID != "" && strings.EqualFold(a.ID, createdBy) && createdBy != ""
}
