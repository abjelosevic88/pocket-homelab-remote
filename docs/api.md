# API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie, a personal token (`Authorization: Bearer prm_…`, Settings → API tokens), a Pocket Core token, or a Pocket Core link token (`pcx_…`, only the routes of its scope). Everything is per person: other people's ids answer 404. Passwords and keys are write-only: they are never returned.

## Computers

| | |
|---|---|
| `GET /machines` | Your computers with `status` (`online`/`offline`/`unknown`, latency, why), `tailnet` (what Tailscale says), `live` (sessions open now), `has_password`, `has_key` |
| `POST /machines` | Add one (below) |
| `GET/PUT/DELETE /machines/{id}` | One computer; PUT takes any subset of the fields |
| `POST /machines/{id}/check` | Check it now |
| `POST /machines/{id}/wake` | Send the Wake-on-LAN signal; it is then looked for every 5 s for two minutes |
| `POST /machines/{id}/trust-again` | Forget its SSH identity (after a reinstall) |
| `GET /tailnet` | Devices on the box's tailnet (admins) |

```json
{"name": "MacBook Pro", "kind": "mac", "host": "macbook-pro", "protocol": "vnc", "port": 0,
 "username": "me", "password": "…", "private_key": "", "domain": "",
 "mac_address": "a4:83:e7:12:9c:01", "wol_broadcast": "", "files_enabled": true, "ssh_port": 22,
 "options": {"color_depth": 24, "read_only": false, "rdp_security": "any", "ignore_cert": true, "resize": true, "audio": false,
             "font_size": 12, "color_scheme": "gray-black", "swap_red_blue": false, "remote_cursor": false},
 "notes": "", "favorite": true}
```

`kind` is `mac`, `windows`, `linux` or `other`; `protocol` is `vnc` (Screen Sharing), `rdp` (Remote Desktop) or `ssh` (a terminal). `port: 0` = the usual one. `clear_password: true` / `clear_key: true` remove the saved ones.

## Sessions

| | |
|---|---|
| `POST /machines/{id}/connect` `{"pin": "…"}` | A one-time ticket for the tunnel (60 s). `{"need_pin": true}` when your connect PIN is asked |
| `GET /tunnel?ticket=…&width&height&dpi&timezone` | The WebSocket tunnel (Guacamole protocol, subprotocol `guacamole`); needs the normal sign-in too |
| `GET /sessions?machine&limit&offset` | The log, newest first: `{items, total, live}` |
| `GET /sessions/live` | Sessions open right now |
| `POST /sessions/{id}/end` | End one of your open sessions, from any device |
| `GET /overview` | Online count, this week and 30 days, recent sessions, what is open, whether guacd answers |

## Files

| | |
|---|---|
| `GET /machines/{id}/files?path=` | A folder over SFTP (`""` = the home folder): `{path, parent, home, entries}` |
| `GET /machines/{id}/files/download?path=&inline=1` | One file |
| `POST /machines/{id}/files/upload?path=` | Multipart field `file` (several allowed); an existing name gets " (2)" |
| `POST /machines/{id}/files/save` `{"path"}` | Copy into your Pocket Files (`<folder>/<machine>/`) |
| `GET /transfers` | Files moved, newest first |

## Pocket Core link scopes

| Scope | Routes |
|---|---|
| `remote:read` | `GET /overview`, `GET /machines`, `GET /machines/{id}`, `GET /sessions`, `POST /machines/{id}/check` |
| `remote:wake` | `POST /machines/{id}/wake` |

No link reaches a session, the tunnel, files or settings.

## Events

`remote.session_started`, `remote.session_ended`, `remote.machine_woken`, `remote.file_saved` (each with `user_email`).

## Account, settings, data

`/auth/*`, `/settings` (`pin` + `pin_current` to set or change the connect PIN, `pin_clear`), `/users`, `/tokens`, `/backup` (admins), `/export.json`, `/export.csv` (sessions), `/import` — as in every Pocket app.
