package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/abjelosevic88/pocket-homelab-remote/internal/agentlink"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

// ---- computers reached through another one ----
//
// Some computers only answer from another computer: a VM on a work tailnet
// one of your boxes is in, a machine behind a bastion. A machine with via_id
// is reached through that computer's SSH, with its saved sign-in: Pocket
// Remote opens the target's port from there, as `ssh -J` does, or, when that
// computer cannot route there itself (Tailscale in userspace, a firewall),
// runs `tailscale nc` on it, as a ProxyCommand would. The way that worked is
// remembered per machine. One SSH connection per in-between computer is
// shared by the status checks, files and sessions, and closed after a few
// idle minutes.

// viaIdle is how long an unused connection to an in-between computer stays.
const viaIdle = 3 * time.Minute

type viaClient struct {
	c     *ssh.Client
	key   string // what it was opened with: a changed sign-in opens a new one
	refs  int
	idle  time.Time
	stale bool // replaced or gone: closes when the last user lets go
}

// viaOf loads the computer m is reached through.
func (s *Server) viaOf(ctx context.Context, m store.Machine) (store.Machine, error) {
	via, err := s.store.GetMachine(ctx, m.UserID, m.ViaID)
	if err != nil {
		return via, fmt.Errorf("the computer %s is reached through is gone (Edit → Reach it through)", m.Name)
	}
	if via.ViaID > 0 || via.AgentID > 0 {
		return via, fmt.Errorf("%s cannot pass connections on: it is reached through something else itself", via.Name)
	}
	return via, nil
}

// acquireVia is a signed-in SSH connection to the computer m is reached
// through; release lets go of it.
func (s *Server) acquireVia(ctx context.Context, m store.Machine) (*ssh.Client, store.Machine, func(), error) {
	via, err := s.viaOf(ctx, m)
	if err != nil {
		return nil, via, nil, err
	}
	key := strings.Join([]string{via.UpdatedAt, via.Host, strconv.Itoa(via.SSHPort), via.Username}, "|")

	// one sign-in at a time per in-between computer
	s.viaMu.Lock()
	lk := s.viaDial[via.ID]
	if lk == nil {
		lk = &sync.Mutex{}
		s.viaDial[via.ID] = lk
	}
	s.viaMu.Unlock()
	lk.Lock()
	defer lk.Unlock()

	s.viaMu.Lock()
	if vc := s.via[via.ID]; vc != nil && !vc.stale && vc.key == key {
		vc.refs++
		s.viaMu.Unlock()
		return vc.c, via, s.releaseVia(vc), nil
	}
	s.viaMu.Unlock()

	c, done, err := s.sshClient(ctx, m.UserID, via, "to pass connections on")
	if err != nil {
		return nil, via, nil, fmt.Errorf("cannot sign in to %s, which %s is reached through: %w", via.Name, m.Name, err)
	}
	vc := &viaClient{c: c, key: key, refs: 1}
	s.viaMu.Lock()
	if old := s.via[via.ID]; old != nil {
		old.stale = true
		if old.refs == 0 {
			old.c.Close()
		}
	}
	s.via[via.ID] = vc
	s.viaMu.Unlock()
	go func() {
		_ = c.Wait()
		done()
		s.viaMu.Lock()
		vc.stale = true
		if s.via[via.ID] == vc {
			delete(s.via, via.ID)
		}
		s.viaMu.Unlock()
	}()
	go func() { // keep it alive through NAT and firewalls
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				c.Close()
				return
			}
		}
	}()
	return c, via, s.releaseVia(vc), nil
}

func (s *Server) releaseVia(vc *viaClient) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.viaMu.Lock()
			defer s.viaMu.Unlock()
			vc.refs--
			vc.idle = time.Now()
			if vc.refs <= 0 && vc.stale {
				vc.c.Close()
			}
		})
	}
}

// sweepVia closes connections to in-between computers nobody used lately.
func (s *Server) sweepVia() {
	s.viaMu.Lock()
	defer s.viaMu.Unlock()
	for id, vc := range s.via {
		if vc.refs <= 0 && time.Since(vc.idle) > viaIdle {
			vc.stale = true
			vc.c.Close()
			delete(s.via, id)
		}
	}
}

// dialThrough opens port on m from the computer it is reached through. done
// lets go of that computer once the connection is closed (closing it also
// calls done).
func (s *Server) dialThrough(ctx context.Context, m store.Machine, port int) (net.Conn, func(), error) {
	c, via, release, err := s.acquireVia(ctx, m)
	if err != nil {
		return nil, nil, err
	}
	addr := net.JoinHostPort(m.Host, strconv.Itoa(port))
	modes := []string{"tcp", "nc"}
	s.viaMu.Lock()
	if s.jumpMode[m.ID] == "nc" {
		modes = []string{"nc", "tcp"}
	}
	s.viaMu.Unlock()
	var why []string
	for _, mode := range modes {
		var conn net.Conn
		var err error
		if mode == "tcp" {
			dc, cancel := context.WithTimeout(ctx, 4*time.Second)
			conn, err = c.DialContext(dc, "tcp", addr)
			cancel()
		} else {
			conn, err = tailscaleNC(ctx, c, m.Host, port)
		}
		if err == nil {
			s.viaMu.Lock()
			s.jumpMode[m.ID] = mode
			s.viaMu.Unlock()
			rc := &releaseConn{Conn: conn, release: release}
			return rc, rc.done, nil
		}
		if mode == "tcp" && errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("no answer")
		}
		why = append(why, err.Error())
	}
	release()
	return nil, nil, fmt.Errorf("%s cannot reach %s: %s", via.Name, addr, strings.Join(why, "; "))
}

