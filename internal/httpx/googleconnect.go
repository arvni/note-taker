package httpx

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/google"
	"github.com/arvinizadi/fathom/internal/rbac"
)

// GoogleDestSaver stores the connected destination credential (implemented by
// *tokens.GoogleDestStore).
type GoogleDestSaver interface {
	Save(ctx context.Context, org db.OrgID, refreshToken, calendarID, email string) error
}

// GoogleConnectHandler runs the "Connect Google Calendar" OAuth flow so an admin
// can authorize the destination calendar from the UI (spec §42).
type GoogleConnectHandler struct {
	oauth      *google.OAuthClient
	store      GoogleDestSaver
	sessions   *rbac.SessionManager
	auth       *AuthMiddleware
	calendarID string
	stateKey   []byte
	secure     bool
}

func NewGoogleConnectHandler(oauth *google.OAuthClient, store GoogleDestSaver, sessions *rbac.SessionManager, auth *AuthMiddleware, calendarID string, stateKey []byte, secure bool) *GoogleConnectHandler {
	if calendarID == "" {
		calendarID = "primary"
	}
	return &GoogleConnectHandler{oauth: oauth, store: store, sessions: sessions, auth: auth, calendarID: calendarID, stateKey: stateKey, secure: secure}
}

const gStateCookie = "fathom_gconnect"

func (h *GoogleConnectHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/google/connect", h.auth.RequirePerm(rbac.ManageCalendars, h.connect))
	mux.HandleFunc("GET /oauth/google/callback", h.callback)
}

func (h *GoogleConnectHandler) connect(w http.ResponseWriter, r *http.Request) {
	if h.oauth == nil {
		http.Error(w, "Google OAuth not configured (set GOOGLE_OAUTH_CLIENT_ID/SECRET/REDIRECT_URL)", http.StatusNotImplemented)
		return
	}
	state := randToken32()
	payload := base64.RawURLEncoding.EncodeToString([]byte(state + "|" + itoa64(time.Now().Add(10*time.Minute).Unix())))
	http.SetCookie(w, &http.Cookie{Name: gStateCookie, Value: payload + "." + h.sign(payload), Path: "/",
		HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, h.oauth.AuthCodeURL(state), http.StatusFound)
}

func (h *GoogleConnectHandler) callback(w http.ResponseWriter, r *http.Request) {
	if h.oauth == nil {
		http.Error(w, "Google OAuth not configured", http.StatusNotImplemented)
		return
	}
	// The admin must be signed in — the credential is stored for their org.
	principal, _, err := h.sessions.Verify(r)
	if err != nil {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	if !h.verifyState(r) {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code: "+r.URL.Query().Get("error"), http.StatusBadRequest)
		return
	}
	access, refresh, err := h.oauth.Exchange(r.Context(), code)
	if err != nil {
		log.Printf("google connect: exchange: %v", err)
		http.Error(w, "could not connect Google", http.StatusBadGateway)
		return
	}
	email := h.fetchEmail(r.Context(), access)
	if err := h.store.Save(r.Context(), db.OrgID(principal.OrgID), refresh, h.calendarID, email); err != nil {
		log.Printf("google connect: save: %v", err)
		http.Error(w, "could not save credential", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: gStateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.secure})
	http.Redirect(w, r, "/app/", http.StatusFound)
}

func (h *GoogleConnectHandler) fetchEmail(ctx context.Context, accessToken string) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var u struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(body, &u)
	return u.Email
}

func (h *GoogleConnectHandler) verifyState(r *http.Request) bool {
	c, err := r.Cookie(gStateCookie)
	if err != nil {
		return false
	}
	dot := -1
	for i := len(c.Value) - 1; i >= 0; i-- {
		if c.Value[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return false
	}
	payload, sig := c.Value[:dot], c.Value[dot+1:]
	if subtle.ConstantTimeCompare([]byte(sig), []byte(h.sign(payload))) != 1 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	parts := string(raw)
	bar := -1
	for i := 0; i < len(parts); i++ {
		if parts[i] == '|' {
			bar = i
			break
		}
	}
	if bar < 0 {
		return false
	}
	return parts[:bar] == r.URL.Query().Get("state")
}

func (h *GoogleConnectHandler) sign(payload string) string {
	mac := hmac.New(sha256.New, h.stateKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randToken32() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
