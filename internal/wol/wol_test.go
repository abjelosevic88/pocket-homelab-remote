package wol

import "testing"

func TestPacket(t *testing.T) {
	for _, s := range []string{"a4:83:e7:12:9c:01", "A4-83-E7-12-9C-01", "a483e7129c01"} {
		hw, err := ParseMAC(s)
		if err != nil || hw.String() != "a4:83:e7:12:9c:01" {
			t.Fatalf("%s → %v %v", s, hw, err)
		}
	}
	hw, _ := ParseMAC("a4:83:e7:12:9c:01")
	p := Packet(hw)
	if len(p) != 102 || p[0] != 0xff || p[6] != 0xa4 || p[101] != 0x01 {
		t.Fatalf("packet = %x", p)
	}
	if _, err := ParseMAC("zz"); err == nil {
		t.Fatal("bad address accepted")
	}
}
