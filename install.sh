#!/usr/bin/env bash
set -euo pipefail

repo="avyayv/personal-automation-cli"
app="${PERSONAL_AUTOMATION_CLI_APP:-${1:-}}"
version="${PERSONAL_AUTOMATION_CLI_VERSION:-}"
install_dir="${PERSONAL_AUTOMATION_CLI_INSTALL_DIR:-${INSTALL_DIR:-}}"

usage() {
  cat <<'EOF'
Install a personal-automation-cli binary.

Usage: install.sh <apple-home|gree|oura>

Environment:
  PERSONAL_AUTOMATION_CLI_APP          Binary to install when no argument is provided
  PERSONAL_AUTOMATION_CLI_VERSION      Release version or tag to install (default: latest)
  PERSONAL_AUTOMATION_CLI_INSTALL_DIR  Install directory (default: /usr/local/bin or ~/.local/bin)
  INSTALL_DIR           Fallback install directory
EOF
}

case "$app" in
  apple-home|gree|oura) ;;
  -h|--help) usage; exit 0 ;;
  "") usage >&2; exit 2 ;;
  *)
    echo "unsupported personal-automation-cli binary: $app" >&2
    usage >&2
    exit 2
    ;;
esac

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "personal-automation-cli installer requires '$1'" >&2
    exit 1
  fi
}

need curl
need install
need tar
need mktemp

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin) archive_os="macOS" ;;
  linux) archive_os="linux" ;;
  *)
    echo "unsupported OS for personal-automation-cli install: $os" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="x86_64" ;;
  aarch64|arm64) arch="arm64" ;;
  *)
    echo "unsupported architecture for personal-automation-cli install: $(uname -m)" >&2
    exit 1
    ;;
esac

if [[ -z "$version" ]]; then
  latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${repo}/releases/latest")"
  version="${latest_url##*/}"
fi
if [[ "$version" != v* ]]; then
  version="v${version}"
fi
if [[ ! "$version" =~ ^v[0-9][0-9A-Za-z._+-]*$ ]]; then
  echo "could not resolve personal-automation-cli release version: $version" >&2
  exit 1
fi

archive_version="${version#v}"
archive="${app}_${archive_version}_${archive_os}_${arch}.tar.gz"
checksums="personal-automation-cli_${archive_version}_checksums.txt"
base_url="https://github.com/${repo}/releases/download/${version}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

curl -fsSL "${base_url}/${archive}" -o "${tmpdir}/${archive}"
curl -fsSL "${base_url}/${checksums}" -o "${tmpdir}/${checksums}"

expected="$(awk -v file="$archive" '$NF == file || $NF == "./" file || $NF == "*" file { print $1; exit }' "${tmpdir}/${checksums}")"
if [[ -z "$expected" ]]; then
  echo "checksum file does not contain ${archive}" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmpdir}/${archive}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${tmpdir}/${archive}" | awk '{print $1}')"
else
  echo "personal-automation-cli installer requires 'sha256sum' or 'shasum' to verify downloads" >&2
  exit 1
fi
actual_lower="$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')"
expected_lower="$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')"
if [[ "$actual_lower" != "$expected_lower" ]]; then
  echo "checksum mismatch for ${archive}" >&2
  exit 1
fi

tar -xzf "${tmpdir}/${archive}" -C "$tmpdir"
if [[ ! -f "${tmpdir}/${app}" ]]; then
  echo "release archive does not contain a ${app} binary" >&2
  exit 1
fi

if [[ -z "$install_dir" ]]; then
  if [[ "$(id -u)" == "0" || -w "/usr/local/bin" ]]; then
    install_dir="/usr/local/bin"
  else
    install_dir="${HOME:-$PWD}/.local/bin"
  fi
fi

mkdir -p "$install_dir"
install -m 0755 "${tmpdir}/${app}" "${install_dir}/${app}"

echo "${app} ${version} installed at ${install_dir}/${app}"
if [[ -n "${GITHUB_PATH:-}" ]]; then
  echo "$install_dir" >> "$GITHUB_PATH"
fi
if ! command -v "$app" >/dev/null 2>&1; then
  echo "Add ${install_dir} to your PATH to run '${app}' from anywhere."
fi
"${install_dir}/${app}" --help >/dev/null
