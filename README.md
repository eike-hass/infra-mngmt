# infra-mngmt

Unified Claude Code config viewer and process manager for Windows + WSL2 + devcontainer setups.

Surfaces every Claude Code entity — MCP servers, skills, commands, hooks, agents, memory files, CLAUDE.md rules — across all scopes in one place: your global `~/.claude/`, every project's `.claude/`, and inside any running devcontainer. Pairs that view with a live services panel backed by [process-compose](https://github.com/F1bonacc1/process-compose) so you can see whether the services those entities depend on are actually running, and start or stop them from the same UI.

---

## Architecture

```
Windows host
├── llama.cpp, whisper, …                     ports 8080, 9090, …
└── process-compose.exe  :9999                manages Windows-native services
    config: %USERPROFILE%\.config\infra-mngmt\process-compose.yaml

WSL2                                          ← infra-mngmt runs here (native process)
├── Docker daemon
│   ├── devcontainer-A   /workspace → ~/projects/project-a
│   │   └── claude-code-config-A  volume → /home/node/.claude
│   ├── devcontainer-B   /workspace → ~/projects/project-b
│   │   └── .claude/              bind → ~/projects/project-b/.claude
│   └── …
├── socat :8080 → (windows host):8080         bridges Windows service ports into WSL2
├── process-compose      :9998                manages WSL2 services (socat, WSL-side MCPs)
│   config: ~/.config/infra-mngmt/process-compose.yaml
└── infra-mngmt          :7842
    ├── reads  ~/.claude/                     global scope
    ├── reads  each project .claude/          project scope (extra_paths in config)
    ├── reads  devcontainer .claude/          auto-discovered via devcontainer labels
    ├── talks  localhost:9998                 WSL2 process-compose
    └── talks  wsl-windows:9999              Windows process-compose (NAT-safe hostname)

Windows browser  →  http://localhost:7842     (WSL2 ports are auto-forwarded)
```

**Why WSL2 native, not inside a devcontainer?** Running in WSL2 directly gives infra-mngmt access to the Docker socket, all project directories, and `/mnt/c` for Windows paths. Devcontainers are observed, not where the observer lives.

---

## Prerequisites

- WSL2 with Docker installed (Docker Desktop or Docker Engine in WSL2)
- A devcontainer with Go 1.24+ — used to build the binary; Go does not need to be installed in WSL2 itself
- `socat` in WSL2: `sudo apt install socat`

---

## 1. Install process-compose

process-compose is the runtime supervisor. Install it on both sides.

### WSL2

```bash
VERSION=$(curl -s https://api.github.com/repos/F1bonacc1/process-compose/releases/latest \
  | grep tag_name | cut -d'"' -f4)
curl -Lo /tmp/pc.tgz \
  "https://github.com/F1bonacc1/process-compose/releases/download/${VERSION}/process-compose_linux_amd64.tar.gz"
tar -xzf /tmp/pc.tgz -C /tmp
sudo install /tmp/process-compose /usr/local/bin/process-compose
rm /tmp/pc.tgz /tmp/process-compose
```

### Windows

Run in PowerShell:

```powershell
$version = (Invoke-RestMethod https://api.github.com/repos/F1bonacc1/process-compose/releases/latest).tag_name
$url = "https://github.com/F1bonacc1/process-compose/releases/download/$version/process-compose_windows_amd64.zip"
$dest = "$env:LOCALAPPDATA\Programs\process-compose"
Invoke-WebRequest $url -OutFile "$env:TEMP\pc.zip"
Expand-Archive "$env:TEMP\pc.zip" -DestinationPath $dest -Force
Remove-Item "$env:TEMP\pc.zip"
```

Then add it to your user PATH:

```powershell
$dest = "$env:LOCALAPPDATA\Programs\process-compose"
[Environment]::SetEnvironmentVariable(
  "PATH",
  [Environment]::GetEnvironmentVariable("PATH", "User") + ";$dest",
  "User"
)
```

Open a new PowerShell window after setting PATH, then verify:

```powershell
process-compose version
```

---

## 2. Build infra-mngmt

Build inside a devcontainer (Go is already available there). The workspace directory is bind-mounted, so the binary is immediately visible from WSL2.

**In a devcontainer terminal:**

```bash
go build -o /workspace/dist/infra-mngmt ./cmd/infra-mngmt
```

If this is the first build and module dependencies aren't cached yet:

```bash
GOPROXY=direct GONOSUMDB='*' go build -o /workspace/dist/infra-mngmt ./cmd/infra-mngmt
```

**Then from a WSL2 terminal, copy the binary into your PATH:**

```bash
mkdir -p ~/.local/bin
# replace ~/projects/infra-mngmt with the actual path to your checkout
cp ~/projects/infra-mngmt/dist/infra-mngmt ~/.local/bin/
```

