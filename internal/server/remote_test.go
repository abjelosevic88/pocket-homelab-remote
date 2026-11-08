package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/abjelosevic88/pocket-homelab-remote/internal/guac"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

type testApp struct {
	t     *testing.T
	s     *Server
	st    *store.Store
	ts    *httptest.Server
	alice string // admin token
	bob   string // member token
}

func newTestApp(t *testing.T, guacd string) *testApp {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig(dir)
	if guacd != "" {
		cfg.GuacdAddr = guacd
	}
	cfg.MaxBodyBytes = 1 << 20
	cfg.UploadMaxBytes = 1 << 20
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	a := &testApp{t: t, s: s, st: st}
	for i, who := range []string{"alice", "bob"} {
		role := "member"
		if i == 0 {
			role = "admin"
		}
		u := &store.User{Email: who + "@example.com", Name: who, Role: role, PasswordHash: "x"}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, err := st.CreateAPIToken(ctx, u.ID, "test")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			a.alice = tok
		} else {
			a.bob = tok
		}
	}
	a.ts = httptest.NewServer(s.Router())
	t.Cleanup(a.ts.Close)
	return a
}

func (a *testApp) do(tok, method, path string, body any) (int, map[string]any, string) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.ts.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m, string(raw)
}

func (a *testApp) addMachine(tok string, in map[string]any) int64 {
	a.t.Helper()
	code, m, raw := a.do(tok, "POST", "/api/v1/machines", in)
	if code != http.StatusCreated {
		a.t.Fatalf("add machine: %d %s", code, raw)
	}
	return int64(m["id"].(float64))
}

// Everything is per person: Bob never sees, changes or reaches Alice's machines.
func TestPerPersonIsolation(t *testing.T) {
	a := newTestApp(t, "")
	id := a.addMachine(a.alice, map[string]any{"name": "Alice's Mac", "kind": "mac", "host": "macbook", "password": "hunter2", "mac_address": "a4:83:e7:12:9c:01", "files_enabled": true})
	p := fmt.Sprintf("/api/v1/machines/%d", id)
	for _, c := range []struct{ method, path string }{
		{"GET", p}, {"PUT", p}, {"DELETE", p}, {"POST", p + "/check"}, {"POST", p + "/wake"}, {"POST", p + "/connect"},
		{"POST", p + "/trust-again"}, {"GET", p + "/files"}, {"GET", p + "/files/download?path=/x"}, {"POST", p + "/files/save"},
	} {
		code, _, raw := a.do(a.bob, c.method, c.path, map[string]any{"name": "mine now", "path": "/x"})
		if code != http.StatusNotFound {
			t.Errorf("bob %s %s = %d %s, want 404", c.method, c.path, code, raw)
		}
	}
	_, _, raw := a.do(a.bob, "GET", "/api/v1/machines", nil)
	if strings.TrimSpace(raw) != "[]" {
		t.Errorf("bob sees machines: %s", raw)
	}
	if code, m, _ := a.do(a.alice, "GET", p, nil); code != 200 || m["name"] != "Alice's Mac" {
		t.Errorf("alice lost her machine: %d %v", code, m)
	}
	// the tailnet is the box's: a member does not get the list
	_, m, _ := a.do(a.bob, "GET", "/api/v1/tailnet", nil)
	if m["available"] != false {
		t.Errorf("member got the tailnet: %v", m)
	}
	// sessions too
	mid := id
	_ = a.st.StartSession(context.Background(), 1, &store.RemoteSession{MachineID: &mid, MachineName: "Alice's Mac", Protocol: "vnc", StartedAt: store.Now()})
	_, m, _ = a.do(a.bob, "GET", "/api/v1/sessions", nil)
	if m["total"].(float64) != 0 {
		t.Errorf("bob sees alice's sessions: %v", m)
	}
}

