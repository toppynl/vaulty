# vaulty

A single static Go binary for LLM-maintained markdown vaults (an
Obsidian-style knowledge base kept up to date by an agent, not a human
typing in an editor). It gives an agent a small set of tools that are
cheaper and more reliable than raw `grep`/`cat`/manual edits:

- a `## Timeline` convention — append-only history at the bottom of a
  page, kept separate from the compiled-truth prose above it
  (`read`, `timeline append`, `lint`)
- section and frontmatter edits (`write`, `frontmatter`) — change one
  section or one field without reading or rewriting the whole page
- an append-only operation log, `log.md` (`log append|last|lint`)
- `find` — fast page discovery by name/metadata, replacing `grep -r`
- `search` — ranked full-text search with source-line snippets,
  replacing "read every page to see if it's relevant"

Every read command is designed to keep an LLM agent's token usage down:
compiled truth only by default (no history unless asked for), `--max-bytes`
truncation, `--headings`/`--section` to jump straight to one part of a
page, and search results that return a couple of snippet lines instead of
whole files. See [`DESIGN.md`](DESIGN.md) for the full spec.

## Let your agent set it up

Paste this into Claude Code, Codex, Gemini CLI, OpenCode or pi, started in
the repo that holds your notes:

```text
Set up vaulty (https://github.com/toppynl/vaulty) for this repo.

1. If `vaulty version` fails, install it:
   curl -fsSL https://raw.githubusercontent.com/toppynl/vaulty/main/scripts/install.sh | bash
2. Install the vaulty skills for the agent you are:
   `vaulty setup claude --dir .` (Claude Code), `vaulty setup agents --dir .`
   (Codex, Gemini CLI, OpenCode) or `vaulty setup pi --dir .` (pi).
3. Read the vaulty-setup skill it just installed
   (.claude/skills/vaulty-setup/SKILL.md, .agents/skills/vaulty-setup/SKILL.md
   or .pi/skills/vaulty-setup/SKILL.md) and follow it: look at the repo, ask me
   what you can't tell from it, then write .vaulty.yml and verify it.

Show me .vaulty.yml before you write it. Don't change permissions, hooks
or the lint baseline without asking.
```

Restart the agent afterwards so it picks up the new skills.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/toppynl/vaulty/main/scripts/install.sh | bash
```

or, from a checkout: `scripts/install.sh`. Downloads the latest release
binary (no GitHub auth, no `gh`, no Go toolchain needed), verifies it
against `checksums.txt`, and installs to `${VAULTY_INSTALL_DIR:-$HOME/.local/bin}`.

- `VAULTY_VERSION=vX.Y.Z` pins a specific release instead of latest.
- `VAULTY_INSTALL_DIR=...` installs somewhere other than `$HOME/.local/bin`.
- Rerunning the script is the upgrade path (idempotent: a no-op if
  you're already at the target version); `--force` reinstalls
  unconditionally.
- Falls back to `go install github.com/toppynl/vaulty/cmd/vaulty@latest`
  only when neither `curl` nor `wget` is available.
- Supported: `linux`/`darwin` × `amd64`/`arm64`.

## Quick start

A vault is a directory of markdown pages, optionally with a `.vaulty.yml`
at its root (every key is optional — see [Configuration](#configuration)):

```yaml
# .vaulty.yml
version: 1
```

A page looks like this — YAML frontmatter, compiled-truth prose, then a
divider and an append-only `## Timeline`:

```markdown
---
type: system
title: Billing
updated: 2026-09-10
---

# Billing

Billing runs on Acme Pay. It invoices customers monthly and syncs
payment status back to the CRM every night.

---

## Timeline

- **2026-08-01** | acme — billing system migrated to Acme Pay.
- **2026-09-10** | acme — added nightly CRM sync.
```

```bash
vaulty lint                                        # check the whole vault
vaulty read billing                                # print the compiled truth only
vaulty timeline append billing "- **2026-09-17** | acme — added dunning emails." --touch
vaulty frontmatter set billing status=active --touch
vaulty find billing                                # discover the page by name
vaulty search "crm sync"                           # ranked full-text search with snippets
vaulty log append deploy "billing v2 shipped" --body "rolled out to all tenants"
```

