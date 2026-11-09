package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/web"
)

func TestLegalPages(t *testing.T) {
	tmpl, err := web.Templates()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewLegalHandler(tmpl, func(context.Context, db.OrgID) (email.Sender, onboarding.Brand) {
		return email.LogSender{}, onboarding.Brand{AppName: "Note Taker", CompanyName: "Acme <Co>", SupportAddr: "it@acme.com"}
	}, 1, 365*24*time.Hour).Register(mux)

	for path, want := range map[string]string{
		"/":        "connects the work calendars",
		"/privacy": "Limited Use requirements",
		"/terms":   "internal tool provided by",
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		body := rr.Body.String()
		if rr.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("%s: code=%d, missing %q", path, rr.Code, want)
		}
		if strings.Contains(body, "Acme <Co>") || !strings.Contains(body, "Acme &lt;Co&gt;") {
			t.Errorf("%s: company name not escaped", path)
		}
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/nope", nil))
	if rr.Code != 404 {
		t.Errorf("unknown path should 404, got %d", rr.Code)
	}
}
