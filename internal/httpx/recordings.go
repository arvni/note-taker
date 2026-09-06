package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/calendar"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/email"
	"github.com/arvinizadi/fathom/internal/fireflies"
	"github.com/arvinizadi/fathom/internal/onboarding"
	"github.com/arvinizadi/fathom/internal/rbac"
)

// RecordingsHandler lists downloaded meeting folders (transcripts/summaries from
// the Fireflies webhook), serves the individual files, and can email a meeting's
// files to the matched employees and/or manually entered addresses. Behind admin
// auth; path inputs are sanitized to the recordings root (no traversal).
type RecordingsHandler struct {
	root string
	auth *AuthMiddleware

	// send dependencies (nil-safe: when unset, the send endpoint reports that
	// email is not configured).
	senderFor      onboarding.Resolver
	matchAttendees func(ctx context.Context, org db.OrgID, start time.Time, window time.Duration, title string) ([]calendar.Attendee, error)
	adminOrg       db.OrgID

	// import dependency (nil-safe): resolves the Fireflies API key for pulling a
	// past meeting by id (e.g. one that predates the webhook). Download-only — it
	// does not email attendees.
	firefliesKeyFor func(ctx context.Context, org db.OrgID) string
}

func NewRecordingsHandler(root string, auth *AuthMiddleware) *RecordingsHandler {
	return &RecordingsHandler{root: root, auth: auth}
}

// WithSend wires the dependencies for emailing a recording's files (the same
// composer used by the webhook auto-notify).
func (h *RecordingsHandler) WithSend(
	senderFor onboarding.Resolver,
	match func(ctx context.Context, org db.OrgID, start time.Time, window time.Duration, title string) ([]calendar.Attendee, error),
	adminOrg db.OrgID,
) *RecordingsHandler {
	h.senderFor, h.matchAttendees, h.adminOrg = senderFor, match, adminOrg
	return h
}

// WithImport wires the Fireflies key resolver so past meetings can be pulled by
// id (download-only; no attendee email).
func (h *RecordingsHandler) WithImport(keyFor func(ctx context.Context, org db.OrgID) string) *RecordingsHandler {
	h.firefliesKeyFor = keyFor
	return h
}

func (h *RecordingsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/recordings", h.auth.RequirePerm(rbac.ViewOrgStatus, h.list))
	mux.HandleFunc("GET /api/v1/recordings/{dir}/files/{name}", h.auth.RequirePerm(rbac.ViewOrgStatus, h.file))
	mux.HandleFunc("POST /api/v1/recordings/{dir}/send", h.auth.RequirePerm(rbac.ManagePolicies, h.send))
	mux.HandleFunc("POST /api/v1/recordings/import", h.auth.RequirePerm(rbac.ManagePolicies, h.importFireflies))
	mux.HandleFunc("POST /api/v1/recordings/sync", h.auth.RequirePerm(rbac.ManagePolicies, h.syncFireflies))
}

// existingTranscriptIDs returns the set of Fireflies transcript ids already
// downloaded, derived from the folder names (…_<id>).
func (h *RecordingsHandler) existingTranscriptIDs() map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(h.root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if i := strings.LastIndex(name, "_"); i >= 0 && i+1 < len(name) {
			out[name[i+1:]] = true
		}
	}
	return out
}