## Vault conventions

- **Compiled truth vs. Timeline.** Everything above the `---` divider is
  the current state of the page, rewritten in place as things change.
  Everything in `## Timeline` (the last section, exactly one per page) is
  an append-only, oldest-first history of what happened — never edited,
  only added to.
- **Entry format.** `- **YYYY-MM-DD** | source — what`, where ` — ` is an
  em-dash (U+2014) with a space on each side. Long entries wrap onto
  continuation lines indented with 2 spaces. `vaulty timeline append`
  writes this format for you; `vaulty lint` checks it.
- **Page resolution.** Anywhere a command takes `<page>`, you can pass a
  bare name (`billing`), a path (`wiki/systems/billing.md`), or a
  `[[wikilink]]`. Bare names are resolved by basename across the
  configured content dirs and must be unique vault-wide.

Run any subcommand with `--json` for machine-readable output, or
`vaulty <cmd> --help` for the full flag list.

## Commands

> **Renamed.** `timeline read` and `timeline lint` are now the top-level
> `read` and `lint`, with the same flags and no aliases; the old spellings
> exit 2. Update hooks and pre-commit scripts that call
> `vaulty timeline lint`. `timeline` keeps only `append`.

### `read`

```bash
vaulty read <page> [--timeline] [--since DATE] [--last N] [--frontmatter] [--headings] [--section HEADING] [--max-bytes N]
```

Prints the compiled truth by default. `--timeline` (implied by
`--since`/`--last`) prints Timeline entries instead; `--headings` lists
section headings (line, line count, byte count) instead of content;
`--section "<heading text>"` prints just that section; `--max-bytes N`
caps the output and reports what was cut.

`--section` also prints `vaulty: section hash <12 hex>` on stderr (JSON:
`section.hash`, over the untruncated section), the value `write --if-hash`
checks; `--headings --json` includes a `hash` per heading. A section runs
from its heading to the next heading of the same or a higher level, and
never past the `---` divider above the Timeline. A heading at or after the
compiled-truth end (the `Timeline` heading itself, or anything below it)
isn't writable, so no hash line/field is printed for it. A heading text
that matches more than one heading exits 2 with `ambiguous section` —
same error, same matching, as `write`.

```bash
vaulty read billing
vaulty read billing --since 2026-08-01
vaulty read billing --headings
vaulty read billing --section "Billing"
```

### `lint`

```bash
vaulty lint [paths...] [--hook] [--changed[=REF]] [--warnings] [--strict] [--write-baseline] [--accept-growth] [--check-baseline [--staged]]
```

Checks Timeline format, page hygiene (an oversized or
work-material-laden compiled truth) and shard hygiene, and exits 1 if it
finds an error-severity issue. `--write-baseline` recomputes the ratchet
baseline used to allow existing debt while blocking new debt (shrink-only
by default — see `DESIGN.md` §6.1a); `--check-baseline [--staged]` is a
read-only pre-commit check that the baseline about to be committed never
grew.

```bash
vaulty lint
vaulty lint --changed        # only .md files changed vs main
vaulty lint --hook           # Claude Code PostToolUse mode (reads hook JSON on stdin)
```

### `timeline append`

```bash
vaulty timeline append <page> "<entry>" [--touch] [--dry-run]
```

Inserts a Timeline entry in date order, creating the divider and
`## Timeline` section if missing. `--touch` also bumps the frontmatter
`updated:` key to today; `--dry-run` prints the resulting block without
writing.

```bash
vaulty timeline append billing "- **2026-09-17** | acme — added dunning emails." --touch
```

### `write`

```bash
vaulty write <page> --section HEADING --if-hash HASH [--touch] [--dry-run] < section
vaulty write <page> --section HEADING --append [--touch] [--dry-run] < text
vaulty write <page> --after HEADING [--touch] [--dry-run] < section
```

