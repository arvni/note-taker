package httpx

import (
	"context"
	"net/http"

	"github.com/arvinizadi/fathom/internal/db"
)

// orgContextKey carries the authenticated organization on the request context.
// It is populated by the admin auth middleware (Phase 10); until then the API
// routes that depend on it reject requests lacking it, so no endpoint is exposed
// without tenant context (spec §41, §48).
type orgContextKey struct{}

// WithOrg returns a context annotated with the authenticated org.
func WithOrg(ctx context.Context, org db.OrgID) context.Context {
	return context.WithValue(ctx, orgContextKey{}, org)
}

// orgFromRequest extracts the authenticated org, if present.
func orgFromRequest(r *http.Request) (db.OrgID, bool) {
	org, ok := r.Context().Value(orgContextKey{}).(db.OrgID)
	return org, ok
}
