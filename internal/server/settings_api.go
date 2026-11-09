package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-remote/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat, "week_start": st.WeekStart,
		"pin_set": st.PINHash != "", "pin_minutes": st.PINMinutes, "touch_mode": st.TouchMode, "scale_mode": st.ScaleMode,
		"keys_bar": st.KeysBar, "confirm_close": st.ConfirmClose,
		"notify_sessions": st.NotifySessions, "notify_offline": st.NotifyOffline,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"files_url": st.FilesURL, "files_token": mask(st.FilesToken), "files_public_url": st.FilesPublicURL, "files_folder": st.FilesFolder,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
		"guacd": s.guacdStatus(ctx), "tailnet": s.tailnet != nil,
	}
	for _, a := range pocketApps {
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
		_, _, _, public := connSettings(st, a)
		if public == "" && via != "" {
			public = s.resolveSibling(ctx, a.slug)
		}
		out[a.slug+"_open_url"] = public
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

var pinRe = regexp.MustCompile(`^\d{4,12}$`)

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in struct {
		store.Settings
		PIN        string `json:"pin"`
		PINCurrent string `json:"pin_current"`
		PINClear   bool   `json:"pin_clear"`
	}
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, msg) }
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	if keys["week_start"] && (in.WeekStart == 1 || in.WeekStart == 7 || in.WeekStart == 6) {
		cur.WeekStart = in.WeekStart
	}
	// The connect PIN: changing or removing one needs the current one.
	if keys["pin"] || (keys["pin_clear"] && in.PINClear) {
		if cur.PINHash != "" && bcrypt.CompareHashAndPassword([]byte(cur.PINHash), []byte(in.PINCurrent)) != nil {
			bad("the current PIN is not right")
			return
		}
		if keys["pin"] && !in.PINClear {
			if !pinRe.MatchString(in.PIN) {
				bad("a PIN is 4 to 12 digits")
				return
			}
			h, _ := bcrypt.GenerateFromPassword([]byte(in.PIN), bcrypt.DefaultCost)
			cur.PINHash = string(h)
		} else {
			cur.PINHash = ""
		}
		s.forgetUnlocks(me.ID)
	}
	if keys["pin_minutes"] {
		if in.PINMinutes < 0 || in.PINMinutes > 24*60 {
			bad("the PIN stays good for 0 to 1440 minutes")
			return
		}
		cur.PINMinutes = in.PINMinutes
	}
	pick := func(k string, dst *string, v string, allowed []string) bool {
		if !keys[k] {
			return true
		}
		for _, a := range allowed {
			if v == a {
				*dst = v
				return true
			}
		}
		bad(fmt.Sprintf("%s must be one of %s", k, strings.Join(allowed, ", ")))
		return false
	}
	if !pick("touch_mode", &cur.TouchMode, in.TouchMode, []string{"trackpad", "touch"}) ||
		!pick("scale_mode", &cur.ScaleMode, in.ScaleMode, []string{"fit", "actual"}) {
		return
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	boolIn("keys_bar", &cur.KeysBar, in.KeysBar)
	boolIn("confirm_close", &cur.ConfirmClose, in.ConfirmClose)
	boolIn("notify_sessions", &cur.NotifySessions, in.NotifySessions)
	boolIn("notify_offline", &cur.NotifyOffline, in.NotifyOffline)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	url("files_url", &cur.FilesURL, in.FilesURL)
	secret("files_token", &cur.FilesToken, in.FilesToken)
	url("files_public_url", &cur.FilesPublicURL, in.FilesPublicURL)
	if keys["files_folder"] {
		f := strings.Trim(strings.TrimSpace(in.FilesFolder), "/")
		if f == "" || strings.Contains(f, "..") {
			bad("choose a folder inside your Pocket Files, like Remote")
			return
		}
		cur.FilesFolder = f
	}
	mode := func(k string, dst *string, v string) {
		if keys[k] && (v == modeCore || v == modeDirect || v == modeOff) {
			*dst = v
		}
	}
	mode("files_mode", &cur.FilesMode, in.FilesMode)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && keys["title"] {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for _, k := range modeKeys {
		if keys[k] {
			s.reportModesSoon()
			break
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	if a, ok := appBySlug(in.Which); ok {
		mode, savedURL, savedTok, _ := connSettings(st, a)
		typed := in.Key != "" && !strings.Contains(in.Key, "••")
		var conn *siblingConn
		if tok := keyOr(savedTok); tok != "" && (typed || s.connMode(mode, savedTok) == modeDirect) {
			base := urlOr(savedURL)
			if base == "" {
				base = s.resolveSibling(ctx, a.slug)
			}
			conn = &siblingConn{client: pocket.New(base, tok, 15*time.Second), via: modeDirect}
		} else {
			conn, err = s.clients(ctx, uid).conn(ctx, a, a.scopes...)
		}
		if err == nil {
			how := "direct connection"
			if conn.via == modeCore {
				how = "through Pocket Core"
			}
			var v string
			if v, err = conn.client.Health(ctx); err == nil {
				msg = fmt.Sprintf("%s %s, %s; files go to the folder %s", a.name, v, how, st.FilesFolder)
			}
		}
	} else if in.Which == "notify" {
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Remote", Body: "Notifications work. You will hear here when someone opens a remote session.", Priority: "info", Tags: []string{"desktop_computer"}})
		msg = "Test message sent via " + n.Format
	} else {
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Remote", Body: "Notifications work.", Priority: "info", Tags: []string{"desktop_computer"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "sessions_opened": s.sessions.Load(),
		"guacd": s.cfg.GuacdAddr, "key_file": s.cfg.KeyFile,
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-remote-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's data. Machine passwords and
// keys are never exported: after an import they are typed in again.
type exportDoc struct {
	App       string                `json:"app"`
	Version   string                `json:"version"`
	Exported  string                `json:"exported_at"`
	Machines  []store.Machine       `json:"machines"`
	Sessions  []store.RemoteSession `json:"sessions"`
	Transfers []store.Transfer      `json:"transfers"`
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := exportDoc{App: "pocket-remote", Version: Version, Exported: store.Now()}
	doc.Machines, _ = s.store.ListMachines(ctx, uid)
	doc.Sessions, _, _ = s.store.ListSessions(ctx, uid, store.SessionFilter{Limit: 1000})
	doc.Transfers, _ = s.store.ListTransfers(ctx, uid, 1000)
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-remote-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// handleExportCSV writes the session log as CSV.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rs, _, err := s.store.ListSessions(ctx, actorID(ctx), store.SessionFilter{Limit: 1000})
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-remote-sessions.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"started_at", "ended_at", "machine", "protocol", "minutes", "device", "ip", "end_reason"})
	for _, x := range rs {
		_ = cw.Write([]string{x.StartedAt, x.EndedAt, x.MachineName, x.Protocol, strconv.FormatFloat(float64(x.DurationS)/60, 'f', 1, 64), x.Device, x.IP, x.EndReason})
	}
	cw.Flush()
}

// handleImportJSON merges an export into the caller's own data. A machine
// with the same name and address is kept as it is; sessions and transfers
// already in the log are skipped, so importing twice adds nothing.
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	if err := decode(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if doc.App != "" && doc.App != "pocket-remote" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Remote export")
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	counts := map[string]int{"machines": 0, "sessions": 0, "transfers": 0, "skipped": 0}
	have, _ := s.store.ListMachines(ctx, uid)
	key := func(name, host string) string { return strings.ToLower(name) + "\x00" + strings.ToLower(host) }
	byKey := map[string]int64{}
	for _, m := range have {
		byKey[key(m.Name, m.Host)] = m.ID
	}
	ids := map[int64]int64{}
	for _, m := range doc.Machines {
		old := m.ID
		if id, ok := byKey[key(m.Name, m.Host)]; ok {
			ids[old] = id
			counts["skipped"]++
			continue
		}
		in := machineInput{Name: m.Name, Kind: m.Kind, Host: m.Host, Protocol: m.Protocol, Port: m.Port, Username: m.Username, Domain: m.Domain,
			MACAddress: m.MACAddress, WOLBroadcast: m.WOLBroadcast, FilesEnabled: m.FilesEnabled, SSHPort: m.SSHPort, Options: m.Options, Notes: m.Notes, Favorite: m.Favorite, Sort: m.Sort,
			Desktop: m.DesktopEnabled, Terminal: m.TerminalEnabled}
		nm := store.Machine{}
		if msg := in.apply(&nm, map[string]bool{"name": true, "kind": true, "host": true, "protocol": true, "port": true, "username": true, "domain": true, "mac_address": true, "wol_broadcast": true, "files_enabled": true, "ssh_port": true, "options": true, "notes": true, "favorite": true, "sort": true, "desktop_enabled": true, "terminal_enabled": true}); msg != "" {
			counts["skipped"]++
			continue
		}
		if err := s.store.CreateMachine(ctx, uid, &nm); err != nil {
			counts["skipped"]++
			continue
		}
		ids[old] = nm.ID
		byKey[key(nm.Name, nm.Host)] = nm.ID
		counts["machines"]++
	}
	seen := map[string]bool{}
	cur, _, _ := s.store.ListSessions(ctx, uid, store.SessionFilter{Limit: 1000})
	for _, x := range cur {
		seen[x.StartedAt+"\x00"+x.MachineName] = true
	}
	for _, x := range doc.Sessions {
		if x.StartedAt == "" || seen[x.StartedAt+"\x00"+x.MachineName] {
			continue
		}
		if x.MachineID != nil {
			if nid, ok := ids[*x.MachineID]; ok {
				x.MachineID = &nid
			} else {
				x.MachineID = nil
			}
		}
		if x.EndedAt == "" {
			x.EndedAt = x.StartedAt
		}
		if err := s.store.StartSession(ctx, uid, &x); err == nil {
			seen[x.StartedAt+"\x00"+x.MachineName] = true
			counts["sessions"]++
		}
	}
	tseen := map[string]bool{}
	curT, _ := s.store.ListTransfers(ctx, uid, 1000)
	for _, t := range curT {
		tseen[t.CreatedAt+"\x00"+t.Path] = true
	}
	for _, t := range doc.Transfers {
		if t.CreatedAt == "" || tseen[t.CreatedAt+"\x00"+t.Path] {
			continue
		}
		if t.MachineID != nil {
			if nid, ok := ids[*t.MachineID]; ok {
				t.MachineID = &nid
			} else {
				t.MachineID = nil
			}
		}
		if err := s.store.AddTransfer(ctx, uid, &t); err == nil {
			tseen[t.CreatedAt+"\x00"+t.Path] = true
			counts["transfers"]++
		}
	}
	writeJSON(w, http.StatusOK, counts)
}
