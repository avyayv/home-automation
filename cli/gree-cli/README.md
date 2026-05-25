# gree-cli

Standalone Go command-line interface for discovering and controlling GREE HVAC units over the LAN. The binary is named `gree`. All command output is JSON. No Python runtime is required.

## Install

```bash
curl -fsSL https://github.com/avyayv/personal-automation-cli/releases/latest/download/install.sh | bash -s -- gree
```

Set `PERSONAL_AUTOMATION_CLI_INSTALL_DIR` or `INSTALL_DIR` to choose a different install directory, and set `PERSONAL_AUTOMATION_CLI_VERSION` to install a specific release.

From source:

```bash
git clone https://github.com/avyayv/personal-automation-cli
cd personal-automation-cli/cli/gree-cli
go build -o ~/.local/bin/gree .
```

## Update

```bash
gree update
```

`gree update` fetches the latest source from `github.com/avyayv/personal-automation-cli`, rebuilds this CLI, and replaces the installed `gree` binary. Pass an explicit target path if needed: `gree update ~/.local/bin/gree`.

## Configuration

The CLI uses `$GREE_CONFIG` when set. Otherwise it reads and writes:

```text
~/.config/gree/config.toml
```

Create and manage it with:

```bash
gree config init
gree config show
gree config set-device --mac c039375d1be7
gree config set scan-wait 2.5
gree config clear-device
```

Device selection precedence:

1. Command-line selectors like `--mac` or `--ip`
2. Configured defaults
3. Automatic selection when exactly one device is discovered

## Usage

```bash
gree devices
gree status
gree on
gree off
gree temp 68
gree mode heat
gree fan auto
```

Use `--ip`, `--mac`, and `--scan-wait` on device commands when needed:

```bash
gree status --mac c039375d1be7
gree temp 68 --ip 192.168.1.50 --scan-wait 3
```

## Local development

```bash
gofmt -w main.go main_test.go
go test ./...
go build -o gree .
./gree --help
```