// syncFireflies lists all Fireflies transcripts and downloads any not already
// present, so the whole meeting history back-fills in one action. Download-only:
// it does NOT email attendees.
func (h *RecordingsHandler) syncFireflies(w http.ResponseWriter, r *http.Request) {
	if h.firefliesKeyFor == nil {
		writeErr(w, http.StatusServiceUnavailable, "Fireflies import is not configured")
		return
	}
	key := h.firefliesKeyFor(r.Context(), h.adminOrg)
	if key == "" {
		writeErr(w, http.StatusBadRequest, "no Fireflies API key configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	c := fireflies.NewClient(key)
	have := h.existingTranscriptIDs()

	const pageSize = 50
	imported, skipped, failed := 0, 0, 0
	var importedTitles []string
	for skip := 0; skip < 5000; skip += pageSize { // hard cap as a safety net
		refs, err := c.ListTranscripts(ctx, pageSize, skip)
		if err != nil {
			// If we already imported some, report partial success rather than 502.
			if imported == 0 && skipped == 0 {
				writeErr(w, http.StatusBadGateway, "fireflies: "+err.Error())
				return
			}
			break
		}
		if len(refs) == 0 {
			break
		}
		for _, ref := range refs {
			if have[ref.ID] {
				skipped++
				continue
			}
			t, err := c.GetTranscript(ctx, ref.ID)
			if err != nil {
				failed++
				continue
			}
			t.AudioURL, t.VideoURL = c.GetMedia(ctx, ref.ID)
			if _, err := c.Download(ctx, h.root, t); err != nil {
				failed++
				continue
			}
			have[ref.ID] = true
			imported++
			importedTitles = append(importedTitles, t.Title)
		}
		if len(refs) < pageSize {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported, "skipped": skipped, "failed": failed, "titles": importedTitles,
	})
}

// importFireflies pulls a Fireflies transcript + summary by id into the
// recordings folder, so a meeting that predates the webhook shows up in the
// list. Download-only: it deliberately does NOT email attendees.
func (h *RecordingsHandler) importFireflies(w http.ResponseWriter, r *http.Request) {
	if h.firefliesKeyFor == nil {
		writeErr(w, http.StatusServiceUnavailable, "Fireflies import is not configured")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "transcript id is required")
		return
	}
	key := h.firefliesKeyFor(r.Context(), h.adminOrg)
	if key == "" {
		writeErr(w, http.StatusBadRequest, "no Fireflies API key configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	c := fireflies.NewClient(key)
	t, err := c.GetTranscript(ctx, req.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "fireflies: "+err.Error())
		return
	}
	t.AudioURL, t.VideoURL = c.GetMedia(ctx, req.ID)
	dir, err := c.Download(ctx, h.root, t)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "download failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dir": filepath.Base(dir), "title": t.Title})
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

// recMeta mirrors the fields we persist in a meeting folder's metadata.json.
type recMeta struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	DateString    string  `json:"dateString"`
	Duration      float64 `json:"duration"`
	TranscriptURL string  `json:"transcript_url"`
}

func readMeta(dir string) recMeta {
	var m recMeta
	if b, err := os.ReadFile(filepath.Join(dir, "metadata.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
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
		meta := readMeta(full)
		if meta.Title != "" || meta.ID != "" {
			m.ID, m.Title, m.Date, m.Duration, m.TranscriptURL = meta.ID, meta.Title, meta.DateString, meta.Duration, meta.TranscriptURL
		}
		if files, err := os.ReadDir(full); err == nil {
			for _, f := range files {
				if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
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

type sendReq struct {
	Emails    []string `json:"emails"`    // manually entered addresses
	Attendees bool     `json:"attendees"` // also send to the meeting's matched employees
}

type sendResp struct {
	Sent       int      `json:"sent"`
	Recipients []string `json:"recipients"`
	Failed     []string `json:"failed"`
}

func (h *RecordingsHandler) send(w http.ResponseWriter, r *http.Request) {
	if h.senderFor == nil {
		writeErr(w, http.StatusServiceUnavailable, "email is not configured")
		return
	}
	dir := r.PathValue("dir")
	if !safeSegment(dir) {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	full := filepath.Join(h.root, dir)
	if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(h.root)+string(os.PathSeparator)) {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if info, err := os.Stat(full); err != nil || !info.IsDir() {
		writeErr(w, http.StatusNotFound, "recording not found")
		return
	}

	var req sendReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	// Build a deduped recipient set: valid manual addresses + matched attendees.
	recips := map[string]bool{}
	var invalid []string
	for _, raw := range req.Emails {
		addr, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil || strings.TrimSpace(raw) == "" {
			if strings.TrimSpace(raw) != "" {
				invalid = append(invalid, raw)
			}
			continue
		}
		recips[strings.ToLower(addr.Address)] = true
	}
	meta := readMeta(full)
	if req.Attendees && h.matchAttendees != nil {
		if start, ok := parseMeetingStart(meta.DateString); ok {
			if atts, err := h.matchAttendees(r.Context(), h.adminOrg, start, matchWindow, meta.Title); err == nil {
				for _, a := range atts {
					recips[strings.ToLower(a.Email)] = true
				}
			}
		}
	}
	if len(recips) == 0 {
		msg := "no valid recipients"
		if len(invalid) > 0 {
			msg = "no valid recipients (invalid: " + strings.Join(invalid, ", ") + ")"
		}
		writeErr(w, http.StatusBadRequest, msg)
		return
	}

	sender, brand := h.senderFor(r.Context(), h.adminOrg)
	atts := readAttachments(full)
	subject := "Meeting notes: " + orDefault(meta.Title, "recording")
	body := recordingEmailBody(meta.Title, meta.DateString, meta.TranscriptURL, brand, full)

	resp := sendResp{}
	for addr := range recips {
		err := sender.Send(r.Context(), email.Message{
			To: addr, From: brand.FromAddr, Subject: subject, HTMLBody: body, Attachments: atts,
		})
		if err != nil {
			resp.Failed = append(resp.Failed, addr)
			continue
		}
		resp.Sent++
		resp.Recipients = append(resp.Recipients, addr)
	}
	sort.Strings(resp.Recipients)
	sort.Strings(resp.Failed)
	writeJSON(w, http.StatusOK, resp)
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
