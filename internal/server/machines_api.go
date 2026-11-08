package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-remote/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/tailnet"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/wol"
)

// ---- machines ----

var (
	kinds     = []string{"mac", "windows", "linux", "other"}
	protocols = []string{"vnc", "rdp", "ssh"}
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-_:%\[\]]{0,252})$`)
)

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// machineInput is what the add/edit form sends. Password and PrivateKey are
// plain text on the way in only; "" with the key absent keeps the saved one.
type machineInput struct {
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	Host          string          `json:"host"`
	Protocol      string          `json:"protocol"`
	Port          int             `json:"port"`
	Username      string          `json:"username"`
	Password      string          `json:"password"`
	PrivateKey    string          `json:"private_key"`
	ClearPassword bool            `json:"clear_password"`
	ClearKey      bool            `json:"clear_key"`
	Domain        string          `json:"domain"`
	MACAddress    string          `json:"mac_address"`
	WOLBroadcast  string          `json:"wol_broadcast"`
	FilesEnabled  bool            `json:"files_enabled"`
	SSHPort       int             `json:"ssh_port"`
	Options       json.RawMessage `json:"options"`
	Notes         string          `json:"notes"`
	Favorite      bool            `json:"favorite"`
	Sort          int             `json:"sort"`
	TailnetID     string          `json:"tailnet_id"`
}

// apply copies the sent fields onto m and checks them ("" = fine).
func (in machineInput) apply(m *store.Machine, keys map[string]bool) string {
	if keys["name"] {
		m.Name = strings.TrimSpace(in.Name)
	}
	if keys["kind"] {
		m.Kind = in.Kind
	}
	if keys["host"] {
		h := strings.TrimSpace(in.Host)
		for _, p := range []string{"vnc://", "rdp://", "ssh://", "http://", "https://"} {
			h = strings.TrimPrefix(h, p)
		}
		m.Host = strings.TrimSuffix(strings.TrimSuffix(h, "/"), ".")
	}
	if keys["protocol"] {
		m.Protocol = in.Protocol
	}
	if keys["port"] {
		m.Port = in.Port
	}
	if keys["username"] {
		m.Username = strings.TrimSpace(in.Username)
	}
	if keys["domain"] {
		m.Domain = strings.TrimSpace(in.Domain)
	}
	if keys["mac_address"] {
		m.MACAddress = strings.TrimSpace(in.MACAddress)
	}
	if keys["wol_broadcast"] {
		m.WOLBroadcast = strings.TrimSpace(in.WOLBroadcast)
	}
	if keys["files_enabled"] {
		m.FilesEnabled = in.FilesEnabled
	}
	if keys["ssh_port"] {
		m.SSHPort = in.SSHPort
	}
	if keys["options"] {
		o := store.ParseOptions(in.Options)
		b, _ := json.Marshal(o)
		m.Options = b
	}
	if keys["notes"] {
		m.Notes = strings.TrimSpace(in.Notes)
	}
	if keys["favorite"] {
		m.Favorite = in.Favorite
	}
	if keys["sort"] {
		m.Sort = in.Sort
	}
	if keys["tailnet_id"] {
		m.TailnetID = strings.TrimSpace(in.TailnetID)
	}
	if m.Kind == "" {
		m.Kind = "other"
	}
	if m.Protocol == "" {
		m.Protocol = defaultProtocol(m.Kind)
	}
	if m.SSHPort == 0 {
		m.SSHPort = 22
	}
	switch {
	case m.Name == "" || len(m.Name) > 80:
		return "give the machine a name (up to 80 characters)"
	case !oneOf(m.Kind, kinds):
		return "the kind is mac, windows, linux or other"
	case !oneOf(m.Protocol, protocols):
		return "connect with Screen Sharing (vnc), Remote Desktop (rdp) or a terminal (ssh)"
	case m.Host == "" || !hostRe.MatchString(m.Host):
		return "the address is a tailnet name (macbook) or an IP address (100.64.0.12), without http://"
	case m.Port < 0 || m.Port > 65535 || m.SSHPort < 1 || m.SSHPort > 65535:
		return "a port is 1 to 65535"
	case len(m.Username) > 200 || len(m.Domain) > 200 || len(m.Notes) > 4000:
		return "a field is too long"
	}
	if m.MACAddress != "" {
		hw, err := wol.ParseMAC(m.MACAddress)
		if err != nil {
			return err.Error()
		}
		m.MACAddress = hw.String()
	}
	if m.WOLBroadcast != "" && !hostRe.MatchString(m.WOLBroadcast) {
		return "the wake address is an IP address like 192.168.1.255"
	}
	return ""
}

func defaultProtocol(kind string) string {
	switch kind {
	case "windows":
		return "rdp"
	case "linux":
		return "ssh"
	}
	return "vnc"
}

// machineView is a machine as the browser sees it: no secrets, with status.
type machineView struct {
	store.Machine
	Port        int             `json:"effective_port"`
	HasPassword bool            `json:"has_password"`
	HasKey      bool            `json:"has_key"`
	Status      machineStatus   `json:"status"`
	Tailnet     *tailnet.Device `json:"tailnet,omitempty"`
	Live        int             `json:"live"` // sessions open right now
}

func (s *Server) view(m store.Machine, devs []tailnet.Device) machineView {
	v := machineView{Machine: m, Port: m.EffectivePort(), HasPassword: m.Secret != "", HasKey: m.PrivateKey != "", Status: s.statusOf(m.ID)}
	if d, ok := tailnet.Match(devs, m.Host); ok {
		v.Tailnet = &d
	}
	v.Live = s.liveCount(m.UserID, m.ID)
	return v
}

// devices is the tailnet, or nothing when it is not here (cached 15 s).
func (s *Server) devices(ctx context.Context) []tailnet.Device {
	if s.tailnet == nil {
		return nil
	}
	if v, at, ok := s.store.CacheGet(ctx, "tailnet:devices"); ok {
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < 15*time.Second {
			var devs []tailnet.Device
			if json.Unmarshal([]byte(v), &devs) == nil {
				return devs
			}
		}
	}
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	devs, err := s.tailnet.Devices(c)
	if err != nil {
		s.log.Debug("tailnet", "err", err)
		return nil
	}
	b, _ := json.Marshal(devs)
	_ = s.store.CachePut(ctx, "tailnet:devices", string(b))
	return devs
}

func (s *Server) handleListMachines(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ms, err := s.store.ListMachines(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "machines")
		return
	}
	devs := s.devices(ctx)
	out := make([]machineView, 0, len(ms))
	for _, m := range ms {
		v := s.view(m, devs)
		if v.Status.State == "unknown" {
			s.checkSoon(m)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetMachine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, err := s.store.GetMachine(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "machine")
		return
	}
	writeJSON(w, http.StatusOK, s.view(m, s.devices(ctx)))
}

// sealSecrets seals a typed password and key onto m (or clears them).
func (s *Server) sealSecrets(m *store.Machine, in machineInput, keys map[string]bool) error {
	if in.ClearPassword {
		m.Secret = ""
	} else if keys["password"] && in.Password != "" && !strings.Contains(in.Password, "••") {
		v, err := s.seal.Seal(in.Password)
		if err != nil {
			return err
		}
		m.Secret = v
	}
	if in.ClearKey {
		m.PrivateKey = ""
	} else if keys["private_key"] && strings.TrimSpace(in.PrivateKey) != "" && !strings.Contains(in.PrivateKey, "••") {
		v, err := s.seal.Seal(strings.TrimSpace(in.PrivateKey) + "\n")
		if err != nil {
			return err
		}
		m.PrivateKey = v
	}
	return nil
}

func (s *Server) handleCreateMachine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in machineInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	m := store.Machine{}
	for _, k := range []string{"name", "kind", "host", "protocol", "port", "username", "domain", "mac_address", "wol_broadcast", "files_enabled", "ssh_port", "options", "notes", "favorite", "sort", "tailnet_id"} {
		keys[k] = true
	}
	if msg := in.apply(&m, keys); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.sealSecrets(&m, in, keys); err != nil {
		s.fail(w, err, "seal")
		return
	}
	uid := actorID(ctx)
	if err := s.store.CreateMachine(ctx, uid, &m); err != nil {
		s.fail(w, err, "add machine")
		return
	}
	go s.checkOne(context.WithoutCancel(ctx), m)
	writeJSON(w, http.StatusCreated, s.view(m, s.devices(ctx)))
}

func (s *Server) handleUpdateMachine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMachine(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "machine")
		return
	}
	var in machineInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	host, sshPort := m.Host, m.SSHPort
	if msg := in.apply(&m, keys); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.sealSecrets(&m, in, keys); err != nil {
		s.fail(w, err, "seal")
		return
	}
	if !strings.EqualFold(host, m.Host) || sshPort != m.SSHPort {
		m.HostKey = "" // another machine: learn its identity again
	}
	if err := s.store.UpdateMachine(ctx, uid, &m); err != nil {
		s.fail(w, err, "save machine")
		return
	}
	go s.checkOne(context.WithoutCancel(ctx), m)
	writeJSON(w, http.StatusOK, s.view(m, s.devices(ctx)))
}

func (s *Server) handleDeleteMachine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if err := s.store.DeleteMachine(ctx, actorID(ctx), id); err != nil {
		s.fail(w, err, "delete machine")
		return
	}
	s.statusMu.Lock()
	delete(s.status, id)
	s.statusMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// handleForgetHostKey trusts a machine's new SSH identity (after a reinstall).
func (s *Server) handleForgetHostKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMachine(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "machine")
		return
	}
	if err := s.store.SetHostKey(ctx, uid, m.ID, ""); err != nil {
		s.fail(w, err, "forget key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- online status ----

// machineStatus is the last reachability check of a machine.
type machineStatus struct {
	State     string `json:"state"` // online | offline | unknown
	CheckedAt string `json:"checked_at,omitempty"`
	LatencyMS int    `json:"latency_ms,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Waking    string `json:"waking_since,omitempty"`
}

