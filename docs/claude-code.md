# Wiring vaulty into a vault's Claude Code setup

This applies these snippets to a vault (Peep's `me` vault, or a vault created
from `me-template`) — a separate step from building vaulty itself. This repo
never edits a vault.

## 1. Install

Local machine:

```bash
scripts/install.sh
```

Cloud / CI bootstrap — no `gh`, no token, no Go toolchain needed (the repo
is public and `install.sh` downloads a release binary directly):

```bash
if ! command -v vaulty >/dev/null 2>&1; then
  curl -fsSL https://raw.githubusercontent.com/toppynl/vaulty/main/scripts/install.sh | bash \
    && export PATH="$HOME/.local/bin:$PATH" && echo "vaulty: installed" \
    || { echo "vaulty: install FAILED"; fail=1; }
else
  echo "vaulty: present ($(vaulty version 2>/dev/null))"
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
permission to rewrite the baseline around the block. Both flags are a
human-only decision (§8d): a skill or agent blocked by the ratchet fixes
the flagged page, or asks Peep — it never runs `--write-baseline` (bare or
with `--accept-growth`) itself. A person runs `--write-baseline` bare
periodically to tighten the baseline after real improvements, and
`--accept-growth` explicitly, after reviewing the growth it reports.

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
makes a few files worth protecting explicitly: `.vaulty-baseline.json` (the
ratchet — DESIGN.md §6.1a), `.vaulty.yml` (severity/overrides/paths config),
and `.claude/settings*.json` (the permissions/hooks below live there — an
agent that can rewrite its own settings can undo all of this). vaulty
itself closes the `--write-baseline`/`--accept-growth` bypasses at the CLI
level (DESIGN.md §6.1a, Peep's 2026-09-15 decisions); the snippets below add
defense in depth around it. They are documentation only — none of this
ships inside a vault from this repo.

**Threat model (Peep's decision, 2026-09-15, hardening round 3).** This
section hardens against a *sloppy* agent — one that runs `rm
.vaulty-baseline.json` while cleaning something up, hand-edits the file
while "fixing" a lint failure, or reaches for `--write-baseline
--accept-growth` because it's blocked and that flag is right there. It does
**not** try to hold against a genuinely adversarial agent, and deliberately
does not attempt a `PreToolUse` Bash-command guard (regex-matching
`rm`/`mv`/`>`/`tee` against the command string) to get closer to that: a CLI
running inside a real shell has a pty, `tmux`, `script`, a heredoc, a
wrapper alias, or a second shell one `bash -c` away from any regex a hook
can write — closing one phrasing just moves the bypass to the next one,
while the regex itself keeps producing false positives on legitimate
commands that happen to contain `rm`, `>` or `tee` as a substring. The
backstops below (deny-listed files, and a check that runs *after* whatever
Bash did, not by pattern-matching what it typed) hold regardless of which
shell trick got there.

**(a) Deny Edit/Write on the protected files.** Belt-and-suspenders against
a skill or agent editing any of them directly instead of through the CLI
(`.claude/settings.json`, `permissions.deny`):

```json
{
  "permissions": {
    "deny": [
      "Edit(.vaulty-baseline.json)",
      "Write(.vaulty-baseline.json)",
      "Edit(.vaulty.yml)",
      "Write(.vaulty.yml)",
      "Edit(.claude/settings*.json)",
      "Write(.claude/settings*.json)"
    ]
  }
}
```

**(b) No `Bash` command-pattern guard, on purpose.** See the threat model
above — a regex `PreToolUse` hook over `Bash` commands was tried and
dropped: a pty/tmux/heredoc detour defeats it trivially, and false
positives on ordinary commands cost more than the coverage is worth. (a)'s
deny-list plus (c)'s after-the-fact check give the same protection against
the sloppiness this is actually scoped to, without either problem.

**(c) Pre-commit backstop (recommended): `vaulty timeline lint
--check-baseline --staged`.** This is the actual second line of defense:
read-only, runs after whatever a Bash command did, and refuses if the
ratchet baseline about to be committed is higher on any page/code than the
one committed at HEAD — regardless of how it got there (a bypassed
`--write-baseline`, a hand-edit, `rm` + a fresh recompute, an override that
happened to zero out a stale entry the wrong way). `--staged` compares the
git index (what the commit will actually ship) rather than the working
copy, so staging a grown baseline and then restoring the file on disk still
gets caught. It shares `resolveWriteBaselineOld` with `--write-baseline`
itself (DESIGN.md §6.1a), so the two can never disagree about what counts
as growth:

```bash
#!/bin/sh
# .git/hooks/pre-commit (or wire into an existing pre-commit runner)
vaulty [--vault <dir>] timeline lint --check-baseline --staged
```

Always run it, unconditionally — don't gate it on a `git diff --cached
--name-only | grep` for the baseline filename first: that grep only
catches the baseline at its default path, misses a vault-configured
`lint.baselinePath` elsewhere (e.g. `sub/.vaulty-baseline.json`), and on a
miss skips the check entirely instead of failing safe. `--check-baseline
--staged` is read-only and cheap, so there is no cost to always running
it.

**(d) Skill-docs rule: agents never run `--write-baseline` or
`--accept-growth`.** Any skill or CLAUDE.md instructing an agent to use
`vaulty` must say so explicitly — writing the baseline (shrink or growth)
is a human review action (DESIGN.md §6.1a); an agent that hits a ratchet
error fixes the underlying page or asks Peep, it does not reach for either
flag to make the check pass.
