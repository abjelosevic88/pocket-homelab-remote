// Package agentlink is the wire between Pocket Remote and Pocket Wave:
// many byte streams (a terminal, a file browser, a desktop) over one
// message connection, with flow control so a fast sender never floods the
// relay in between.
//
// The same file lives in both repositories (pocket-homelab-remote
// internal/agentlink and pocket-homelab-wave internal/agentlink);
// keep them equal.
//
// Every message is one frame: type u8 | stream u32 BE | payload.
//
//	0x01 CTL      both   JSON control message, stream 0
//	0x10 OPEN     R → A  JSON OpenRequest; Pocket Remote picks the stream id
//	0x11 OPENED   A → R  empty
//	0x12 REFUSED  A → R  text: why
//	0x20 DATA     both   bytes, at most MaxData
//	0x21 ACK      both   u32 BE: bytes the reader has taken, credit back to the writer
//	0x30 CLOSE    both   empty: the stream is over
package agentlink

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// Protocol is the WebSocket subprotocol of the link.
const Protocol = "pocket-agent.v1"

// Frame types.
const (
	FrameCtl     byte = 0x01
	FrameOpen    byte = 0x10
	FrameOpened  byte = 0x11
	FrameRefused byte = 0x12
	FrameData    byte = 0x20
	FrameAck     byte = 0x21
	FrameClose   byte = 0x30
)

const (
	// MaxData is the largest DATA payload: small enough that a frame fits
	// one relay message with room to spare.
	MaxData = 16 << 10
	// Window is how many unacknowledged bytes one stream may have in flight.
	Window = 512 << 10
)

// Transport carries whole messages (a WebSocket, or the relay session).
type Transport interface {
	ReadMessage(ctx context.Context) ([]byte, error)
	WriteMessage(ctx context.Context, b []byte) error
	Close() error
}

// OpenRequest asks the agent for a stream.
type OpenRequest struct {
	// Kind is "ssh" (the agent's own SSH server, signed in with Password) or
	// "tcp" (a port on the agent's computer, 127.0.0.1:Port).
	Kind     string `json:"kind"`
	Port     int    `json:"port,omitempty"`
	Password string `json:"password,omitempty"`
}

// Hello is the agent's first control message.
type Hello struct {
	T       string `json:"t"` // "hello"
	V       int    `json:"v"`
	Name    string `json:"name"`    // the computer's name
	Login   string `json:"login"`   // the account the agent runs as
	OS      string `json:"os"`      // darwin | linux | windows
	Version string `json:"version"` // the agent's version
	HostKey string `json:"host_key,omitempty"`
	Ports   []int  `json:"ports,omitempty"` // tcp ports the agent forwards
}

// ErrClosed is returned once the link is gone.
var ErrClosed = errors.New("agentlink: the link is closed")

// Encode builds one frame.
func Encode(typ byte, id uint32, payload []byte) []byte {
	b := make([]byte, 5+len(payload))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:5], id)
	copy(b[5:], payload)
	return b
}

// Mux runs the streams of one link.
type Mux struct {
	t      Transport
	ctx    context.Context
	cancel context.CancelFunc

	wmu sync.Mutex // one frame at a time

	mu      sync.Mutex
	streams map[uint32]*Stream
	nextID  uint32
	pending map[uint32]chan error // OPEN waiting for OPENED/REFUSED
	err     error

	// OnOpen handles an OPEN from the other side (the agent sets it). It runs
	// in its own goroutine; return an error to refuse.
	OnOpen func(req OpenRequest) (net.Conn, error)
	// OnCtl gets every control message.
	OnCtl func(m map[string]any)

	lastIn time.Time
	lastMu sync.Mutex
}

// New wraps a transport. Call Run to start reading.
func New(t Transport) *Mux {
	ctx, cancel := context.WithCancel(context.Background())
	return &Mux{t: t, ctx: ctx, cancel: cancel, streams: map[uint32]*Stream{}, pending: map[uint32]chan error{}, nextID: 1, lastIn: time.Now()}
}

// Done is closed when the link ends.
func (m *Mux) Done() <-chan struct{} { return m.ctx.Done() }

// Err is why the link ended.
func (m *Mux) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// LastHeard is when the other side last sent anything.
func (m *Mux) LastHeard() time.Time {
	m.lastMu.Lock()
	defer m.lastMu.Unlock()
	return m.lastIn
}

// Close ends the link and every stream.
func (m *Mux) Close() error { m.fail(ErrClosed); return nil }

