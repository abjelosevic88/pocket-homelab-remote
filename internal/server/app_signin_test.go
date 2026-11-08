package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The iPhone app signs in through /app-signin: sign in, Allow, a one-time
// code to pocketremote://, traded with the PKCE verifier for its own token.
func TestAppSignin(t *testing.T) {
	a := newTestApp(t, "")
	sid, err := a.st.CreateSession(context.Background(), 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	q := url.Values{"challenge": {challenge}, "device": {"Ana's iPhone"}}.Encode()

	// not signed in: to the login page, coming back afterwards
	res, _ := noFollow.Get(a.ts.URL + "/app-signin?" + q)
	if loc := res.Header.Get("Location"); res.StatusCode != 302 || !strings.HasPrefix(loc, "/login?next=%2Fapp-signin%3F") {
		t.Fatalf("not signed in → %d %s", res.StatusCode, loc)
	}
	// signed in: the Allow page, then the code
	req, _ := http.NewRequest("GET", a.ts.URL+"/app-signin?"+q, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	res, _ = noFollow.Do(req)
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(body), "Ana&#39;s iPhone") {
		t.Fatalf("allow page %d", res.StatusCode)
	}
	req, _ = http.NewRequest("POST", a.ts.URL+"/app-signin", strings.NewReader(url.Values{"challenge": {challenge}, "device": {"Ana's iPhone"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	res, _ = noFollow.Do(req)
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, "pocketremote://signed-in?code=") {
		t.Fatalf("allow → %s", loc)
	}
	code := strings.TrimPrefix(loc, "pocketremote://signed-in?code=")
	exchange := func(c, v string) (int, map[string]any) {
		res, _ := http.Post(a.ts.URL+"/api/v1/auth/app/exchange", "application/json", strings.NewReader(`{"code":"`+c+`","verifier":"`+v+`"}`))
		var m map[string]any
		_ = json.NewDecoder(res.Body).Decode(&m)
		return res.StatusCode, m
	}
	if st, _ := exchange(code, strings.Repeat("x", 50)); st != 401 {
		t.Fatalf("a wrong verifier got %d", st)
	}
	// one try only: even the right verifier fails after a wrong one
	if st, _ := exchange(code, verifier); st != 401 {
		t.Fatalf("a used code got %d", st)
	}
}

// The app's web view has no cookie: the ticket alone opens the tunnel, once,
// and a signed-in caller must be the ticket's own person.
func TestTunnelTicketOnly(t *testing.T) {
	addr, _ := fakeGuacd(t)
	a := newTestApp(t, addr)
	id := a.addMachine(a.alice, map[string]any{"name": "Mac", "kind": "mac", "host": "127.0.0.1", "port": 5999, "password": "pw"})
	ticket := func() string {
		_, r, _ := a.do(a.alice, "POST", "/api/v1/machines/"+itoa(id)+"/connect", map[string]any{})
		return r["ticket"].(string)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws := "ws" + strings.TrimPrefix(a.ts.URL, "http") + "/api/v1/tunnel?ticket="
	open := func(tk string, h http.Header) string {
		c, _, err := websocket.Dial(ctx, ws+tk, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{"guacamole"}})
		if err != nil {
			return "dial: " + err.Error()
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		_, msg, _ := c.Read(ctx)
		return string(msg)
	}
	tk := ticket()
	if m := open(tk, http.Header{"Origin": {"null"}}); !strings.HasPrefix(m, "0.,") {
		t.Fatalf("ticket only: %q", m)
	}
	if m := open(tk, nil); !strings.Contains(m, "error") {
		t.Fatalf("a used ticket opened again: %q", m)
	}
	if m := open(ticket(), http.Header{"Authorization": {"Bearer " + a.bob}}); !strings.Contains(m, "error") {
		t.Fatalf("bob used alice's ticket: %q", m)
	}
	if m := open("", nil); !strings.Contains(m, "error") {
		t.Fatalf("no ticket: %q", m)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