func (s *Server) statusOf(id int64) machineStatus {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if st, ok := s.status[id]; ok {
		return st
	}
	return machineStatus{State: "unknown"}
}

// isDemoHost marks the demo machines: their status is made up, never dialled.
func isDemoHost(h string) bool { return strings.HasSuffix(strings.ToLower(h), ".demo") }

// probe dials the machine's remote desktop port.
func probe(ctx context.Context, m store.Machine) machineStatus {
	now := store.Now()
	if isDemoHost(m.Host) {
		if m.Favorite || m.Kind == "linux" {
			return machineStatus{State: "online", CheckedAt: now, LatencyMS: 8 + int(m.ID*7%30)}
		}
		return machineStatus{State: "offline", CheckedAt: now, Detail: "not answering"}
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	start := time.Now()
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(m.Host, strconv.Itoa(m.EffectivePort())))
	if err != nil {
		detail := "not answering"
		var de *net.DNSError
		switch {
		case errors.As(err, &de):
			detail = "no machine by that name"
		case strings.Contains(err.Error(), "refused"):
			detail = protocolName(m.Protocol) + " is switched off on it"
		}
		return machineStatus{State: "offline", CheckedAt: now, Detail: detail}
	}
	c.Close()
	ms := int(time.Since(start).Milliseconds())
	if ms < 1 {
		ms = 1
	}
	return machineStatus{State: "online", CheckedAt: now, LatencyMS: ms}
}

