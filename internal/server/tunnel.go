package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-remote/internal/guac"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

// ---- opening a session ----
//
// The browser first asks for a connect ticket (POST /machines/{id}/connect),
// giving the person's connect PIN when they set one. The ticket is good for
// one tunnel, for 60 seconds, for the same person. The tunnel itself
// (GET /tunnel?ticket=…, a WebSocket) still needs the normal sign-in; the
// machine's password is added on the server and never reaches the browser.

type ticket struct {
	uid       int64
	machineID int64
	expires   time.Time
	device    string
	ip        string
}

// liveSession is a tunnel that is open right now.
type liveSession struct {
	id        int64
	uid       int64
	machineID int64
	machine   string
	device    string
	started   time.Time
	cancel    context.CancelFunc
}

const ticketTTL = 60 * time.Second

// unlockKey is "this person on this device", so a PIN typed on the phone
// does not unlock the laptop.
func unlockKey(uid int64, r *http.Request) string {
	dev := r.UserAgent()
	if c, err := r.Cookie(sessionCookie); err == nil {
		dev = c.Value
	} else if h := r.Header.Get("Authorization"); h != "" {
		dev = h
	}
	sum := sha256.Sum256([]byte(dev))
	return fmt.Sprintf("%d:%s", uid, hex.EncodeToString(sum[:8]))
}

func (s *Server) forgetUnlocks(uid int64) {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	prefix := fmt.Sprintf("%d:", uid)
	for k := range s.unlocks {
		if strings.HasPrefix(k, prefix) {
			delete(s.unlocks, k)
		}
	}
}

func (s *Server) sweepTickets() {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	now := time.Now()
	for k, t := range s.tickets {
		if now.After(t.expires) {
			delete(s.tickets, k)
		}
	}
	for k, until := range s.unlocks {
		if now.After(until) {
			delete(s.unlocks, k)
		}
	}
}

// pinLocked reports whether too many wrong PINs came in lately (5 in 10 min).
func (s *Server) pinLocked(uid int64) bool {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	var keep []time.Time
	for _, t := range s.pinFails[uid] {
		if time.Since(t) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	s.pinFails[uid] = keep
	return len(keep) >= 5
}

func (s *Server) pinFailed(uid int64) {
	s.ticketMu.Lock()
	s.pinFails[uid] = append(s.pinFails[uid], time.Now())
	s.ticketMu.Unlock()
}

// deviceName is a short "iPhone · Safari" from a user agent.
func deviceName(ua string) string {
	l := strings.ToLower(ua)
	dev := "Computer"
	switch {
	case strings.Contains(l, "iphone"):
		dev = "iPhone"
	case strings.Contains(l, "ipad"):
		dev = "iPad"
	case strings.Contains(l, "android"):
		dev = "Android"
	case strings.Contains(l, "macintosh") || strings.Contains(l, "mac os"):
		dev = "Mac"
	case strings.Contains(l, "windows"):
		dev = "Windows"
	case strings.Contains(l, "linux"):
		dev = "Linux"
	}
	app := ""
	switch {
	case strings.Contains(l, "pocketai"):
		app = "Pocket AI"
	case strings.Contains(l, "edg/"):
		app = "Edge"
	case strings.Contains(l, "firefox") || strings.Contains(l, "fxios"):
		app = "Firefox"
	case strings.Contains(l, "chrome") || strings.Contains(l, "crios"):
		app = "Chrome"
	case strings.Contains(l, "safari"):
		app = "Safari"
	}
	if app == "" {
		return dev
	}
	return dev + " · " + app
}

func clientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if i := strings.LastIndexByte(ip, ':'); i > 0 && !strings.HasSuffix(ip, "]") {
		ip = ip[:i]
	}
	return strings.Trim(ip, "[]")
}

