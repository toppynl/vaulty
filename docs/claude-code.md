# Using vaulty with Claude Code

## 1. Install the binary

```bash
curl -fsSL https://raw.githubusercontent.com/toppynl/vaulty/main/scripts/install.sh | bash
```

The repo is public: no token, `gh` or Go toolchain needed. Rerunning the
script upgrades to the latest release. `VAULTY_INSTALL_DIR` picks the
target directory (default `~/.local/bin`), and `VAULTY_VERSION` pins a
release. In a cloud or CI bootstrap, append `|| echo "vaulty: skipped"` so
a network hiccup never fails the run.

## 2. Install the plugin

This repo is also a Claude Code plugin marketplace:

```
/plugin marketplace add toppynl/vaulty
/plugin install vaulty@vaulty
```

The plugin ships:

| component | what it does |
|---|---|
| `vaulty-read` skill | find pages (`find`/`search`) and read compiled truth, sections or Timeline history |
| `vaulty-write` skill | append Timeline entries (`--touch`) and log entries instead of hand-editing |
| `vaulty-maintain` skill | lint, fix findings, respect the ratchet baseline, set vaulty up in a vault |
| `vault-reader` agent | read-only haiku subagent: returns verbatim fragments + verdict, keeps page text out of the main context |

The plugin version follows vaulty releases, so the skills match the
latest binary. The plugin ships no hooks or permissions: those change how
a vault behaves and are opt-in (below).

Without plugins, copy `skills/*` into the vault's `.claude/skills/` and
`agents/vault-reader.md` into `.claude/agents/`. Vault-specific rules
(which dirs matter, index conventions, commit policy) belong in the
vault's own CLAUDE.md or a local agent that builds on these.

## 3. Permission allowlist

In the vault's `.claude/settings.json`:

```json
{ "permissions": { "allow": ["Bash(vaulty:*)"] } }
```

## 4. Lint on edit (PostToolUse hook)

Create the ratchet baseline once first (DESIGN.md §6.1a). Without
`.vaulty-baseline.json` the ratchet is inactive and PG002 blocks every
oversized legacy page, not just pages that grew:

```bash
vaulty timeline lint --write-baseline
git add .vaulty-baseline.json && git commit -m "add ratchet baseline"
```

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

The set of files checked is `lint.hook_paths` in `.vaulty.yml` (default
`["wiki/**"]`); the hook matcher only sees tool names. A missing binary is
a silent no-op. `vaulty timeline append` runs through Bash, so appending a
Timeline line does not trigger the hook.

## 5. Hardening for agent-driven vaults

An agent with `Bash(vaulty:*)` can reach a few files worth protecting:
`.vaulty-baseline.json` (the ratchet), `.vaulty.yml` (paths, severities,
overrides) and `.claude/settings*.json` (which hold these permissions and
hooks).

**Threat model.** This hardens against a *sloppy* agent: one that deletes
the baseline while cleaning up, hand-edits it to pass lint, or reaches for
`--write-baseline --accept-growth` because it's blocked. It does not try to
stop an adversarial agent with a regex guard on Bash commands. A heredoc,
`bash -c`, tmux or an alias gets around any pattern, and the false
positives cost more than the coverage. The backstops below work whatever
the shell did.

vaulty itself enforces two boundaries:

- **Content allowlist** (DESIGN.md §3.5): only `.md` files under `dirs`,
  outside hidden folders, not excluded, checked after symlinks. An agent
  steered by chat input can't use vaulty to read `.git/config` or other
  secrets.
- **Shrink-only baseline**: `--write-baseline` refuses growth unless
  `--accept-growth` is given.

**(a) Deny direct edits** (`.claude/settings.json`):

```json
{
  "permissions": {
    "deny": [
      "Edit(.vaulty-baseline.json)", "Write(.vaulty-baseline.json)",
      "Edit(.vaulty.yml)", "Write(.vaulty.yml)",
      "Edit(.claude/settings*.json)", "Write(.claude/settings*.json)"
    ]
  }
}
```

**(b) Pre-commit backstop:**

```bash
#!/bin/sh
# .git/hooks/pre-commit
vaulty timeline lint --check-baseline --staged
```

It is read-only and fails if the staged baseline is higher than HEAD's for
any page, however that happened. Run it unconditionally: gating it on a
grep for the baseline filename misses a configured `lint.baseline_path`.

**(c) Agents never write the baseline.** Any skill or CLAUDE.md that uses
vaulty must say so; the `vaulty-maintain` skill does. An agent blocked by
the ratchet fixes the page or asks a human.
