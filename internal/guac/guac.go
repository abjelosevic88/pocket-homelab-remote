// Package guac speaks the Guacamole protocol to guacd, the engine that does
// the actual VNC, RDP and SSH work: it opens a connection with the machine's
// settings (handshake) and then reads whole instructions, so the browser
// tunnel can pass them on one complete instruction at a time.
//
// An instruction is LEN.VALUE elements separated by ',' and ended by ';'.
// LEN counts Unicode code points, not bytes.
package guac

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Instruction is one decoded instruction.
type Instruction struct {
	Opcode string
	Args   []string
}

// Encode renders an instruction in wire form.
func Encode(opcode string, args ...string) string {
	var b strings.Builder
	el := func(s string) {
		b.WriteString(strconv.Itoa(utf8.RuneCountInString(s)))
		b.WriteByte('.')
		b.WriteString(s)
	}
	el(opcode)
	for _, a := range args {
		b.WriteByte(',')
		el(a)
	}
	b.WriteByte(';')
	return b.String()
}

// String is the wire form.
func (in Instruction) String() string { return Encode(in.Opcode, in.Args...) }

// ErrMalformed is a stream that is not the Guacamole protocol.
var ErrMalformed = errors.New("guacamole: malformed instruction")

// Reader reads whole instructions from a stream.
type Reader struct {
	r *bufio.Reader
}

// NewReader wraps a stream.
func NewReader(r io.Reader) *Reader { return &Reader{r: bufio.NewReaderSize(r, 64<<10)} }

// maxElement guards against a broken stream asking for gigabytes.
const maxElement = 16 << 20

// ReadRaw reads the next instruction and returns it in wire form together
// with its opcode (the browser gets the raw text unchanged).
func (rd *Reader) ReadRaw() (raw string, opcode string, err error) {
	var b strings.Builder
	first := true
	for {
		// length
		n := 0
		digits := 0
		for {
			c, err := rd.r.ReadByte()
			if err != nil {
				return "", "", err
			}
			b.WriteByte(c)
			if c == '.' {
				break
			}
			if c < '0' || c > '9' || digits > 9 {
				return "", "", ErrMalformed
			}
			n = n*10 + int(c-'0')
			digits++
		}
		if digits == 0 || n > maxElement {
			return "", "", ErrMalformed
		}
		// n code points
		start := b.Len()
		for i := 0; i < n; i++ {
			r, size, err := rd.r.ReadRune()
			if err != nil {
				return "", "", err
			}
			if r == utf8.RuneError && size == 1 {
				b.WriteByte(0xEF) // keep the count right; guacd only sends UTF-8
				b.WriteByte(0xBF)
				b.WriteByte(0xBD)
				continue
			}
			b.WriteRune(r)
		}
		if first {
			opcode = b.String()[start:]
			first = false
		}
		c, err := rd.r.ReadByte()
		if err != nil {
			return "", "", err
		}
		b.WriteByte(c)
		switch c {
		case ';':
			return b.String(), opcode, nil
		case ',':
		default:
			return "", "", ErrMalformed
		}
	}
}

// Read reads and decodes the next instruction.
func (rd *Reader) Read() (Instruction, error) {
	raw, _, err := rd.ReadRaw()
	if err != nil {
		return Instruction{}, err
	}
	return Parse(raw)
}

// Buffered reports whether more bytes are already waiting.
func (rd *Reader) Buffered() int { return rd.r.Buffered() }

// Parse decodes one instruction in wire form.
func Parse(raw string) (Instruction, error) {
	var els []string
	rest := raw
	for {
		dot := strings.IndexByte(rest, '.')
		if dot <= 0 {
			return Instruction{}, ErrMalformed
		}
		n, err := strconv.Atoi(rest[:dot])
		if err != nil || n < 0 {
			return Instruction{}, ErrMalformed
		}
		rest = rest[dot+1:]
		// advance n runes
		i := 0
		for k := 0; k < n; k++ {
			if i >= len(rest) {
				return Instruction{}, ErrMalformed
			}
			_, size := utf8.DecodeRuneInString(rest[i:])
			i += size
		}
		if i >= len(rest) {
			return Instruction{}, ErrMalformed
		}
		els = append(els, rest[:i])
		term := rest[i]
		rest = rest[i+1:]
		if term == ';' {
			break
		}
		if term != ',' {
			return Instruction{}, ErrMalformed
		}
	}
	return Instruction{Opcode: els[0], Args: els[1:]}, nil
}

