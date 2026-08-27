// Package web embeds server-side templates and static assets so they ship inside
// the binary.
package web

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Templates parses all embedded HTML templates.
func Templates() (*template.Template, error) {
	return template.ParseFS(templatesFS, "templates/*.html")
}
