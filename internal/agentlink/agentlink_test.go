package agentlink

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// memT is one end of an in-memory message pipe.
type memT struct {
	in, out chan []byte
	once    *sync.Once
	done    chan struct{}
}

func memPair() (*memT, *memT) {
	a, b := make(chan []byte, 1024), make(chan []byte, 1024)
	once, done := &sync.Once{}, make(chan struct{})
	return &memT{in: a, out: b, once: once, done: done}, &memT{in: b, out: a, once: once, done: done}
}

func (t *memT) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.in:
		return b, nil
	case <-t.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *memT) WriteMessage(ctx context.Context, b []byte) error {
	select {
	case t.out <- append([]byte(nil), b...):
		return nil
	case <-t.done:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *memT) Close() error { t.once.Do(func() { close(t.done) }); return nil }

// echoAgent answers "tcp" opens with an echo server and refuses the rest.
func pair(t *testing.T) (*Mux, *Mux) {
	ta, tb := memPair()
	remote, agent := New(ta), New(tb)
	agent.OnOpen = func(req OpenRequest) (net.Conn, error) {
		if req.Kind != "tcp" {
			return nil, errors.New("no such thing")
		}
		a, b := net.Pipe()
		go func() { _, _ = io.Copy(b, b); b.Close() }()
		return a, nil
	}
	go remote.Run()
	go agent.Run()
	t.Cleanup(func() { remote.Close(); agent.Close() })
	return remote, agent
}

func TestBigTransferBothWays(t *testing.T) {
	remote, _ := pair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := remote.Open(ctx, OpenRequest{Kind: "tcp", Port: 7})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 6<<20+123)
	rand.Read(want)
	go func() { st.Write(want) }()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(st, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("echo differs")
	}
	st.Close()
}

func TestManyTinyWrites(t *testing.T) {
	remote, _ := pair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := remote.Open(ctx, OpenRequest{Kind: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	const n = 200000
	go func() {
		for i := 0; i < n; i++ {
			if _, err := st.Write([]byte{byte(i)}); err != nil {
				return
			}
		}
	}()
	got := make([]byte, n)
	if _, err := io.ReadFull(st, got); err != nil {
		t.Fatal(err)
	}
	for i := range got {
		if got[i] != byte(i) {
			t.Fatalf("byte %d", i)
		}
	}
}

func TestRefusedAndClose(t *testing.T) {
	remote, agent := pair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := remote.Open(ctx, OpenRequest{Kind: "nope"}); err == nil || err.Error() != "no such thing" {
		t.Fatalf("refusal: %v", err)
	}
	st, err := remote.Open(ctx, OpenRequest{Kind: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		agent.mu.Lock()
		n := len(agent.streams)
		agent.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent kept the stream after a close")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The link ending ends open streams.
	st2, err := remote.Open(ctx, OpenRequest{Kind: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	agent.Close()
	st2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := st2.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read after link end: %v", err)
	}
}

func TestReadDeadline(t *testing.T) {
	remote, _ := pair(t)
	st, err := remote.Open(context.Background(), OpenRequest{Kind: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	st.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	start := time.Now()
	if _, err := st.Read(make([]byte, 1)); !errors.Is(err, errDeadline()) {
		t.Fatalf("deadline: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("deadline noticed late")
	}
}

func TestPing(t *testing.T) {
	ta, tb := memPair()
	remote, agent := New(ta), New(tb)
	got := make(chan map[string]any, 1)
	remote.OnCtl = func(m map[string]any) {
		if m["t"] == "pong" {
			got <- m
		}
	}
	go remote.Run()
	go agent.Run()
	defer remote.Close()
	remote.Ctl(map[string]any{"t": "ping", "at": 42})
	select {
	case m := <-got:
		if m["at"] != float64(42) {
			t.Fatalf("pong %v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no pong")
	}
}

func errDeadline() error { return osErrDeadline }

var osErrDeadline = os.ErrDeadlineExceeded
