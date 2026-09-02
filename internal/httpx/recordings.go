package httpx

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arvinizadi/fathom/internal/rbac"
)

// RecordingsHandler lists downloaded meeting folders (transcripts/summaries from
// the Fireflies webhook) and serves the individual files. Behind admin auth;
// path inputs are sanitized to the recordings root (no traversal).
type RecordingsHandler struct {
	root string
	auth *AuthMiddleware
}

func NewRecordingsHandler(root string, auth *AuthMiddleware) *RecordingsHandler {
	return &RecordingsHandler{root: root, auth: auth}
}

func (h *RecordingsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/recordings", h.auth.RequirePerm(rbac.ViewOrgStatus, h.list))
	mux.HandleFunc("GET /api/v1/recordings/{dir}/files/{name}", h.auth.RequirePerm(rbac.ViewOrgStatus, h.file))
}

type recFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type recMeeting struct {
	Dir           string    `json:"dir"`
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Date          string    `json:"date"`
	Duration      float64   `json:"duration"`
	TranscriptURL string    `json:"transcript_url"`
	Files         []recFile `json:"files"`
}

func (h *RecordingsHandler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(h.root)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"meetings": []any{}})
		return
	}
	meetings := []recMeeting{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		m := recMeeting{Dir: dir, Title: dir}
		full := filepath.Join(h.root, dir)
		if b, err := os.ReadFile(filepath.Join(full, "metadata.json")); err == nil {
			var meta struct {
				ID            string  `json:"id"`
				Title         string  `json:"title"`
				DateString    string  `json:"dateString"`
				Duration      float64 `json:"duration"`
				TranscriptURL string  `json:"transcript_url"`
			}
			if json.Unmarshal(b, &meta) == nil {
				m.ID, m.Title, m.Date, m.Duration, m.TranscriptURL = meta.ID, meta.Title, meta.DateString, meta.Duration, meta.TranscriptURL
			}
		}
		if files, err := os.ReadDir(full); err == nil {
			for _, f := range files {
				if f.IsDir() {
					continue
				}
				info, err := f.Info()
				if err != nil {
					continue
				}
				m.Files = append(m.Files, recFile{Name: f.Name(), Size: info.Size()})
			}
		}
		meetings = append(meetings, m)
	}
	// Newest meeting date first.
	sort.Slice(meetings, func(i, j int) bool { return meetings[i].Date > meetings[j].Date })
	writeJSON(w, http.StatusOK, map[string]any{"meetings": meetings})
}

func (h *RecordingsHandler) file(w http.ResponseWriter, r *http.Request) {
	dir := r.PathValue("dir")
	name := r.PathValue("name")
	if !safeSegment(dir) || !safeSegment(name) {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	path := filepath.Join(h.root, dir, name)
	// Defense-in-depth: ensure the resolved path stays under the root.
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(h.root)+string(os.PathSeparator)) {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if r.URL.Query().Get("dl") == "1" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	}
	http.ServeFile(w, r, path)
}

// safeSegment rejects empty, dot, and any segment containing a path separator or
// parent reference.
func safeSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, "/\\") || strings.Contains(s, "..") {
		return false
	}
	return true
}