// handleConnect checks the PIN and hands out a one-time connect ticket.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMachine(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "machine")
		return
	}
	var in struct {
		PIN string `json:"pin"`
	}
	_ = decode(r, &in)
	st, _ := s.store.GetSettings(ctx, uid)
	key := unlockKey(uid, r)
	if st.PINHash != "" {
		s.ticketMu.Lock()
		until, unlocked := s.unlocks[key]
		s.ticketMu.Unlock()
		if !unlocked || time.Now().After(until) {
			if s.pinLocked(uid) {
				writeErr(w, http.StatusTooManyRequests, "too many wrong PINs; try again in ten minutes")
				return
			}
			if in.PIN == "" {
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "need_pin": true})
				return
			}
			if bcrypt.CompareHashAndPassword([]byte(st.PINHash), []byte(in.PIN)) != nil {
				s.pinFailed(uid)
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "need_pin": true, "error": "That PIN is not right."})
				return
			}
			if st.PINMinutes > 0 {
				s.ticketMu.Lock()
				s.unlocks[key] = time.Now().Add(time.Duration(st.PINMinutes) * time.Minute)
				s.ticketMu.Unlock()
			}
		}
	}
	if isDemoHost(m.Host) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "This is a demo machine; add one of your own computers to connect."})
		return
	}
	g := s.guacdStatus(ctx)
	if ok, _ := g["ok"].(bool); !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": g["detail"]})
		return
	}
	tok := store.RandomToken(24)
	s.ticketMu.Lock()
	s.tickets[tok] = ticket{uid: uid, machineID: m.ID, expires: time.Now().Add(ticketTTL), device: deviceName(r.UserAgent()), ip: clientIP(r)}
	s.ticketMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ticket": tok, "protocol": m.Protocol, "name": m.Name})
}

// takeTicket returns and removes a ticket that belongs to uid.
func (s *Server) takeTicket(tok string, uid int64) (ticket, bool) {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	t, ok := s.tickets[tok]
	if !ok {
		return ticket{}, false
	}
	delete(s.tickets, tok)
	if t.uid != uid || time.Now().After(t.expires) {
		return ticket{}, false
	}
	return t, true
}

// guacParams builds guacd's parameters for a machine.
func (s *Server) guacParams(m store.Machine) (map[string]string, error) {
	pass, err := s.seal.Open(m.Secret)
	if err != nil {
		return nil, err
	}
	key, err := s.seal.Open(m.PrivateKey)
	if err != nil {
		return nil, err
	}
	o := store.ParseOptions(m.Options)
	p := map[string]string{"hostname": m.Host, "port": strconv.Itoa(m.EffectivePort())}
	b := func(v bool) string {
		if v {
			return "true"
		}
		return ""
	}
	if o.ColorDepth > 0 {
		p["color-depth"] = strconv.Itoa(o.ColorDepth)
	}
	p["read-only"] = b(o.ReadOnly)
	switch m.Protocol {
	case "vnc":
		p["password"] = pass
		p["username"] = m.Username // macOS Screen Sharing signs in with the Mac account
		p["swap-red-blue"] = b(o.SwapRedBlue)
		if o.RemoteCursor {
			p["cursor"] = "remote"
		}
		p["clipboard-encoding"] = "UTF-8"
	case "rdp":
		p["username"] = m.Username
		p["password"] = pass
		p["domain"] = m.Domain
		p["security"] = firstNonEmpty(o.RDPSecurity, "any")
		p["ignore-cert"] = b(o.IgnoreCert)
		if o.Resize {
			p["resize-method"] = "display-update"
		}
		p["disable-audio"] = b(!o.Audio)
		p["enable-font-smoothing"] = "true"
		p["enable-wallpaper"] = ""
		p["enable-theming"] = "true"
		p["normalize-clipboard"] = "preserve"
	case "ssh":
		p["username"] = m.Username
		p["password"] = pass
		p["private-key"] = key
		p["font-size"] = strconv.Itoa(max(o.FontSize, 0))
		if o.FontSize == 0 {
			p["font-size"] = "12"
		}
		p["color-scheme"] = firstNonEmpty(o.ColorScheme, "gray-black")
		// the Nerd Font in deploy/guacd: eza icons, starship and powerline prompts draw
		// instead of empty boxes (a plain guacd falls back to its monospace font)
		p["font-name"] = firstNonEmpty(o.FontName, terminalFont)
		p["terminal-type"] = "xterm-256color"
		p["scrollback"] = "2000"
	}
	return p, nil
}

// terminalFont is the font terminal sessions use unless a machine chooses another.
const terminalFont = "JetBrainsMono NFM"

// guacError is an error instruction the browser client understands.
func guacError(msg string, code int) string {
	return guac.Encode("error", msg, strconv.Itoa(code))
}

