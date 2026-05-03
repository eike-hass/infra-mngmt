# Security

## Threat model

infra-mngmt is a local management dashboard. The primary attack surface is:

1. **Network access** — the HTTP server exposes process control (start/stop/restart) and config file read access.
2. **process-compose APIs** — infra-mngmt proxies to process-compose instances that run on Windows and WSL2.
3. **Docker socket** — infra-mngmt reads container metadata via the Docker socket at startup.
4. **Config/token files** — tokens for both infra-mngmt and process-compose are stored on disk.

---

## infra-mngmt hardening

### Authentication

A bearer token is generated automatically on first run and stored at `~/.config/infra-mngmt/token` with `0600` permissions. The file is created with a 64-hex-character random token (32 bytes from `crypto/rand`).

All routes except `/login` and `/logout` require a session cookie issued after presenting the correct token. Token comparison uses `crypto/subtle.ConstantTimeCompare` to prevent timing attacks.

Session IDs are 16 random bytes encoded as hex. Sessions are stored only in memory (lost on restart, requiring re-login).

To disable authentication (local-only, trusted environment), remove `token_file` from `config.json`. **Do not do this if the service is reachable from other machines.**

### Bind address

The default bind address is `127.0.0.1:7842` (loopback only). A startup warning is logged when binding to any other address.

If you need LAN access, bind to `0.0.0.0:7842` **and** keep authentication enabled. Use a reverse proxy with TLS if the service is accessible outside a trusted LAN segment.

### Input validation

Process and instance names in API calls are validated against `^[a-zA-Z0-9_\-]{1,128}$` before being forwarded to process-compose. This prevents path traversal in the process-compose REST API URL path.

### Template escaping

All HTML templates use Go's `html/template` package, which auto-escapes all values. No `template.HTML` casting is used, so user-controlled content (entity names, MCP attributes, log lines) cannot inject script.

### CSRF

Login form uses `SameSite=Strict` cookies. All state-mutating routes (`/process/start`, `/process/stop`, `/process/restart`, `/compose/start`) are `POST`-only and served via HTMX, which also sends `HX-Request: true` headers (not relied upon for security, but layered).

### Config file permissions

`config.json` and the token file are written with `0600` permissions. Ensure the directory (`~/.config/infra-mngmt/`) is not world-readable.

---

## process-compose hardening

process-compose is the actual process supervisor. Its API exposes start/stop/restart and log access for every managed process. **Compromise of process-compose is equivalent to shell access for any managed service user.**

### Default bind address (risk)

By default, `process-compose` binds to `0.0.0.0` on its port. This means:

- The **WSL2-side** instance (`:9998`) is reachable from the Windows host and potentially from the LAN.
- The **Windows-side** instance (`:9999`) is reachable from the LAN if Windows Firewall permits it.

### Recommended mitigations

#### WSL2-side process-compose

Bind to loopback only — infra-mngmt connects to it locally:

```bash
process-compose up -f ~/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --tui=false
```

Update your systemd unit:

```ini
ExecStart=/usr/local/bin/process-compose up \
  -f %h/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --tui=false
```

#### Windows-side process-compose

Windows process-compose must bind to `0.0.0.0` so WSL2 can reach it via NAT (the WSL2 guest cannot connect to `127.0.0.1` on the Windows host). Use Windows Firewall to restrict which source IPs can reach that port:

```powershell
# Block all inbound connections to port 9999 except from the WSL2 subnet.
# Replace 172.16.0.0/12 with the exact subnet shown by `wsl hostname -I` if needed.
New-NetFirewallRule `
  -DisplayName "process-compose WSL2 only" `
  -Direction Inbound `
  -Protocol TCP `
  -LocalPort 9999 `
  -RemoteAddress 172.16.0.0/12 `
  -Action Allow

New-NetFirewallRule `
  -DisplayName "process-compose block all" `
  -Direction Inbound `
  -Protocol TCP `
  -LocalPort 9999 `
  -Action Block
```

Firewall rules are evaluated in priority order — the more-specific Allow rule above the Block rule will permit only WSL2 traffic.

#### Enable process-compose authentication

process-compose supports bearer token authentication via `--api-token`.

**Generate a token — WSL2:**

```bash
head -c 32 /dev/urandom | xxd -p -c 64 > ~/.config/infra-mngmt/pc-token
chmod 600 ~/.config/infra-mngmt/pc-token
```

**Generate a token — Windows (PowerShell):**

```powershell
$bytes = [System.Security.Cryptography.RandomNumberGenerator]::GetBytes(32)
$token = ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLower()
$dir   = "$env:USERPROFILE\.config\infra-mngmt"
New-Item -ItemType Directory -Force $dir | Out-Null
$token | Set-Content "$dir\pc-token" -NoNewline
# restrict file to current user only (remove inherited ACEs, grant read to owner)
icacls "$dir\pc-token" /inheritance:r /grant:r "${env:USERNAME}:(R)" | Out-Null
```

**Start process-compose with the token:**

WSL2:
```bash
process-compose up \
  -f ~/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --api-token "$(cat ~/.config/infra-mngmt/pc-token)" \
  --tui=false
```

Windows (PowerShell):
```powershell
$token = (Get-Content "$env:USERPROFILE\.config\infra-mngmt\pc-token" -Raw).Trim()
process-compose up `
  -f "$env:USERPROFILE\.config\infra-mngmt\process-compose.yaml" `
  --port 9999 `
  --api-token $token `
  --tui=false
```

**Add the matching token to infra-mngmt's `config.json`** so it can authenticate against each instance:

```json
{
  "process_compose": [
    {
      "name": "wsl",
      "endpoint": "http://localhost:9998",
      "token": "<contents of ~/.config/infra-mngmt/pc-token>"
    },
    {
      "name": "windows",
      "endpoint": "http://wsl-windows:9999",
      "token": "<contents of %USERPROFILE%\\.config\\infra-mngmt\\pc-token>"
    }
  ]
}
```

**Systemd integration (WSL2):** load the token from the file at service start to avoid hardcoding it in the unit:

```ini
[Service]
ExecStart=/bin/sh -c 'exec /usr/local/bin/process-compose up \
  -f ${HOME}/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --api-token "$(cat ${HOME}/.config/infra-mngmt/pc-token)" \
  --tui=false'
```

**Task Scheduler integration (Windows):** read the token inside the autostart script (see README §7) rather than embedding it in the task definition.

---

## Docker socket access

infra-mngmt opens the Docker socket (`/var/run/docker.sock`) at startup to discover devcontainer sources. Docker socket access is equivalent to root on the host. No mitigation is applied here — this is an inherent property of the Docker API. infra-mngmt only reads container metadata (no container exec, no image pull, no volume write outside the sidecar pattern).

If this is a concern, run infra-mngmt without Docker socket access; it will log a warning and skip container source discovery, but all host filesystem sources remain functional.

---

## Summary checklist

| Control | Status |
|---|---|
| Auth token auto-generated with `crypto/rand` | ✅ |
| Token stored `0600` | ✅ |
| Constant-time token comparison | ✅ |
| `SameSite=Strict` session cookie | ✅ |
| `HttpOnly` session cookie | ✅ |
| All HTML auto-escaped via `html/template` | ✅ |
| Process name input validation | ✅ |
| Default bind to loopback | ✅ |
| Warning on non-loopback bind | ✅ |
| WSL2 process-compose bound to loopback | manual — see above |
| Windows process-compose firewalled | manual — see above |
| process-compose token auth | manual — see above |
