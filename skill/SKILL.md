---
name: personal-automation
description: Use when the user asks to use personal automation CLIs, especially Oura Ring health data or GREE HVAC/AC control.
---

# Personal Automation

Use this skill for basic operation of the personal automation CLIs:

- `oura` — query Oura Ring data.
- `gree` — discover/control GREE HVAC units.

## Install / update

Install the CLIs from the latest release:

```bash
curl -fsSL https://github.com/avyayv/personal-automation-cli/releases/latest/download/install.sh | bash -s -- oura
curl -fsSL https://github.com/avyayv/personal-automation-cli/releases/latest/download/install.sh | bash -s -- gree
```

Update:

```bash
oura update
gree update
```

## Oura

Authentication:

```bash
export OURA_TOKEN="..."
```

Or save the token:

```bash
oura config init
oura config set-token "..."
oura config show
```

Useful commands:

```bash
oura personal-info
oura ring-configuration
oura daily-activity
oura daily-readiness
oura daily-sleep
oura daily-stress
oura daily-spo2
oura sleep
oura heartrate
```

Common date options:

```bash
oura daily-sleep --days 14
oura daily-readiness --start-date 2026-05-01 --end-date 2026-05-06
oura heartrate --start-datetime 2026-05-06T00:00:00Z --end-datetime 2026-05-06T12:00:00Z
```

Fallback for unsupported Oura endpoints:

```bash
oura get daily_activity --param start_date=2026-05-01 --param end_date=2026-05-06
oura get /v2/usercollection/workout --param start_date=2026-05-01 --param end_date=2026-05-06
```

## GREE

Discover devices and check status:

```bash
gree devices
gree status
```

Control AC:

```bash
gree on
gree off
gree temp 68
gree mode heat
gree mode cool
gree fan auto
```

Save default device/config:

```bash
gree config init
gree config set-device --mac c039375d1be7
gree config set-device --ip 192.168.1.50
gree config set scan-wait 2.5
gree config show
```

If needed, target a device explicitly:

```bash
gree status --mac c039375d1be7
gree temp 68 --ip 192.168.1.50
```

All CLI output is JSON.
