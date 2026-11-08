// Package tailnet reads the box's Tailscale status through tailscaled's
// local socket: which devices are on the tailnet and which are online.
// Read only; nothing here changes the tailnet.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// Device is one node on the tailnet.
type Device struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`     // host name
	DNSName  string   `json:"dns_name"` // full MagicDNS name, no trailing dot
	OS       string   `json:"os"`       // macOS | windows | linux | iOS | android
	IPs      []string `json:"ips"`
	Online   bool     `json:"online"`
	LastSeen string   `json:"last_seen,omitempty"`
	Self     bool     `json:"self,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// Client talks to tailscaled over its socket.
type Client struct {
	socket string
	http   *http.Client
}

// New returns a client for the socket, or nil when the path is empty or missing.
func New(socket string) *Client {
	if socket == "" {
		return nil
	}
	if _, err := os.Stat(socket); err != nil {
		return nil
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return &Client{socket: socket, http: &http.Client{Transport: tr, Timeout: 5 * time.Second}}
}

type peer struct {
	ID           string
	HostName     string
	DNSName      string
	OS           string
	Online       bool
	LastSeen     string
	TailscaleIPs []string
	Tags         []string
}

// Devices lists every node, the box itself included, online ones first.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	if c == nil {
		return nil, errors.New("Tailscale is not available here")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://local-tailscaled.sock/localapi/v0/status", nil)
	req.Header.Set("Sec-Tailscale", "localapi")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Tailscale did not answer: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Tailscale answered %s", res.Status)
	}
	var st struct {
		BackendState string
		Self         peer
		Peer         map[string]peer
	}
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return nil, err
	}
	conv := func(p peer, self bool) Device {
		d := Device{ID: p.ID, Name: p.HostName, DNSName: strings.TrimSuffix(p.DNSName, "."), OS: p.OS, IPs: p.TailscaleIPs, Online: p.Online || self, Self: self, Tags: p.Tags}
		if !strings.HasPrefix(p.LastSeen, "0001") {
			d.LastSeen = p.LastSeen
		}
		return d
	}
	out := []Device{conv(st.Self, true)}
	for _, p := range st.Peer {
		out = append(out, conv(p, false))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Match finds the device a host name or address belongs to.
func Match(devs []Device, host string) (Device, bool) {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h == "" {
		return Device{}, false
	}
	for _, d := range devs {
		if strings.EqualFold(d.Name, h) || strings.EqualFold(d.DNSName, h) || strings.HasPrefix(strings.ToLower(d.DNSName), h+".") {
			return d, true
		}
		for _, ip := range d.IPs {
			if ip == h {
				return d, true
			}
		}
	}
	return Device{}, false
}
