package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/abjelosevic88/pocket-homelab-remote/internal/agentlink"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

// ---- Pocket Remote Agent ----
//
// A computer outside the tailnet runs Pocket Remote Agent, which connects out
// through Pocket Core's relay (an agent pairing: its key reaches only
// /api/v1/agent/…) and opens GET /api/v1/agent/link here. Everything for that
// computer then goes over the link: the terminal and files through the
// agent's own SSH server (signed in with a one-time password per stream), the
// desktop through a forwarded local port (Screen Sharing, 5900). guacd dials
// a one-shot port on 127.0.0.1 that is piped into the link.

// agentConn is one connected agent.
type agentConn struct {
	id      int64
	uid     int64
	machine int64
	hello   agentlink.Hello
	mux     *agentlink.Mux
	since   time.Time
	rttMS   atomic.Int64
}

func (s *Server) agentConnFor(id int64) *agentConn {
	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	return s.agents[id]
}

// agentOf is the connected agent of a machine, or why there is none.
func (s *Server) agentOf(m store.Machine) (*agentConn, error) {
	ac := s.agentConnFor(m.AgentID)
	if ac == nil || ac.uid != m.UserID {
		return nil, fmt.Errorf("Pocket Remote Agent on %s is not connected (is the computer on and awake?)", m.Name)
	}
	return ac, nil
}

// wsTransport is a WebSocket as an agentlink transport.
type wsTransport struct{ c *websocket.Conn }

func (t wsTransport) ReadMessage(ctx context.Context) ([]byte, error) {
	for {
		typ, b, err := t.c.Read(ctx)
		if err != nil {
			return nil, err
		}
		if typ == websocket.MessageBinary {
			return b, nil
		}
	}
}
func (t wsTransport) WriteMessage(ctx context.Context, b []byte) error {
	return t.c.Write(ctx, websocket.MessageBinary, b)
}
func (t wsTransport) Close() error { return t.c.CloseNow() }

func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// GET /api/v1/agent/link (WebSocket): an agent, through Core's relay.
func (s *Server) handleAgentLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tok := bearer(r)
	if tok == "" || !s.core.Configured() {
		writeErr(w, http.StatusUnauthorized, "agents come through Pocket Core")
		return
	}
	in, err := s.core.Introspect(ctx, tok)
	if err != nil || !in.Active || in.Scope != "agent" || in.Device == nil || in.User == nil || in.User.Email == "" {
		writeErr(w, http.StatusUnauthorized, "not an agent")
		return
	}
	u, err := s.coreIdentityUser(ctx, in)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unknown person")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{agentlink.Protocol}, InsecureSkipVerify: true})
	if err != nil {
		return
	}
	if ws.Subprotocol() != agentlink.Protocol {
		ws.Close(websocket.StatusPolicyViolation, "speak "+agentlink.Protocol)
		return
	}
	ws.SetReadLimit(1 << 20)
	mux := agentlink.New(wsTransport{ws})
	hellos := make(chan agentlink.Hello, 1)
	ac := &agentConn{uid: u.ID, mux: mux, since: time.Now()}
	mux.OnCtl = func(m map[string]any) {
		switch m["t"] {
		case "hello":
			var h agentlink.Hello
			b, _ := json.Marshal(m)
			if json.Unmarshal(b, &h) == nil {
				select {
				case hellos <- h:
				default:
				}
			}
		case "pong":
			if at, ok := m["at"].(float64); ok {
				ac.rttMS.Store(max(time.Now().UnixMilli()-int64(at), 1))
			}
		}
	}
	go mux.Run()
	defer mux.Close()
	var hello agentlink.Hello
	select {
	case hello = <-hellos:
	case <-mux.Done():
		return
	case <-time.After(20 * time.Second):
		return
	}
	ac.hello = hello
	name := strings.TrimSpace(hello.Name)
	if name == "" {
		name = strings.TrimSuffix(in.Device.Name, " (remote)")
	}
	if len(name) > 80 {
		name = name[:80]
	}
	ag, _, err := s.store.UpsertAgent(ctx, store.Agent{UserID: u.ID, CoreDeviceID: in.Device.ID, Name: name, Login: trimTo(hello.Login, 100),
		OS: trimTo(hello.OS, 20), Version: trimTo(hello.Version, 40), HostKey: trimTo(hello.HostKey, 2000)})
	if err != nil {
		s.log.Warn("agent: save", "err", err)
		return
	}
	m, err := s.store.MachineForAgent(ctx, u.ID, ag.ID)
	if errors.Is(err, store.ErrNotFound) {
		m = store.Machine{Name: name, Kind: agentKind(hello.OS), Host: "agent", Protocol: "vnc", Username: ag.Login,
			DesktopEnabled: false, TerminalEnabled: true, FilesEnabled: true, SSHPort: 22, HostKey: ag.HostKey, AgentID: ag.ID,
			Options: json.RawMessage(`{}`)}
		if err := s.store.CreateMachine(ctx, u.ID, &m); err != nil {
			s.log.Warn("agent: add machine", "err", err)
			return
		}
		s.log.Info("agent: new computer", "machine", m.Name, "user", u.Email)
	} else if err != nil {
		return
	} else if m.HostKey != ag.HostKey && ag.HostKey != "" {
		// The agent is known through Core's pairing; its SSH key is whatever it says.
		_ = s.store.SetHostKey(ctx, u.ID, m.ID, ag.HostKey)
		m.HostKey = ag.HostKey
	}
	ac.id, ac.machine = ag.ID, m.ID
	s.agentsMu.Lock()
	old := s.agents[ag.ID]
	s.agents[ag.ID] = ac
	s.agentsMu.Unlock()
	if old != nil {
		old.mux.Close()
	}
	s.log.Info("agent: connected", "machine", m.Name, "agent", ag.ID, "version", hello.Version)
	_ = mux.Ctl(map[string]any{"t": "welcome", "machine_id": m.ID, "name": m.Name})
	_ = mux.Ctl(map[string]any{"t": "ping", "at": time.Now().UnixMilli()})
	go s.checkOne(context.WithoutCancel(ctx), m)

	defer func() {
		s.agentsMu.Lock()
		if s.agents[ag.ID] == ac {
			delete(s.agents, ag.ID)
		}
		s.agentsMu.Unlock()
		if cur, err := s.store.GetMachine(context.Background(), u.ID, m.ID); err == nil {
			go s.checkOne(context.Background(), cur)
		}
		s.log.Info("agent: gone", "machine", m.Name, "why", mux.Err())
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	recheck := time.NewTicker(5 * time.Minute)
	defer recheck.Stop()
	for {
		select {
		case <-mux.Done():
			return
		case <-ping.C:
			if time.Since(mux.LastHeard()) > 75*time.Second {
				return
			}
			_ = mux.Ctl(map[string]any{"t": "ping", "at": time.Now().UnixMilli()})
		case <-recheck.C:
			// Removed in Core (device or app switch) = gone here too.
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			in, err := s.core.Introspect(c, tok)
			cancel()
			if err == nil && !in.Active {
				return
			}
		}
	}
}

