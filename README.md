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
make build
```

`make build` runs `go build` with `-ldflags "-X main.buildEpoch=$(date +%s)"` so the resulting binary reports its compile timestamp via `infra-mngmt version` and a startup log line — handy for confirming a deploy actually picked up new code.

The devcontainer firewall already allowlists the Go module proxy (`proxy.golang.org`, `sum.golang.org`, `dl.google.com`); standard `go mod tidy` and `make build` work without env overrides.

> The dev container is Compose-based (`.devcontainer/docker-compose.yml` is the portable base). To add machine-specific bind mounts — your Windows-side config, sibling project checkouts — edit `.devcontainer/docker-compose.override.yml` after marking it skip-worktree so your edits stay local; see that file's header for the exact steps.
>
> **Dev container volumes (migration note).** Renaming a named volume is a *data migration*, not a config edit: Compose mounts a different volume and the old data is **orphaned, not moved**. Compose also project-prefixes named volumes unless pinned with `name:`. If a mount rename ever leaves `~/.claude` or shell history empty, the data is safe in the old volume — copy it across (container stopped):
>
> ```bash
> # find the orphan (grep transcripts for repo-unique symbols; or pick the newest)
> docker run --rm -v <old-volume>:/from:ro -v <new-volume>:/to alpine sh -c 'cp -a /from/. /to/'
> # e.g. <new-volume> is infra-mngmt_devcontainer_claude-code-config
> ```
>
> `playwright-browsers-v2` and `gh-config` are pinned with `name:` so they stay shared across all your devcontainers (no re-download / re-auth).

**Then from a WSL2 terminal, copy the binary into your PATH:**

```bash
mkdir -p ~/.local/bin
# replace <checkout> with the path to your infra-mngmt checkout
cp <checkout>/dist/infra-mngmt ~/.local/bin/
```

Verify:

```bash
infra-mngmt -h
infra-mngmt version    # commit + build epoch
```

---

## 3. Configure

Config lives at `~/.config/infra-mngmt/config.yaml`. If the file doesn't exist, defaults are used (bind `127.0.0.1:7842`, no process-compose instances, no extra paths). The loader also accepts a legacy `config.json` and logs a deprecation warning.

On first run infra-mngmt auto-generates a bearer token at `~/.config/infra-mngmt/token` (0600). You will need it to log in at `http://localhost:7842/login`.

### Full config reference

```yaml
bind: 127.0.0.1:7842
token_file: ~/.config/infra-mngmt/token
process_compose:
  - name: wsl
    endpoint: http://localhost:9998
    binary: /usr/local/bin/process-compose
    compose_file: ~/.config/infra-mngmt/process-compose.yaml
    token_file: ~/.config/infra-mngmt/process-compose.token
  - name: windows
    endpoint: http://wsl-windows:9999
    binary: /c/Users/<user>/AppData/Local/Programs/process-compose/process-compose.exe
    compose_file: /c/Users/<user>/.config/infra-mngmt/process-compose.yaml
    token_file: /c/Users/<user>/.config/infra-mngmt/process-compose.token
trusted_networks:
  - 127.0.0.0/8 # the local browser (Windows → WSL is loopback-forwarded) stays logged-in-free
  - ::1/128
  # Don't add the Docker bridge (172.17.0.0/16): it trusts every container on it — see SECURITY.md.
extra_paths:
  - ~/projects/project-a
  - ~/projects/project-b
```

Each `process_compose` entry can carry the API token for its instance one of two ways: `token` (literal) or `token_file` (path read at startup). The file form is preferred — it keeps the secret out of `config.yaml` and the same path can be passed to process-compose itself via `--token-file`, so both sides read one file. Sent on the wire as the `X-PC-Token-Key` header. Leave both unset if the instance has no auth. See [SECURITY.md](SECURITY.md) for the full setup.