Verify:

```bash
infra-mngmt -h
```

---

## 3. Configure

Config lives at `~/.config/infra-mngmt/config.json`. If the file doesn't exist, defaults are used (bind `127.0.0.1:7842`, no process-compose instances, no extra paths).

On first run infra-mngmt auto-generates a bearer token at `~/.config/infra-mngmt/token` (0600). You will need it to log in at `http://localhost:7842/login`.

### Full config reference

```json
{
  "bind": "127.0.0.1:7842",
  "token_file": "~/.config/infra-mngmt/token",
  "process_compose": [
    {
      "name": "wsl",
      "endpoint": "http://localhost:9998",
      "binary": "/usr/local/bin/process-compose",
      "compose_file": "/home/youruser/.config/infra-mngmt/process-compose.yaml",
      "token_file": "/home/youruser/.config/infra-mngmt/process-compose.token"
    },
    {
      "name": "windows",
      "endpoint": "http://wsl-windows:9999",
      "binary": "/mnt/c/Users/youruser/AppData/Local/Programs/process-compose/process-compose.exe",
      "compose_file": "/mnt/c/Users/youruser/.config/infra-mngmt/process-compose.yaml",
      "token_file": "/mnt/c/Users/youruser/.config/infra-mngmt/process-compose.token"
    }
  ],
  "extra_paths": [
    "/home/youruser/projects/project-a",
    "/home/youruser/projects/project-b"
  ]
}
```

Each `process_compose` entry can carry the API token for its instance one of two ways: `token` (literal) or `token_file` (path read at startup). The file form is preferred — it keeps the secret out of `config.json` and the same path can be passed to process-compose itself via `--token-file`, so both sides read one file. Sent on the wire as the `X-PC-Token-Key` header. Leave both unset if the instance has no auth. See [SECURITY.md](SECURITY.md) for the full setup.

### Field reference

| Field | Default | Description |
|---|---|---|
| `bind` | `127.0.0.1:7842` | Listen address. Set to `0.0.0.0:7842` for LAN access. |
| `token_file` | `~/.config/infra-mngmt/token` | Path to the infra-mngmt bearer token. Set to `""` to disable auth. |
| `process_compose[].name` | required | Display name shown in the services tab. |
| `process_compose[].endpoint` | required | Base URL of the process-compose management API. Supports the `wsl-windows` hostname (see below). |
| `process_compose[].binary` | optional | Path to the process-compose binary. When set, a **▶ start** button appears in the UI if the endpoint is unreachable. |
| `process_compose[].compose_file` | optional | Path to the process-compose YAML passed to `binary` on bootstrap. |
| `process_compose[].token` | optional | API token (literal) for this process-compose instance, sent as `X-PC-Token-Key`. Takes precedence over `token_file` when both are set. |
| `process_compose[].token_file` | optional | Path to a file containing the API token. Read at startup; same path can be passed to process-compose's own `--token-file`. Preferred over `token` for keeping secrets out of config. |
| `extra_paths` | `[]` | Additional project root directories to scan for `.claude/` beyond `~` and `$CWD`. |

### WSL2 NAT networking — the `wsl-windows` hostname

With WSL2's default NAT networking the Windows host IP changes on every `wsl --shutdown`. Use `wsl-windows` as the hostname in any endpoint and infra-mngmt will substitute the real IP at startup:

```json
"endpoint": "http://wsl-windows:9999"
```

The substitution reads WSL's default-route gateway from `/proc/net/route` — the actual Windows host as the WSL guest sees it. (On older WSL setups this matched `/etc/resolv.conf`'s nameserver; on Windows 11 with Hyper-V firewall mode that nameserver is now a DNS proxy bound to WSL's loopback and isn't routable. The default-route gateway is correct in both topologies.) The config file never needs to change between restarts.

If you switch to mirrored networking (`networkingMode=mirrored` in `%USERPROFILE%\.wslconfig`), drop the sentinel and use `http://localhost:9999` directly.

---

## 4. Process-compose config files

Process-compose YAMLs are **infrastructure config** — they manage services like llama.cpp, socat bridges, and MCP server processes. They have nothing to do with Claude Code configuration and do not belong in `.claude/` directories. Keep them in `~/.config/infra-mngmt/` or any path that makes sense for your setup.

### WSL2 — `~/.config/infra-mngmt/process-compose.yaml`

Manages socat bridges that forward Windows service ports into WSL2, plus any WSL-native MCP servers.

```yaml
processes:
  socat-llama:
    command: >
      socat TCP-LISTEN:8080,fork,reuseaddr
        TCP:$(awk '/nameserver/{print $2; exit}' /etc/resolv.conf):8080
    availability:
      restart: always

  socat-whisper:
    command: >
      socat TCP-LISTEN:9090,fork,reuseaddr
        TCP:$(awk '/nameserver/{print $2; exit}' /etc/resolv.conf):9090
    availability:
      restart: always
```

