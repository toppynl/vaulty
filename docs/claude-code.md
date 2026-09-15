# Wiring vaulty into a vault's Claude Code setup

This applies these snippets to a vault (Peep's `me` vault, or a vault created
from `me-template`) — a separate step from building vaulty itself. This repo
never edits a vault.

## 1. Install

Local machine:

```bash
scripts/install.sh
```

Cloud / CI bootstrap (needs `gh` + a token with access to the private repo),
placed after any `gh` auth setup:

```bash
if ! command -v vaulty >/dev/null 2>&1; then
  arch=$(uname -m); case "$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac
  if command -v gh >/dev/null 2>&1 && [ -n "${GH_TOKEN:-}${GITHUB_TOKEN:-}" ]; then
    mkdir -p "$HOME/.local/bin" \
      && gh release download --repo toppynl/vaulty --pattern "vaulty_linux_${arch}.tar.gz" -O - \
         | tar -xz -C "$HOME/.local/bin" vaulty \
      && export PATH="$HOME/.local/bin:$PATH" && echo "vaulty: installed" \
      || { echo "vaulty: install FAILED"; fail=1; }
  elif command -v go >/dev/null 2>&1; then
    GOPRIVATE=github.com/toppynl go install github.com/toppynl/vaulty/cmd/vaulty@latest >/dev/null 2>&1 \
      && export PATH="$PATH:$(go env GOPATH)/bin" && echo "vaulty: installed via go" \
      || { echo "vaulty: install FAILED"; fail=1; }
  else
    echo "vaulty: no gh+token or go, skipping"; fail=1
  fi
else
  echo "vaulty: present ($(vaulty --version 2>/dev/null))"
fi
```

## 2. Permission allowlist

Add to the vault's `.claude/settings.json` (`permissions.allow`):

```json
"Bash(vaulty:*)"
```

## 3. PostToolUse hook (touched-page enforcement)

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit",
        "hooks": [
          {
            "type": "command",
            "command": "command -v vaulty >/dev/null 2>&1 || exit 0; vaulty timeline lint --hook"
          }
        ]
      }
    ]
  }
}
```

The file filter (which paths get checked) lives in `vaulty` itself
(`lint.hook_paths` in `.vaulty.yml`, default `["wiki/**"]`) — the hook
matcher only sees tool names, not paths. A missing `vaulty` binary is a
silent no-op, never a blocker.

## 4. vault-reader agent

Add `Bash` to the agent's tool list. Replace "read the page, stop at the
divider" instructions with:

- `vaulty timeline read <name>` — compiled truth only (default reading).
- `vaulty timeline read <name> --since YYYY-MM-DD` or `--last N` — only when
  the question is actually about history or "wanneer" (when).

For a page over the token threshold, grep for headings and read by offset
until a `--section` flag exists (reserved, not yet built).

## 5. ingest / brief-watch / correction-capture / weekly-review

Every "append a Timeline line" instruction becomes:

```bash
vaulty timeline append <page> "- **YYYY-MM-DD** | [[P]] — reason"
```

Add `--touch` only where the skill wants `updated:` bumped — not for
backlink log lines. `vaulty timeline append` runs through the Bash tool
(not Edit/Write), so it does not trigger the PostToolUse hook — appending a
Timeline line is not a "touch" of compiled truth.

## 6. lint skill

- Touched-file check: `vaulty timeline lint --changed` (findings on the
  changed files must be fixed).
- Vault-wide health: `vaulty timeline lint` (prints the `count PG001`/
  `count PG002` summary lines; per-file findings need `--warnings` to show
  warnings too).

## 7. `.vaulty.yml`

Peep's vault matches the built-in defaults, so no config file is required.
Ship `examples/vaulty.yml` as a documented reference; copy it to the vault
root as `.vaulty.yml` only if a key needs overriding (e.g.
`lint.severity: {TL006: error}` once the legacy `| what` backlog is fixed).
