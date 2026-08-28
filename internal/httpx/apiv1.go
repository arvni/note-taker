package httpx

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/rbac"
)

// StatsProvider / EmployeeLister are the read side for the SPA (implemented by
// *directory.Repo).
type StatsProvider interface {
	Stats(ctx context.Context, org db.OrgID) (directory.OrgStats, error)
	EmployeeRows(ctx context.Context, org db.OrgID) ([]directory.EmployeeRow, error)
}

// APIv1 serves the JSON API consumed by the admin SPA. All routes are behind the
// session auth + CSRF middleware (spec §41, §48); reads require ViewOrgStatus,
// writes require ManageEmployees.
type APIv1 struct {
	stats      StatsProvider
	reconciler Reconciler
	revoker    Revoker
	auth       *AuthMiddleware
}

func NewAPIv1(stats StatsProvider, reconciler Reconciler, revoker Revoker, auth *AuthMiddleware) *APIv1 {
	return &APIv1{stats: stats, reconciler: reconciler, revoker: revoker, auth: auth}
}

func (a *APIv1) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me", a.auth.Require(a.me))
	mux.HandleFunc("GET /api/v1/stats", a.auth.RequirePerm(rbac.ViewOrgStatus, a.getStats))
	mux.HandleFunc("GET /api/v1/employees", a.auth.RequirePerm(rbac.ViewOrgStatus, a.listEmployees))
	mux.HandleFunc("POST /api/v1/employees/import", a.auth.RequirePerm(rbac.ManageEmployees, a.importCSV))
	mux.HandleFunc("POST /api/v1/employees/{id}/revoke", a.auth.RequirePerm(rbac.ManageEmployees, a.revoke))
}

func (a *APIv1) me(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"email": p.Email, "role": p.Role, "org_id": p.OrgID, "csrf": csrfFrom(r),
	})
}

func (a *APIv1) getStats(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	s, err := a.stats.Stats(r.Context(), db.OrgID(p.OrgID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "stats failed")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *APIv1) listEmployees(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	rows, err := a.stats.EmployeeRows(r.Context(), db.OrgID(p.OrgID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	if rows == nil {
		rows = []directory.EmployeeRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"employees": rows})
}

func (a *APIv1) importCSV(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad upload")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	users, rowErrs, err := directory.ParseCSV(file)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rep, err := a.reconciler.Reconcile(r.Context(), db.OrgID(p.OrgID), users)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reconcile failed")
		return
	}
	errStrs := make([]string, 0, len(rowErrs))
	for _, e := range rowErrs {
		errStrs = append(errStrs, e.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"parsed": len(users), "created": rep.Created, "updated": rep.Updated,
		"disabled": rep.Disabled, "reactivated": rep.Reactivated,
		"conflicts": rep.Conflicts, "row_errors": errStrs,
	})
}

func (a *APIv1) revoke(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := a.revoker.Revoke(r.Context(), db.OrgID(p.OrgID), id); err != nil {
		log.Printf("apiv1 revoke: %v", err)
		writeErr(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