func (m *Mux) fail(err error) {
	m.mu.Lock()
	if m.err == nil {
		m.err = err
	}
	streams := m.streams
	m.streams = map[uint32]*Stream{}
	pending := m.pending
	m.pending = map[uint32]chan error{}
	m.mu.Unlock()
	m.cancel()
	_ = m.t.Close()
	for _, st := range streams {
		st.closeLocal()
	}
	for _, ch := range pending {
		select {
		case ch <- ErrClosed:
		default:
		}
	}
}

func (m *Mux) write(typ byte, id uint32, p []byte) error {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	if m.ctx.Err() != nil {
		return ErrClosed
	}
	ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
	defer cancel()
	if err := m.t.WriteMessage(ctx, Encode(typ, id, p)); err != nil {
		go m.fail(err)
		return ErrClosed
	}
	return nil
}

// Ctl sends a control message.
func (m *Mux) Ctl(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return m.write(FrameCtl, 0, b)
}

// Run reads frames until the link ends; it returns why.
func (m *Mux) Run() error {
	for {
		b, err := m.t.ReadMessage(m.ctx)
		if err != nil {
			m.fail(err)
			return m.Err()
		}
		m.lastMu.Lock()
		m.lastIn = time.Now()
		m.lastMu.Unlock()
		if len(b) < 5 {
			continue
		}
		typ, id, p := b[0], binary.BigEndian.Uint32(b[1:5]), b[5:]
		m.dispatch(typ, id, p)
	}
}

func (m *Mux) stream(id uint32) *Stream {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streams[id]
}

func (m *Mux) dispatch(typ byte, id uint32, p []byte) {
	switch typ {
	case FrameCtl:
		var c map[string]any
		if json.Unmarshal(p, &c) != nil {
			return
		}
		if c["t"] == "ping" {
			_ = m.Ctl(map[string]any{"t": "pong", "at": c["at"]})
		}
		if m.OnCtl != nil {
			m.OnCtl(c)
		}
	case FrameOpen:
		var req OpenRequest
		if json.Unmarshal(p, &req) != nil || m.OnOpen == nil || m.stream(id) != nil {
			_ = m.write(FrameRefused, id, []byte("bad request"))
			return
		}
		st := m.newStream(id)
		go func() {
			conn, err := m.OnOpen(req)
			if err != nil {
				m.drop(id)
				_ = m.write(FrameRefused, id, []byte(err.Error()))
				return
			}
			if err := m.write(FrameOpened, id, nil); err != nil {
				conn.Close()
				return
			}
			Pipe(st, conn)
		}()
	case FrameOpened, FrameRefused:
		m.mu.Lock()
		ch := m.pending[id]
		delete(m.pending, id)
		m.mu.Unlock()
		if ch == nil {
			return
		}
		if typ == FrameOpened {
			ch <- nil
		} else {
			ch <- &RefusedError{Reason: string(p)}
		}
	case FrameData:
		if st := m.stream(id); st != nil {
			st.push(append([]byte(nil), p...))
		}
	case FrameAck:
		if st := m.stream(id); st != nil && len(p) >= 4 {
			st.credit(int(binary.BigEndian.Uint32(p)))
		}
	case FrameClose:
		if st := m.stream(id); st != nil {
			m.drop(id)
			st.closeLocal()
		}
	}
}

// RefusedError is the agent's answer to an OPEN it would not do.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return e.Reason }

// Open asks the other side for a stream.
func (m *Mux) Open(ctx context.Context, req OpenRequest) (*Stream, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.err != nil {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	id := m.nextID
	m.nextID += 2 // the opener's ids are odd; the agent never opens
	ch := make(chan error, 1)
	m.pending[id] = ch
	m.mu.Unlock()
	st := m.newStream(id)
	if err := m.write(FrameOpen, id, b); err != nil {
		m.drop(id)
		return nil, err
	}
	select {
	case err := <-ch:
		if err != nil {
			m.drop(id)
			return nil, err
		}
		return st, nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.pending, id)
		m.mu.Unlock()
		m.drop(id)
		_ = m.write(FrameClose, id, nil)
		return nil, ctx.Err()
	case <-m.ctx.Done():
		return nil, ErrClosed
	}
}

func (m *Mux) newStream(id uint32) *Stream {
	st := &Stream{m: m, id: id, sendCredit: Window, done: make(chan struct{})}
	st.cond = sync.NewCond(&st.mu)
	m.mu.Lock()
	m.streams[id] = st
	m.mu.Unlock()
	return st
}

