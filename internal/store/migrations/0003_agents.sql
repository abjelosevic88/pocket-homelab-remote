-- Pocket Remote Agent: a small app on a computer outside the tailnet that
-- connects out through Pocket Core's relay. One row per paired agent (its
-- Core remote device); its computer is a machine with agent_id set.
CREATE TABLE agents (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  core_device_id INTEGER NOT NULL,
  name           TEXT NOT NULL,
  login          TEXT NOT NULL DEFAULT '',
  os             TEXT NOT NULL DEFAULT '',
  version        TEXT NOT NULL DEFAULT '',
  host_key       TEXT NOT NULL DEFAULT '',
  last_seen_at   TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, core_device_id)
);
ALTER TABLE machines ADD COLUMN agent_id INTEGER NOT NULL DEFAULT 0;
