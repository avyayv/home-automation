---
name: personal-automation-cli
description: Guidance for AI agents working on this repository's Go CLIs: apple-home, gree, and oura. Use when editing, testing, documenting, installing, or releasing any CLI in personal-automation-cli.
---

# Personal Automation CLI Agent Skill

This repository contains standalone Go CLIs under `cli/`:

- `cli/apple-home-cli/` builds the `apple-home` binary for Apple Home inventory and control backends.
- `cli/gree-cli/` builds the `gree` binary for GREE HVAC discovery and LAN control.
- `cli/oura-ring-cli/` builds the `oura` binary for the Oura Ring API.

## Non-negotiables

- Keep these CLIs Go-only; do not add Python runtime dependencies.
- Run `gofmt` before committing Go changes.
- Run `go test ./...` in every touched CLI directory.
- Update the root `README.md` and the relevant `cli/*/README.md` when commands, install flows, or config behavior change.
- `gree` and `oura` command output is JSON; do not add table, pretty-table, or other non-JSON output modes.
- Keep release/install references pointed at `github.com/avyayv/personal-automation-cli`.

## Build, test, and install

From the repo root, test all CLIs:

```bash
for dir in cli/apple-home-cli cli/gree-cli cli/oura-ring-cli; do
  (cd "$dir" && gofmt -w $(find . -name '*.go') && go test ./...)
done
```

Build individual binaries:

```bash
(cd cli/apple-home-cli && go build -o apple-home ./cmd/apple-home)
(cd cli/gree-cli && go build -o gree .)
(cd cli/oura-ring-cli && go build -o oura .)
```

Install globally for this user:

```bash
(cd cli/apple-home-cli && go install ./cmd/apple-home)
(cd cli/gree-cli && go build -o /Users/avyay/.local/bin/gree .)
(cd cli/oura-ring-cli && go build -o /Users/avyay/.local/bin/oura .)
chmod +x /Users/avyay/.local/bin/gree /Users/avyay/.local/bin/oura
```

## CLI notes

### apple-home

Useful commands:

```bash
apple-home doctor
apple-home list homes
apple-home list rooms
apple-home list devices
apple-home list scenes
apple-home find "Kitchen Lights"
apple-home shortcuts-template
```

### gree

Default config path is `~/.config/gree/config.toml`; override with `GREE_CONFIG`.

Selection precedence:

1. Command flags: `--mac` / `--ip`
2. Saved config
3. Auto-select if exactly one device is discovered

Useful commands:

```bash
gree devices [--scan-wait seconds]
gree status [--mac mac] [--ip ip] [--scan-wait seconds]
gree temp <degrees> [--mac mac] [--ip ip]
gree on [--mac mac] [--ip ip]
gree off [--mac mac] [--ip ip]
gree mode <auto|cool|dry|fan|heat> [--mac mac] [--ip ip]
gree fan <auto|low|medium-low|medium|medium-high|high> [--mac mac] [--ip ip]
gree update [install_path]
```

### oura

Default config path is `~/.config/oura/config.toml`; override with `OURA_CONFIG`. Use `OURA_TOKEN` or `oura config set-token <token>` for auth.

Useful commands:

```bash
oura personal-info
oura ring-configuration
oura daily-activity
oura daily-readiness --days 14
oura daily-sleep --start-date 2026-05-01 --end-date 2026-05-06
oura heartrate --start-datetime 2026-05-06T00:00:00Z --end-datetime 2026-05-06T12:00:00Z
oura get <endpoint> --param key=value
oura update [install_path]
```

## Release/install assets

The GitHub Actions release workflow builds `apple-home`, `gree`, and `oura` for macOS/Linux on amd64/arm64, publishes tarballs, and publishes `personal-automation-cli_<version>_checksums.txt` plus `install.sh`.

The installer uses:

- `PERSONAL_AUTOMATION_CLI_APP`
- `PERSONAL_AUTOMATION_CLI_VERSION`
- `PERSONAL_AUTOMATION_CLI_INSTALL_DIR`
- `INSTALL_DIR` as a generic install-dir fallback
