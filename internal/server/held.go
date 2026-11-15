package server

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/abjelosevic88/pocket-homelab-remote/internal/guac"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

// ---- terminals that outlive the tunnel ----
//
// An iPhone app in the background loses its socket within seconds, and guacd
// ends a connection when its last user leaves: the shell, its screen and the
// half-typed line were gone. So a terminal's first guacd user (the anchor)
// stays here, answering guacd's syncs and keep-alives, and every tunnel joins
// it as another user; guacd sends a joiner the whole screen. A tunnel that
// drops leaves the terminal held for termHold, and the same person connecting
// to the same computer takes it back. Ending the session (Guacamole's
// "disconnect"), ending it from another device, the shell exiting or the time
// running out closes it.

// termHold is how long a terminal waits with no one looking.
const termHold = 15 * time.Minute

// termIdle drops a tunnel that has sent nothing (not even its pings) this long.
const termIdle = 90 * time.Second

type heldTerm struct {
	s         *Server
	uid       int64
	machineID int64
	anchor    *guac.Conn
	wmu       sync.Mutex // writes to the anchor
	sess      store.RemoteSession
	ls        *liveSession
	ctx       context.Context // ends with the terminal
	bytesIn   atomic.Int64
	bytesOut  atomic.Int64

	// guarded by s.heldMu
	att      *termAttach // the tunnel looking at it; nil while held
	dev      string      // the device that looked last
	detached time.Time
	timer    *time.Timer
	ended    bool
}

// termAttach is one tunnel joined to a held terminal.
type termAttach struct {
	dev    string
	ctx    context.Context
	cancel context.CancelFunc
}

// serveTerminal opens a terminal, or takes back one held for this person, and
// relays it until the tunnel goes.
func (s *Server) serveTerminal(ctx context.Context, ws *websocket.Conn, send func(context.Context, string) error, refuse func(string, int), t ticket, m store.Machine, cfg guac.Config) {
	ht, a := s.takeHeld(t)
	if ht != nil {
		s.log.Info("remote: terminal taken back", "machine", m.Name, "session", ht.sess.ID)
	} else {
		anchor, err := s.dialGuacd(ctx, cfg)
		if err != nil {
			refuse(s.dialFailure(ctx, m, err))
			return
		}
		sess, ls, tctx, err := s.openSession(ctx, t, m)
		if err != nil {
			anchor.Close()
			refuse("could not log the session", statusServerError)
			return
		}
		ht = &heldTerm{s: s, uid: t.uid, machineID: m.ID, anchor: anchor, sess: sess, ls: ls, ctx: tctx, dev: t.devKey}
		a = ht.newAttach(t.devKey)
		s.heldMu.Lock()
		ht.att = a
		s.held[sess.ID] = ht
		s.heldMu.Unlock()
		go ht.keep()
	}

	jcfg := cfg
	jcfg.Join = ht.anchor.ID
	gc, err := s.dialGuacd(a.ctx, jcfg)
	if err != nil {
		// the terminal went (its shell ended, guacd restarted): say why, close it
		ht.end("lost")
		refuse(s.dialFailure(ctx, m, err))
		return
	}
	defer gc.Close()
	if err := send(a.ctx, guac.Encode("", gc.ID)); err != nil {
		ht.detach(a)
		return
	}
	why, bye := s.relay(a.ctx, ws, send, gc, m, termIdle, &ht.bytesIn, &ht.bytesOut)
	gc.Close()
	ws.Close(websocket.StatusNormalClosure, "")
	if bye {
		ht.end("closed")
		return
	}
	s.log.Info("remote: terminal held", "machine", m.Name, "session", ht.sess.ID, "why", why)
	ht.detach(a)
}

// takeHeld finds this person's held terminal on the ticket's computer and
// attaches to it: the one this device left first (an iPhone back from
// another app may still have its old tunnel open: that one is cut off), else
// the one left most recently. One another device is looking at stays theirs.
func (s *Server) takeHeld(t ticket) (*heldTerm, *termAttach) {
	s.heldMu.Lock()
	defer s.heldMu.Unlock()
	var best *heldTerm
	score := func(h *heldTerm) int {
		n := 0
		if h.dev == t.devKey {
			n += 2
		}
		if h.att != nil {
			n++
		}
		return n
	}
	for _, h := range s.held {
		if h.ended || h.uid != t.uid || h.machineID != t.machineID || (h.att != nil && h.att.dev != t.devKey) {
			continue
		}
		if best == nil || score(h) > score(best) || (score(h) == score(best) && h.detached.After(best.detached)) {
			best = h
		}
	}
	if best == nil {
		return nil, nil
	}
	if best.att != nil {
		best.att.cancel()
	}
	if best.timer != nil {
		best.timer.Stop()
	}
	a := best.newAttach(t.devKey)
	best.att = a
	best.dev = t.devKey
	best.ls.held.Store(false)
	return best, a
}

func (ht *heldTerm) newAttach(dev string) *termAttach {
	ctx, cancel := context.WithCancel(ht.ctx)
	return &termAttach{dev: dev, ctx: ctx, cancel: cancel}
}

// detach lets the tunnel go and holds the terminal (unless another tunnel
// took it meanwhile).
func (ht *heldTerm) detach(a *termAttach) {
	a.cancel()
	s := ht.s
	s.heldMu.Lock()
	defer s.heldMu.Unlock()
	if ht.ended || ht.att != a {
		return
	}
	ht.att = nil
	ht.detached = time.Now()
	ht.ls.held.Store(true)
	ht.timer = time.AfterFunc(termHold, func() {
		s.heldMu.Lock()
		over := !ht.ended && ht.att == nil && time.Since(ht.detached) >= termHold-time.Second
		s.heldMu.Unlock()
		if over {
			ht.end("left too long")
		}
	})
}

// keep is the anchor's side of the conversation: it answers guacd's syncs
// (else guacd thinks the anchor lags and slows the screen for everyone) and
// says something every few seconds (guacd drops a user silent for 15 s).
func (ht *heldTerm) keep() {
	go func() {
		tk := time.NewTicker(5 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-ht.ctx.Done():
				ht.end("ended from another device")
				return
			case <-tk.C:
				if err := ht.write(guac.Encode("nop")); err != nil {
					ht.end("lost")
					return
				}
			}
		}
	}()
	for {
		raw, op, err := ht.anchor.R.ReadRaw()
		if err != nil {
			ht.end("closed") // the shell ended, or guacd went
			return
		}
		if op == "sync" {
			if in, err := guac.Parse(raw); err == nil && len(in.Args) > 0 {
				_ = ht.write(guac.Encode("sync", in.Args[0]))
			}
		}
	}
}

func (ht *heldTerm) write(text string) error {
	ht.wmu.Lock()
	defer ht.wmu.Unlock()
	_ = ht.anchor.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := ht.anchor.Write([]byte(text))
	return err
}

// end closes the terminal for good and logs the session's end.
func (ht *heldTerm) end(reason string) {
	s := ht.s
	s.heldMu.Lock()
	if ht.ended {
		s.heldMu.Unlock()
		return
	}
	ht.ended = true
	delete(s.held, ht.sess.ID)
	if ht.timer != nil {
		ht.timer.Stop()
	}
	a := ht.att
	ht.att = nil
	s.heldMu.Unlock()
	if a != nil {
		a.cancel()
	}
	ht.ls.cancel()
	ht.anchor.Close()
	s.dropLive(ht.sess.ID)
	s.endSession(ht.uid, ht.sess, ht.ls, reason, ht.bytesIn.Load(), ht.bytesOut.Load())
}
