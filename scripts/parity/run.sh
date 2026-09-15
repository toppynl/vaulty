#!/usr/bin/env bash
# Parity harness (DESIGN.md §10.3): diffs `vaulty timeline dump` against the
# Node oracle (scripts/lib/timeline.mjs in the me vault) over the real vault
# at HEAD and at the pre-ASC-migration commit, plus every golden fixture
# vault. Local gate only — the vault is private, this never runs in CI.
#
# Usage: scripts/parity/run.sh
# Env:   VAULT_SRC (default /var/www/personal/me)
#        REFS      (default "HEAD 9e7bffa^")
#        PARITY_TMP (default /var/www/tmp/vaulty-parity — never /tmp)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VAULT_SRC="${VAULT_SRC:-/var/www/personal/me}"
REFS="${REFS:-HEAD 9e7bffa^}"
PARITY_TMP="${PARITY_TMP:-/var/www/tmp/vaulty-parity}"

status=0

echo "Building vaulty..."
VAULTY_BIN="$REPO_ROOT/.parity-vaulty"
(cd "$REPO_ROOT" && go build -o "$VAULTY_BIN" ./cmd/vaulty)
trap 'rm -f "$VAULTY_BIN"' EXIT

dump_and_diff() {
  local label="$1" root="$2"
  local node_out="$PARITY_TMP/${label}.node.jsonl"
  local go_out="$PARITY_TMP/${label}.go.jsonl"

  node "$REPO_ROOT/scripts/parity/oracle-dump.mjs" "$root" > "$node_out"
  "$VAULTY_BIN" timeline dump --vault "$root" > "$go_out"

  local node_norm="$PARITY_TMP/${label}.node.norm"
  local go_norm="$PARITY_TMP/${label}.go.norm"
  jq -cS . "$node_out" > "$node_norm"
  jq -cS . "$go_out" > "$go_norm"

  local files blocks
  files=$(wc -l < "$node_norm" | tr -d ' ')
  blocks=$(jq '[.blocks | length] | add // 0' "$node_out" | awk '{s+=$1} END{print s+0}')

  if diff -u "$node_norm" "$go_norm" > "$PARITY_TMP/${label}.diff"; then
    echo "OK   $label: files=$files blocks=$blocks — zero diff"
  else
    echo "FAIL $label: files=$files blocks=$blocks — diff at $PARITY_TMP/${label}.diff"
    status=1
  fi
}

mkdir -p "$PARITY_TMP"

for ref in $REFS; do
  label="ref-$(echo "$ref" | tr '/^ ' '___')"
  extract="$PARITY_TMP/extract-$label"
  rm -rf "$extract"
  mkdir -p "$extract"
  git -C "$VAULT_SRC" archive "$ref" wiki me now archive | tar -x -C "$extract"
  dump_and_diff "$label" "$extract"
  rm -rf "$extract"
done

if [ -d "$REPO_ROOT/testdata/golden" ]; then
  for vault_dir in "$REPO_ROOT"/testdata/golden/*/vault; do
    [ -d "$vault_dir" ] || continue
    case_name="$(basename "$(dirname "$vault_dir")")"
    dump_and_diff "golden-$case_name" "$vault_dir"
  done
fi

if [ "$status" -ne 0 ]; then
  echo "PARITY FAILED"
  exit 1
fi
echo "PARITY OK"
