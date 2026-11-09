package httpx

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/onboarding"
)

// LegalHandler serves the public home, privacy-policy and terms pages. Google
// requires a home page and privacy policy on the app's authorized domain before
// an OAuth app can be published to production.
type LegalHandler struct {
	templates     *template.Template
	resolve       onboarding.Resolver
	org           db.OrgID
	retentionDays int
}

func NewLegalHandler(t *template.Template, resolve onboarding.Resolver, org db.OrgID, retention time.Duration) *LegalHandler {
	return &LegalHandler{templates: t, resolve: resolve, org: org, retentionDays: int(retention.Hours() / 24)}
}

func (h *LegalHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.page("home", "Home"))
	mux.HandleFunc("GET /privacy", h.page("privacy", "Privacy policy"))
	mux.HandleFunc("GET /terms", h.page("terms", "Terms of service"))
}

// legalUpdated is the date the policy text last changed.
const legalUpdated = "9 November 2026"

func (h *LegalHandler) page(page, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := h.data(r.Context())
		data["Page"], data["Title"] = page, title
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		if err := h.templates.ExecuteTemplate(w, "legal.html", data); err != nil {
			log.Printf("legal: render %s: %v", page, err)
		}
	}
}

func (h *LegalHandler) data(ctx context.Context) map[string]any {
	_, b := h.resolve(ctx, h.org)
	app, company := b.AppName, b.CompanyName
	if app == "" {
		app = "Calendar Bridge"
	}
	if company == "" {
		company = "the company"
	}
	return map[string]any{
		"AppName": app, "CompanyName": company, "SupportAddr": b.SupportAddr,
		"RetentionDays": h.retentionDays, "Updated": legalUpdated, "Year": time.Now().Year(),
	}
}
