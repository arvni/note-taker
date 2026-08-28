package httpx

import (
	"context"
	"html/template"
	"log"
	"net/http"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/rbac"
)

// Reconciler applies a directory snapshot to employees (spec §20), implemented
// by *directory.Reconciler.
type Reconciler interface {
	Reconcile(ctx context.Context, org db.OrgID, users []directory.User) (directory.Report, error)
}

// ImportHandler lets an admin upload an employee CSV (spec §3 fallback) and
// reconciles it (invite new, offboard inactive). Guarded by admin auth + CSRF.
type ImportHandler struct {
	reconciler Reconciler
	auth       *AuthMiddleware
	templates  *template.Template
}

func NewImportHandler(reconciler Reconciler, auth *AuthMiddleware, tmpl *template.Template) *ImportHandler {
	return &ImportHandler{reconciler: reconciler, auth: auth, templates: tmpl}
}

func (h *ImportHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/import", h.auth.RequirePerm(rbac.ManageEmployees, h.form))
	mux.HandleFunc("POST /admin/employees/import", h.auth.RequirePerm(rbac.ManageEmployees, h.upload))
}

func (h *ImportHandler) form(w http.ResponseWriter, r *http.Request) {
	h.render(w, "import.html", map[string]any{"CSRF": csrfFrom(r)})
}

func (h *ImportHandler) upload(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	// 5MB cap on the uploaded CSV.
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		http.Error(w, "bad upload", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	users, rowErrs, err := directory.ParseCSV(file)
	if err != nil {
		h.render(w, "import.html", map[string]any{"CSRF": csrfFrom(r), "Error": err.Error()})
		return
	}
	rep, err := h.reconciler.Reconcile(r.Context(), db.OrgID(p.OrgID), users)
	if err != nil {
		log.Printf("import: reconcile: %v", err)
		http.Error(w, "reconcile failed", http.StatusInternalServerError)
		return
	}

	rowErrStrs := make([]string, 0, len(rowErrs))
	for _, e := range rowErrs {
		rowErrStrs = append(rowErrStrs, e.Error())
	}
	h.render(w, "import.html", map[string]any{
		"CSRF":        csrfFrom(r),
		"Parsed":      len(users),
		"Created":     rep.Created,
		"Updated":     rep.Updated,
		"Disabled":    rep.Disabled,
		"Reactivated": rep.Reactivated,
		"Conflicts":   rep.Conflicts,
		"RowErrors":   rowErrStrs,
		"Done":        true,
	})
}

func (h *ImportHandler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("import: render: %v", err)
	}
}
