package autopilot

import (
	"net/http"

	"github.com/togo-framework/auth"
)

// meResponse is what GET /api/autopilot/me returns. It names the level only:
// never role names, e-mail or permissions.
type meResponse struct {
	ID     string `json:"id"`
	Level  string `json:"level"`  // none | viewer | reporter | operator
	Source string `json:"source"` // database | token
}

// me tells a signed-in caller its CURRENT Autopilot level, so a client (a native
// app, for example) can decide which controls to show instead of learning by
// failing. Unlike the data routes it does not answer 403 below a level: a caller
// with no Autopilot role gets 200 with level "none", so the client can render a
// "no access" state.
//
// The level is read the same way a write reads it, from the account (UserByID),
// so a demotion shows at once even though the session token still lists the old
// role ("source": "database"). A personal access token has no Autopilot role and
// is never mapped to one: level "none", read from the token ("source": "token").
// No session or an expired token is 401, a deleted account 401, and an account
// store that cannot be read 503, the same split as the write path.
func (s *server) me(w http.ResponseWriter, r *http.Request) {
	// Per caller and changes with a role grant: no status is ever cached, so a shared
	// cache cannot keep a 401 or a 503 and show it to the next caller.
	w.Header().Set("Cache-Control", "no-store")
	out, status := s.currentAccess(r)
	if status == http.StatusServiceUnavailable {
		writeErr(w, status, "temporarily unavailable")
		return
	}
	if status != 0 {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// currentAccess resolves the caller's current level. It returns 0 or the HTTP
// status to answer (401, 503). Without the auth plugin (the development-only
// opt-out) the caller is a local operator, as everywhere else.
func (s *server) currentAccess(r *http.Request) (meResponse, int) {
	if s.identity != nil { // test seam: the identity comes from the function
		id, ok := s.identity(r)
		if !ok || id == nil {
			return meResponse{}, http.StatusUnauthorized
		}
		return meResponse{ID: id.ID, Level: levelOf(id).String(), Source: "token"}, 0
	}
	if s.auth == nil {
		return meResponse{ID: "dev", Level: levelOperator.String(), Source: "token"}, 0
	}
	id, ok := auth.IdentityFrom(r.Context())
	if !ok || id == nil {
		return meResponse{}, http.StatusUnauthorized
	}
	if id.Guard == patGuard {
		return meResponse{ID: id.ID, Level: levelNone.String(), Source: "token"}, 0
	}
	cur, err := s.auth.UserByID(r.Context(), id.ID)
	if err != nil {
		return meResponse{}, http.StatusServiceUnavailable
	}
	if cur == nil {
		return meResponse{}, http.StatusUnauthorized // deleted account
	}
	return meResponse{ID: cur.ID, Level: levelOf(cur).String(), Source: "database"}, 0
}