func protocolName(p string) string {
	switch p {
	case "rdp":
		return "Remote Desktop"
	case "ssh":
		return "Remote Login (SSH)"
	}
	return "Screen Sharing"
}

// checkOne checks one machine and remembers the result.
func (s *Server) checkOne(ctx context.Context, m store.Machine) machineStatus {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st := probe(c, m)
	s.statusMu.Lock()
	prev, had := s.status[m.ID]
	if st.State == "offline" && prev.Waking != "" {
		if t, err := time.Parse(time.RFC3339Nano, prev.Waking); err == nil && time.Since(t) < 3*time.Minute {
			st.Waking = prev.Waking
		}
	}
	s.status[m.ID] = st
	s.statusMu.Unlock()
	if st.State == "online" {
		_ = s.store.SeenOnline(context.WithoutCancel(ctx), m.ID, st.CheckedAt)
	}
	if had && prev.State == "online" && st.State == "offline" && m.Favorite {
		s.notifyOffline(context.WithoutCancel(ctx), m, st)
	}
	return st
}

// checkSoon checks a machine nobody has looked at yet, once at a time.
func (s *Server) checkSoon(m store.Machine) {
	s.statusMu.Lock()
	if _, busy := s.status[m.ID]; busy {
		s.statusMu.Unlock()
		return
	}
	s.status[m.ID] = machineStatus{State: "unknown"}
	s.statusMu.Unlock()
	go s.checkOne(context.Background(), m)
}

// checkAll checks every machine, a few at a time.
func (s *Server) checkAll(ctx context.Context) {
	ms, err := s.store.AllMachines(ctx)
	if err != nil {
		return
	}
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for _, m := range ms {
		wg.Add(1)
		sem <- struct{}{}
		go func(m store.Machine) {
			defer wg.Done()
			defer func() { <-sem }()
			s.checkOne(ctx, m)
		}(m)
	}
	wg.Wait()
}

func (s *Server) handleCheckMachine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, err := s.store.GetMachine(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "machine")
		return
	}
	st := s.checkOne(ctx, m)
	writeJSON(w, http.StatusOK, st)
}

// handleWake sends the Wake-on-LAN signal and watches for the machine.
func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMachine(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "machine")
		return
	}
	if m.MACAddress == "" {
		writeErr(w, http.StatusBadRequest, "add the machine's network card address (Edit → Wake) to wake it from here")
		return
	}
	if !isDemoHost(m.Host) {
		if err := wol.Send(m.MACAddress, m.WOLBroadcast); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "the wake signal could not be sent: " + err.Error()})
			return
		}
	}
	now := store.Now()
	s.statusMu.Lock()
	st := s.status[m.ID]
	if st.State == "" {
		st.State = "unknown"
	}
	st.Waking = now
	s.status[m.ID] = st
	s.statusMu.Unlock()
	s.publish(ctx, uid, "remote.machine_woken", m.Name, map[string]any{"machine_id": m.ID, "machine": m.Name, "via": firstNonEmpty(linkApp(ctx), "remote")})
	// look for it every 5 s for two minutes
	go func(m store.Machine) {
		bg := context.WithoutCancel(ctx)
		for i := 0; i < 24; i++ {
			time.Sleep(5 * time.Second)
			if s.checkOne(bg, m).State == "online" {
				s.statusMu.Lock()
				st := s.status[m.ID]
				st.Waking = ""
				s.status[m.ID] = st
				s.statusMu.Unlock()
				return
			}
		}
	}(m)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Wake signal sent to " + m.Name + ". It usually takes 10 to 60 seconds."})
}

