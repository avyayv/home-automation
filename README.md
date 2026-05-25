# Personal Automation CLI

Collection of standalone personal automation command-line tools.

## CLIs

- [`cli/apple-home-cli`](cli/apple-home-cli) — Go CLI binary named `apple-home` for Apple Home inventory and generic control backends.
- [`cli/gree-cli`](cli/gree-cli) — Go CLI binary named `gree` for discovering and controlling GREE HVAC units over the LAN.
- [`cli/oura-ring-cli`](cli/oura-ring-cli) — Go CLI binary named `oura` for querying the Oura Ring API.

## Install Apple Home CLI

```bash
curl -fsSL https://github.com/avyayv/personal-automation-cli/releases/latest/download/install.sh | bash -s -- apple-home
apple-home doctor
```

## Install GREE CLI

```bash
curl -fsSL https://github.com/avyayv/personal-automation-cli/releases/latest/download/install.sh | bash -s -- gree
```

## Install Oura Ring CLI

```bash
curl -fsSL https://github.com/avyayv/personal-automation-cli/releases/latest/download/install.sh | bash -s -- oura
```

Set `PERSONAL_AUTOMATION_CLI_INSTALL_DIR` or `INSTALL_DIR` to choose a different install directory, and set `PERSONAL_AUTOMATION_CLI_VERSION` to install a specific release.

Create an Oura personal access token at https://cloud.ouraring.com/personal-access-tokens, then set `OURA_TOKEN` or run `oura config set-token <token>`.

## Local development

```bash
cd cli/gree-cli
gofmt -w main.go main_test.go
go test ./...
go build -o gree .
./gree --help

cd ../oura-ring-cli
gofmt -w main.go main_test.go
go test ./...
go build -o oura .
./oura --help
```