// SplitAll decodes every complete instruction in s (a browser message may
// carry several) and returns the raw text of each.
func SplitAll(s string) ([]string, error) {
	var out []string
	for len(s) > 0 {
		end, err := instructionEnd(s)
		if err != nil {
			return out, err
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	return out, nil
}

func instructionEnd(s string) (int, error) {
	i := 0
	for {
		dot := strings.IndexByte(s[i:], '.')
		if dot <= 0 {
			return 0, ErrMalformed
		}
		n, err := strconv.Atoi(s[i : i+dot])
		if err != nil || n < 0 {
			return 0, ErrMalformed
		}
		i += dot + 1
		for k := 0; k < n; k++ {
			if i >= len(s) {
				return 0, ErrMalformed
			}
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
		}
		if i >= len(s) {
			return 0, ErrMalformed
		}
		switch s[i] {
		case ';':
			return i + 1, nil
		case ',':
			i++
		default:
			return 0, ErrMalformed
		}
	}
}

// Config is what one connection needs: the protocol and its parameters
// (hostname, port, username, password, …, by guacd's parameter names).
type Config struct {
	Protocol   string
	Params     map[string]string
	Width      int
	Height     int
	DPI        int
	Audio      []string
	Video      []string
	Image      []string
	Timezone   string
	ClientName string
}

// Conn is an open guacd connection after the handshake.
type Conn struct {
	net.Conn
	R  *Reader
	ID string // guacd's connection id
}

// Error is an error guacd reported (status codes as in the protocol).
type Error struct {
	Message string
	Code    int
}

func (e *Error) Error() string { return e.Message }

// protocolVersion is what we tell guacd we speak.
const protocolVersion = "VERSION_1_5_0"

// Dial connects to guacd at addr and performs the handshake.
func Dial(ctx context.Context, addr string, cfg Config) (*Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("the remote desktop engine (guacd at %s) is not running: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(dl)
	} else {
		_ = nc.SetDeadline(time.Now().Add(30 * time.Second))
	}
	c := &Conn{Conn: nc, R: NewReader(nc)}
	fail := func(err error) (*Conn, error) {
		nc.Close()
		return nil, err
	}
	send := func(op string, args ...string) error {
		_, err := io.WriteString(nc, Encode(op, args...))
		return err
	}
	if err := send("select", cfg.Protocol); err != nil {
		return fail(err)
	}
	args, err := c.expect("args")
	if err != nil {
		return fail(err)
	}
	w, h, dpi := cfg.Width, cfg.Height, cfg.DPI
	if w <= 0 || h <= 0 {
		w, h = 1024, 768
	}
	if dpi <= 0 {
		dpi = 96
	}
	if err := send("size", strconv.Itoa(w), strconv.Itoa(h), strconv.Itoa(dpi)); err != nil {
		return fail(err)
	}
	if err := send("audio", cfg.Audio...); err != nil {
		return fail(err)
	}
	if err := send("video", cfg.Video...); err != nil {
		return fail(err)
	}
	img := cfg.Image
	if len(img) == 0 {
		img = []string{"image/png", "image/jpeg", "image/webp"}
	}
	if err := send("image", img...); err != nil {
		return fail(err)
	}
	if cfg.Timezone != "" {
		if err := send("timezone", cfg.Timezone); err != nil {
			return fail(err)
		}
	}
	if cfg.ClientName != "" {
		if err := send("name", cfg.ClientName); err != nil {
			return fail(err)
		}
	}
	vals := make([]string, len(args.Args))
	for i, name := range args.Args {
		if strings.HasPrefix(name, "VERSION_") {
			vals[i] = protocolVersion
			continue
		}
		vals[i] = cfg.Params[name]
	}
	if err := send("connect", vals...); err != nil {
		return fail(err)
	}
	ready, err := c.expect("ready")
	if err != nil {
		return fail(err)
	}
	if len(ready.Args) > 0 {
		c.ID = ready.Args[0]
	}
	_ = nc.SetDeadline(time.Time{})
	return c, nil
}

// expect reads until the opcode arrives; an "error" from guacd fails.
func (c *Conn) expect(op string) (Instruction, error) {
	for {
		in, err := c.R.Read()
		if err != nil {
			return in, fmt.Errorf("the remote desktop engine closed the connection: %w", err)
		}
		switch in.Opcode {
		case op:
			return in, nil
		case "error":
			e := &Error{Message: "the remote desktop engine refused the connection"}
			if len(in.Args) > 0 {
				e.Message = in.Args[0]
			}
			if len(in.Args) > 1 {
				e.Code, _ = strconv.Atoi(in.Args[1])
			}
			return in, e
		}
	}
}
