package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

// moveApp starts Remote behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@ (both members to Core).
func moveApp(t *testing.T) *testApp {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "remote:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"remote","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	cfg := testConfig(t.TempDir())
	cfg.CoreURL = coreSrv.URL
	cfg.MaxBodyBytes = 1 << 20
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	a := &testApp{t: t, s: s, st: st}
	for i, who := range []string{"alice", "bob"} {
		u := &store.User{Email: who + "@example.com", Name: who, Role: map[bool]string{true: "admin", false: "member"}[i == 0], PasswordHash: "x"}
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

func (a *testApp) moveRaw(tok, method, path, ctype string, body []byte) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveApp(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/export.csv", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/machines", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "POST", "/api/v1/machines", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if code, body := a.moveRaw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := a.moveRaw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 2 || len(p.Imports) != 1 || p.Exports[0].Kind != core.KindPocket {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's computers to Bob without their passwords: the import
// adds once, and nothing of one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveApp(t)
	id := a.addMachine(a.alice, map[string]any{"name": "Mac", "kind": "mac", "host": "macbook", "password": "hunter2-secret"})
	_ = a.st.StartSession(context.Background(), 1, &store.RemoteSession{MachineID: &id, MachineName: "Mac", Protocol: "vnc", StartedAt: "2026-11-01T10:00:00Z", EndedAt: "2026-11-01T10:05:00Z", DurationS: 300})
	code, export := a.moveRaw("pcx_alice_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "macbook") || strings.Contains(string(export), "hunter2-secret") {
		t.Fatalf("alice's export: %d %s", code, export)
	}
	if _, bob := a.moveRaw("pcx_bob_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(bob), "macbook") {
		t.Fatalf("bob's export holds alice's computers: %s", bob)
	}
	imp := func() map[string]any {
		code, out := a.moveRaw("pcx_bob_import", "POST", "/api/v1/import", "application/json", export)
		if code != 200 {
			t.Fatalf("import: %d %s", code, out)
		}
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		return m
	}
	if first := imp(); first["machines"] != 1.0 || first["sessions"] != 1.0 {
		t.Fatalf("first import: %v", first)
	}
	if second := imp(); second["machines"] != 0.0 || second["sessions"] != 0.0 || second["transfers"] != 0.0 {
		t.Fatalf("second import added: %v", second)
	}
	bob, _ := a.st.ListMachines(context.Background(), 2)
	alice, _ := a.st.ListMachines(context.Background(), 1)
	if len(bob) != 1 || bob[0].Secret != "" || len(alice) != 1 {
		t.Fatalf("machines: bob %+v, alice %d", bob, len(alice))
	}
}
