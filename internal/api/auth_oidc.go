package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teo-dev/teo/internal/auth"
	"github.com/teo-dev/teo/internal/oidc"
)

// oidcAuthenticator is the slice of *oidc.Provider the handlers need; an
// interface so tests can supply a fake without a live IdP.
type oidcAuthenticator interface {
	AuthCodeURL(state, nonce string) string
	Exchange(ctx context.Context, code string) (rawIDToken string, err error)
	VerifyIDToken(ctx context.Context, raw, nonce string) (*oidc.IDClaims, error)
}

// RoleResolver maps a verified OIDC identity to TEO roles (and may upsert the
// user). Returning an empty slice means "no roles" — the caller defaults to
// RoleEngineer so a first-time SSO user isn't locked out.
type RoleResolver func(ctx context.Context, email, subject string) ([]auth.Role, error)

const (
	stateCookie = "teo_oidc_state"
	nonceCookie = "teo_oidc_nonce"
	flowTTL     = 10 * time.Minute
)

// oidcLogin starts the Authorization Code flow: stash CSRF state + replay nonce
// in short-lived cookies and 302 to the IdP.
func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeProblem(w, http.StatusServiceUnavailable, "SSO not configured",
			"set TEO_OIDC_ISSUER and TEO_OIDC_CLIENT_ID to enable sign-in")
		return
	}
	state, err := randToken()
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Internal error", "could not start sign-in")
		return
	}
	nonce, err := randToken()
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Internal error", "could not start sign-in")
		return
	}
	s.setFlowCookie(w, stateCookie, state)
	s.setFlowCookie(w, nonceCookie, nonce)
	http.Redirect(w, r, s.oidc.AuthCodeURL(state, nonce), http.StatusFound)
}

// oidcCallback completes the flow: validate state, exchange the code, verify the
// ID token, resolve roles, issue a TEO JWT into an httpOnly session cookie, and
// redirect back to the UI.
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeProblem(w, http.StatusServiceUnavailable, "SSO not configured", "sign-in is disabled")
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		writeProblem(w, http.StatusUnauthorized, "Sign-in failed",
			e+": "+r.URL.Query().Get("error_description"))
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeProblem(w, http.StatusBadRequest, "Bad callback", "missing code or state")
		return
	}
	stateCk, err := r.Cookie(stateCookie)
	if err != nil || stateCk.Value == "" || !constantTimeEqual(stateCk.Value, state) {
		writeProblem(w, http.StatusBadRequest, "Bad callback", "state mismatch (possible CSRF or expired sign-in)")
		return
	}
	nonce := ""
	if c, err := r.Cookie(nonceCookie); err == nil {
		nonce = c.Value
	}

	ctx := r.Context()
	raw, err := s.oidc.Exchange(ctx, code)
	if err != nil {
		writeProblem(w, http.StatusBadGateway, "Sign-in failed", "token exchange failed")
		return
	}
	claims, err := s.oidc.VerifyIDToken(ctx, raw, nonce)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "Sign-in failed", "could not verify identity token")
		return
	}

	roles := []auth.Role{auth.RoleEngineer}
	if s.roleResolver != nil {
		if resolved, err := s.roleResolver(ctx, claims.Email, claims.Subject); err == nil && len(resolved) > 0 {
			roles = resolved
		}
	}

	token, err := s.jwt.Issue(claims.Subject, claims.Email, roles)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Internal error", "could not issue session")
		return
	}
	s.setSessionCookie(w, token)
	s.clearCookie(w, stateCookie)
	s.clearCookie(w, nonceCookie)

	dest := s.uiBaseURL
	if dest == "" {
		dest = "/"
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// oidcLogout clears the session cookie.
func (s *Server) oidcLogout(w http.ResponseWriter, r *http.Request) {
	s.clearCookie(w, auth.SessionCookie)
	dest := s.uiBaseURL
	if dest == "" {
		dest = "/"
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// session reports the currently authenticated principal (from the session
// cookie the auth middleware already verified). The UI calls it to know who is
// signed in and what they can do.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	if p == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "not signed in")
		return
	}
	roles := make([]string, len(p.Roles))
	for i, role := range p.Roles {
		roles[i] = string(role)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"userId": p.UserID,
		"email":  p.Email,
		"roles":  roles,
	})
}

// refresh re-issues a fresh session JWT for an already-authenticated browser,
// extending the session without a round-trip to the IdP (S-03-02 "JWT refresh").
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	if p == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "not signed in")
		return
	}
	token, err := s.jwt.Issue(p.UserID, p.Email, p.Roles)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Internal error", "could not refresh session")
		return
	}
	s.setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"expiresInSeconds": int(s.cfg.JWTTTL.Seconds()),
	})
}

// --- cookie helpers --------------------------------------------------------

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.JWTTTL.Seconds()),
	})
}

func (s *Server) setFlowCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/auth",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(flowTTL.Seconds()),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	path := "/"
	if name == stateCookie || name == nonceCookie {
		path = "/auth"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// DBRoleResolver upserts the signed-in user into teo.users (linking the OIDC
// subject) and returns their global roles from teo.user_roles. A user with no
// assigned roles yields an empty slice, so the callback's RoleEngineer default
// applies. Returns no roles (not an error) when the IdP supplied no email,
// since teo.users keys on a non-null email.
func DBRoleResolver(pool *pgxpool.Pool) RoleResolver {
	return func(ctx context.Context, email, subject string) ([]auth.Role, error) {
		if email == "" {
			return nil, nil
		}
		var userID string
		if err := pool.QueryRow(ctx, `
            INSERT INTO teo.users (email, display_name, oidc_subject)
            VALUES ($1, $1, NULLIF($2,''))
            ON CONFLICT (email) DO UPDATE
                SET oidc_subject = COALESCE(teo.users.oidc_subject, EXCLUDED.oidc_subject),
                    updated_at = now()
            RETURNING id
        `, email, subject).Scan(&userID); err != nil {
			return nil, err
		}
		rows, err := pool.Query(ctx,
			`SELECT role FROM teo.user_roles WHERE user_id = $1 AND repo_id IS NULL`, userID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var roles []auth.Role
		for rows.Next() {
			var r string
			if err := rows.Scan(&r); err != nil {
				return nil, err
			}
			roles = append(roles, auth.Role(r))
		}
		return roles, rows.Err()
	}
}

func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// constantTimeEqual avoids leaking the state value via timing on comparison.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
