---
name: infra-mngmt-config
description: Use when the user wants to register a long-running service (MCP server, llama.cpp/whisper backend, socat bridge, WSL-native daemon) with infra-mngmt so it shows up in the services panel and matches MCP status badges — or when editing `~/.config/infra-mngmt/config.json` or a `process-compose.yaml` it references. Do NOT trigger for generic "add a service" / "add an MCP server" requests that aren't tied to infra-mngmt.
---

# infra-mngmt config & process-compose entries

[infra-mngmt](https://github.com/eike-hass/infra-mngmt) is a unified Claude Code config viewer + process supervisor for Windows + WSL2 + devcontainers. It surfaces `.claude/` entities across scopes and shows live status of the services those entities depend on, by querying [process-compose](https://github.com/F1bonacc1/process-compose) instances on each tier.

**Source of truth**: when uncertain, read the infra-mngmt repo's `README.md` and `CLAUDE.md` — they always reflect the current schema. This skill is a quick reference, not a replacement.

## Two layers, do not confuse them

1. **App config** — `~/.config/infra-mngmt/config.json` — registers which process-compose instances the app talks to.
2. **process-compose YAML** — wherever `compose_file` points (convention: `~/.config/infra-mngmt/process-compose.yaml` on each tier) — declares the actual processes.

A new service usually means **adding to (2)**. You only touch (1) when adding a whole new tier (e.g. a third process-compose instance) or changing endpoints/tokens.

## App config schema (`~/.config/infra-mngmt/config.json`)

```json
{
  "bind": "127.0.0.1:7842",
  "token_file": "/home/user/.config/infra-mngmt/token",
  "process_compose": [
    {
      "name": "wsl",
      "endpoint": "http://localhost:9998",
      "binary": "/usr/local/bin/process-compose",
      "compose_file": "/home/user/.config/infra-mngmt/process-compose.yaml",
      "token": ""
    },
    {
      "name": "windows",
      "endpoint": "http://wsl-windows:9999",
      "binary": "/mnt/c/Users/user/AppData/Local/Programs/process-compose/process-compose.exe",
      "compose_file": "/mnt/c/Users/user/.config/infra-mngmt/process-compose.yaml",
      "token": ""
    }
  ],
  "extra_paths": []
}
```

Field rules:
- `name` — free-form label shown in the UI.
- `endpoint` — process-compose REST URL. The literal hostname `wsl-windows` is a **sentinel**: at startup the app resolves it to the Windows host IP from `/etc/resolv.conf`. Use it for the Windows tier so the config survives WSL2 NAT restarts. Do not hardcode `172.x.x.x`.
- `binary` — optional. When set, the UI shows a ▶ start button if the endpoint is unreachable.
- `compose_file` — optional, passed to `binary` on bootstrap. Required if you want bootstrap to work.
- `token` — bearer token if process-compose auth is enabled; empty otherwise.
- `extra_paths` — additional project roots to scan; each must contain a `.claude/` subdir.

## process-compose YAML

This is **infrastructure config**, not Claude Code config — it does **not** belong in `.claude/`. Keep it under `~/.config/infra-mngmt/` (or any path the `compose_file` field points to).

### Tier split

| Tier | Lives at | What goes here |
|---|---|---|
| WSL2 | `~/.config/infra-mngmt/process-compose.yaml` | socat bridges (Windows port → WSL), WSL-native MCP servers and daemons |
| Windows | `%USERPROFILE%\.config\infra-mngmt\process-compose.yaml` | GPU/CPU services that must run on the Windows host (llama.cpp, whisper, etc.) |

### WSL example

```yaml
processes:
  socat-llama:
    command: >
      socat TCP-LISTEN:8080,fork,reuseaddr
        TCP:$(awk '/nameserver/{print $2; exit}' /etc/resolv.conf):8080
    availability:
      restart: always
```

### Windows example

```yaml
processes:
  llama-cpp:
    command: C:\tools\llama\server.exe -m models\llama-3-8b.gguf --port 8080 -ngl 99
    availability:
      restart: on_failure
```

The Windows instance must bind `0.0.0.0` (process-compose's default) so WSL2 can reach it via NAT.

## MCP status-badge naming convention

infra-mngmt matches MCP server entries in `.claude/settings.json` to process-compose processes by **case-insensitive substring** in either direction (the entity name contains the process name, or vice versa). To get a green dot:

- MCP server in `.claude/settings.json`: `"llama-cpp"`
- process-compose process name: `llama-cpp` (or `llama-cpp-server`, etc.)

Orange dot = compose online but no name match → either the process isn't there or the names diverged.

## When adding a new service — checklist

1. Decide tier: does it need GPU / Windows-only deps? → Windows YAML. Otherwise → WSL YAML.
2. Append a `processes:` entry. Pick a name that substring-matches the MCP server name in `.claude/settings.json` if one will reference it.
3. Set `availability.restart` (`always` for bridges, `on_failure` for servers that may legitimately exit).
4. If this is a brand-new tier (rare), also add a `process_compose[]` entry to `config.json`.
5. Reload: `curl -X POST http://<endpoint>/project/configuration/reload` against the relevant process-compose, or restart the unit. Do **not** kill the process-compose REST API — that takes the whole tier offline.
6. Verify in the infra-mngmt UI that the process appears and the MCP card's dot turns green.

## Don'ts

- Don't put `process-compose.yaml` inside `.claude/` — it's not a Claude Code entity.
- Don't hardcode the WSL2 gateway IP in `endpoint`; use `wsl-windows`.
- Don't invent new scopes. infra-mngmt only knows global (`~/.claude/`) and per-project (`<repo>/.claude/`).
- Don't commit secrets into `compose_file` paths that are world-readable; the `token` field in `config.json` is for the process-compose API token, not service credentials.
