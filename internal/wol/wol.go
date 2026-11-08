// Package wol sends Wake-on-LAN magic packets.
package wol

import (
	"bytes"
	"fmt"
	"net"
	"strings"
)

// ParseMAC accepts aa:bb:cc:dd:ee:ff, aa-bb-…, or aabbccddeeff.
func ParseMAC(s string) (net.HardwareAddr, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if len(s) == 12 && !strings.ContainsAny(s, ":-.") {
		var b strings.Builder
		for i := 0; i < 12; i += 2 {
			if i > 0 {
				b.WriteByte(':')
			}
			b.WriteString(s[i : i+2])
		}
		s = b.String()
	}
	hw, err := net.ParseMAC(strings.ReplaceAll(s, "-", ":"))
	if err != nil || len(hw) != 6 {
		return nil, fmt.Errorf("%q is not a network card address (like a1:b2:c3:d4:e5:f6)", s)
	}
	return hw, nil
}

// Packet is the magic packet for a MAC: six 0xFF then the address 16 times.
func Packet(hw net.HardwareAddr) []byte {
	var b bytes.Buffer
	b.Write(bytes.Repeat([]byte{0xff}, 6))
	for i := 0; i < 16; i++ {
		b.Write(hw)
	}
	return b.Bytes()
}

// Send broadcasts the packet to addr ("" = 255.255.255.255) on ports 9 and 7.
func Send(mac, addr string) error {
	hw, err := ParseMAC(mac)
	if err != nil {
		return err
	}
	if addr == "" {
		addr = "255.255.255.255"
	}
	pkt := Packet(hw)
	var last error
	sent := 0
	for _, port := range []string{"9", "7"} {
		host, p := addr, port
		if h, pp, err := net.SplitHostPort(addr); err == nil {
			host, p = h, pp
		}
		ua, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(host, p))
		if err != nil {
			return fmt.Errorf("broadcast address: %w", err)
		}
		c, err := net.DialUDP("udp4", nil, ua)
		if err != nil {
			last = err
			continue
		}
		if _, err := c.Write(pkt); err != nil {
			last = err
		} else {
			sent++
		}
		c.Close()
		if p != port { // an explicit port: send once
			break
		}
	}
	if sent == 0 && last != nil {
		return last
	}
	return nil
}
