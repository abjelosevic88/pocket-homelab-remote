CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);


-- A computer a person reaches from here: a Mac (Screen Sharing, VNC), a
-- Windows PC (Remote Desktop, RDP) or a Linux machine (VNC or a terminal over
-- SSH). The password and private key are sealed with the box key
-- (DATA_DIR/remote.key) and never leave the server.
CREATE TABLE machines (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  kind            TEXT NOT NULL DEFAULT 'other',   -- mac | windows | linux | other
  host            TEXT NOT NULL,                   -- tailnet name or address
  protocol        TEXT NOT NULL DEFAULT 'vnc',     -- vnc | rdp | ssh
  port            INTEGER NOT NULL DEFAULT 0,      -- 0 = the protocol's usual port
  username        TEXT NOT NULL DEFAULT '',
  secret          TEXT NOT NULL DEFAULT '',        -- sealed password
  private_key     TEXT NOT NULL DEFAULT '',        -- sealed SSH key (terminal and files)
  domain          TEXT NOT NULL DEFAULT '',        -- Windows domain
  mac_address     TEXT NOT NULL DEFAULT '',        -- for Wake-on-LAN
  wol_broadcast   TEXT NOT NULL DEFAULT '',        -- "" = 255.255.255.255
  files_enabled   INTEGER NOT NULL DEFAULT 0,      -- browse its files over SFTP
  ssh_port        INTEGER NOT NULL DEFAULT 22,
  host_key        TEXT NOT NULL DEFAULT '',        -- SSH host key seen first (trust on first use)
  options         TEXT NOT NULL DEFAULT '{}',      -- JSON: colour depth, read-only, RDP security…
  notes           TEXT NOT NULL DEFAULT '',
  favorite        INTEGER NOT NULL DEFAULT 0,
  sort            INTEGER NOT NULL DEFAULT 0,
  tailnet_id      TEXT NOT NULL DEFAULT '',        -- Tailscale node, when added from the tailnet
  last_used_at    TEXT NOT NULL DEFAULT '',
  last_online_at  TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX machines_user ON machines(user_id, sort, name);

-- Every remote session: who, which machine, from where, how long.
CREATE TABLE remote_sessions (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  machine_id   INTEGER REFERENCES machines(id) ON DELETE SET NULL,
  machine_name TEXT NOT NULL DEFAULT '',
  protocol     TEXT NOT NULL DEFAULT '',
  started_at   TEXT NOT NULL,
  ended_at     TEXT NOT NULL DEFAULT '',
  duration_s   INTEGER NOT NULL DEFAULT 0,
  device       TEXT NOT NULL DEFAULT '',            -- "iPhone · Safari"
  ip           TEXT NOT NULL DEFAULT '',
  end_reason   TEXT NOT NULL DEFAULT '',            -- closed | lost | error: …
  bytes_in     INTEGER NOT NULL DEFAULT 0,
  bytes_out    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX remote_sessions_user ON remote_sessions(user_id, started_at);

-- Files copied to or from a machine.
CREATE TABLE transfers (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  machine_id  INTEGER REFERENCES machines(id) ON DELETE SET NULL,
  machine_name TEXT NOT NULL DEFAULT '',
  direction   TEXT NOT NULL,                        -- download | upload | to_files
  path        TEXT NOT NULL DEFAULT '',
  name        TEXT NOT NULL DEFAULT '',
  size        INTEGER NOT NULL DEFAULT 0,
  status      TEXT NOT NULL DEFAULT 'done',         -- done | failed
  error       TEXT NOT NULL DEFAULT '',
  target      TEXT NOT NULL DEFAULT '',             -- where it went (a Pocket Files path)
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX transfers_user ON transfers(user_id, created_at);
