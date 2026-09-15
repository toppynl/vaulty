#!/usr/bin/env bash
# Installs the vaulty binary (DESIGN.md §11). Idempotent: exits 0 if vaulty
# is already on PATH, unless --force is given.
#
#   scripts/install.sh [--force]
#
# Install location: $VAULTY_INSTALL_DIR, default $HOME/.local/bin.
set -euo pipefail

force=0
for arg in "$@"; do
  case "$arg" in
    --force) force=1 ;;
  esac
done

if command -v vaulty >/dev/null 2>&1 && [ "$force" -ne 1 ]; then
  echo "vaulty: already installed ($(vaulty --version 2>/dev/null))"
  exit 0
fi

DIR="${VAULTY_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$DIR"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "vaulty: unsupported arch $arch" >&2; exit 1 ;;
esac

if command -v gh >/dev/null 2>&1 && [ -n "${GH_TOKEN:-}${GITHUB_TOKEN:-}" ]; then
  echo "vaulty: installing via gh release download ($os/$arch)..."
  gh release download --repo toppynl/vaulty --pattern "vaulty_${os}_${arch}.tar.gz" -O - \
    | tar -xz -C "$DIR" vaulty
  echo "vaulty: installed to $DIR/vaulty"
elif command -v go >/dev/null 2>&1; then
  echo "vaulty: gh unavailable or no token; installing via go install..."
  GOPRIVATE=github.com/toppynl go install github.com/toppynl/vaulty/cmd/vaulty@latest
  echo "vaulty: installed via go install (see \$(go env GOPATH)/bin)"
else
  echo "vaulty: no gh+token and no go toolchain found; cannot install" >&2
  exit 1
fi

echo "vaulty: add $DIR to PATH if it isn't already"
