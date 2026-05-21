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

To disable authentication (local-only, trusted environment), remove `token_file` from `config.yaml`. **Do not do this if the service is reachable from other machines.** A safer middle ground is `trusted_networks:` — list CIDRs whose source IPs bypass the login flow (e.g. `127.0.0.0/8`, `172.17.0.0/16` for the Docker bridge) without disabling auth wholesale.

### Bind address

The default bind address is `127.0.0.1:7842` (loopback only). A startup warning is logged when binding to any other address.

If you need LAN access, bind to `0.0.0.0:7842` **and** keep authentication enabled. Use a reverse proxy with TLS if the service is accessible outside a trusted LAN segment.

### Input validation

Process and instance names in API calls are validated against `^[a-zA-Z0-9_\-]{1,128}$` before being forwarded to process-compose. This prevents path traversal in the process-compose REST API URL path.

### Template escaping

All HTML templates use Go's `html/template` package, which auto-escapes all values. `template.HTML` is used only for compile-time SVG icon constants (e.g. `chevSVG`, `kindIcon`, `toolIcon`) and `template.CSS` only via the `safeCSS` FuncMap helper for self-constructed OKLCH color strings (see [docs/frontend-architecture.md §4.3](docs/frontend-architecture.md)) — never for user-controlled data. Auto-escape enforcement is mechanically verified by `TestGuideline_TemplateHTMLOnlyForIcons` in [internal/web/frontend_guidelines_test.go](internal/web/frontend_guidelines_test.go), which fails if `template.HTML(x)` ever wraps an identifier that doesn't end in `SVG` / `Icon`.

### CSRF

Login form uses `SameSite=Strict` cookies. All state-mutating routes (`/process/start`, `/process/stop`, `/process/restart`, `/compose/start`, `/compose/reload`, `/bridge/apply`, `/bridge/reset`, `/bridges/apply`, `/bridges/refresh`, `/decl-container/start`, `/decl-container/stop`, `/containers/refresh`, `/api/container/start`, `/api/container/stop`, `/api/refresh`, `/api/entity`) are `POST`-only and served via HTMX, which also sends `HX-Request: true` headers (not relied upon for security, but layered).

### Config file permissions

`config.yaml` (or legacy `config.json`) and the token file are written with `0600` permissions. Ensure the directory (`~/.config/infra-mngmt/`) is not world-readable.

---

## process-compose hardening

process-compose is the actual process supervisor. Its API exposes start/stop/restart and log access for every managed process. **Compromise of process-compose is equivalent to shell access for any managed service user.**

### Default bind address

`process-compose --address` defaults to `localhost`. Consequences:

- The **WSL2-side** instance (`:9998`) is loopback-only by default — not reachable from the Windows host or the LAN unless `--address` is overridden.
- The **Windows-side** instance (`:9999`) must be started with `--address 0.0.0.0` for infra-mngmt in WSL2 to reach it (the WSL2 guest cannot dial Windows-side `127.0.0.1` over NAT). That bind, combined with Windows Firewall, is the actual exposure surface.

### Recommended mitigations

#### WSL2-side process-compose

The default loopback bind is correct here. Keep it explicit in the systemd unit so a future config change can't silently widen exposure:

```ini
ExecStart=/usr/local/bin/process-compose up \
  -f %h/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --token-file %h/.config/infra-mngmt/process-compose.token \
  --tui=false
```

#### Windows-side process-compose

Bind to `0.0.0.0` (required for WSL2 reachability) and use Windows Firewall to restrict source. Default Windows inbound policy is deny, so a single scoped Allow rule is sufficient — no Block rule needed (and a Block rule would in fact override any Allow unless `-OverrideBlockRules $true` is set).

Scope by interface (preferred — robust across WSL2 NAT subnet changes):

```powershell
New-NetFirewallRule `
  -DisplayName "process-compose (WSL only)" `
  -Direction Inbound -Protocol TCP -LocalPort 9999 -Action Allow `
  -InterfaceAlias 'vEthernet (WSL*)' `
  -Profile Any
```

Or scope by source subnet, if you prefer:

```powershell
# Adjust the subnet to match the source IP Windows sees from your WSL2 NAT —
# inspect with: Get-NetIPAddress -InterfaceAlias 'vEthernet (WSL*)'
New-NetFirewallRule `
  -DisplayName "process-compose (WSL only)" `
  -Direction Inbound -Protocol TCP -LocalPort 9999 -Action Allow `
  -RemoteAddress 172.16.0.0/12 `
  -Profile Any
```

#### Enable process-compose authentication

process-compose accepts an API token via the `--token-file <path>` flag (or the `PC_API_TOKEN` / `PC_API_TOKEN_PATH` env vars). Clients must send the token in the `X-PC-Token-Key` HTTP header. The token must be at least 20 characters; the snippets below produce 64 hex chars.

**Generate a token — WSL2:**

```bash
head -c 32 /dev/urandom | xxd -p -c 64 > ~/.config/infra-mngmt/process-compose.token
chmod 600 ~/.config/infra-mngmt/process-compose.token
```

**Generate a token — Windows (PowerShell):**

```powershell
$bytes = [System.Security.Cryptography.RandomNumberGenerator]::GetBytes(32)
$token = ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLower()
$dir   = "$env:USERPROFILE\.config\infra-mngmt"
New-Item -ItemType Directory -Force $dir | Out-Null
$token | Set-Content "$dir\process-compose.token" -NoNewline
# restrict file to current user only (remove inherited ACEs, grant read to owner)
icacls "$dir\process-compose.token" /inheritance:r /grant:r "${env:USERNAME}:(R)" | Out-Null
```

**Start process-compose with the token:**

WSL2:
```bash
process-compose up \
  -f ~/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --token-file ~/.config/infra-mngmt/process-compose.token \
  --tui=false
```

Windows (PowerShell):
```powershell
process-compose up `
  -f "$env:USERPROFILE\.config\infra-mngmt\process-compose.yaml" `
  --port 9999 `
  --address 0.0.0.0 `
  --token-file "$env:USERPROFILE\.config\infra-mngmt\process-compose.token" `
  --tui=false
```

**Point infra-mngmt at the same files** in `config.yaml` — the per-instance `token_file` field makes both sides read the same secret without embedding it in config:

```yaml
process_compose:
  - name: wsl
    endpoint: http://localhost:9998
    token_file: /home/<user>/.config/infra-mngmt/process-compose.token
  - name: windows
    endpoint: http://wsl-windows:9999
    token_file: /c/Users/<user>/.config/infra-mngmt/process-compose.token
```

(A literal `token: "<value>"` is also accepted and takes precedence when set, but the file-based form keeps secrets out of the config and lets infra-mngmt's bootstrap pass `--token-file` straight through.)

**Systemd integration (WSL2):** the unit shown above already passes `--token-file %h/...` directly. No shell wrapper or token interpolation is needed because process-compose reads the file itself.

**Task Scheduler integration (Windows):** the autostart script invoked by Task Scheduler should pass `--token-file` to `process-compose.exe` (see README §7). The token never appears on the command line or in the task definition.

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
