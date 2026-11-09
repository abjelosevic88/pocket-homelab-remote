-- A computer has ways in: its desktop (Screen Sharing / Remote Desktop, the
-- protocol column) and its terminal (SSH on ssh_port, the same sign-in as files).
ALTER TABLE machines ADD COLUMN desktop_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE machines ADD COLUMN terminal_enabled INTEGER NOT NULL DEFAULT 0;

-- Terminal-only machines: the terminal moves to ssh_port, the desktop is off.
UPDATE machines SET
  desktop_enabled = 0,
  terminal_enabled = 1,
  ssh_port = CASE WHEN port > 0 THEN port ELSE ssh_port END,
  port = 0,
  protocol = CASE kind WHEN 'mac' THEN 'vnc' WHEN 'other' THEN 'vnc' ELSE 'rdp' END
WHERE protocol = 'ssh';

-- A Mac or Linux machine with files on already has Remote Login: offer its terminal.
UPDATE machines SET terminal_enabled = 1 WHERE files_enabled = 1 AND kind IN ('mac', 'linux');