// Guacamole status codes used here.
const (
	statusServerError      = 512
	statusUpstreamError    = 515
	statusUpstreamNotFound = 519
	statusClientForbidden  = 771
)

// handleTunnel is the WebSocket tunnel between the browser and guacd.
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"guacamole"}})
	if err != nil {
		return
	}
	ws.SetReadLimit(8 << 20)
	var wmu sync.Mutex
	send := func(c context.Context, text string) error {
		wmu.Lock()
		defer wmu.Unlock()
		wc, cancel := context.WithTimeout(c, 30*time.Second)
		defer cancel()
		return ws.Write(wc, websocket.MessageText, []byte(text))
	}
	refuse := func(msg string, code int) {
		_ = send(ctx, guacError(msg, code))
		ws.Close(websocket.StatusNormalClosure, "")
	}
	q := r.URL.Query()
	t, ok := s.takeTicket(q.Get("ticket"), uid)
	if !ok {
		refuse("This connection link has expired. Connect again.", statusClientForbidden)
		return
	}
	m, err := s.store.GetMachine(ctx, uid, t.machineID)
	if err != nil {
		refuse("That machine is gone.", statusClientForbidden)
		return
	}
	params, err := s.guacParams(m)
	if err != nil {
		refuse("The saved password cannot be read: "+err.Error(), statusServerError)
		return
	}
	atoi := func(k string, def, lo, hi int) int {
		n, err := strconv.Atoi(q.Get(k))
		if err != nil || n < lo || n > hi {
			return def
		}
		return n
	}
	cfg := guac.Config{
		Protocol: m.Protocol, Params: params,
		Width: atoi("width", 1280, 200, 8192), Height: atoi("height", 800, 200, 8192), DPI: atoi("dpi", 96, 48, 400),
		Timezone: firstNonEmpty(q.Get("timezone"), time.Now().Location().String()), ClientName: "Pocket Remote",
	}
	if m.Protocol == "rdp" && store.ParseOptions(m.Options).Audio {
		cfg.Audio = []string{"audio/L16", "audio/L8"}
	}
	dctx, dcancel := context.WithTimeout(ctx, 25*time.Second)
	gc, err := guac.Dial(dctx, s.cfg.GuacdAddr, cfg)
	dcancel()
	if err != nil {
		code := statusUpstreamError
		var ge *guac.Error
		if errors.As(err, &ge) && ge.Code > 0 {
			code = ge.Code
		}
		if strings.Contains(err.Error(), "not running") {
			code = statusUpstreamNotFound
		}
		s.log.Info("remote: connect failed", "machine", m.Name, "err", err)
		refuse(err.Error(), code)
		return
	}
	defer gc.Close()

	// log it
	sess := store.RemoteSession{MachineID: &m.ID, MachineName: m.Name, Protocol: m.Protocol, Device: t.device, IP: t.ip}
	if err := s.store.StartSession(ctx, uid, &sess); err != nil {
		refuse("could not log the session", statusServerError)
		return
	}
	_ = s.store.TouchMachine(ctx, uid, m.ID)
	s.sessions.Add(1)
	tctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	ls := &liveSession{id: sess.ID, uid: uid, machineID: m.ID, machine: m.Name, device: t.device, started: time.Now(), cancel: cancel}
	s.liveMu.Lock()
	s.live[sess.ID] = ls
	s.liveMu.Unlock()
	defer func() {
		s.liveMu.Lock()
		delete(s.live, sess.ID)
		s.liveMu.Unlock()
	}()
	s.publish(tctx, uid, "remote.session_started", m.Name, map[string]any{"machine_id": m.ID, "machine": m.Name, "protocol": m.Protocol, "device": t.device, "session_id": sess.ID})
	go s.notifySession(context.WithoutCancel(ctx), uid, m, t)

	if err := send(tctx, guac.Encode("", gc.ID)); err != nil {
		s.endSession(uid, sess, ls, "lost", 0, 0)
		return
	}

	var bytesIn, bytesOut atomic.Int64
	reason := make(chan string, 2)
	// guacd → browser: whole instructions, a few at a time
	go func() {
		var buf strings.Builder
		for {
			raw, _, err := gc.R.ReadRaw()
			if err != nil {
				if errors.Is(err, io.EOF) {
					reason <- "closed"
				} else if tctx.Err() != nil {
					reason <- "closed"
				} else {
					reason <- "lost"
				}
				return
			}
			buf.WriteString(raw)
			if gc.R.Buffered() > 0 && buf.Len() < 32<<10 {
				continue
			}
			bytesOut.Add(int64(buf.Len()))
			if err := send(tctx, buf.String()); err != nil {
				reason <- "lost"
				return
			}
			buf.Reset()
		}
	}()
	// browser → guacd; the tunnel's own pings are answered here
	go func() {
		for {
			_, data, err := ws.Read(tctx)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
					reason <- "closed"
				} else if tctx.Err() != nil {
					reason <- "ended from another device"
				} else {
					reason <- "lost"
				}
				return
			}
			bytesIn.Add(int64(len(data)))
			msg := string(data)
			parts, perr := guac.SplitAll(msg)
			if perr != nil {
				reason <- "error: the browser sent something that is not a remote desktop instruction"
				return
			}
			for _, p := range parts {
				if strings.HasPrefix(p, "0.,") || p == "0.;" {
					if in, err := guac.Parse(p); err == nil && len(in.Args) > 0 && in.Args[0] == "ping" {
						_ = send(tctx, p)
					}
					continue
				}
				if _, err := io.WriteString(gc, p); err != nil {
					reason <- "lost"
					return
				}
			}
		}
	}()
	why := ""
	select {
	case why = <-reason:
	case <-tctx.Done():
		why = "ended from another device"
	}
	cancel()
	gc.Close()
	ws.Close(websocket.StatusNormalClosure, "")
	s.endSession(uid, sess, ls, why, bytesIn.Load(), bytesOut.Load())
}

