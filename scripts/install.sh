#!/usr/bin/env bash
# Installs (or upgrades) the vaulty binary from a public GitHub release
# (DESIGN.md §11). No GitHub auth, no `gh`, no Go toolchain needed.
#
#   scripts/install.sh [--force]
#   curl -fsSL https://raw.githubusercontent.com/toppynl/vaulty/main/scripts/install.sh | bash
#
# Idempotent: if vaulty is already installed and its version matches the
# target release's version, exits 0 without touching anything. Rerunning
# the script is how you upgrade. --force always reinstalls.
#
# Env:
#   VAULTY_INSTALL_DIR  install directory (default: $HOME/.local/bin)
#   VAULTY_VERSION      tag to install, e.g. v0.3.1 (default: latest release)
set -euo pipefail

force=0
for arg in "$@"; do
  case "$arg" in
    --force) force=1 ;;
  esac
done

DIR="${VAULTY_INSTALL_DIR:-$HOME/.local/bin}"
repo="toppynl/vaulty"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "vaulty: unsupported arch $arch" >&2; exit 1 ;;
esac

if [ -n "${VAULTY_VERSION:-}" ]; then
  base_url="https://github.com/${repo}/releases/download/${VAULTY_VERSION}"
else
  base_url="https://github.com/${repo}/releases/latest/download"
fi

fetch() {
  # fetch <url> <outfile>
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    return 1
  fi
}

have_downloader=0
{ command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1; } && have_downloader=1

if [ "$have_downloader" -eq 1 ]; then
  asset="vaulty_${os}_${arch}.tar.gz"

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

  fetch "${base_url}/${asset}" "$tmp/${asset}"
  fetch "${base_url}/checksums.txt" "$tmp/checksums.txt"

  ( cd "$tmp"
    if command -v sha256sum >/dev/null 2>&1; then
      grep " ${asset}\$" checksums.txt | sha256sum -c -
    elif command -v shasum >/dev/null 2>&1; then
      grep " ${asset}\$" checksums.txt | shasum -a 256 -c -
    else
      echo "vaulty: no sha256sum/shasum available to verify checksums" >&2
      exit 1
    fi
  )

  tar -xzf "$tmp/${asset}" -C "$tmp" vaulty
  chmod +x "$tmp/vaulty"

  # goreleaser's main.version (and so `vaulty version`) has no leading
  # "v"; release tags do. Compare bare-to-bare against what's on PATH.
  target_version=$("$tmp/vaulty" version 2>/dev/null | tr -d '[:space:]')

  if [ "$force" -ne 1 ] && command -v vaulty >/dev/null 2>&1; then
    installed_version=$(vaulty version 2>/dev/null | tr -d '[:space:]' || true)
    if [ -n "$target_version" ] && [ "$installed_version" = "$target_version" ]; then
      echo "vaulty: already at $installed_version, nothing to do"
      exit 0
    fi
  fi

  mkdir -p "$DIR"
  # Atomic install: write alongside the target, then rename into place.
  cp "$tmp/vaulty" "$DIR/vaulty.new"
  mv -f "$DIR/vaulty.new" "$DIR/vaulty"

  echo "vaulty: installed to $DIR/vaulty ($target_version)"
elif command -v go >/dev/null 2>&1; then
  echo "vaulty: no curl/wget found; falling back to go install..."
  go install "github.com/toppynl/vaulty/cmd/vaulty@${VAULTY_VERSION:-latest}"
  echo "vaulty: installed via go install (see \$(go env GOPATH)/bin)"
else
  echo "vaulty: no curl/wget and no go toolchain found; cannot install" >&2
  exit 1
fi

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "vaulty: add $DIR to PATH if it isn't already" ;;
esac
