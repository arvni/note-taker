package httpx

import (
	"html/template"
	"testing"

	"github.com/arvinizadi/fathom/web"
)

func webTemplates(t *testing.T) (*template.Template, error) {
	t.Helper()
	return web.Templates()
}
