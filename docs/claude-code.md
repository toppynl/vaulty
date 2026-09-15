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

Before wiring the hook, create the ratchet baseline once (DESIGN.md §6.1a) —
without `.vaulty-baseline.json` the ratchet is inactive and PG002 blocks
every oversized legacy page unconditionally, not just growth:

```bash
vaulty timeline lint --write-baseline
git add .vaulty-baseline.json && git commit -m "add ratchet baseline"
```

From then on, `--write-baseline` is shrink-only by default: it tightens
pages that improved but refuses to raise a page's baselined debt, so a
skill or agent blocked by the hook cannot use its `Bash(vaulty:*)`
permission to rewrite the baseline around the block. Only `--accept-growth`
raises it, and that flag is a human-only decision — skills and agents run
`vaulty timeline lint --write-baseline` bare, never with `--accept-growth`;
a person runs that explicitly, after reviewing the growth it reports.

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

`--touch`/`--dry-run` work in any position — before or after `<page>
"<entry>"` — so `vaulty timeline append <page> "<entry>" --touch` and
`vaulty timeline append --touch <page> "<entry>"` are equivalent; use
whichever reads better in a skill. The entry may also be written without
its leading `- ` (`"**YYYY-MM-DD** | ..."`); `append` adds the bullet
itself. If an entry must literally start with `--`, put it after a bare
`--` separator: `vaulty timeline append <page> -- "--literal entry"`.

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

To keep a working layer like `now/tracking/**` (a `type: work` layer that
churns constantly and isn't compiled knowledge) out of the TL006/TL008
ratchet entirely — plain warnings forever, never promoted to error for
being un-baselined, never written into `.vaulty-baseline.json` — add:

```yaml
lint:
  overrides:
    - paths: ["now/tracking/**"]
      ratchet:
        TL006: false
        TL008: false
```

`lint.overrides` is generic (any glob, any code, `severity` too, per
DESIGN.md §4.1) — this is just the one exemption Peep's vault actually
needs today.

## 8. Hardening for agent-driven vaults

An agent vault gives its own sessions broad `Bash(vaulty:*)` access, which
makes two files worth protecting explicitly: `.vaulty-baseline.json` (the
ratchet — DESIGN.md §6.1a) and `.vaulty.yml` (severity/overrides/paths
config). vaulty itself closes the `--write-baseline`/`--accept-growth`
bypasses at the CLI level (DESIGN.md §6.1a, Peep's 2026-09-15 decision);
the snippets below add defense in depth around it. They are documentation
only — none of this ships inside a vault from this repo.

**(a) Deny Edit/Write on the ratchet files.** Belt-and-suspenders against a
skill or agent editing either file directly instead of through the CLI
(`.claude/settings.json`, `permissions.deny`):

```json
{
  "permissions": {
    "deny": [
      "Edit(.vaulty-baseline.json)",
      "Write(.vaulty-baseline.json)",
      "Edit(.vaulty.yml)",
      "Write(.vaulty.yml)"
    ]
  }
}
```

**(b) `PreToolUse` hook refusing risky Bash commands.** Blocks a `Bash`
invocation that names `accept-growth`, or that touches either file via
`rm`, `mv`, `>` or `tee` — the ways to remove or rewrite them outside the
CLI's own safeguards:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "python3 -c \"import json,sys,re; d=json.load(sys.stdin); cmd=d.get('tool_input',{}).get('command',''); pat=r'accept-growth|(rm|mv|>|tee)[^&|;]*(\\\\.vaulty-baseline\\\\.json|\\\\.vaulty\\\\.yml)'; sys.exit(2) if re.search(pat, cmd) else sys.exit(0)\""
          }
        ]
      }
    ]
  }
}
```

Exit 2 from a `PreToolUse` hook blocks the tool call and feeds the message
back to the agent (same convention as the `--hook` lint mode, §3).

**(c) Optional pre-commit check: baseline never rises versus HEAD.** Since
`resolveWriteBaselineOld` already diffs against HEAD (DESIGN.md §6.1a),
this is redundant with the CLI's own refusal — but a cheap, independent
second line of defense for a commit that somehow bypassed it (e.g. a
hand-edited commit, not `--write-baseline` at all):

```bash
#!/bin/sh
# .git/hooks/pre-commit (or wire into an existing pre-commit runner)
git diff --cached --name-only | grep -qx '.vaulty-baseline.json' || exit 0
git show HEAD:.vaulty-baseline.json > /tmp/vaulty-baseline-head.json 2>/dev/null || exit 0
python3 -c "
import json, sys
old = json.load(open('/tmp/vaulty-baseline-head.json'))['pages']
new = json.load(open('.vaulty-baseline.json'))['pages']
for path, entry in new.items():
    base = old.get(path, {'tl006': 0, 'tl008': 0, 'pg002_tokens': 0})
    for code in ('tl006', 'tl008', 'pg002_tokens'):
        if entry.get(code, 0) > base.get(code, 0):
            print(f'{path} {code} would rise {base.get(code, 0)} -> {entry.get(code, 0)}')
            sys.exit(1)
"
```