Start (run once; systemd handles it on subsequent boots — see §6):

```bash
process-compose up -f ~/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --tui=false &
```

`--address 127.0.0.1` restricts the API to loopback — infra-mngmt talks to it locally so this is safe and recommended. See [SECURITY.md](SECURITY.md) for adding bearer token auth.

### Windows — `%USERPROFILE%\.config\infra-mngmt\process-compose.yaml`

Manages Windows-native GPU/CPU services:

```yaml
processes:
  llama-cpp:
    command: C:\tools\llama\server.exe -m models\llama-3-8b.gguf --port 8080 -ngl 99
    availability:
      restart: on_failure

  whisper:
    command: C:\tools\whisper\server.exe --port 9090
    availability:
      restart: on_failure
```

Start (PowerShell; add to Windows startup or Task Scheduler):

```powershell
process-compose up -f "$env:USERPROFILE\.config\infra-mngmt\process-compose.yaml" --port 9999 --tui=false
```

The Windows instance must bind to `0.0.0.0` (the default) so WSL2 can reach it over NAT. See [SECURITY.md](SECURITY.md) for firewall rules to restrict which source IPs can reach port 9999.

### Naming convention for MCP status badges

infra-mngmt matches MCP server entries in `settings.json` to process-compose processes by name substring (case-insensitive). Name your processes after the MCP servers they back:

```yaml
# MCP server name in settings.json: "llama-cpp"
# process name below:                "llama-cpp"  ← matched; dot turns green when running
processes:
  llama-cpp:
    command: …
```

An **orange dot** on an MCP card means compose is online but no process name matches — the service is either not configured in process-compose or named differently.

---

## 5. Devcontainer auto-discovery

No labels are required for standard VS Code devcontainers. infra-mngmt discovers them via the `devcontainer.local_folder` label that VS Code and the devcontainer CLI set automatically on every container they create.

From that label it finds the project root, then inspects the container's mounts:
- **Named volume** at `*/.claude` → read via throwaway sidecar container
- **Bind mount** from a host path at `*/.claude` → read directly from the WSL2 filesystem

Your existing `devcontainer.json` works as-is — no extra configuration needed.

For containers **not** created by VS Code (bare `docker run`, CI containers, etc.), add one label to opt in:

```bash
docker run --label claude.managed=true \
           --label claude.project.root=/home/user/myproject \
           …
```

`claude.project.root` is optional but sets the project scope shown in the UI. Without it, scope is inferred from bind-mount paths where possible, or shown as global.

---

## 6. Run as a systemd service (WSL2)

Enable systemd in WSL2 if you haven't already — add to `/etc/wsl.conf`:

```ini
[boot]
systemd=true
```

Restart WSL2 (`wsl --shutdown` from PowerShell), then create the two unit files:

**`~/.config/systemd/user/infra-mngmt.service`**:

```ini
[Unit]
Description=infra-mngmt
After=network.target

[Service]
ExecStart=%h/.local/bin/infra-mngmt
Restart=on-failure
Environment=HOME=%h

[Install]
WantedBy=default.target
```

**`~/.config/systemd/user/process-compose.service`**:

```ini
[Unit]
Description=process-compose (WSL)
After=network.target

[Service]
ExecStart=/usr/local/bin/process-compose up \
  -f %h/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --token-file %h/.config/infra-mngmt/process-compose.token \
  --tui=false
Restart=on-failure
Environment=HOME=%h

[Install]
WantedBy=default.target
```

The `--token-file` flag is process-compose's native auth mechanism — it reads the file directly, no shell wrapper needed. Generate the token once with `head -c 32 /dev/urandom | xxd -p -c 64 > ~/.config/infra-mngmt/process-compose.token && chmod 600 ~/.config/infra-mngmt/process-compose.token`. Drop the flag if you don't want auth (not recommended; see [SECURITY.md](SECURITY.md)).

Enable and start both:

```bash
systemctl --user enable --now infra-mngmt
systemctl --user enable --now process-compose
```

> **WSL2 gotcha:** if `systemctl --user` returns `Failed to connect to bus`, add this to `~/.bashrc`:
> ```bash
> export DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$(id -u)/bus
> ```
> This happens when WSL2 is started headlessly (e.g. by Task Scheduler) and the bus address isn't propagated to child shells, even though the user systemd instance is running.

---

## 7. Start everything on Windows boot

Three things need to autostart: the Windows process-compose instance, the WSL2 process-compose instance, and infra-mngmt itself. The cleanest approach is a single Task Scheduler entry that fires them all via a PowerShell script.

### Create the startup script

Save as `%USERPROFILE%\.config\infra-mngmt\autostart.ps1`:

```powershell
$pcExe     = "$env:LOCALAPPDATA\Programs\process-compose\process-compose.exe"
$cfg       = "$env:USERPROFILE\.config\infra-mngmt\process-compose.yaml"
$tokenFile = "$env:USERPROFILE\.config\infra-mngmt\process-compose.token"
$logFile   = "$env:USERPROFILE\.config\infra-mngmt\process-compose.log"

$pcArgs = @(
    '-f', $cfg,
    '--tui=false',
    '--port', '9999',
    '--address', '0.0.0.0',
    '--token-file', $tokenFile,
    '--log-file', $logFile,
    '--log-no-color'
)

# Detached: PowerShell exits immediately so Task Scheduler sees the action
# as completed (and won't kill it at -ExecutionTimeLimit). process-compose
# keeps running and writes its own log via --log-file.
Start-Process -FilePath $pcExe -ArgumentList $pcArgs -WindowStyle Hidden

# Start WSL2 services + infra-mngmt inside WSL2.
# wsl -e runs a single command; bash -lc sources the user profile (needed for PATH).
Start-Process -NoNewWindow -FilePath "wsl" -ArgumentList `
  "-e", "bash", "-lc",
  "systemctl --user start process-compose infra-mngmt"
```

Three Windows-specific points worth knowing:

- **`--address 0.0.0.0`** is required because WSL2 reaches Windows over the virtual NIC, not loopback. Default `localhost` would make the listener invisible to WSL.
- **`--token-file`** matches what's in `config.json`'s `token_file` for the `windows` entry — both sides read the same file.
- On Windows 11 with the **Hyper-V firewall** (you'll see the adapter name `vEthernet (WSL (Hyper-V firewall))`), regular `New-NetFirewallRule` rules don't apply to WSL traffic. You need `New-NetFirewallHyperVRule`. See [SECURITY.md](SECURITY.md#process-compose-hardening) for the exact command.

> If you skipped systemd, replace the WSL2 `systemctl` line with explicit background commands:
> ```
> "process-compose up -f ~/.config/infra-mngmt/process-compose.yaml --port 9998 --address 127.0.0.1 --tui=false & infra-mngmt &"
> ```

### Register with Task Scheduler

Run once in PowerShell (elevated not required):

```powershell
$script = "$env:USERPROFILE\.config\infra-mngmt\autostart.ps1"

$action  = New-ScheduledTaskAction `
  -Execute "powershell.exe" `
  -Argument "-NonInteractive -WindowStyle Hidden -File `"$script`""

$trigger = New-ScheduledTaskTrigger -AtLogOn

$settings = New-ScheduledTaskSettingsSet `
  -ExecutionTimeLimit (New-TimeSpan -Minutes 2) `
  -RestartCount 2 `
  -RestartInterval (New-TimeSpan -Minutes 1)

Register-ScheduledTask `
  -TaskName "infra-mngmt autostart" `
  -Action   $action `
  -Trigger  $trigger `
  -Settings $settings `
  -RunLevel Highest `
  -Force
```

This triggers at logon for the current user, runs hidden, and retries twice if WSL2 hasn't initialised yet.

To run it immediately without logging out:

```powershell
Start-ScheduledTask -TaskName "infra-mngmt autostart"
```

To remove it:

```powershell
Unregister-ScheduledTask -TaskName "infra-mngmt autostart" -Confirm:$false
```

### Verify everything started

```powershell
# Windows process-compose health
Invoke-RestMethod http://localhost:9999/live

# WSL2: infra-mngmt and process-compose
wsl -e bash -lc "systemctl --user status infra-mngmt process-compose --no-pager"
```

Then open **http://localhost:7842** — both services panels should show green.

---

## 8. Access

Open **http://localhost:7842** in your Windows browser. WSL2 automatically forwards the port.

On first access you will be prompted for the bearer token. Read it from WSL2:

```bash
cat ~/.config/infra-mngmt/token
```

In VS Code remote sessions the port appears in the **Ports** panel and can be opened from there.

---

## Security

See [SECURITY.md](SECURITY.md) for the full threat model and hardening recommendations, including:

- How infra-mngmt authentication works (auto-generated token, session cookies)
- Restricting the WSL2 process-compose API to loopback (`--address 127.0.0.1`)
- Firewall rules for the Windows process-compose instance
- Enabling bearer token auth on process-compose itself

---

## Development

All commands run inside the devcontainer:

```bash
go run ./cmd/infra-mngmt           # run without building
go build -o dist/infra-mngmt ./cmd/infra-mngmt  # build binary
go test ./...                      # run tests
go vet ./...                       # vet
```

The devcontainer firewall blocks the Go module proxy. Use `GOPROXY=direct GONOSUMDB='*'` when adding dependencies or running `go mod tidy`.
