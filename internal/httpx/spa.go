package httpx

import (
	"io/fs"
	"net/http"
	"strings"
)

// SPAHandler serves the embedded admin SPA under /app/ with client-side-routing
// fallback: unknown paths return index.html so the React router can handle them.
type SPAHandler struct {
	files fs.FS
}

func NewSPAHandler(files fs.FS) *SPAHandler { return &SPAHandler{files: files} }

func (h *SPAHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /app/", h.serve)
	// Bare /admin redirects into the SPA.
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/", http.StatusFound)
	})
}

func (h *SPAHandler) serve(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/app/")
	if p == "" {
		p = "index.html"
	}
	// Serve the asset if it exists; otherwise fall back to index.html so the
	// SPA can handle the route.
	if f, err := h.files.Open(p); err == nil {
		f.Close()
	} else {
		p = "index.html"
	}
	// Long-cache fingerprinted assets; never cache index.html.
	if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	servePath := "/" + p
	if p == "index.html" {
		servePath = "/" // FileServer serves index.html for "/" without a redirect
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = servePath
	http.FileServerFS(h.files).ServeHTTP(w, r2)
}