func (s *Server) endSession(uid int64, sess store.RemoteSession, ls *liveSession, reason string, in, out int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dur := int(time.Since(ls.started).Seconds())
	_ = s.store.EndSession(ctx, uid, sess.ID, store.Now(), dur, reason, in, out)
	s.publish(ctx, uid, "remote.session_ended", sess.MachineName, map[string]any{"machine": sess.MachineName, "session_id": sess.ID, "seconds": dur, "reason": reason})
}

// notifySession tells the person a session opened (so a session they did
// not open is noticed at once).
func (s *Server) notifySession(ctx context.Context, uid int64, m store.Machine, t ticket) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil || !st.NotifySessions || st.NotifyURL == "" {
		return
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	_ = n.Send(ctx, notify.Message{Title: "Remote session: " + m.Name, Body: fmt.Sprintf("A session to %s opened from %s (%s). Not you? End it in Pocket Remote → Sessions.", m.Name, t.device, t.ip), Priority: "info", Tags: []string{"desktop_computer"}, Link: s.cfg.BaseURL + "/sessions"})
}

// ---- live sessions ----

type liveView struct {
	ID        int64  `json:"id"`
	MachineID int64  `json:"machine_id"`
	Machine   string `json:"machine"`
	Device    string `json:"device"`
	StartedAt string `json:"started_at"`
}

func (s *Server) liveFor(uid int64) []liveView {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	out := []liveView{}
	for _, l := range s.live {
		if l.uid == uid {
			out = append(out, liveView{l.id, l.machineID, l.machine, l.device, l.started.UTC().Format(time.RFC3339)})
		}
	}
	return out
}

func (s *Server) liveCount(uid, machineID int64) int {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	n := 0
	for _, l := range s.live {
		if l.uid == uid && l.machineID == machineID {
			n++
		}
	}
	return n
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.liveFor(actorID(r.Context())))
}

// handleEndSession ends one of the caller's open sessions (from any device).
func (s *Server) handleEndSession(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	id := idParam(r, "id")
	s.liveMu.Lock()
	l, ok := s.live[id]
	s.liveMu.Unlock()
	if !ok || l.uid != uid {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	l.cancel()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := store.SessionFilter{MachineID: int64(queryInt(r, "machine")), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")}
	rs, total, err := s.store.ListSessions(ctx, actorID(ctx), f)
	if err != nil {
		s.fail(w, err, "sessions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rs, "total": total, "live": s.liveFor(actorID(ctx))})
}

func (s *Server) handleListTransfers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ts, err := s.store.ListTransfers(ctx, actorID(ctx), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "transfers")
		return
	}
	writeJSON(w, http.StatusOK, ts)
}
