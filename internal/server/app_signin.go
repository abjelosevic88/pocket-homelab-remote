package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Signing in the iPhone app. The app opens /app-signin in the system's
// sign-in sheet (ASWebAuthenticationSession), which shares Safari's cookies
// and can use passkeys — an app's own web view can do neither, so Pocket
// Core's sign-in through a passkey-only provider (Pocket ID) never finished
// there. Signed in, the person allows the app on a page of ours; the app gets
// a one-time code at pocketremote://signed-in and trades it, with the
// verifier only it knows (PKCE, S256), for its own API token.

const appCallback = "pocketremote://signed-in"

var appChallenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43,128}$`)

type appCode struct {
	userID    int64
	device    string
	challenge string
	expires   time.Time
}

var appCodes = struct {
	sync.Mutex
	m map[string]appCode
}{m: map[string]appCode{}}

func appDevice(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	if s == "" {
		s = "iPhone"
	}
	return s
}

var appSigninPage = template.Must(template.New("app").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Allow Pocket Remote</title>
<style>
:root { color-scheme: light dark; --accent: #3a5bd9; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; font: 16px/1.5 -apple-system, system-ui, sans-serif; background: Canvas; color: CanvasText; }
main { width: min(380px, 100% - 40px); text-align: center; }
img { width: 64px; height: 64px; border-radius: 16px; }
h1 { font-size: 22px; margin: 14px 0 6px; }
p { margin: 0 0 22px; opacity: .75; }
button, a.btn { display: block; width: 100%; box-sizing: border-box; font: inherit; font-weight: 600; padding: 13px; border-radius: 12px; border: 0; cursor: pointer; text-decoration: none; }
button { background: var(--accent); color: #fff; margin-bottom: 10px; }
a.btn { color: var(--accent); background: transparent; }
</style></head><body><main>
<img src="/apple-touch-icon.png" alt="">
<h1>Sign in to the app?</h1>
<p>The Pocket Remote app on <b>{{.Device}}</b> gets its own access to your computers, as <b>{{.Who}}</b>. You can revoke it under Settings → API tokens.</p>
<form method="post" action="/app-signin">
<input type="hidden" name="challenge" value="{{.Challenge}}"><input type="hidden" name="device" value="{{.Device}}">
<button type="submit">Allow</button>
</form>
<a class="btn" href="{{.Cancel}}">Cancel</a>
</main></body></html>`))

// GET /app-signin?challenge=&device= : sign in first if needed, then ask.
func (s *Server) handleAppSignin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !appChallenge.MatchString(q.Get("challenge")) {
		http.Error(w, "This page is for the Pocket Remote app.", http.StatusBadRequest)
		return
	}
	u, _ := s.resolveUser(r)
	if u == nil {
		back := "/app-signin?" + url.Values{"challenge": {q.Get("challenge")}, "device": {appDevice(q.Get("device"))}}.Encode()
		if s.oidcEnabled() {
			http.Redirect(w, r, "/api/v1/auth/oidc/start?next="+url.QueryEscape(back), http.StatusFound)
		} else {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(back), http.StatusFound)
		}
		return
	}
	who := u.Name
	if who == "" {
		who = u.Email
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = appSigninPage.Execute(w, map[string]string{"Device": appDevice(q.Get("device")), "Who": who, "Challenge": q.Get("challenge"),
		"Cancel": appCallback + "?error=cancelled"})
}

// POST /app-signin (the Allow button) → pocketremote://signed-in?code=
func (s *Server) handleAppSigninAllow(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && o != "null" && s.cfg.BaseURL != "" && !strings.EqualFold(o, originOf(s.cfg.BaseURL)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, _ := s.resolveUser(r)
	challenge := r.PostFormValue("challenge")
	if u == nil || !appChallenge.MatchString(challenge) {
		http.Redirect(w, r, appCallback+"?error=signin", http.StatusSeeOther)
		return
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	code := hex.EncodeToString(b)
	appCodes.Lock()
	for k, c := range appCodes.m {
		if time.Now().After(c.expires) {
			delete(appCodes.m, k)
		}
	}
	appCodes.m[code] = appCode{userID: u.ID, device: appDevice(r.PostFormValue("device")), challenge: challenge, expires: time.Now().Add(3 * time.Minute)}
	appCodes.Unlock()
	http.Redirect(w, r, appCallback+"?code="+code, http.StatusSeeOther)
}

// POST /api/v1/auth/app/exchange {code, verifier} → {token, info}
func (s *Server) handleAppExchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	appCodes.Lock()
	c, ok := appCodes.m[in.Code]
	delete(appCodes.m, in.Code) // one try
	appCodes.Unlock()
	sum := sha256.Sum256([]byte(in.Verifier))
	if !ok || time.Now().After(c.expires) || subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(c.challenge)) != 1 {
		writeErr(w, http.StatusUnauthorized, "That sign-in has expired. Try again.")
		return
	}
	plain, t, err := s.store.CreateAPIToken(r.Context(), c.userID, "Pocket Remote app · "+c.device)
	if err != nil {
		s.fail(w, err, "create token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": plain, "info": t})
}

func originOf(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