`bridges.yaml`, `dependencies.yaml`, `containers.yaml`, and `model-rates.yaml` are auto-discovered alongside `config.yaml` (or set `bridges_file:`/`dependencies_file:`/`containers_file:`/`model_rates_file:` to override). They drive the bridge applier (Windows portproxy + firewall via one UAC prompt; WSL-tier socat relays via a process-compose fragment that the user's main YAML references with `extends:` — see §4), the entity → service/bridge dependency resolver, the declared-container panel, and the Open Design card's blended-cost rollup respectively. `model-rates.yaml` is optional; when absent the OD card simply renders `—` in every cost slot.

### Field reference

| Field                            | Default                                         | Description                                                                                                                                                                          |
| -------------------------------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `bind`                           | `127.0.0.1:7842`                                | Listen address. Set to `0.0.0.0:7842` for LAN access.                                                                                                                                |
| `token_file`                     | `~/.config/infra-mngmt/token`                   | Path to the infra-mngmt bearer token. Set to `""` to disable auth.                                                                                                                   |
| `trusted_networks`               | `[]`                                            | CIDRs whose source IPs bypass auth entirely. Use loopback only — a Docker-bridge CIDR trusts every container on the bridge. Headless clients should send `Authorization: Bearer <token>` rather than rely on a trusted CIDR.                |
| `process_compose[].name`         | required                                        | Display name shown in the services tab; also the tier identifier in `dependencies.yaml`.                                                                                             |
| `process_compose[].endpoint`     | required                                        | Base URL of the process-compose management API. Supports the `wsl-windows` hostname (see below).                                                                                     |
| `process_compose[].binary`       | optional                                        | Path to the process-compose binary. When set, a **▶ start** button appears in the UI if the endpoint is unreachable.                                                                 |
| `process_compose[].compose_file` | optional                                        | Path to the process-compose YAML passed to `binary` on bootstrap.                                                                                                                    |
| `process_compose[].token`        | optional                                        | API token (literal) for this process-compose instance, sent as `X-PC-Token-Key`. Takes precedence over `token_file` when both are set.                                               |
| `process_compose[].token_file`   | optional                                        | Path to a file containing the API token. Read at startup; same path can be passed to process-compose's own `--token-file`. Preferred over `token` for keeping secrets out of config. |
| `llama_servers`                  | `[]`                                            | llama.cpp endpoints to probe for live stats (`/health`, `/props`, `/metrics`, `/slots`). Each entry: `instance` + `process` (matched against a process-compose process row so the llama button only renders where it can probe), `endpoint` (supports `wsl-windows`), and optional `api_key`/`api_key_file`. See `config.go` `LlamaServer`. |
| `bridges_file`                   | `bridges.yaml` alongside config                 | Path to bridges.yaml.                                                                                                                                                                |
| `bridges_compose_file`           | `process-compose.bridges.yaml` alongside config | Path to the generated process-compose fragment that runs `tier: wsl, type: socat` relays. The user's main process-compose.yaml is expected to reference it via `extends:` — see §4.  |
| `dependencies_file`              | `dependencies.yaml` alongside config            | Path to dependencies.yaml.                                                                                                                                                           |
| `containers_file`                | `containers.yaml` alongside config              | Path to containers.yaml.                                                                                                                                                             |
| `model_rates_file`               | `model-rates.yaml` alongside config             | Path to operator-supplied per-model token pricing (USD per million tokens) used by the Open Design card's blended-cost rollup. Optional — missing file → every cost slot renders `—`. |
| `extra_paths`                    | `[]`                                            | Additional project root directories to scan for `.claude/` beyond `~` and `$CWD`.                                                                                                    |

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

Hosts WSL-native services and one-shot operational entries (e.g. a self-deploy entry that copies a freshly-built `dist/infra-mngmt` and restarts the systemd unit). Reaching Windows-side services from inside WSL is handled by the **bridges layer** (§ Bridges below), not by socat in this YAML — declarative bridges replace the older `socat + awk /etc/resolv.conf` pattern.

```yaml
extends: process-compose.bridges.yaml # generated by infra-mngmt — do not edit
processes: {} # empty is fine; the supervisor stays up via --keep-project
```

The `extends:` line pulls in `~/.config/infra-mngmt/process-compose.bridges.yaml`, a fragment that `infra-mngmt bridges apply` rewrites for every `tier: wsl, type: socat` entry in `bridges.yaml`. The fragment merges into your `processes:` map at PC startup; entries you define here win on name collisions, so the user-owned YAML stays the source of truth for everything except the generated bridge processes. Don't hand-edit the fragment.

**One-time setup**: run `infra-mngmt bridges apply` once after cloning so the fragment file exists. Without it process-compose fails to start with "extended file not found".

**How `${windows-host-ip}` is resolved at runtime.** WSL → Windows socat relays need the Windows host's vEthernet IP, which renumbers on every `wsl --shutdown`. The fragment embeds an inline backtick subshell (` `` /usr/sbin/ip route | awk '/^default/ {print $3}' `` `) directly in each socat command, so bash re-evaluates it every time the bridge process is (re)launched. We deliberately don't:

- Bake the IP at fragment-write time — it would go stale on the next WSL bounce.
- Use `$(infra-mngmt addr windows-host)` — systemd's user PATH excludes `~/.local/bin/`, so the subshell silently returns empty and socat loops back to localhost (`TCP::8080`).
- Use process-compose's `env_cmds` — its `POST /project/configuration` reload doesn't re-evaluate them on all PC versions, so an updated IP after a mid-session WSL bounce wouldn't reach the spawned bridge processes.

Backticks are invisible to envsubst (which only knows `$`/`${}`/`$$`), so they pass through to bash unchanged. Refresh cadence: every socat (re)start picks up the current gateway. A WSL bounce, a `bridges apply`-triggered reload that restarts the process, and PC's own restart-on-failure all re-resolve the IP — no operator intervention needed.

Start (run once; systemd handles it on subsequent boots — see §6):

```bash
process-compose up -f ~/.config/infra-mngmt/process-compose.yaml \
  --port 9998 \
  --address 127.0.0.1 \
  --keep-project \
  --tui=false &
```

`--address 127.0.0.1` restricts the API to loopback — infra-mngmt talks to it locally so this is safe and recommended. `--keep-project` keeps the supervisor running when `processes:` is empty (or every entry has terminated), preserving the REST API. See [SECURITY.md](SECURITY.md) for adding bearer token auth.

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

### Linking entities to services and bridges (`dependencies.yaml`)

The resolver maps any Claude Code entity (MCP server, skill, hook, …) to the services and bridges it depends on via `~/.config/infra-mngmt/dependencies.yaml`. Status pills on entity cards are the worst-of state across all matched needs.

```yaml
dependencies:
  - entity: mcp:llama
    scope: "*"
    needs: [service:llama-server, bridge:llama-cpp]
  - entity: skill:summarize-doc
    scope: host
    needs: [service:llama-server]
```

`needs[i]` is `service:<process-name>[@<tier>]` or `bridge:<bridge-name>`. Tier qualifier (`@wsl`, `@windows`) is only needed when the same process name exists on multiple tiers. See `.claude/skills/infra-mngmt-config/SKILL.md` for the full schema.

If no rule matches an MCP entity, the resolver falls back to the legacy substring match against process-compose process names (so existing setups keep working). An **orange dot** means compose is online but nothing matches — either add a `dependencies.yaml` rule or rename the process.

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
           --label claude.project.root=~/myproject \
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
>
> ```bash
> export DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$(id -u)/bus
> ```
>
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
- **`--token-file`** matches what's in `config.yaml`'s `token_file` for the `windows` entry — both sides read the same file.
- On Windows 11 with the **Hyper-V firewall** (you'll see the adapter name `vEthernet (WSL (Hyper-V firewall))`), regular `New-NetFirewallRule` rules don't apply to WSL traffic. You need `New-NetFirewallHyperVRule`. See [SECURITY.md](SECURITY.md#process-compose-hardening) for the exact command.

> If you skipped systemd, replace the WSL2 `systemctl` line with explicit background commands:
>
> ```
> "process-compose up -f ~/.config/infra-mngmt/process-compose.yaml --port 9998 --address 127.0.0.1 --tui=false & infra-mngmt &"
> ```

### Register with Task Scheduler

Run once in PowerShell (elevated not required):

```powershell
$script = "$env:USERPROFILE\.config\infra-mngmt\autostart.ps1"

$action  = New-ScheduledTaskAction `
  -Execute "powershell.exe" `
  -Argument "-NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$script`""

$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME

$settings = New-ScheduledTaskSettingsSet `
  -ExecutionTimeLimit (New-TimeSpan -Minutes 2) `
  -RestartCount 2 `
  -RestartInterval (New-TimeSpan -Minutes 1) `
  -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable

Register-ScheduledTask `
  -TaskName "infra-mngmt autostart" `
  -Action   $action `
  -Trigger  $trigger `
  -Settings $settings `
  -Force
```

`-ExecutionPolicy Bypass` is required: the default `LocalMachine` policy is `Restricted` (or `RemoteSigned` post-domain-join), under which an unsigned `.ps1` exits 1 silently when invoked via Task Scheduler. Symptom: task fires on time but `Get-ScheduledTaskInfo` shows `LastTaskResult: 1` and nothing comes up.

This triggers at logon for the current user, runs hidden, and retries twice if WSL2 hasn't initialised yet. `-RunLevel Highest` is intentionally omitted — the script runs in your own user profile and binds only local ports, so it needs no elevation; requesting `Highest` only adds a silent-failure mode on accounts where Task Scheduler can't auto-elevate.

Verify the registration actually works:

```powershell
Start-ScheduledTask -TaskName "infra-mngmt autostart"
Start-Sleep 2
Get-ScheduledTaskInfo -TaskName "infra-mngmt autostart" |
  Select TaskName, LastRunTime, LastTaskResult
# LastTaskResult must be 0. Anything else means the script failed; re-run by
# hand with `powershell.exe -NonInteractive -WindowStyle Hidden -ExecutionPolicy
# Bypass -File "<path>"` to see the actual error.
```

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
make build                # build dist/infra-mngmt with -X main.buildEpoch stamp
make test                 # full test suite
make check                # fmt + vet + lint + test (pre-commit gate)
make tidy                 # go mod tidy
```

The devcontainer firewall allowlists the Go module proxy (`proxy.golang.org`, `sum.golang.org`, `dl.google.com`) — no `GOPROXY=direct` workaround needed.

After a code change, redeploy via the `infra-mngmt-deploy` process-compose entry — one-click from the services panel (the browser already holds a session), or, from inside the devcontainer, authenticate the POST with the bearer token (the host token file is bind-mounted at `/wsl-config/token`). Derive the host gateway rather than hard-coding it — a Compose-based devcontainer sits on its own Docker network, so the gateway is **not** the default-bridge `172.17.0.1`:

```bash
GW=$(ip route | awk '/default/{print $3; exit}')   # host gateway (e.g. 172.22.0.1)
curl -fsS -H "Authorization: Bearer $(cat /wsl-config/token)" \
  -X POST "http://$GW:7842/process/start?instance=wsl&process=infra-mngmt-deploy"
# then poll the public, no-auth /api/version until build_epoch changes:
curl -fsS "http://$GW:7842/api/version" | jq .build_epoch
```

The originating request dies mid-deploy and the server recovers in ~1 s with the new binary; the bearer token (unlike a session cookie) survives the restart, so the curl above is stateless. Manual fallback: `cp dist/infra-mngmt ~/.local/bin/ && systemctl --user restart infra-mngmt`.

### Project docs

- [CLAUDE.md](CLAUDE.md) — project structure, tech stack, key design decisions
- [docs/frontend-architecture.md](docs/frontend-architecture.md) — **binding** for any change under [internal/web/](internal/web/) (Go templates, CSS, JS modules, HTMX vs. SSE vs. fetch decision tree, asset vendoring policy, checklist for adding UI)
- [TESTING.md](TESTING.md) — test layout, mock patterns
- [SECURITY.md](SECURITY.md) — threat model and hardening
- [docs/proposals/](docs/proposals/) — design proposals for in-flight work
