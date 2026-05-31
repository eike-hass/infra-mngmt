---
name: infra-mngmt-config
description: Use when the user wants to register a long-running service (MCP server, llama.cpp/whisper backend, socat bridge, WSL-native daemon) with infra-mngmt so it shows up in the services panel and matches MCP status badges — or when editing `~/.config/infra-mngmt/config.yaml`, `bridges.yaml`, `dependencies.yaml`, or a `process-compose.yaml` it references. Do NOT trigger for generic "add a service" / "add an MCP server" requests that aren't tied to infra-mngmt.
---

# infra-mngmt config & process-compose entries

[infra-mngmt](https://github.com/eike-hass/infra-mngmt) is a unified Claude Code config viewer + process supervisor for Windows + WSL2 + devcontainers. It surfaces `.claude/` entities across scopes and shows live status of the services those entities depend on, by querying [process-compose](https://github.com/F1bonacc1/process-compose) instances on each tier.

**Source of truth**: when uncertain, read the infra-mngmt repo's `README.md` and `CLAUDE.md` — they always reflect the current schema. This skill is a quick reference, not a replacement.

## Four files, do not confuse them

1. **App config** — `~/.config/infra-mngmt/config.yaml` — registers which process-compose instances the app talks to and points at the bridge / dependency files.
2. **process-compose YAML** — wherever `compose_file` points (convention: `~/.config/infra-mngmt/process-compose.yaml` on each tier) — declares the actual long-running processes.
3. **Bridges** — `~/.config/infra-mngmt/bridges.yaml` — declares persistent network bridges (Windows portproxy + firewall, optionally WSL socat). Applied via `infra-mngmt bridges apply` (single UAC prompt for the batch).
4. **Dependencies** — `~/.config/infra-mngmt/dependencies.yaml` — declares which entities (MCP servers, skills, …) need which suppliers (services or bridges). The resolver computes worst-of state across all needs and lights up the entity badge accordingly.

A new service usually means **adding to (2)** plus an entry in (4). For ports bound to `127.0.0.1` on the Windows host you also add a (3) bridge.

## App config schema (`~/.config/infra-mngmt/config.yaml`)

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
bridges_file: ""        # optional; defaults to bridges.yaml alongside config.yaml
dependencies_file: ""   # optional; defaults to dependencies.yaml alongside config.yaml
trusted_networks: [127.0.0.0/8]   # CIDRs that bypass auth; loopback only
extra_paths: []
```

Field rules:
- `name` — free-form label shown in the UI; also the tier identifier in `dependencies.yaml` (`service:foo@<name>`).
- `endpoint` — process-compose REST URL. The literal hostname `wsl-windows` is a **sentinel**: at startup the app resolves it to the Windows host IP from WSL's default-route gateway (`/proc/net/route`), with `/etc/resolv.conf` as a fallback for legacy setups. Use it for the Windows tier so the config survives WSL2 NAT restarts. Do not hardcode `172.x.x.x`.
- `binary` — optional. When set, the UI shows a ▶ start button if the endpoint is unreachable.
- `compose_file` — optional, passed to `binary` on bootstrap. Required if you want bootstrap to work.
- `token` / `token_file` — process-compose API token. Use `token_file` for cleanliness; the same path can be passed to process-compose via `--token-file`. Leave both unset if auth is disabled. `token` (literal) takes precedence when both are set.
- `trusted_networks` — optional list of CIDRs whose source IPs bypass bearer-token / session-cookie auth. Empty (default) means every request is authenticated when `token_file` is set; loopback is NOT trusted unless listed. Add `127.0.0.0/8` (and `::1/128`) for login-free local browser access. Do NOT add the Docker bridge CIDR `172.17.0.0/16` — it trusts every container on the bridge. Headless clients (deploy scripts, CI) should send `Authorization: Bearer <token>` instead of relying on a trusted CIDR.
- `bridges_file` / `dependencies_file` — optional override paths; otherwise infra-mngmt auto-discovers `bridges.yaml` and `dependencies.yaml` alongside `config.yaml`.
- `extra_paths` — additional project roots to scan; each must contain a `.claude/` subdir.

## process-compose YAML

This is **infrastructure config**, not Claude Code config — it does **not** belong in `.claude/`. Keep it under `~/.config/infra-mngmt/` (or any path the `compose_file` field points to).

### Tier split

| Tier | Lives at | What goes here |
|---|---|---|
| WSL2 | `~/.config/infra-mngmt/process-compose.yaml` | WSL-native MCP servers and daemons. Persistent socat bridges go in `bridges.yaml` instead. |
| Windows | `%USERPROFILE%\.config\infra-mngmt\process-compose.yaml` | GPU/CPU services that must run on the Windows host (llama.cpp, whisper, etc.) |

### Windows example

```yaml
processes:
  llama-server:
    command: cmd.exe /C "C:\tools\llama.cpp\start-server.bat"
    availability:
      restart: on_failure
    shutdown:
      signal: 15
      timeout_seconds: 10
```

The Windows process-compose itself runs unelevated. For ports the service binds to `127.0.0.1` only, declare a Windows-tier `portproxy+firewall` bridge in `bridges.yaml` and apply it once with `infra-mngmt bridges apply` (single UAC prompt). Do **not** make process-compose elevated to manage netsh state — the blast radius is too wide.

## Bridges schema (`~/.config/infra-mngmt/bridges.yaml`)

```yaml
bridges:
  # tier=windows: netsh portproxy + firewall rule. Persistent state on the
  # Windows host. Listens on the WSL adapter so WSL/devcontainer can reach
  # a Windows-bound 127.0.0.1 service.
  - name: producer-pal
    tier: windows
    type: portproxy+firewall
    listen: { addr: "${wsl-host-ip}", port: 3350 }
    connect: { addr: 127.0.0.1, port: 3350, family: auto }
    firewall:
      remote: 172.18.0.0/16
      display_name: "Producer Pal MCP"

  # tier=wsl: socat relay running as a process-compose entry on the WSL host.
  # Re-originates connections from the WSL host's eth0 IP so they pass the
  # Windows portproxy's firewall remote filter. Required because Hyper-V's
  # firewall drops forwarded packets sourced from docker0 (172.17/16) on
  # their way to the Windows-side bridge — sourcing from WSL eth0 (in the
  # `firewall.remote` CIDR) does pass.
  - name: producer-pal-relay
    tier: wsl
    type: socat
    listen: { addr: 172.17.0.1, port: 3350 }
    connect: { addr: "${windows-host-ip}", port: 3350, family: auto }
```

Field rules:
- `name` — globally unique; consumers reference it as `bridge:<name>` in `dependencies.yaml`.
- `tier` — `wsl` (socat) or `windows` (portproxy+firewall). `container:*` reserved for future use.
- `${wsl-host-ip}` (Windows-tier listen): resolved at apply time via `Get-NetIPAddress -InterfaceAlias 'vEthernet (WSL*)'` and baked into the netsh rule.
- `${windows-host-ip}` (WSL-tier connect): rendered as an inline bash backtick subshell (` `` /usr/sbin/ip route | awk '/^default/ {print $3}' `` `) embedded in the socat command. Bash re-evaluates it every time the bridge process is (re)launched — covers `wsl --shutdown` IP renumbering, PC restarts, and `bridges apply`-triggered reloads. Backticks are invisible to process-compose's envsubst preprocessor so they pass through untouched.
- `connect.family` — `auto` (the default) and `v4` both emit `v4tov4` portproxy rules. Set `v6` only when the connect target is genuinely IPv6-only (use an IPv6 connect address like `::1` in the same entry); a service bound to `::` accepts IPv4 fine through dual-stack and should stay on `v4tov4`.
- `firewall.remote` — CIDR allowed to reach the listener (typically the WSL2 NAT range, `172.18.0.0/16` on stock setups).
- The applier restarts `iphlpsvc` after each Windows-tier batch to flush the kernel listener cache.

How `tier: wsl` bridges materialize:
- `infra-mngmt bridges apply` rewrites `~/.config/infra-mngmt/process-compose.bridges.yaml` (the path is configurable via `bridges_compose_file:` in `config.yaml`).
- The user's main `process-compose.yaml` references it via `extends: process-compose.bridges.yaml` — see README §4 for the one-line setup.
- Each `wsl/socat` bridge becomes a `bridge-<name>` process with `availability.restart: always`, `namespace: bridges`, and an `nc -z` readiness probe targeting the listen address.
- Apply also triggers a `POST /project/configuration` reload on the WSL PC instance. Reset additionally calls `/process/stop` on the dropped `bridge-*` processes (PC's reload doesn't reliably reconcile deletions on all versions).

CLI: `infra-mngmt bridges {list,status,apply,reset}`. Apply/reset for Windows bridges trigger one UAC prompt for the whole batch; WSL bridges flow entirely through the process-compose fragment + REST API.

## Dependencies schema (`~/.config/infra-mngmt/dependencies.yaml`)

```yaml
dependencies:
  - entity: mcp:llama
    scope: "*"
    needs:
      - service:llama-server
      - bridge:llama-cpp

  - entity: skill:summarize-doc
    scope: host
    needs:
      - service:llama-server
```

Field rules:
- `entity` — `<kind>:<name>`, exact (case-insensitive) match against an entity surfaced by infra-mngmt. Kinds: `mcp`, `skill`, `hook`, `agent`, `memory`, `command`, `claude_md`, `setting`.
- `scope` — `*` (any), `host`, `project:*`, `project:<absolute-path>`, `container:*`, `container:<folder>`. Multiple matching rules union their needs.
- `needs[i]` — `service:<process-name>[@<tier>]` or `bridge:<bridge-name>`. Tier qualifier is only needed when the same process name exists on multiple tiers.

The resolver returns worst-of state across all matched needs. If no rule matches an MCP entity, it falls back to legacy substring matching against process names so existing setups keep working.

## When adding a new service — checklist

1. Decide tier for the *backend*: GPU / Windows-only → Windows YAML. Otherwise → WSL YAML.
2. Append a `processes:` entry to the relevant `process-compose.yaml`. Use a stable, hyphenated name.
3. If the service binds `127.0.0.1` on Windows: add a `portproxy+firewall` bridge to `bridges.yaml`. Apply with `infra-mngmt bridges apply`.
4. Add an entry to `dependencies.yaml` pointing the consuming entity (`mcp:foo`, `skill:bar`) at the new service / bridge. Pick the right scope.
5. Reload process-compose: `curl -X POST http://<endpoint>/project/configuration/reload`. Do **not** kill the REST API — that takes the whole tier offline.
6. Verify in the infra-mngmt UI: process appears, bridge state is "active", entity card's dot turns green.

## Don'ts

- Don't put `process-compose.yaml`, `bridges.yaml`, or `dependencies.yaml` inside `.claude/` — they're infra-mngmt config, not Claude Code entities.
- Don't hardcode the WSL2 gateway IP in `endpoint`; use `wsl-windows`.
- Don't make Windows-tier process-compose run elevated just so it can manage netsh state — use the bridges layer instead.
- Don't invent new scopes. infra-mngmt only knows `host`, `project:<repo>`, and (reserved) `container:<folder>`.
- Don't commit secrets into `compose_file` paths that are world-readable; `token` / `token_file` are for the process-compose API token, not service credentials.
