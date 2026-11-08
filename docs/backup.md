# Backup

One SQLite file: `DATA_DIR/pocket-remote.db` (WAL mode). Take a consistent copy with `pocket-remote backup` (writes `DATA_DIR/backups/pocket-remote-<time>.db` with `VACUUM INTO`) or Settings → Backup & data → Download database (admin). Back up that copy, not the live file. `DATA_DIR/core.token*` is the Pocket Core registration.

`DATA_DIR/remote.key` seals the saved machine passwords and keys: **keep it with the database backup**. Without it the computers stay, but each password has to be typed again.

Restore: stop the app, replace `pocket-remote.db` (remove `-wal`/`-shm` files) and `remote.key`, start.

Every person can also export their own data as JSON (`/api/v1/export.json`, re-importable) or the session log as CSV. Passwords are never in an export.
