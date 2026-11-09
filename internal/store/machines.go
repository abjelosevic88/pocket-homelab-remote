package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

const machineCols = `id, user_id, name, kind, host, protocol, port, username, secret, private_key, domain,
	mac_address, wol_broadcast, files_enabled, ssh_port, host_key, options, notes, favorite, sort, tailnet_id,
	last_used_at, last_online_at, created_at, updated_at, desktop_enabled, terminal_enabled`

func scanMachine(sc interface{ Scan(...any) error }) (Machine, error) {
	var m Machine
	var files, fav, desk, term int
	var opts string
	err := sc.Scan(&m.ID, &m.UserID, &m.Name, &m.Kind, &m.Host, &m.Protocol, &m.Port, &m.Username, &m.Secret, &m.PrivateKey, &m.Domain,
		&m.MACAddress, &m.WOLBroadcast, &files, &m.SSHPort, &m.HostKey, &opts, &m.Notes, &fav, &m.Sort, &m.TailnetID,
		&m.LastUsedAt, &m.LastOnlineAt, &m.CreatedAt, &m.UpdatedAt, &desk, &term)
	m.FilesEnabled, m.Favorite = files == 1, fav == 1
	m.DesktopEnabled, m.TerminalEnabled = desk == 1, term == 1
	if !json.Valid([]byte(opts)) {
		opts = "{}"
	}
	m.Options = json.RawMessage(opts)
	return m, err
}

// ListMachines returns one person's machines, favourites first.
func (s *Store) ListMachines(ctx context.Context, uid int64) ([]Machine, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+machineCols+` FROM machines WHERE user_id = ? ORDER BY favorite DESC, sort, name COLLATE NOCASE`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Machine{}
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AllMachines returns every person's machines (the background status check).
func (s *Store) AllMachines(ctx context.Context) ([]Machine, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+machineCols+` FROM machines ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Machine{}
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMachine returns one of a person's machines (someone else's = ErrNotFound).
func (s *Store) GetMachine(ctx context.Context, uid, id int64) (Machine, error) {
	m, err := scanMachine(s.DB.QueryRowContext(ctx, `SELECT `+machineCols+` FROM machines WHERE user_id = ? AND id = ?`, uid, id))
	return m, notFound(err)
}

func optionsText(raw json.RawMessage) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return "{}"
	}
	return string(raw)
}

// CreateMachine adds a machine for a person.
func (s *Store) CreateMachine(ctx context.Context, uid int64, m *Machine) error {
	if m.SSHPort == 0 {
		m.SSHPort = 22
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO machines (user_id, name, kind, host, protocol, port, username, secret, private_key, domain,
		mac_address, wol_broadcast, files_enabled, ssh_port, host_key, options, notes, favorite, sort, tailnet_id, last_used_at, last_online_at,
		desktop_enabled, terminal_enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, m.Name, m.Kind, m.Host, m.Protocol, m.Port, m.Username, m.Secret, m.PrivateKey, m.Domain,
		m.MACAddress, m.WOLBroadcast, boolInt(m.FilesEnabled), m.SSHPort, m.HostKey, optionsText(m.Options), m.Notes, boolInt(m.Favorite), m.Sort, m.TailnetID,
		m.LastUsedAt, m.LastOnlineAt, boolInt(m.DesktopEnabled), boolInt(m.TerminalEnabled))
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.UserID = uid
	got, err := s.GetMachine(ctx, uid, m.ID)
	if err == nil {
		*m = got
	}
	return err
}

// UpdateMachine saves a person's machine.
func (s *Store) UpdateMachine(ctx context.Context, uid int64, m *Machine) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE machines SET name = ?, kind = ?, host = ?, protocol = ?, port = ?, username = ?, secret = ?, private_key = ?, domain = ?,
		mac_address = ?, wol_broadcast = ?, files_enabled = ?, ssh_port = ?, host_key = ?, options = ?, notes = ?, favorite = ?, sort = ?, tailnet_id = ?,
		desktop_enabled = ?, terminal_enabled = ?,
		updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE user_id = ? AND id = ?`,
		m.Name, m.Kind, m.Host, m.Protocol, m.Port, m.Username, m.Secret, m.PrivateKey, m.Domain,
		m.MACAddress, m.WOLBroadcast, boolInt(m.FilesEnabled), m.SSHPort, m.HostKey, optionsText(m.Options), m.Notes, boolInt(m.Favorite), m.Sort, m.TailnetID,
		boolInt(m.DesktopEnabled), boolInt(m.TerminalEnabled), uid, m.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	got, err := s.GetMachine(ctx, uid, m.ID)
	if err == nil {
		*m = got
	}
	return err
}

// DeleteMachine removes a person's machine (its log entries stay, by name).
func (s *Store) DeleteMachine(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM machines WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostKey remembers the SSH host key seen on first contact.
func (s *Store) SetHostKey(ctx context.Context, uid, id int64, key string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE machines SET host_key = ? WHERE user_id = ? AND id = ?`, key, uid, id)
	return err
}

// TouchMachine records when a machine was last used.
func (s *Store) TouchMachine(ctx context.Context, uid, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE machines SET last_used_at = ? WHERE user_id = ? AND id = ?`, Now(), uid, id)
	return err
}

// SeenOnline records when the status check last reached a machine.
func (s *Store) SeenOnline(ctx context.Context, id int64, at string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE machines SET last_online_at = ? WHERE id = ?`, at, id)
	return err
}

// ---- the session log ----

const sessionCols = `id, user_id, machine_id, machine_name, protocol, started_at, ended_at, duration_s, device, ip, end_reason, bytes_in, bytes_out`

func scanSession(sc interface{ Scan(...any) error }) (RemoteSession, error) {
	var r RemoteSession
	var mid sql.NullInt64
	err := sc.Scan(&r.ID, &r.UserID, &mid, &r.MachineName, &r.Protocol, &r.StartedAt, &r.EndedAt, &r.DurationS, &r.Device, &r.IP, &r.EndReason, &r.BytesIn, &r.BytesOut)
	if mid.Valid {
		r.MachineID = &mid.Int64
	}
	return r, err
}

// StartSession logs the start of a remote session.
func (s *Store) StartSession(ctx context.Context, uid int64, r *RemoteSession) error {
	if r.StartedAt == "" {
		r.StartedAt = Now()
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO remote_sessions (user_id, machine_id, machine_name, protocol, started_at, ended_at, duration_s, device, ip, end_reason, bytes_in, bytes_out)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, nullI(r.MachineID), r.MachineName, r.Protocol, r.StartedAt, r.EndedAt, r.DurationS, r.Device, r.IP, r.EndReason, r.BytesIn, r.BytesOut)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	r.UserID = uid
	return nil
}

// EndSession closes a logged session.
func (s *Store) EndSession(ctx context.Context, uid, id int64, endedAt string, dur int, reason string, in, out int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE remote_sessions SET ended_at = ?, duration_s = ?, end_reason = ?, bytes_in = ?, bytes_out = ? WHERE user_id = ? AND id = ?`,
		endedAt, dur, reason, in, out, uid, id)
	return err
}

