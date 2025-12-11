#!/usr/bin/env bash
set -euo pipefail

# Build and install openhued.
# Usage: ./Install.sh [/install/path/openhued]

target="${1:-/usr/local/bin/openhued}"
target_dir="$(dirname "$target")"

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain not found in PATH" >&2
  exit 1
fi

build_dir="$(mktemp -d)"
cleanup() { rm -rf "$build_dir"; }
trap cleanup EXIT

echo "Building openhued..."
go build -o "$build_dir/openhued" .

install_cmd=(install -m 0755 "$build_dir/openhued" "$target")
if [ ! -w "$target_dir" ]; then
  echo "Installing to $target (requires sudo)..."
  install_cmd=(sudo "${install_cmd[@]}")
else
  echo "Installing to $target..."
fi

"${install_cmd[@]}"
echo "openhued installed at $target"
