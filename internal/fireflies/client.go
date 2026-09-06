// Package fireflies is a minimal client for the Fireflies.ai GraphQL API
// (https://api.fireflies.ai/graphql). It fetches a meeting's transcript and AI
// summary and writes them to a well-organized per-meeting directory. Audio/video
// URLs require a paid Fireflies plan and are fetched best-effort.
package fireflies

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const endpoint = "https://api.fireflies.ai/graphql"

// Client calls the Fireflies GraphQL API with a bearer API key.
type Client struct {
	key  string
	HTTP *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{key: apiKey, HTTP: &http.Client{Timeout: 45 * time.Second}}
}

// Sentence is one line of the transcript.
type Sentence struct {
	Index       int     `json:"index"`
	SpeakerName string  `json:"speaker_name"`
	Text        string  `json:"text"`
	StartTime   float64 `json:"start_time"`
}

// Transcript is the subset of a Fireflies transcript we persist.
type Transcript struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	DateString     string         `json:"dateString"`
	Duration       float64        `json:"duration"`
	HostEmail      string         `json:"host_email"`
	OrganizerEmail string         `json:"organizer_email"`
	Participants   []string       `json:"participants"`
	TranscriptURL  string         `json:"transcript_url"`
	AudioURL       string         `json:"audio_url"`
	VideoURL       string         `json:"video_url"`
	Summary        map[string]any `json:"summary"`
	Sentences      []Sentence     `json:"sentences"`
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fireflies: status %d: %s", resp.StatusCode, truncate(raw))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("fireflies: decode: %w", err)
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("fireflies: %s (%s)", env.Errors[0].Message, env.Errors[0].Code)
	}
	return json.Unmarshal(env.Data, out)
}

// GetTranscript fetches the transcript, summary, and metadata (the fields
// available on all plans). Audio/video are fetched separately by GetMedia so a
// paid-plan restriction on those does not null the whole response.
func (c *Client) GetTranscript(ctx context.Context, id string) (*Transcript, error) {
	const q = `query T($id: String!) {
  transcript(id: $id) {
    id title dateString duration transcript_url
    host_email organizer_email participants
    summary { overview short_summary action_items keywords bullet_gist outline }
    sentences { index speaker_name text start_time }
  }
}`
	var out struct {
		Transcript Transcript `json:"transcript"`
	}
	if err := c.do(ctx, q, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	return &out.Transcript, nil
}

// TranscriptRef is a lightweight listing entry (no sentences/summary).
type TranscriptRef struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	DateString string `json:"dateString"`
}

// ListTranscripts returns a page of the workspace's transcripts, newest first,
// so the app can discover and back-fill meetings that predate the webhook.
func (c *Client) ListTranscripts(ctx context.Context, limit, skip int) ([]TranscriptRef, error) {
	const q = `query L($limit: Int, $skip: Int) {
  transcripts(limit: $limit, skip: $skip) { id title dateString }
}`
	var out struct {
		Transcripts []TranscriptRef `json:"transcripts"`
	}
	if err := c.do(ctx, q, map[string]any{"limit": limit, "skip": skip}, &out); err != nil {
		return nil, err
	}
	return out.Transcripts, nil
}

// GetMedia best-effort fetches the audio/video URLs (paid plans only). On a
// paid-required or any error it returns empty strings without failing.
func (c *Client) GetMedia(ctx context.Context, id string) (audioURL, videoURL string) {
	const q = `query M($id: String!) { transcript(id: $id) { audio_url video_url } }`
	var out struct {
		Transcript struct {
			AudioURL string `json:"audio_url"`
			VideoURL string `json:"video_url"`
		} `json:"transcript"`
	}
	if err := c.do(ctx, q, map[string]any{"id": id}, &out); err != nil {
		return "", ""
	}
	return out.Transcript.AudioURL, out.Transcript.VideoURL
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// Download writes the transcript + summary + metadata into a per-meeting folder
// under root: root/<YYYY-MM-DD>_<title-slug>_<id>/. Audio/video are downloaded
// only when their URLs are available (paid plan). Returns the folder path.
func (c *Client) Download(ctx context.Context, root string, t *Transcript) (string, error) {
	day := "nodate"
	if len(t.DateString) >= 10 {
		day = t.DateString[:10]
	}
	slug := strings.Trim(slugRe.ReplaceAllString(t.Title, "-"), "-")
	if slug == "" {
		slug = "meeting"
	}
	dir := filepath.Join(root, fmt.Sprintf("%s_%s_%s", day, slug, t.ID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	meta := map[string]any{
		"id": t.ID, "title": t.Title, "dateString": t.DateString, "duration": t.Duration,
		"host_email": t.HostEmail, "organizer_email": t.OrganizerEmail,
		"participants": t.Participants, "transcript_url": t.TranscriptURL,
		"audio_url": t.AudioURL, "video_url": t.VideoURL,
	}
	if err := writeJSON(filepath.Join(dir, "metadata.json"), meta); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(dir, "transcript.json"), t.Sentences); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(dir, "summary.json"), t.Summary); err != nil {
		return "", err
	}

	// Readable transcript.
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s  (%.1f min)\n\n", t.Title, t.DateString, t.Duration)
	for _, s := range t.Sentences {
		fmt.Fprintf(&b, "[%02d:%02d] %s: %s\n", int(s.StartTime)/60, int(s.StartTime)%60, orDash(s.SpeakerName), s.Text)
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.txt"), []byte(b.String()), 0o644); err != nil {
		return "", err
	}

	// Readable summary.
	var sb strings.Builder
	for _, kv := range [][2]string{{"OVERVIEW", "overview"}, {"SHORT SUMMARY", "short_summary"}, {"ACTION ITEMS", "action_items"}, {"KEYWORDS", "keywords"}, {"GIST", "bullet_gist"}} {
		if v, ok := t.Summary[kv[1]]; ok && v != nil {
			fmt.Fprintf(&sb, "== %s ==\n%s\n\n", kv[0], asText(v))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.txt"), []byte(sb.String()), 0o644); err != nil {
		return "", err
	}

	// Media (paid plan only): download if URLs present.
	if t.AudioURL != "" {
		if err := c.fetchFile(ctx, t.AudioURL, filepath.Join(dir, "audio")); err != nil {
			os.WriteFile(filepath.Join(dir, "audio.download-error.txt"), []byte(err.Error()), 0o644)
		}
	}
	if t.VideoURL != "" {
		if err := c.fetchFile(ctx, t.VideoURL, filepath.Join(dir, "video")); err != nil {
			os.WriteFile(filepath.Join(dir, "video.download-error.txt"), []byte(err.Error()), 0o644)
		}
	}
	return dir, nil
}

// fetchFile downloads a URL to dest, appending the extension inferred from the
// URL path (defaults kept simple: .mp3/.mp4 when present, else none).
func (c *Client) fetchFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	if ext := extOf(url); ext != "" {
		dest += ext
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func writeJSON(path string, v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func asText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.MarshalIndent(v, "", " ")
	return string(b)
}

func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func extOf(url string) string {
	u := url
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "."); i >= 0 && len(u)-i <= 5 {
		return u[i:]
	}
	return ""
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}
