package httpx

import (
	"html/template"
	"log"
	"net/http"

	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/directory"
	"github.com/arvinizadi/fathom/internal/rbac"
)

// DashboardDeps are the dashboard's collaborators.
type DashboardDeps struct {
	Employees   *directory.Repo
	Calendars   *calendar.Repo
	Revoker     Revoker
	Auth        *AuthMiddleware
	Templates   *template.Template
	CompanyName string
}

// DashboardHandler serves the admin and employee dashboards (spec §46-47).
type DashboardHandler struct{ d DashboardDeps }

func NewDashboardHandler(d DashboardDeps) *DashboardHandler { return &DashboardHandler{d: d} }

// Register wires dashboard routes behind auth + permission checks (spec §41).
func (h *DashboardHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", h.d.Auth.RequirePerm(rbac.ViewOrgStatus, h.admin))
	mux.HandleFunc("GET /me", h.d.Auth.RequirePerm(rbac.ViewOwnStatus, h.me))
	mux.HandleFunc("POST /me/disconnect", h.d.Auth.RequirePerm(rbac.RevokeSelf, h.disconnect))
	mux.HandleFunc("POST /me/calendars", h.d.Auth.RequirePerm(rbac.ConnectSelf, h.manageCalendars))
}

func (h *DashboardHandler) admin(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	org := db.OrgID(p.OrgID)
	stats, err := h.d.Employees.Stats(r.Context(), org)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	rows, err := h.d.Employees.EmployeeRows(r.Context(), org)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	h.render(w, "admin_dashboard.html", map[string]any{
		"CompanyName": h.d.CompanyName, "Stats": stats, "Rows": rows,
	})
}

func (h *DashboardHandler) me(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	cals, err := h.d.Calendars.ListAllCalendars(r.Context(), p.EmployeeID)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	emp, err := h.d.Employees.GetByID(r.Context(), db.OrgID(p.OrgID), p.EmployeeID)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	h.render(w, "employee_dashboard.html", map[string]any{
		"Status": emp.OnboardingStatus, "Calendars": cals, "CSRF": csrfFrom(r),
	})
}

func (h *DashboardHandler) disconnect(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	if err := h.d.Revoker.Revoke(r.Context(), db.OrgID(p.OrgID), p.EmployeeID); err != nil {
		log.Printf("dashboard: self-disconnect: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/me", http.StatusSeeOther)
}

func (h *DashboardHandler) manageCalendars(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// The set of checked calendars becomes the enabled set; others are disabled.
	enabled := map[string]bool{}
	for _, uid := range r.Form["enabled"] {
		enabled[uid] = true
	}
	all, err := h.d.Calendars.ListAllCalendars(r.Context(), p.EmployeeID)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	for _, c := range all {
		want := enabled[c.UID]
		if want != c.Enabled {
			if err := h.d.Calendars.SetCalendarEnabled(r.Context(), p.EmployeeID, c.UID, want); err != nil {
				log.Printf("dashboard: set calendar enabled: %v", err)
			}
		}
	}
	http.Redirect(w, r, "/me", http.StatusSeeOther)
}

func (h *DashboardHandler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.d.Templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("dashboard: render %s: %v", name, err)
	}
}

// compile-time: DashboardHandler uses context for typed helpers.