// Passwords are sealed in the database and never sent back.
func TestSecretsNeverLeave(t *testing.T) {
	a := newTestApp(t, "")
	id := a.addMachine(a.alice, map[string]any{"name": "PC", "kind": "windows", "host": "100.64.0.9", "username": "me", "password": "hunter2", "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"})
	m, err := a.st.GetMachine(context.Background(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Secret, "v1:") || strings.Contains(m.Secret, "hunter2") {
		t.Fatalf("password not sealed: %q", m.Secret)
	}
	if plain, _ := a.s.seal.Open(m.Secret); plain != "hunter2" {
		t.Fatalf("seal round trip = %q", plain)
	}
	for _, path := range []string{"/api/v1/machines", fmt.Sprintf("/api/v1/machines/%d", id), "/api/v1/export.json"} {
		_, _, raw := a.do(a.alice, "GET", path, nil)
		if strings.Contains(raw, "hunter2") || strings.Contains(raw, m.Secret) || strings.Contains(raw, "PRIVATE KEY") {
			t.Errorf("%s leaks a secret: %s", path, raw)
		}
	}
	_, v, _ := a.do(a.alice, "GET", fmt.Sprintf("/api/v1/machines/%d", id), nil)
	if v["has_password"] != true || v["has_key"] != true || v["protocol"] != "rdp" || v["effective_port"].(float64) != 3389 {
		t.Errorf("view = %v", v)
	}
	// an edit without a password keeps the saved one
	a.do(a.alice, "PUT", fmt.Sprintf("/api/v1/machines/%d", id), map[string]any{"name": "Gaming PC"})
	m2, _ := a.st.GetMachine(context.Background(), 1, id)
	if m2.Secret != m.Secret || m2.Name != "Gaming PC" {
		t.Errorf("edit lost the password or the name: %+v", m2)
	}
	// and clear_password removes it
	a.do(a.alice, "PUT", fmt.Sprintf("/api/v1/machines/%d", id), map[string]any{"clear_password": true})
	m3, _ := a.st.GetMachine(context.Background(), 1, id)
	if m3.Secret != "" {
		t.Errorf("password not cleared")
	}
}

func TestMachineValidation(t *testing.T) {
	a := newTestApp(t, "")
	for _, in := range []map[string]any{
		{"name": "", "host": "x"},
		{"name": "x", "host": ""},
		{"name": "x", "host": "a b"},
		{"name": "x", "host": "pc", "protocol": "telnet"},
		{"name": "x", "host": "pc", "port": 70000},
		{"name": "x", "host": "pc", "mac_address": "nope"},
	} {
		if code, _, raw := a.do(a.alice, "POST", "/api/v1/machines", in); code != http.StatusBadRequest {
			t.Errorf("%v accepted: %d %s", in, code, raw)
		}
	}
	// a pasted vnc:// address is tidied
	id := a.addMachine(a.alice, map[string]any{"name": "Mac", "kind": "mac", "host": "vnc://macbook.tail0c02cf.ts.net/"})
	_, v, _ := a.do(a.alice, "GET", fmt.Sprintf("/api/v1/machines/%d", id), nil)
	if v["host"] != "macbook.tail0c02cf.ts.net" || v["protocol"] != "vnc" {
		t.Errorf("host = %v protocol = %v", v["host"], v["protocol"])
	}
}

// A link token reaches only what its scope covers; never a session or files.
func TestLinkScopes(t *testing.T) {
	cases := []struct {
		scope, method, path string
		want                bool
	}{
		{"remote:read", "GET", "/api/v1/machines", true},
		{"remote:read", "GET", "/api/v1/machines/3", true},
		{"remote:read", "GET", "/api/v1/sessions", true},
		{"remote:read", "POST", "/api/v1/machines/3/check", true},
		{"remote:read", "POST", "/api/v1/machines/3/wake", false},
		{"remote:wake", "POST", "/api/v1/machines/3/wake", true},
		{"remote:read remote:wake", "POST", "/api/v1/machines/3/connect", false},
		{"remote:read remote:wake", "GET", "/api/v1/tunnel", false},
		{"remote:read remote:wake", "GET", "/api/v1/machines/3/files", false},
		{"remote:read remote:wake", "PUT", "/api/v1/machines/3", false},
		{"remote:read remote:wake", "GET", "/api/v1/settings", false},
		{"", "GET", "/api/v1/auth/me", true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := linkAllows(strings.Fields(c.scope), r); got != c.want {
			t.Errorf("%q %s %s = %v, want %v", c.scope, c.method, c.path, got, c.want)
		}
	}
}

// Exporting and importing twice adds things once.
func TestExportImportIdempotent(t *testing.T) {
	a := newTestApp(t, "")
	id := a.addMachine(a.alice, map[string]any{"name": "Mac", "kind": "mac", "host": "macbook", "password": "pw"})
	_ = a.st.StartSession(context.Background(), 1, &store.RemoteSession{MachineID: &id, MachineName: "Mac", Protocol: "vnc", StartedAt: "2026-11-01T10:00:00Z", EndedAt: "2026-11-01T10:05:00Z", DurationS: 300})
	_, _, raw := a.do(a.alice, "GET", "/api/v1/export.json", nil)
	var doc map[string]any
	_ = json.Unmarshal([]byte(raw), &doc)
	// into Bob's account, twice
	_, c1, _ := a.do(a.bob, "POST", "/api/v1/import", doc)
	_, c2, _ := a.do(a.bob, "POST", "/api/v1/import", doc)
	if c1["machines"].(float64) != 1 || c1["sessions"].(float64) != 1 {
		t.Errorf("first import = %v", c1)
	}
	if c2["machines"].(float64) != 0 || c2["sessions"].(float64) != 0 {
		t.Errorf("second import added again: %v", c2)
	}
	ms, _ := a.st.ListMachines(context.Background(), 2)
	if len(ms) != 1 || ms[0].Secret != "" {
		t.Errorf("bob's machines = %+v (no password should come along)", ms)
	}
}

// Settings and the connect PIN are each person's own.
func TestPINPerPerson(t *testing.T) {
	a := newTestApp(t, "")
	id := a.addMachine(a.alice, map[string]any{"name": "Mac", "kind": "mac", "host": "macbook"})
	if code, _, raw := a.do(a.alice, "PUT", "/api/v1/settings", map[string]any{"pin": "12"}); code != 400 {
		t.Errorf("short PIN accepted: %s", raw)
	}
	a.do(a.alice, "PUT", "/api/v1/settings", map[string]any{"pin": "4242", "touch_mode": "touch"})
	_, sa, _ := a.do(a.alice, "GET", "/api/v1/settings", nil)
	_, sb, _ := a.do(a.bob, "GET", "/api/v1/settings", nil)
	if sa["pin_set"] != true || sb["pin_set"] != false || sa["touch_mode"] != "touch" || sb["touch_mode"] != "trackpad" {
		t.Fatalf("settings leaked between people: alice %v bob %v", sa, sb)
	}
	if _, ok := sa["pin_hash"]; ok {
		t.Errorf("the PIN hash is sent to the browser")
	}
	path := fmt.Sprintf("/api/v1/machines/%d/connect", id)
	_, r1, _ := a.do(a.alice, "POST", path, map[string]any{})
	if r1["need_pin"] != true {
		t.Errorf("no PIN asked: %v", r1)
	}
	_, r2, _ := a.do(a.alice, "POST", path, map[string]any{"pin": "0000"})
	if r2["error"] == nil {
		t.Errorf("wrong PIN accepted: %v", r2)
	}
	_, r3, _ := a.do(a.alice, "POST", path, map[string]any{"pin": "4242"})
	if r3["need_pin"] == true {
		t.Errorf("right PIN refused: %v", r3)
	}
	for i := 0; i < 5; i++ {
		a.do(a.alice, "POST", path, map[string]any{"pin": "1111"})
	}
	if code, _, _ := a.do(a.alice, "POST", path, map[string]any{"pin": "4242"}); code != http.StatusTooManyRequests {
		t.Errorf("no lock-out after wrong PINs: %d", code)
	}
	// changing the PIN needs the current one
	if code, _, _ := a.do(a.alice, "PUT", "/api/v1/settings", map[string]any{"pin_clear": true, "pin_current": "9999"}); code != 400 {
		t.Errorf("PIN removed without the current one")
	}
}

// fakeGuacd answers the handshake, then sends a sync and echoes what it gets.
func fakeGuacd(t *testing.T) (string, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan string, 64)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rd := guac.NewReader(c)
				sel, err := rd.Read()
				if err != nil {
					return // the status probe only knocks
				}
				got <- "select:" + strings.Join(sel.Args, ",")
				io.WriteString(c, guac.Encode("args", "VERSION_1_5_0", "hostname", "port", "password", "username"))
				for {
					in, err := rd.Read()
					if err != nil {
						return
					}
					if in.Opcode == "connect" {
						got <- "connect:" + strings.Join(in.Args, ",")
						break
					}
				}
				io.WriteString(c, guac.Encode("ready", "$abc"))
				io.WriteString(c, guac.Encode("size", "0", "1024", "768")+guac.Encode("sync", "1"))
				for {
					raw, op, err := rd.ReadRaw()
					if err != nil {
						got <- "eof"
						return
					}
					got <- "relay:" + op
					if op == "key" {
						io.WriteString(c, raw)
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String(), got
}

// The tunnel: ticket → guacd handshake with the sealed password → relay both
// ways, pings answered, the session logged.
func TestTunnelEndToEnd(t *testing.T) {
	addr, got := fakeGuacd(t)
	a := newTestApp(t, addr)
	id := a.addMachine(a.alice, map[string]any{"name": "Mac", "kind": "mac", "host": "127.0.0.1", "port": 5999, "username": "me", "password": "s3cret"})
	_, r, raw := a.do(a.alice, "POST", fmt.Sprintf("/api/v1/machines/%d/connect", id), map[string]any{})
	tk, _ := r["ticket"].(string)
	if tk == "" {
		t.Fatalf("no ticket: %s", raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{"Authorization": {"Bearer " + a.bob}}
	// Bob cannot use Alice's ticket (and it is gone after one try)
	wsURL := "ws" + strings.TrimPrefix(a.ts.URL, "http") + "/api/v1/tunnel?width=800&height=600&ticket=" + tk
	if c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{"guacamole"}}); err == nil {
		_, msg, _ := c.Read(ctx)
		if !strings.Contains(string(msg), "error") {
			t.Fatalf("bob got in: %s", msg)
		}
		c.Close(websocket.StatusNormalClosure, "")
	}
	_, r, _ = a.do(a.alice, "POST", fmt.Sprintf("/api/v1/machines/%d/connect", id), map[string]any{})
	tk = r["ticket"].(string)
	wsURL = "ws" + strings.TrimPrefix(a.ts.URL, "http") + "/api/v1/tunnel?width=800&height=600&ticket=" + tk
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + a.alice}}, Subprotocols: []string{"guacamole"}})
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := c.Read(ctx)
	if err != nil || !strings.HasPrefix(string(first), "0.,") {
		t.Fatalf("first message = %q %v (want the tunnel id)", first, err)
	}
	_, next, _ := c.Read(ctx)
	if !strings.Contains(string(next), "4.sync") || !strings.HasSuffix(string(next), ";") {
		t.Fatalf("relay = %q", next)
	}
	if v := <-got; v != "select:vnc" {
		t.Errorf("select = %s", v)
	}
	if v := <-got; v != "connect:VERSION_1_5_0,127.0.0.1,5999,s3cret,me" {
		t.Errorf("connect = %s", v)
	}
	// a ping is answered by the tunnel, a key goes to guacd and comes back
	c.Write(ctx, websocket.MessageText, []byte(guac.Encode("", "ping", "123")))
	_, pong, _ := c.Read(ctx)
	if string(pong) != guac.Encode("", "ping", "123") {
		t.Errorf("pong = %q", pong)
	}
	c.Write(ctx, websocket.MessageText, []byte(guac.Encode("key", "65", "1")+guac.Encode("key", "65", "0")))
	_, echo, _ := c.Read(ctx)
	if !strings.Contains(string(echo), "3.key") {
		t.Errorf("echo = %q", echo)
	}
	if live := a.s.liveFor(1); len(live) != 1 {
		t.Errorf("live sessions = %v", live)
	}
	c.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rs, _, _ := a.st.ListSessions(context.Background(), 1, store.SessionFilter{})
		if len(rs) == 1 && rs[0].EndedAt != "" {
			if rs[0].EndReason != "closed" || rs[0].MachineName != "Mac" {
				t.Errorf("logged session = %+v", rs[0])
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the session was not logged as ended")
}
