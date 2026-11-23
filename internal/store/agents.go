package store

import (
	"context"
	"database/sql"
	"errors"
)

// Agent is a paired Pocket Remote Agent (one per Core remote device).
type Agent struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"-"`
	CoreDeviceID int64  `json:"core_device_id"`
	Name         string `json:"name"`
	Login        string `json:"login"`
	OS           string `json:"os"`
	Version      string `json:"version"`
	HostKey      string `json:"host_key"`
	LastSeenAt   string `json:"last_seen_at"`
	CreatedAt    string `json:"created_at"`
}

const agentCols = `id, user_id, core_device_id, name, login, os, version, host_key, last_seen_at, created_at`

func scanAgent(sc interface{ Scan(...any) error }) (Agent, error) {
	var a Agent
	err := sc.Scan(&a.ID, &a.UserID, &a.CoreDeviceID, &a.Name, &a.Login, &a.OS, &a.Version, &a.HostKey, &a.LastSeenAt, &a.CreatedAt)
	return a, notFound(err)
}

// UpsertAgent records an agent that just connected and returns it (new = first time).
func (s *Store) UpsertAgent(ctx context.Context, a Agent) (Agent, bool, error) {
	got, err := scanAgent(s.DB.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE user_id = ? AND core_device_id = ?`, a.UserID, a.CoreDeviceID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Agent{}, false, err
	}
	if errors.Is(err, ErrNotFound) {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO agents (user_id, core_device_id, name, login, os, version, host_key, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			a.UserID, a.CoreDeviceID, a.Name, a.Login, a.OS, a.Version, a.HostKey, Now())
		if err != nil {
			return Agent{}, false, err
		}
		id, _ := res.LastInsertId()
		got, err = s.GetAgent(ctx, a.UserID, id)
		return got, true, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE agents SET name = ?, login = ?, os = ?, version = ?, host_key = ?, last_seen_at = ? WHERE id = ?`,
		a.Name, a.Login, a.OS, a.Version, a.HostKey, Now(), got.ID)
	if err != nil {
		return Agent{}, false, err
	}
	got, err = s.GetAgent(ctx, a.UserID, got.ID)
	return got, false, err
}

// GetAgent returns one of a person's agents.
func (s *Store) GetAgent(ctx context.Context, uid, id int64) (Agent, error) {
	return scanAgent(s.DB.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE user_id = ? AND id = ?`, uid, id))
}

// DeleteAgent forgets an agent.
func (s *Store) DeleteAgent(ctx context.Context, uid, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM agents WHERE user_id = ? AND id = ?`, uid, id)
	return err
}

// MachineForAgent is the machine an agent's computer is (ErrNotFound = none yet).
func (s *Store) MachineForAgent(ctx context.Context, uid, agentID int64) (Machine, error) {
	m, err := scanMachine(s.DB.QueryRowContext(ctx, `SELECT `+machineCols+` FROM machines WHERE user_id = ? AND agent_id = ? ORDER BY id LIMIT 1`, uid, agentID))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, notFound(err)
}
