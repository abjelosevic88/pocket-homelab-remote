package server

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

// seedDemo fills the owner's account with a few computers and three months
// of sessions and file transfers (DEMO_DATA=true, for screenshots and trying
// the app). Demo machines live on ".demo" addresses: never dialled, their
// status is made up. Runs once.
func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	ctx = context.WithoutCancel(ctx)
	key := fmt.Sprintf("seeded:%d:demo", owner)
	if _, _, done := s.store.CacheGet(ctx, key); done {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	pw, _ := s.seal.Seal("demo-password")
	machines := []store.Machine{
		{Name: "MacBook Pro", Kind: "mac", Host: "macbook-pro.demo", Protocol: "vnc", DesktopEnabled: true, TerminalEnabled: true, Username: "aleksandar", Secret: pw, MACAddress: "a4:83:e7:12:9c:01", FilesEnabled: true, Favorite: true, Notes: "Screen Sharing on, Remote Login on."},
		{Name: "Gaming PC", Kind: "windows", Host: "gaming-pc.demo", Protocol: "rdp", DesktopEnabled: true, Username: "Aleksandar", Secret: pw, MACAddress: "2c:f0:5d:aa:41:7e", Options: []byte(`{"rdp_security":"nla","ignore_cert":true,"resize":true}`), Favorite: true},
		{Name: "Home server", Kind: "linux", Host: "home-server.demo", Protocol: "rdp", DesktopEnabled: true, TerminalEnabled: true, Username: "aleksandar", Secret: pw, FilesEnabled: true, Options: []byte(`{"font_size":13}`)},
		{Name: "Mom's laptop", Kind: "windows", Host: "mom-laptop.demo", Protocol: "rdp", DesktopEnabled: true, Username: "Mama", Secret: pw, Notes: "Ask her to keep it plugged in; it sleeps after 30 minutes."},
		{Name: "NAS", Kind: "linux", Host: "truenas.demo", Protocol: "rdp", TerminalEnabled: true, Username: "admin", Secret: pw, FilesEnabled: true, MACAddress: "00:11:32:7a:bc:de"},
	}
	for i := range machines {
		if err := s.store.CreateMachine(ctx, owner, &machines[i]); err != nil {
			return err
		}
	}
	devices := []string{"iPhone · Pocket AI", "iPhone · Safari", "Mac · Safari", "iPad · Safari", "Windows · Edge"}
	reasons := []string{"closed", "closed", "closed", "closed", "lost"}
	now := time.Now()
	weights := []int{6, 4, 5, 1, 2}
	total := 0
	for _, w := range weights {
		total += w
	}
	for d := 90; d >= 0; d-- {
		day := now.AddDate(0, 0, -d)
		n := rng.Intn(3)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			n += rng.Intn(2)
		}
		for k := 0; k < n; k++ {
			pick := rng.Intn(total)
			mi := 0
			for acc := 0; mi < len(weights); mi++ {
				acc += weights[mi]
				if pick < acc {
					break
				}
			}
			m := machines[mi]
			start := time.Date(day.Year(), day.Month(), day.Day(), 7+rng.Intn(15), rng.Intn(60), rng.Intn(60), 0, time.Local)
			if start.After(now) {
				continue
			}
			proto := m.Protocol
			if !m.DesktopEnabled || (m.TerminalEnabled && rng.Intn(3) == 0) {
				proto = "ssh"
			}
			dur := 60 + rng.Intn(40*60)
			if proto == "ssh" {
				dur = 30 + rng.Intn(12*60)
			}
			r := store.RemoteSession{MachineID: &machines[mi].ID, MachineName: m.Name, Protocol: proto,
				StartedAt: start.UTC().Format(time.RFC3339Nano), EndedAt: start.Add(time.Duration(dur) * time.Second).UTC().Format(time.RFC3339Nano),
				DurationS: dur, Device: devices[rng.Intn(len(devices))], IP: fmt.Sprintf("100.%d.%d.%d", 64+rng.Intn(60), rng.Intn(255), 1+rng.Intn(254)),
				EndReason: reasons[rng.Intn(len(reasons))], BytesIn: int64(dur * (200 + rng.Intn(800))), BytesOut: int64(dur * (20000 + rng.Intn(90000)))}
			if err := s.store.StartSession(ctx, owner, &r); err != nil {
				return err
			}
			if m.FilesEnabled && rng.Intn(4) == 0 {
				files := []string{"Invoice-2026-10.pdf", "holiday.jpg", "notes.txt", "backup-config.yml", "Presentation.key", "scan-0042.pdf"}
				f := files[rng.Intn(len(files))]
				dirs := []string{"download", "to_files", "upload"}
				dir := dirs[rng.Intn(len(dirs))]
				t := store.Transfer{MachineID: &machines[mi].ID, MachineName: m.Name, Direction: dir, Path: "/Users/aleksandar/Documents/" + f, Name: f,
					Size: int64(20_000 + rng.Intn(8_000_000)), CreatedAt: start.Add(time.Duration(dur/2) * time.Second).UTC().Format(time.RFC3339Nano)}
				if dir == "to_files" {
					t.Target = "Remote/" + m.Name + "/" + f
				}
				if err := s.store.AddTransfer(ctx, owner, &t); err != nil {
					return err
				}
			}
		}
	}
	if _, err := s.store.DB.ExecContext(ctx, `UPDATE machines SET last_used_at = COALESCE((SELECT MAX(started_at) FROM remote_sessions r WHERE r.machine_id = machines.id), '') WHERE user_id = ?`, owner); err != nil {
		return err
	}
	_ = s.store.CachePut(ctx, key, "1")
	return nil
}
