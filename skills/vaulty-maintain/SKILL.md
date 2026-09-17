---
name: vaulty-maintain
description: "Keep a markdown vault healthy with vaulty lint: check changed pages, read vault-wide findings, respect the ratchet baseline, and wire vaulty into a vault's Claude Code setup (permissions, PostToolUse hook, pre-commit check). Use for vault lint/health tasks, lint failures or hook errors, or setting up vaulty in a new vault."
---

# Maintaining a vault with vaulty

## Lint

```bash
vaulty timeline lint --changed          # pages changed vs main (or --changed=<ref>)
vaulty timeline lint <page|dir>...      # specific pages or dirs
vaulty timeline lint --warnings         # whole vault, warnings included
vaulty log lint                         # log.md headings
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
- `vaulty timeline lint --check-baseline` is read-only and safe to run
  anytime.

## Setting up vaulty in a vault

Suggest these changes to the user; they change permissions and hooks, so
don't apply them silently.

1. **Config**: `.vaulty.yml` at the vault root. Every key is optional;
   `vaulty config print` shows the effective values. The key setting is
   `dirs` (content dirs; `.` means the whole root). Set `search.analyzers`
   for stemming languages, e.g. `[en]` or `[nl, en]`.
2. **Permission**: `"Bash(vaulty:*)"` in `.claude/settings.json`
   `permissions.allow`. Also deny Edit/Write on `.vaulty-baseline.json`,
   `.vaulty.yml` and `.claude/settings*.json`.
3. **Lint on edit** (PostToolUse, matcher `Edit|Write|MultiEdit`):
   `command -v vaulty >/dev/null 2>&1 || exit 0; vaulty timeline lint --hook`.
   The files it checks are set by `lint.hook_paths` in `.vaulty.yml`.
4. **Baseline**: run `vaulty timeline lint --write-baseline` once (the user
   runs it) and commit it.
5. **Pre-commit**: `vaulty timeline lint --check-baseline --staged`.

## Search index

`vaulty search --stats` shows the cache path and freshness. The index
updates itself; use `--rebuild` only when stats look wrong. The cache
lives outside the repo; never commit it.
