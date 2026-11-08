package guac

import (
	"io"
	"strings"
	"testing"
)

func TestEncodeParse(t *testing.T) {
	raw := Encode("clipboard", "0", "text/plain", "Ćevapi 🍖 ok")
	if raw != "9.clipboard,1.0,10.text/plain,11.Ćevapi 🍖 ok;" {
		t.Fatalf("encode = %q", raw)
	}
	in, err := Parse(raw)
	if err != nil || in.Opcode != "clipboard" || in.Args[2] != "Ćevapi 🍖 ok" {
		t.Fatalf("parse = %+v %v", in, err)
	}
	if Encode("", "ping", "1") != "0.,4.ping,1.1;" {
		t.Fatalf("internal = %q", Encode("", "ping", "1"))
	}
}

// slowReader hands out one byte at a time, like a slow network.
type slowReader struct{ r io.Reader }

func (s slowReader) Read(p []byte) (int, error) { return s.r.Read(p[:1]) }

func TestReaderStreams(t *testing.T) {
	stream := Encode("sync", "123") + Encode("name", "Žena") + Encode("nop")
	rd := NewReader(slowReader{strings.NewReader(stream)})
	var ops []string
	for {
		raw, op, err := rd.ReadRaw()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(raw); err != nil {
			t.Fatalf("%q does not parse back: %v", raw, err)
		}
		ops = append(ops, op)
	}
	if strings.Join(ops, " ") != "sync name nop" {
		t.Fatalf("ops = %v", ops)
	}
}

func TestSplitAll(t *testing.T) {
	msg := Encode("mouse", "1", "2", "0") + Encode("key", "65", "1")
	parts, err := SplitAll(msg)
	if err != nil || len(parts) != 2 || parts[1] != "3.key,2.65,1.1;" {
		t.Fatalf("split = %v %v", parts, err)
	}
	for _, bad := range []string{"4.key,", "x.key;", "3.key", "5.key;"} {
		if _, err := SplitAll(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
