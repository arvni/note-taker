package httpx

import (
	"context"
	"net/http"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/rbac"
)

type principalKey struct{}

// withPrincipal annotates the request context with the authenticated principal.
func withPrincipal(ctx context.Context, p *rbac.Principal, csrf string) context.Context {
	ctx = context.WithValue(ctx, principalKey{}, p)
	ctx = WithOrg(ctx, db.OrgID(p.OrgID))
	ctx = context.WithValue(ctx, csrfKey{}, csrf)
	return ctx
}

type csrfKey struct{}

// PrincipalFrom returns the authenticated principal, if any.
func PrincipalFrom(r *http.Request) (*rbac.Principal, bool) {
	p, ok := r.Context().Value(principalKey{}).(*rbac.Principal)
	return p, ok
}

func csrfFrom(r *http.Request) string {
	s, _ := r.Context().Value(csrfKey{}).(string)
	return s
}

// AuthMiddleware verifies the session and injects the principal + org context.
// Unauthenticated requests get 401. State-changing methods must also carry a
// valid CSRF token (spec §38).
type AuthMiddleware struct {
	sessions *rbac.SessionManager
}

func NewAuthMiddleware(sessions *rbac.SessionManager) *AuthMiddleware {
	return &AuthMiddleware{sessions: sessions}
}

// Require wraps a handler, enforcing an authenticated session (spec §41, §48).
func (a *AuthMiddleware) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, csrf, err := a.sessions.Verify(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// CSRF check for unsafe methods (spec §38).
		if !safeMethod(r.Method) {
			submitted := r.Header.Get("X-CSRF-Token")
			if submitted == "" {
				submitted = r.FormValue("csrf_token")
			}
			if err := rbac.VerifyCSRF(csrf, submitted); err != nil {
				http.Error(w, "csrf", http.StatusForbidden)
				return
			}
		}
		r = r.WithContext(withPrincipal(r.Context(), p, csrf))
		next(w, r)
	}
}

// RequirePerm wraps a handler, enforcing an authenticated session AND a
// permission (spec §41).
func (a *AuthMiddleware) RequirePerm(perm rbac.Permission, next http.HandlerFunc) http.HandlerFunc {
	return a.Require(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r)
		if p == nil || !p.Can(perm) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}