// releaseConn lets go of the in-between computer when it closes.
type releaseConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (c *releaseConn) Close() error {
	err := c.Conn.Close()
	c.done()
	return err
}

func (c *releaseConn) done() { c.once.Do(c.release) }

// tailscaleNC runs `tailscale nc host port` on the in-between computer and
// treats its input and output as the connection. It counts as open once the
// far side says something (SSH and VNC speak first) or nc is still running
// after a moment (Remote Desktop waits for the client); nc ending at once
// means it could not connect.
func tailscaleNC(ctx context.Context, c *ssh.Client, host string, port int) (net.Conn, error) {
	if !hostRe.MatchString(host) {
		return nil, errors.New("not an address")
	}
	sess, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	var stderr strings.Builder
	sess.Stderr = &limitedWriter{w: &stderr, n: 400}
	if err := sess.Start("tailscale nc '" + host + "' " + strconv.Itoa(port)); err != nil {
		sess.Close()
		return nil, fmt.Errorf("tailscale nc: %w", err)
	}
	pr, pw := io.Pipe()
	first := make(chan error, 1)
	go func() {
		buf := make([]byte, 32*1024)
		said := false
		for {
			n, err := stdout.Read(buf)
			if !said && (n > 0 || err != nil) {
				said = true
				if n > 0 {
					first <- nil
				} else {
					first <- err
				}
			}
			if n > 0 {
				if _, werr := pw.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()
	nc := &ncConn{sess: sess, stdin: stdin, r: pr}
	t := time.NewTimer(2500 * time.Millisecond)
	defer t.Stop()
	select {
	case err := <-first:
		if err == nil {
			return nc, nil
		}
	case <-t.C:
		return nc, nil
	case <-ctx.Done():
	}
	nc.Close()
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = "it ended without connecting"
	}
	return nil, fmt.Errorf("tailscale nc: %s", msg)
}

// ncConn is a command's input and output as a net.Conn.
type ncConn struct {
	sess  *ssh.Session
	stdin io.WriteCloser
	r     *io.PipeReader
	once  sync.Once
}

func (c *ncConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *ncConn) Write(b []byte) (int, error) { return c.stdin.Write(b) }
func (c *ncConn) Close() error {
	c.once.Do(func() {
		c.stdin.Close()
		c.sess.Close()
		c.r.Close()
	})
	return nil
}
func (c *ncConn) LocalAddr() net.Addr              { return viaAddr{} }
func (c *ncConn) RemoteAddr() net.Addr             { return viaAddr{} }
func (c *ncConn) SetDeadline(time.Time) error      { return nil }
func (c *ncConn) SetReadDeadline(time.Time) error  { return nil }
func (c *ncConn) SetWriteDeadline(time.Time) error { return nil }

type viaAddr struct{}

func (viaAddr) Network() string { return "tcp" }
func (viaAddr) String() string  { return "via" }

type limitedWriter struct {
	mu sync.Mutex
	w  *strings.Builder
	n  int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.n - l.w.Len(); room > 0 {
		if len(p) > room {
			l.w.Write(p[:room])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}

// viaBridge points guacd's parameters at a one-shot local port piped to the
// way in's port through the in-between computer. m is the machine for the way
// in (ForWay). The in-between computer is signed in to now, so a wrong
// sign-in is said at once; the target's port is opened when guacd dials.
func (s *Server) viaBridge(ctx context.Context, m store.Machine, params map[string]string) error {
	_, _, release, err := s.acquireVia(ctx, m)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.AgentBridgeListen, "0"))
	if err != nil {
		release()
		return err
	}
	params["hostname"] = s.cfg.AgentBridgeHost
	params["port"] = strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	port := m.EffectivePort()
	go func() {
		defer release() // the pooled connection stays while the piped one holds its own
		defer ln.Close()
		if tl, ok := ln.(*net.TCPListener); ok {
			_ = tl.SetDeadline(time.Now().Add(40 * time.Second))
		}
		c, err := ln.Accept()
		ln.Close()
		if err != nil {
			return
		}
		dc, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		up, _, err := s.dialThrough(dc, m, port)
		cancel()
		if err != nil {
			s.log.Info("via: bridge", "machine", m.Name, "err", err)
			c.Close()
			return
		}
		agentlink.Pipe(c, up)
	}()
	return nil
}

// viaProbe is a way in's status through the in-between computer.
func (s *Server) viaProbe(ctx context.Context, m store.Machine) wayStatus {
	start := time.Now()
	c, _, err := s.dialThrough(ctx, m, m.EffectivePort())
	if err != nil {
		ws := wayStatus{State: "offline", Detail: err.Error()}
		if strings.Contains(err.Error(), "refused") {
			ws.Detail, ws.Refused = protocolName(m.Protocol)+" is switched off on it", true
		}
		return ws
	}
	c.Close()
	return wayStatus{State: "online", LatencyMS: max(int(time.Since(start).Milliseconds()), 1)}
}
