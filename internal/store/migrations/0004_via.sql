-- A computer reached through another one's SSH (a jump host): via_id is
-- that computer's machine id, 0 = reached directly.
ALTER TABLE machines ADD COLUMN via_id INTEGER NOT NULL DEFAULT 0;