Edits one compiled-truth section, with the content on stdin:

- `--section H --if-hash HASH` replaces section `H`. Stdin is the whole
  section, heading line included, at the same level (the text may change,
  so a rename is fine). `--if-hash` is required and must match the hash
  `read --section` printed; if the section changed since, it's refused.
- `--section H --append` adds stdin to the end of `H`'s region. For a
  section with subheadings that is the end of its last subsection. The
  text may only contain headings deeper than `H`. If the region's last
  line and stdin's first line are both list items, they're joined with a
  single newline so the list stays tight; otherwise a blank line separates
  them.
- `--after H` inserts a new section after `H`'s region. Stdin starts with
  its heading, at `H`'s level or deeper, with text not already used on the
  page.

Refused with exit 3: a Timeline section or anything below the divider (use
`timeline append`), a hash mismatch, any heading in the stdin content —
not just the lead heading — that duplicates a heading elsewhere on the
page (outside the section being replaced) or another heading within the
content itself, content with a standalone `---` line or a heading that
would end the section early. A heading text that matches more than one
heading exits 2.
Before writing, vaulty checks that the bytes outside the section, the
Timeline and every other heading are unchanged, and re-reads the file
right before an atomic write. `--dry-run` prints the resulting section;
`--json` reports `path`, `mode`, `heading`, `line` and the new `hash`.

```bash
vaulty read billing --section "Owners"          # stderr: vaulty: section hash 1a2b3c4d5e6f
vaulty write billing --section "Owners" --if-hash 1a2b3c4d5e6f --touch <<'EOF'
## Owners

- Finance team
EOF
vaulty write billing --after "Owners" <<'EOF'
## Risks

- Single payment provider.
EOF
```

### `frontmatter`

```bash
vaulty frontmatter get <page> [key...]
vaulty frontmatter set <page> key=value... [--touch] [--dry-run]
vaulty frontmatter add <page> <key> <value>... [--touch] [--dry-run]
vaulty frontmatter remove <page> <key> <value>... [--touch] [--dry-run]
vaulty frontmatter unset <page> <key>... [--touch] [--dry-run]
```

Reads and edits flat YAML frontmatter line by line, so quoting, flow
(`[a, b]`) or block (`- a`) list style, comments and key order are kept.
`get` prints the block or the named keys as written (`--json`: decoded
values), and exits 1 when the page has no frontmatter or none of the keys. `set` writes scalars, quoting values that need it, and refuses a
list key. `add`/`remove` edit list items: `add` skips values already
present and creates a flow list when the key is missing. A value starting
with `-` needs `--` in front. `unset` removes keys. `--touch` bumps
`updated:` only when something else changed; a call that changes nothing
prints `unchanged`.

Refused with exit 3: shapes it doesn't edit (nested maps, block scalars,
multi-line flow lists), frontmatter that isn't valid YAML, duplicate keys.
Before writing, vaulty checks that the body is byte-identical and every
key it didn't touch decodes to the same value.

```bash
vaulty frontmatter get billing status
vaulty frontmatter set billing status=active owner="[[finance]]" --touch
vaulty frontmatter add billing related "[[crm]]" "[[ledger]]"
vaulty frontmatter remove billing tags legacy
vaulty frontmatter unset billing draft
```

### `log append` / `last` / `lint`

```bash
vaulty log append <op> <title> [--body TEXT] [--date YYYY-MM-DD]
vaulty log last [-n N] [--op OP] [--since DATE]
vaulty log lint
```

A separate, flat append-only log (`log.md` by default), one heading per
operation: `## [YYYY-MM-DD] <op> | <title>`, optionally followed by a
free-form body. `op` must match `^[a-z0-9-]+$`.

```bash
vaulty log append deploy "billing v2 shipped" --body "rolled out to all tenants"
vaulty log last -n 5 --op deploy
vaulty log lint    # malformed headings fail the exit code; out-of-order dates are a warning only
```

### `find`