// ---- the tailnet ----

// handleTailnet lists the devices on the box's tailnet, marking the ones the
// caller already added. The tailnet is the box's, so only its admin sees it.
func (s *Server) handleTailnet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	if s.tailnet == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "detail": "Tailscale is not reachable from Pocket Remote (TAILSCALE_SOCKET).", "devices": []any{}})
		return
	}
	if u.Role != "admin" {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "detail": "The tailnet belongs to the box; its admin sees the list. Type your computer's name instead.", "devices": []any{}})
		return
	}
	devs, err := s.tailnet.Devices(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "detail": err.Error(), "devices": []any{}})
		return
	}
	ms, _ := s.store.ListMachines(ctx, u.ID)
	type dev struct {
		tailnet.Device
		Added    int64  `json:"added_machine_id,omitempty"`
		Kind     string `json:"kind"`
		Protocol string `json:"protocol"`
		Fit      bool   `json:"fit"` // a computer, not a phone or TV
	}
	out := []dev{}
	for _, d := range devs {
		if d.Self {
			continue
		}
		kind := kindForOS(d.OS)
		x := dev{Device: d, Kind: kind, Protocol: defaultProtocol(kind), Fit: kind != "other"}
		for _, m := range ms {
			if m.TailnetID == d.ID {
				x.Added = m.ID
				break
			}
			if _, ok := tailnet.Match([]tailnet.Device{d}, m.Host); ok {
				x.Added = m.ID
				break
			}
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "devices": out})
}

func kindForOS(os string) string {
	switch strings.ToLower(os) {
	case "macos", "darwin":
		return "mac"
	case "windows":
		return "windows"
	case "linux", "freebsd", "openbsd":
		return "linux"
	}
	return "other"
}

// ---- overview ----

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	ms, err := s.store.ListMachines(ctx, uid)
	if err != nil {
		s.fail(w, err, "overview")
		return
	}
	online := 0
	for _, m := range ms {
		if s.statusOf(m.ID).State == "online" {
			online++
		}
	}
	recent, _, _ := s.store.ListSessions(ctx, uid, store.SessionFilter{Limit: 5})
	week, _ := s.store.StatsSince(ctx, uid, time.Now().AddDate(0, 0, -7).UTC().Format(time.RFC3339Nano))
	month, _ := s.store.StatsSince(ctx, uid, time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339Nano))
	writeJSON(w, http.StatusOK, map[string]any{
		"machines": len(ms), "online": online, "recent": recent, "week": week, "month": month,
		"live": s.liveFor(uid), "guacd": s.guacdStatus(ctx),
	})
}

// guacdStatus says whether the remote desktop engine answers (cached 30 s).
func (s *Server) guacdStatus(ctx context.Context) map[string]any {
	if v, at, ok := s.store.CacheGet(ctx, "guacd:status"); ok {
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < 30*time.Second {
			return map[string]any{"ok": v == "ok", "detail": strings.TrimPrefix(v, "err:"), "addr": s.cfg.GuacdAddr}
		}
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.DialContext(ctx, "tcp", s.cfg.GuacdAddr)
	v := "ok"
	if err != nil {
		v = "err:the remote desktop engine (guacd) is not running at " + s.cfg.GuacdAddr
	} else {
		c.Close()
	}
	_ = s.store.CachePut(ctx, "guacd:status", v)
	return map[string]any{"ok": v == "ok", "detail": strings.TrimPrefix(v, "err:"), "addr": s.cfg.GuacdAddr}
}

// ---- notifications ----

func (s *Server) notifyOffline(ctx context.Context, m store.Machine, st machineStatus) {
	set, err := s.store.GetSettings(ctx, m.UserID)
	if err != nil || !set.NotifyOffline || set.NotifyURL == "" {
		return
	}
	n := notify.New(set.NotifyURL, set.NotifyFormat)
	_ = n.Send(ctx, notify.Message{Title: m.Name + " went offline", Body: fmt.Sprintf("Pocket Remote cannot reach %s any more (%s).", m.Name, st.Detail), Priority: "info", Tags: []string{"desktop_computer"}, Link: s.cfg.BaseURL + "/"})
}