func trimTo(v string, n int) string {
	v = strings.TrimSpace(v)
	if len(v) > n {
		return v[:n]
	}
	return v
}

func agentKind(goos string) string {
	switch goos {
	case "darwin":
		return "mac"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	}
	return "other"
}

// agentOpen opens a stream to the agent's computer.
func (s *Server) agentOpen(ctx context.Context, m store.Machine, req agentlink.OpenRequest) (net.Conn, error) {
	ac, err := s.agentOf(m)
	if err != nil {
		return nil, err
	}
	octx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st, err := ac.mux.Open(octx, req)
	if err != nil {
		var re *agentlink.RefusedError
		if errors.As(err, &re) && re.Reason == "refused" {
			return nil, fmt.Errorf("%s is switched off on %s", protocolName(m.Protocol), m.Name)
		}
		return nil, fmt.Errorf("Pocket Remote Agent on %s: %w", m.Name, err)
	}
	return st, nil
}

// agentSSH opens the agent's SSH server with a fresh one-time password.
func (s *Server) agentSSH(ctx context.Context, m store.Machine) (net.Conn, string, error) {
	pass := store.RandomToken(24)
	c, err := s.agentOpen(ctx, m, agentlink.OpenRequest{Kind: "ssh", Password: pass})
	return c, pass, err
}

// agentLogin is the account the agent's shells run as.
func (s *Server) agentLogin(m store.Machine) string {
	if ac := s.agentConnFor(m.AgentID); ac != nil && ac.hello.Login != "" {
		return ac.hello.Login
	}
	return m.Username
}