// CloseOpenSessions marks sessions a restart cut off.
func (s *Store) CloseOpenSessions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE remote_sessions SET ended_at = started_at, end_reason = 'server restarted' WHERE ended_at = ''`)
	return err
}

// SessionFilter narrows the log.
type SessionFilter struct {
	MachineID int64
	Limit     int
	Offset    int
}

// ListSessions returns one person's sessions, newest first, and the total.
func (s *Store) ListSessions(ctx context.Context, uid int64, f SessionFilter) ([]RemoteSession, int, error) {
	where := `WHERE user_id = ?`
	args := []any{uid}
	if f.MachineID > 0 {
		where += ` AND machine_id = ?`
		args = append(args, f.MachineID)
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM remote_sessions `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sessionCols+` FROM remote_sessions `+where+` ORDER BY started_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []RemoteSession{}
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// SessionStats sums one person's sessions since a time.
type SessionStats struct {
	Sessions int `json:"sessions"`
	Seconds  int `json:"seconds"`
	Machines int `json:"machines"`
}

// StatsSince returns how much a person used remote sessions since a time.
func (s *Store) StatsSince(ctx context.Context, uid int64, since string) (SessionStats, error) {
	var st SessionStats
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(duration_s), 0), COUNT(DISTINCT machine_name) FROM remote_sessions WHERE user_id = ? AND started_at >= ?`, uid, since).
		Scan(&st.Sessions, &st.Seconds, &st.Machines)
	return st, err
}

// ---- file transfers ----

// AddTransfer logs a file copied to or from a machine.
func (s *Store) AddTransfer(ctx context.Context, uid int64, t *Transfer) error {
	if t.CreatedAt == "" {
		t.CreatedAt = Now()
	}
	if t.Status == "" {
		t.Status = "done"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO transfers (user_id, machine_id, machine_name, direction, path, name, size, status, error, target, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, nullI(t.MachineID), t.MachineName, t.Direction, t.Path, t.Name, t.Size, t.Status, t.Error, t.Target, t.CreatedAt)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	t.UserID = uid
	return nil
}

// ListTransfers returns one person's recent transfers, newest first.
func (s *Store) ListTransfers(ctx context.Context, uid int64, limit int) ([]Transfer, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, machine_id, machine_name, direction, path, name, size, status, error, target, created_at
		FROM transfers WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Transfer{}
	for rows.Next() {
		var t Transfer
		var mid sql.NullInt64
		if err := rows.Scan(&t.ID, &t.UserID, &mid, &t.MachineName, &t.Direction, &t.Path, &t.Name, &t.Size, &t.Status, &t.Error, &t.Target, &t.CreatedAt); err != nil {
			return nil, err
		}
		if mid.Valid {
			t.MachineID = &mid.Int64
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
