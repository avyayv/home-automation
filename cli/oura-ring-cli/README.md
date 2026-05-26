# oura-ring-cli

Standalone Go command-line interface for the Oura Ring API. The binary is named `oura`. Command output is compact JSON by default. No Python runtime is required.

## Install

```bash
curl -fsSL https://github.com/avyayv/personal-automation-cli/releases/latest/download/install.sh | bash -s -- oura
```

Set `PERSONAL_AUTOMATION_CLI_INSTALL_DIR` or `INSTALL_DIR` to choose a different install directory, and set `PERSONAL_AUTOMATION_CLI_VERSION` to install a specific release.

From source:

```bash
git clone https://github.com/avyayv/personal-automation-cli
cd personal-automation-cli/cli/oura-ring-cli
go build -o ~/.local/bin/oura .
```

## Authentication

Create a personal access token at:

https://cloud.ouraring.com/personal-access-tokens

Use either an environment variable:

```bash
export OURA_TOKEN="..."
oura personal-info
```

Or save it in the CLI config:

```bash
oura config init
oura config set-token "..."
oura config show
```

The CLI uses `$OURA_CONFIG` when set. Otherwise it reads and writes:

```text
~/.config/oura/config.toml
```

Saved config files are written with `0600` permissions and `config show` redacts the token.

## Usage

```bash
oura personal-info
oura doctor
oura ring-configuration
oura daily-activity
oura daily-readiness --days 14
oura daily-sleep --start-date 2026-05-01 --end-date 2026-05-06
oura sleep --start-date 2026-05-01 --end-date 2026-05-06
oura heartrate --start-datetime 2026-05-06T00:00:00Z --end-datetime 2026-05-06T12:00:00Z
```

Date-based commands default to the last 7 days ending today. `heartrate` defaults to the last 24 hours.

Collection commands return concise summaries by default so common agent queries do not dump high-frequency arrays. For example, `sleep` omits per-epoch heart-rate/HRV/movement/sleep-phase arrays, `daily-activity` omits the `met` and `class_5_min` arrays, and `heartrate` returns hourly buckets. Use `doctor` to check auth/API reachability without printing profile data. Add `--raw` to any collection command when you need the full Oura API response:

```bash
oura sleep --days 7 --raw
oura daily-activity --days 7 --raw
oura heartrate --raw
```

For even smaller responses, use `--select` with comma-separated JSON field paths:

```bash
oura sleep --days 7 --select data.day,data.total_sleep_min,data.efficiency
oura heartrate --select data.hour,data.avg_bpm,data.min_bpm,data.max_bpm
```

For endpoints that do not have a first-class command, use `get`:

```bash
oura get daily_activity --param start_date=2026-05-01 --param end_date=2026-05-06
oura get /v2/usercollection/workout --param start_date=2026-05-01 --param end_date=2026-05-06
oura get sleep --param start_date=2026-05-01 --param end_date=2026-05-06 --raw
```

## Update

```bash
oura update
```

`oura update` fetches the latest source from `github.com/avyayv/personal-automation-cli`, rebuilds this CLI, and replaces the installed `oura` binary. Pass an explicit target path if needed: `oura update ~/.local/bin/oura`.

## Local development

```bash
gofmt -w main.go main_test.go
go test ./...
go build -o oura .
./oura --help
```