func (m *Mux) drop(id uint32) {
	m.mu.Lock()
	delete(m.streams, id)
	m.mu.Unlock()
}

// Stream is one byte stream of a link; it is a net.Conn.
type Stream struct {
	m  *Mux
	id uint32

	mu         sync.Mutex
	cond       *sync.Cond // credit arrived, data arrived, closed, a deadline may have passed
	sendCredit int
	rbuf       []byte // received, not read yet (at most Window bytes)
	unacked    int
	closed     bool
	rdl, wdl   time.Time

	done chan struct{}
	once sync.Once
}

func (s *Stream) push(b []byte) {
	s.mu.Lock()
	if len(s.rbuf)+len(b) > Window {
		s.mu.Unlock()
		s.Close() // the other side sent past its window: a broken peer
		return
	}
	s.rbuf = append(s.rbuf, b...)
	s.mu.Unlock()
	s.cond.Broadcast()
}

func (s *Stream) credit(n int) {
	s.mu.Lock()
	s.sendCredit += n
	s.mu.Unlock()
	s.cond.Broadcast()
}

func (s *Stream) closeLocal() {
	s.once.Do(func() {
		close(s.done)
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.cond.Broadcast()
	})
}

func passed(t time.Time) bool { return !t.IsZero() && time.Now().After(t) }

// wait sleeps on the condition (s.mu held), waking at least every 250 ms so
// a deadline is noticed.
func (s *Stream) wait() {
	t := time.AfterFunc(250*time.Millisecond, s.cond.Broadcast)
	s.cond.Wait()
	t.Stop()
}

// Read implements net.Conn.
func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	for len(s.rbuf) == 0 {
		if s.closed {
			s.mu.Unlock()
			return 0, io.EOF
		}
		if passed(s.rdl) {
			s.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		s.wait()
	}
	n := copy(p, s.rbuf)
	s.rbuf = s.rbuf[n:]
	if len(s.rbuf) == 0 {
		s.rbuf = nil
	}
	s.unacked += n
	ack := 0
	if s.unacked >= Window/4 || len(s.rbuf) == 0 && s.unacked >= MaxData {
		ack, s.unacked = s.unacked, 0
	}
	closed := s.closed
	s.mu.Unlock()
	if ack > 0 && !closed {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(ack))
		_ = s.m.write(FrameAck, s.id, b[:])
	}
	return n, nil
}

// Write implements net.Conn.
func (s *Stream) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		s.mu.Lock()
		for s.sendCredit <= 0 && !s.closed && !passed(s.wdl) {
			s.wait()
		}
		if s.closed {
			s.mu.Unlock()
			return written, io.ErrClosedPipe
		}
		if passed(s.wdl) {
			s.mu.Unlock()
			return written, os.ErrDeadlineExceeded
		}
		n := min(len(p), MaxData, s.sendCredit)
		s.sendCredit -= n
		s.mu.Unlock()
		if err := s.m.write(FrameData, s.id, p[:n]); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// Close implements net.Conn.
func (s *Stream) Close() error {
	select {
	case <-s.done:
		return nil
	default:
	}
	s.m.drop(s.id)
	s.closeLocal()
	_ = s.m.write(FrameClose, s.id, nil)
	return nil
}

// LocalAddr implements net.Conn.
func (s *Stream) LocalAddr() net.Addr { return addr("agentlink") }

// RemoteAddr implements net.Conn.
func (s *Stream) RemoteAddr() net.Addr { return addr("agentlink") }

// SetDeadline implements net.Conn.
func (s *Stream) SetDeadline(t time.Time) error {
	s.mu.Lock()
	s.rdl, s.wdl = t, t
	s.mu.Unlock()
	s.cond.Broadcast()
	return nil
}

// SetReadDeadline implements net.Conn.
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	s.rdl = t
	s.mu.Unlock()
	s.cond.Broadcast()
	return nil
}

// SetWriteDeadline implements net.Conn.
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.wdl = t
	s.mu.Unlock()
	s.cond.Broadcast()
	return nil
}

type addr string

func (a addr) Network() string { return "agentlink" }
func (a addr) String() string  { return string(a) }

// Pipe copies both ways until either side ends, then closes both.
func Pipe(a, b net.Conn) {
	var once sync.Once
	end := func() { once.Do(func() { a.Close(); b.Close() }) }
	go func() { _, _ = io.Copy(a, b); end() }()
	_, _ = io.Copy(b, a)
	end()
}
