# Configuration

Environment variables; every one has a default. Personal settings (connect PIN, touch mode, notifications, connections) live in the UI per person — the `NOTIFY_*` values below are copied once into the first admin's settings.

| Variable | Default | What |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL (links in pushes, the OIDC callback) |
| `DATA_DIR` | `./data` | Database, machine key, Core token, backups |
| `DB_PATH` | `$DATA_DIR/pocket-remote.db` | SQLite file |
| `KEY_FILE` | `$DATA_DIR/remote.key` | Seals the machine passwords and keys (AES-256-GCM). Made on first start; **back it up with the database**, without it the saved passwords cannot be read |
| `GUACD_ADDR` | `127.0.0.1:4822` | Apache Guacamole's guacd, which does the VNC, RDP and SSH work. Keep it on localhost or a private network: guacd has no sign-in of its own |
| `TAILSCALE_SOCKET` | `/var/run/tailscale/tailscaled.sock` | Read-only: "Add from your tailnet" (admins) and what Tailscale says about a machine. Missing = those parts are hidden |
| `STATUS_EVERY` | `1m` | How often every machine's remote port is checked |
| `UPLOAD_MAX_BYTES` | `4294967296` | Largest file sent to a machine (4 GB) |
| `MAX_BODY_BYTES` | `67108864` | Largest JSON request, e.g. an export being imported (64 MB) |
| `TZ` | – | Time zone (sent to the remote desktop too) |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | – | Seed the first admin while the database is empty |
| `SESSION_TTL`, `SECURE_COOKIES` | `720h`, `false` | Set `SECURE_COOKIES=true` behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL`, `NOTIFY_FORMAT` | – | Apprise (`…/notify/key`), ntfy topic URL or JSON webhook; format `apprise`/`ntfy`/`json` |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY`, `METRICS_ENABLED`, `LOG_LEVEL`, `LOG_FORMAT` | `true`, `true`, `info`, `text` | |
| `DEMO_DATA` | `false` | Seed five demo computers (on `.demo` addresses, never dialled) and three months of sessions into the owner account |

Commands: `pocket-remote backup [-o FILE]`, `pocket-remote reset-password [-email] [-password]`, `pocket-remote version`.

## Networking

Run Pocket Remote with host networking (see `docker-compose.yml`): Wake-on-LAN packets are broadcast on the box's own network, your computers are reached over its tailnet, and guacd listens on `127.0.0.1` only. In a bridge network wake signals stay inside Docker.

## Getting a computer ready

- **Mac**: System Settings → General → Sharing → *Screen Sharing* on; sign in with the Mac account. For files, *Remote Login* on as well.
- **Windows** (Pro / Enterprise): Settings → System → *Remote Desktop* on. For files, *OpenSSH Server* (Settings → Apps → Optional features).
- **Linux**: an SSH server gives a terminal and files; a VNC server or xrdp gives a desktop.
