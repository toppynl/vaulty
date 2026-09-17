---
name: vaulty-maintain
description: "Keep a markdown vault healthy with vaulty lint: check changed pages, read vault-wide findings, respect the ratchet baseline, and wire vaulty into a vault's Claude Code setup (permissions, PostToolUse hook, pre-commit check). Use for vault lint/health tasks, lint failures or hook errors, or setting up vaulty in a new vault."
---

# Maintaining a vault with vaulty

## Lint

```bash
vaulty lint --changed          # pages changed vs main (or --changed=<ref>)
vaulty lint <path|dir>...      # file paths or dirs (not bare page names)
vaulty lint --warnings         # whole vault, warnings included
vaulty log lint                # log.md headings
```

Exit 1 means errors. Fix the flagged page: each finding names a code
(e.g. `TL006`, `PG002`), the line, and what's wrong. `--json` gives
structured findings.

- **Timeline codes (TL…)**: format, order and placement of entries. Re-add
  a bad entry with `vaulty timeline append` instead of hand-fixing order.
- **Page codes (PG…)**: work material (checklists, unit/step tables, agent
  logs) above the divider, or compiled truth over the size budget. Move
  work material out and condense the compiled truth; don't move text into
  the Timeline to dodge the budget.

## Ratchet baseline (`.vaulty-baseline.json`)

Legacy debt is frozen per page, so it may shrink but not grow.

- **Never run `--write-baseline` or `--accept-growth` yourself.** Writing
  the baseline is a human decision. When the ratchet blocks you, fix the
  page or tell the user what grew and why.
- `vaulty lint --check-baseline` is read-only and safe to run
  anytime.

## Setting up vaulty in a vault

Suggest these changes to the user; they change permissions and hooks, so
don't apply them silently.

1. **Config**: `.vaulty.yml` at the vault root; the vaulty-setup skill
   walks through writing it with the user. `vaulty config print` shows
   the effective values.
2. **Permission**: `"Bash(vaulty:*)"` in `.claude/settings.json`
   `permissions.allow`. Also deny Edit/Write on `.vaulty-baseline.json`,
   `.vaulty.yml` and `.claude/settings*.json`.
3. **Lint on edit** (PostToolUse, matcher `Edit|Write|MultiEdit`):
   `command -v vaulty >/dev/null 2>&1 || exit 0; vaulty lint --hook`.
   The files it checks are set by `lint.hook_paths` in `.vaulty.yml`.
   `vaulty write`, `vaulty frontmatter` and `vaulty timeline append` run
   through Bash and don't trigger it; run `vaulty lint <path>` after them.
4. **Baseline**: run `vaulty lint --write-baseline` once (the user
   runs it) and commit it.
5. **Pre-commit**: `vaulty lint --check-baseline --staged`.

## Upgrading from `timeline lint` / `timeline read`

These moved to `vaulty lint` and `vaulty read` (same flags, no aliases).
The old spellings exit 2 (`unknown command` or `unknown flag`). A hook, pre-commit
script or vault CLAUDE.md that still says `vaulty timeline lint` or
`vaulty timeline read` needs updating; point the user at it.

## Search index

`vaulty search --stats` shows the cache path and freshness. The index
updates itself; use `--rebuild` only when stats look wrong. The cache
lives outside the repo; never commit it.