// agentBridge points guacd's parameters at a one-shot local port piped into
// the agent: the agent's SSH server for the terminal, its forwarded port for
// the desktop. m is the machine for the way in (ForWay).
func (s *Server) agentBridge(ctx context.Context, m store.Machine, params map[string]string) error {
	if _, err := s.agentOf(m); err != nil {
		return err
	}
	req := agentlink.OpenRequest{Kind: "tcp", Port: m.EffectivePort()}
	if m.Protocol == "ssh" {
		req = agentlink.OpenRequest{Kind: "ssh", Password: store.RandomToken(24)}
		params["username"] = s.agentLogin(m)
		params["password"] = req.Password
		params["private-key"] = ""
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.AgentBridgeListen, "0"))
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	params["hostname"] = s.cfg.AgentBridgeHost
	params["port"] = strconv.Itoa(port)
	go func() {
		defer ln.Close()
		if tl, ok := ln.(*net.TCPListener); ok {
			_ = tl.SetDeadline(time.Now().Add(40 * time.Second))
		}
		c, err := ln.Accept()
		ln.Close()
		if err != nil {
			return
		}
		st, err := s.agentOpen(context.WithoutCancel(ctx), m, req)
		if err != nil {
			s.log.Info("agent: bridge", "machine", m.Name, "err", err)
			c.Close()
			return
		}
		agentlink.Pipe(c, st)
	}()
	return nil
}

// agentProbe is a way in's status through the agent.
func (s *Server) agentProbe(ctx context.Context, m store.Machine) wayStatus {
	ac, err := s.agentOf(m)
	if err != nil {
		return wayStatus{State: "offline", Detail: "Pocket Remote Agent is not connected"}
	}
	rtt := int(max(ac.rttMS.Load(), 1))
	if m.Protocol == "ssh" {
		return wayStatus{State: "online", LatencyMS: rtt}
	}
	start := time.Now()
	c, err := s.agentOpen(ctx, m, agentlink.OpenRequest{Kind: "tcp", Port: m.EffectivePort()})
	if err != nil {
		ws := wayStatus{State: "offline", Detail: err.Error()}
		if strings.Contains(err.Error(), "switched off") {
			ws.Detail, ws.Refused = protocolName(m.Protocol)+" is switched off on it", true
		}
		return ws
	}
	c.Close()
	return wayStatus{State: "online", LatencyMS: max(int(time.Since(start).Milliseconds()), 1)}
}

// agentRemoved tells a connected agent its computer was deleted here, and
// forgets the agent.
func (s *Server) agentRemoved(ctx context.Context, m store.Machine) {
	if ac := s.agentConnFor(m.AgentID); ac != nil && ac.uid == m.UserID {
		_ = ac.mux.Ctl(map[string]any{"t": "removed"})
		time.AfterFunc(2*time.Second, func() { ac.mux.Close() })
	}
	_ = s.store.DeleteAgent(ctx, m.UserID, m.AgentID)
}

// agentView is what the browser sees of a machine's agent.
type agentView struct {
	Connected bool   `json:"connected"`
	Since     string `json:"since,omitempty"`
	Login     string `json:"login,omitempty"`
	OS        string `json:"os,omitempty"`
	Version   string `json:"version,omitempty"`
}

func (s *Server) agentViewOf(m store.Machine) *agentView {
	if m.AgentID == 0 {
		return nil
	}
	v := &agentView{}
	if ac := s.agentConnFor(m.AgentID); ac != nil && ac.uid == m.UserID {
		v.Connected, v.Since = true, ac.since.UTC().Format(time.RFC3339)
		v.Login, v.OS, v.Version = ac.hello.Login, ac.hello.OS, ac.hello.Version
	}
	return v
}

// POST /api/v1/agents/pair-link: a one-time link to paste into Pocket Remote
// Agent, made by Pocket Core for the signed-in person.
func (s *Server) handleAgentPairLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	if !s.core.Configured() || s.core.Token() == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "no_core", "error": "Pocket Remote Agent connects through Pocket Core's remote access; connect Pocket Remote to Pocket Core first."})
		return
	}
	body, _ := json.Marshal(map[string]string{"email": u.Email})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.core.BaseURL(), "/")+"/api/v1/remote/agent-pair", bytes.NewReader(body))
	if err != nil {
		s.fail(w, err, "pair link")
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.core.Token())
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "core", "error": "Pocket Core did not answer: " + err.Error()})
		return
	}
	defer res.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	_ = json.Unmarshal(b, &out)
	switch {
	case res.StatusCode == http.StatusOK:
		writeJSON(w, http.StatusOK, out)
	case res.StatusCode == http.StatusNotFound && out["error"] == nil:
		writeJSON(w, http.StatusConflict, map[string]string{"code": "old_core", "error": "This Pocket Core is too old for agents; update it to 0.44 or newer."})
	default:
		msg, _ := out["error"].(string)
		if msg == "" {
			msg = fmt.Sprintf("Pocket Core said HTTP %d", res.StatusCode)
		}
		code, _ := out["code"].(string)
		writeJSON(w, http.StatusConflict, map[string]string{"code": code, "error": msg})
	}
}
