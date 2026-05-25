# apple-home-cli

Go CLI/package for Apple Home inventory and generic control backends.

## Install

```bash
curl -fsSL https://github.com/avyayv/personal-automation-cli/releases/latest/download/install.sh | bash -s -- apple-home
apple-home doctor
```

Set `PERSONAL_AUTOMATION_CLI_INSTALL_DIR` or `INSTALL_DIR` to choose a different install directory, and set `PERSONAL_AUTOMATION_CLI_VERSION` to install a specific release.

From source:

```bash
cd /Users/avyay/code/cli/personal-automation-cli/cli/apple-home-cli
GOBIN=$HOME/.local/bin go install ./cmd/apple-home
```

## Commands

```bash
apple-home doctor
apple-home list homes
apple-home list rooms
apple-home list devices
apple-home list scenes
apple-home find "Kitchen Lights"
```

Generic My Leviton control:

```bash
export MYLEVITON_EMAIL='you@example.com'
export MYLEVITON_PASSWORD='...'

apple-home myleviton devices
apple-home set "Kitchen Lights" on
apple-home set "Kitchen Lights" --brightness 35
apple-home get "Kitchen Lights"
```

Generic Shortcuts bridge:

```bash
apple-home shortcuts-template
apple-home set "Kitchen Lights" on --backend shortcuts
apple-home scene "Good Night" --backend shortcuts
```

## Go package

Importable package:

```go
import "github.com/avyayv/personal-automation-cli/cli/apple-home-cli/pkg/applehome"
```

Useful entry points:

- `applehome.NewHomeDB("").Devices(true)`
- `applehome.NewHomeDB("").FindDevice("Kitchen Lights")`
- `applehome.NewLevitonClient("", "", false).SetState(...)`
- `applehome.NewShortcutsBackend("").Run(...)`