```bash
vaulty find [<term>...] [--limit N] [--type TYPE] [--body] [--only DIR|GLOB] [--where KEY=VALUE] [--json]
```

Ranks vault pages by term match over slug, frontmatter `title`/`aliases`/
`tags`, the `index.md` summary and the first H1 — a faster, ranked
replacement for `grep -r`/`find` as a discovery step (the fields and their
weights are configurable, `find.fields` in [Configuration](#configuration)).
`--body` also matches compiled-truth text as a lowest-weight fallback.
`--only` (repeatable or comma-separated) restricts to a directory
(`--only wiki`) or a glob (`--only "wiki/*.md"`). `--where key=value`
(repeatable, exact/case-sensitive, AND'ed — same as `search --where` below)
filters on any frontmatter key; combined with `--type`/`--only` and no
terms at all, it lists every matching page sorted by path.

```bash
vaulty find billing
vaulty find acme pay --type system
vaulty find --where status=active
```

### `search`

```bash
vaulty search <query...> [--only DIR|GLOB] [--type T] [--where KEY=VALUE] [--limit N] [--timeline] [--no-cache] [--rebuild] [--json]
vaulty search --stats [--json]
```

BM25-ranked full-text search over page content: ranked pages plus up to
two highlighted source-line snippets each, so an agent can judge
relevance without reading whole pages.

Query syntax:

| Form | Meaning |
|---|---|
| `word` | Plain term, OR'ed with other terms; pages matching more terms rank higher. |
| `"exact phrase"` | Words in this order (unstemmed). |
| `term~` / `term~1` / `term~2` | Fuzzy match (edit distance auto-picked, or pinned to 1/2). |
| `term*` | Prefix match. |
| `-term`, `-"phrase"`, `-term~`, `-term*` | Exclude pages matching it. |
| `key:value`, `key:"quoted value"` | Exact frontmatter filter on any key (`tag:` is an alias for `tags:`, configurable via `search.field_aliases`). |
| `-key:value` | Exclude pages with that frontmatter value. |

`--type T` is shorthand for `--where type=T`; `--where key=value`
(repeatable) is an exact, AND'ed frontmatter filter, and a list-valued key
matches if the list contains the value.

```bash
vaulty search "crm sync"
vaulty search delivery -hookdeck --type system
vaulty search --where status=active
vaulty search --stats
```

Results come from a per-vault cache under `$VAULTY_CACHE_DIR` (default
`os.UserCacheDir()/vaulty`, one subdirectory per vault), rebuilt fully
when its format/mapping/config hash is stale and updated incrementally
otherwise (unchanged files are skipped by size+mtime, changed ones are
re-indexed, deleted ones are dropped). If the cache can't be used for any
reason — no writable cache dir, a lock that can't be acquired, a failed
rebuild — the command falls back to indexing in memory for that call and
prints one warning to stderr; a cache problem never fails the search.
`--no-cache` always indexes in memory; `--rebuild` forces a full rebuild
and keeps using the cache afterward. `--stats` reports the cache path,
page count, index size and last-update time without touching it.

Every text field is indexed once per `search.analyzers` entry (default
`[standard]`, language-neutral) plus once with a raw, unstemmed analyzer
used for phrase/fuzzy/prefix queries. A vault in a stemmed language (e.g.
Dutch) lists it in `.vaulty.yml` for better recall on plain-word queries.
Each of the seven content fields (`title`, `aliases`, `slug`, `h1`, `index`,
`tags`, `body`) carries its own query-time boost, configurable via
`search.boosts` in [Configuration](#configuration).

### `setup`

```bash
vaulty setup claude                 # .claude/skills + .claude/agents/vault-reader.md
vaulty setup agents                 # .agents/skills (Codex, Gemini CLI, OpenCode, pi)
vaulty setup pi                     # .pi/skills
vaulty setup claude codex --global  # ~/.claude, ~/.agents
vaulty setup pi --dir ../notes --dry-run
```

Installs the skills that ship with this binary (`vaulty-read`,
`vaulty-write`, `vaulty-maintain`, `vaulty-setup`) for one or more agent
harnesses; `claude` also gets the read-only `vault-reader` agent. Targets:

| target | aliases | installs into (project / `--global`) |
|---|---|---|
| `agents` | `codex`, `gemini`, `opencode` | `.agents/skills` / `~/.agents/skills` |
| `claude` | | `.claude/skills`, `.claude/agents` / `~/.claude/...` |
| `pi` | | `.pi/skills` / `~/.pi/agent/skills` |

Files go into the vault root by default, `--dir` for another project
directory, `--global` for the home directory. Each target root gets a
`.vaulty-setup.json` manifest with a checksum per installed file, so
rerunning `setup` after upgrading vaulty updates the skills. A file that
exists but wasn't installed by `setup`, or was edited since, is a conflict:
nothing is written and the command exits 3 unless `--force` is given.
`--dry-run` shows the plan; `--json` prints it.

Pick one target per harness: pi reads both `.agents/skills` and
`.pi/skills`, and OpenCode reads both `.agents/skills` and
`.claude/skills`, so installing both shows each skill twice.

### `config print`

```bash
vaulty config print
```

Prints the effective config (defaults merged with `.vaulty.yml`), the
resolved vault root, and the config file path (empty if none was found).

## Configuration

Place `.vaulty.yml` at the vault root. Every key is optional; shown below
are the built-in defaults (see [`DESIGN.md`](DESIGN.md) §4.1 for full
detail, including `lint.overrides`):

```yaml
version: 1

# Content dirs, relative to the root. Only .md files under these dirs (no
# hidden "."-segments, not excluded) are vault content: every command,
# including path arguments, refuses anything else. See "Content boundary".
dirs: [wiki, me, now, archive]

# Globs never scanned ("dir/**" = everything below dir; else path.Match).
exclude: []

frontmatter:
  updated_key: updated          # bumped by --touch (`timeline append`, `write`, `frontmatter`)

timeline:
  heading: "## Timeline"        # exact heading line
  divider: "---"                # line directly above the heading
  entry_gap: auto                # blank lines between entries on append: auto | 0 | 1

lint:
  hook_paths: ["wiki/**"]       # files `lint --hook` checks
  page_checks:
    paths: ["wiki/**"]                          # where the page-hygiene checks apply
    compiled_truth_max_tokens: 3000              # estimate = ceil(bytes/4) above the divider
    checklist: true                              # "- [ ]" / "- [x]" above the divider = work material
    table_keywords: [unit, units, step, steps, stap, stappen]
    heading_keywords: ["agent log"]
  severity: {}                   # per-code override, e.g. {TL006: error, TL008: off}
  baseline_path: .vaulty-baseline.json   # ratchet file, relative to the vault root
  overrides: []                  # per-path severity/ratchet exemptions (see DESIGN.md §4.1)
  shard:
    type_dirs: ["wiki/*"]        # a dir is a hub-directory candidate when its parent matches this

log:
  path: log.md                   # `log append|last|lint` target, relative to the root

fields:                          # shared by find and search
  type: type                     # frontmatter key holding the page type (--type, facets)
  title: title                   # frontmatter key holding the page title (title-display fallback)

find:
  index: index.md                # vault-relative path `find` reads index summaries from
  fields:                        # scoring sources, in tie-break order; replaces this list wholesale
    - { source: slug,        weight: 100, match: token }  # exact slug match keeps a bonus
    - { source: frontmatter, key: title,   weight: 40, match: token }
    - { source: frontmatter, key: aliases, weight: 40, match: token }
    - { source: frontmatter, key: tags,    weight: 25, match: token }
    - { source: index,       weight: 20, match: token }
    - { source: h1,          weight: 20, match: token }
    - { source: body,        weight: 5,  match: token }   # --body only, last-resort fallback

search:
  analyzers: [standard]          # text analyzers, one sub-field each; standard = language-neutral.
                                  # A stemmed-language vault lists codes instead, e.g. [nl, en].
  boosts:                        # query-time weight per content field; merges onto these defaults
    title: 5.0
    aliases: 5.0
    slug: 5.0
    h1: 3.0
    index: 3.0
    tags: 2.0
    body: 1.0
  field_aliases: { tag: tags }   # query-string `key:value` field name -> frontmatter key; merges too
```

A vault-defined `find.fields` can add its own frontmatter fields — e.g. an
`id` field, matched whole (no tokenizing) so an id containing `/` still
matches exactly:

```yaml
find:
  fields:
    - { source: slug, weight: 100, match: token }
    - { source: frontmatter, key: id, weight: 60, match: exact }
    - { source: frontmatter, key: title, weight: 40, match: token }
```

An annotated copy ships at [`examples/vaulty.yml`](examples/vaulty.yml).

## Using with Claude Code / LLM agents

`vaulty` is built to sit behind an agent. It ships skills for reading
(`vaulty-read`), writing (`vaulty-write`: section and frontmatter edits,
Timeline and log entries), maintaining (`vaulty-maintain`) and configuring
(`vaulty-setup`) a vault, plus a read-only `vault-reader` subagent for
Claude Code.

- **Any harness**: `vaulty setup <claude|agents|pi>` installs them into the
  vault (see [`setup`](#setup)); the [prompt above](#let-your-agent-set-it-up)
  has an agent do the whole setup.
- **Claude Code plugin**: this repo is also a plugin marketplace:

  ```
  /plugin marketplace add toppynl/vaulty
  /plugin install vaulty@vaulty
  ```

See [`docs/claude-code.md`](docs/claude-code.md) for permissions, the
lint-on-edit hook and hardening.

## Content boundary

vaulty only reads, indexes and writes **vault content**: `.md` files under
`dirs`, with no hidden (`.`-prefixed) segment, not matched by `exclude`.
Paths are checked after following symlinks. This is an allowlist, so
`.git/config` (which may hold a token), `.github/`, `.env` files, anything
outside `dirs` and non-markdown files are unreachable through every
command. Page arguments, `find`, `search` results (including a stale
cache), `lint`, `write`, `frontmatter` and config-named files (`log.path`, `find.index`,
`lint.baseline_path`) are all covered, so an agent driven by untrusted chat
input cannot use vaulty to read repo secrets. Details: DESIGN.md §3.5.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success; lint found no error-severity finding (warnings allowed unless `--strict`) |
| 1 | Lint: at least one error-severity finding (or a warning, under `--strict`) |
| 2 | Usage error: bad flag/argument, page not found/ambiguous/outside the vault, invalid config, missing git ref (also `lint --hook` with findings) |
| 3 | Refused: append validation failed, section hash mismatch, or the page/log wasn't safe to write |
| 4 | I/O error reading or writing a file |

Every subcommand also accepts `--json` for a single machine-readable JSON
document on stdout; diagnostics go into that JSON rather than being mixed
into stdout.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

Golden CLI tests live in `internal/cli/testdata/golden/`; after an
intentional behavior change, regenerate them with:

```bash
go test ./... -update
```

## Releasing

Releases are cut by [release-please](https://github.com/googleapis/release-please):

1. Every PR title must be a conventional-commit subject (`feat:`, `fix:`,
   `feat!:` for a breaking change, ...) — enforced by `pr-title.yml`.
   Merges are squash-merged, so the PR title becomes the commit
   release-please reads.
2. On push to `main`, `release-please.yml` keeps an up-to-date "release
   PR" that accumulates `CHANGELOG.md` entries from merged PR titles.
3. Merging that release PR is the release: release-please tags `vX.Y.Z`
   and publishes a GitHub Release with the changelog notes.
4. In the same workflow run, a `goreleaser` job checks out that tag and
   builds `linux`/`darwin` × `amd64`/`arm64` binaries, attaching the
   archives and `checksums.txt` to the release.

`release.yml` (tag-push triggered) remains as a manual escape hatch for a
hand-pushed tag; it skips goreleaser if a release already exists for that
tag, so it can never double-release.

## License

MIT — see LICENSE.
