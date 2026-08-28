// Package web embeds server-side templates and the built admin SPA so they ship
// inside the binary.
package web

import (
	"embed"
	"html/template"
	"io/fs"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed all:spa/dist
var spaFS embed.FS

// Templates parses all embedded HTML templates.
func Templates() (*template.Template, error) {
	return template.ParseFS(templatesFS, "templates/*.html")
}

// SPA returns the built single-page admin app's file system (the contents of
// spa/dist), or an error if it hasn't been built.
func SPA() (fs.FS, error) {
	return fs.Sub(spaFS, "spa/dist")
}
