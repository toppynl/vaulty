# vaulty — design (U10 step 1)

`vaulty` is a single static Go binary for LLM-maintained markdown vaults
(Peep's `me` vault and vaults created from `me-template`). The first scope is
the `## Timeline` convention: `vaulty timeline lint|read|append`. The command
tree leaves room for `index`, a general `lint`, `migrate`, `dream` and similar.

The reference oracle is `scripts/lib/timeline.mjs` plus `scripts/timeline-asc.mjs`
in the me vault. It was verified zero-loss on all 719 vault files. Go's parse
and sort semantics are ported from it line-for-line, checked against the real
vault by a round-trip identity check (§10.3) rather than a Node-diff, and it
fixes its API gaps:

- parse never stops;
- it reports line numbers, raw date text and diagnostics;
- date ranges are compared as ranges;
- append keeps blank-line spacing consistent.

This document is the implementation spec. When it and the oracle disagree on
parse or sort behaviour, the oracle wins and this document is fixed.

---

## 1. Vault conventions this tool enforces

A page is laid out as follows:

```
---                      <- frontmatter (YAML, flat)
type: initiative
updated: 2026-09-15
---

# Title                  <- compiled truth: current state, rewritten in place
...

---                      <- divider (plain '---' line; blank lines allowed before the heading)

## Timeline              <- exactly one; the last section; nothing after it

- **2026-08-03** | Peep — kickoff held.
- **2026-09-10** | [[pim-migratie]] — cut-over moved to Q4; long entries
  wrap onto indented continuation lines.
```

Rules:

- Timeline entries are in ascending order: oldest first, newest at the bottom.
- The Timeline is append-only.
- An entry is `- **YYYY-MM-DD** | source — what`, where ` — ` is an em-dash
  (U+2014) with a space on each side.
- Long entries hard-wrap onto continuation lines indented with 2 spaces.
- Legacy date forms exist and must keep parsing:

| Form | Example |
|---|---|
| Month only | `2026-07`, `2026-03 (heel maand)` |
| Decade of a month | `2026-08-2x` |
| Day range | `2026-09-10/12` |
| Month range | `2021-02/03` |

Measured on the vault at `cf1b6ae`:

- Scope: 719 `.md` files in `wiki me now archive`, containing 140 Timeline
  blocks. All blocks parse, and all are already ascending.
- Entries: 828 in total. 501 are multi-line. Of the 2,250 continuation lines,
  2,249 are indented.
- Blank lines between entries: 0 in 657 cases, 1 in 31 cases.
- Blank lines before the first entry: always 1.
- Blank lines at the end of the block: 1, once 2.

---

## 2. Repo layout and the name

The local directory is `/var/www/lib/vault-cli`; the orchestrator moves it
later. The module is `github.com/toppynl/vaulty`, and the remote is
`github.com/toppynl/vaulty` (private).

```
cmd/vaulty/main.go        entry; os.Exit(cli.Execute(...)); version via ldflags
internal/name/            THE name: Binary="vaulty", ConfigFile, env var names
internal/cli/             cobra tree, flag parsing, human/JSON rendering, exit codes
internal/config/          .vaulty.yml schema, defaults, Load/Validate
internal/vault/           root discovery, page resolution, walking, globs
internal/doc/             raw bytes + line index + frontmatter span (no normalizing)
internal/timeline/        dates, Parse, sort/serialize (oracle port), Append
internal/lint/            TL*/PG* checks, modes, counts
internal/setup/           `vaulty setup`: install embedded skills/agents per harness (§20)
embed.go                  package vaulty: embeds skills/ and agents/ for `setup`
internal/safety/          pre-write verification shared by every writer
internal/diag/            Diag type + codes
scripts/parity/           vault round-trip live proof (package parity, step 2)
scripts/install.sh        installer (step 5)
testdata/golden/          CLI golden cases (synthetic content only, §10.2)
examples/vaulty.yml       documented defaults
.claude-plugin/           Claude Code plugin + single-plugin marketplace manifest
skills/, agents/          plugin skills (vaulty-read/-write/-maintain/-setup) and vault-reader agent
docs/claude-code.md       wiring vaulty into a vault's Claude Code setup
.goreleaser.yaml, .github/workflows/{ci,release}.yml
```

**Where the name lives.** To rename the tool:

1. Change `Binary` in `internal/name/name.go`. The config file name
   (`"." + Binary + ".yml"`) and the env vars (`VAULTY_ROOT`, `VAULTY_TODAY`)
   are derived from it.
2. Sed the name in:
   - the module path in `go.mod` and every import;
   - the directory `cmd/vaulty/`;
   - `.goreleaser.yaml` (project, binary, archive names, release repo);
   - `.github/workflows/ci.yml` (the build path);
   - `.gitignore`;
   - `examples/vaulty.yml`;
   - `scripts/install.sh`;
   - this document.
3. Run `gh repo rename`.

Go code must never hard-code the name; it uses `name.Binary` / `name.ConfigFile`.

Dependencies are `github.com/spf13/cobra` and `gopkg.in/yaml.v3`, the same
stack as `toppynl/clickup-cli`, plus `github.com/blevesearch/bleve/v2` for
`search`'s index (§19; pure Go — its `go-faiss` dependency is never used at
runtime). Everything else is the standard library. The binary is built with
CGO disabled.

---

## 3. CLI

### 3.1 Command tree

```
vaulty [--vault DIR] [--json]
├── timeline
│   ├── lint   [paths...] [--hook] [--changed[=REF]] [--warnings] [--strict] [--write-baseline]
│   ├── read   <page> [--timeline] [--since DATE] [--last N] [--frontmatter] [--headings] [--section HEADING] [--max-bytes N]
│   └── append <page> "<entry>" [--touch] [--dry-run]
├── log
│   ├── append <op> <title> [--body TEXT] [--date YYYY-MM-DD]
│   ├── last   [-n|--n N] [--op OP] [--since DATE]
│   └── lint
├── find [<term>...] [--limit N] [--type TYPE] [--body] [--only DIR|GLOB] [--where KEY=VALUE] [--json]
├── search <query...> [--only DIR|GLOB] [--type T] [--where KEY=VALUE] [--limit N] [--timeline] [--no-cache] [--rebuild] [--json]
│   └── --stats [--json]              (cache path, pages, index size, last update)
├── config print                     (step 1: effective config + root + config path)
└── version / --version
```

These commands are reserved for later. Do not implement them, but do not
take their names for anything else:

| Command | Purpose |
|---|---|
| `index` | Check or regenerate `index.md` lines |
| `lint` | Umbrella: runs `timeline lint` plus dead links, orphans, frontmatter schema, hot.md date/size checks |
| `migrate timeline-asc` | Port of `timeline-asc.mjs` on top of `SortAscending` + `safety.Verify{PreserveBlankCount}` |
| `dream extract` | Dream-routine extraction |
| `backlinks` | Backlink queries |

`timeline lint` hosts the two page checks (PG001, PG002) because both are
defined relative to the Timeline divider. The later umbrella `vaulty lint`
calls into the same `lint` package.

### 3.2 Global flags and environment

| Flag or variable | Meaning |
|---|---|
| `--vault DIR` | Vault root. Beats everything else (§4.2). |
| `--json` | One JSON document on stdout (schemas per command below). Diagnostics go into the JSON, never mixed into stdout. |
| `VAULTY_ROOT` | Root override, below `--vault`. |
| `VAULTY_CACHE_DIR` | Base cache directory for `search`'s index (default `os.UserCacheDir()/vaulty`); one subdirectory per vault (§19.3). |
| `VAULTY_TODAY=YYYY-MM-DD` | Pins "today" for `--touch`, `log append`, future-date warnings and goldens. Validated once, in `Execute` before any command runs: a set-but-invalid value (not a real `YYYY-MM-DD` date) exits 2 with `$VAULTY_TODAY: invalid date "<value>" (want YYYY-MM-DD)` rather than silently propagating a garbage "today" into whatever the command was about to do. |

Output conventions:

- stdout carries results only.
- stderr carries human diagnostics, summaries and errors, each prefixed
  `vaulty:`.
- Human output has no colour.
- All paths printed are vault-relative, slash-separated.

### 3.3 Page resolution (`<page>` argument)

1. Normalize wikilinks. If the argument starts with `[[`, strip `[[`/`]]`,
   then cut at the first `|` or `#`.
2. Path mode applies when the argument contains `/` or ends in `.md`. Try,
   in order, the first existing regular file:
   - `cwd/arg`
   - `root/arg`
   - the same two with `.md` appended, if the argument has no `.md`.
   After `filepath.EvalSymlinks`, the file must be inside the root, else
   `ErrOutside`, and it must be vault content (§3.5), else `ErrNotContent`.
3. Otherwise, name mode. Walk `config.dirs` (archive included, excludes
   applied) for files whose basename is exactly `arg + ".md"` (case-sensitive).
   - 1 match: use it.
   - 0 matches: `ErrNotFound`.
   - More than 1: `ErrAmbiguous`, and the message lists the matches.
     Filenames are unique vault-wide by convention, so ambiguity is a real
     error.

All four errors exit with code 2.

### 3.4 Exit codes

| code | meaning |
|---|---|
| 0 | success; lint found no error-severity findings (warnings allowed unless `--strict`) |
| 1 | lint: at least one error-severity finding (or warning under `--strict`) |
| 2 | usage: bad flag/arg, page not found/ambiguous/outside, invalid config, git ref missing. **Also `lint --hook` with findings** (Claude Code PostToolUse feeds stderr back to the model only on exit 2) |
| 3 | refused: append validation failed, page state unsafe to write, safety check failed, file changed during write |
| 4 | I/O error reading/writing a file |

In hook mode, an internal failure (unreadable config, parser panic caught by
`recover`) prints to stderr and exits 1, never 2. A tool bug must not be
turned into a "fix your page" instruction to the model.

### 3.5 Content boundary (allowlist)

vaulty runs as a tool for agents that can be steered by whoever talks to
them, inside a checkout whose `.git/config` may hold a token. So vaulty
decides what vault content is, and everything else is unreachable through
any command. The rule is an allowlist, not a denylist of known-bad paths
like `.git`: a new kind of sensitive file needs no new rule.

A path is **vault content** (`vault.IsContent`) only when it is:

- a `.md` file,
- under one of `config.dirs` (`.` allows the whole root),
- with no segment starting with `.` (so `.git`, `.github`, `.raw`, `.env`
  dirs and hidden files are all out, even under `dirs: ["."]`),
- not matched by `exclude`.

It is checked on the symlink-resolved path (`vault.ContentFile`), so a
symlink inside `wiki/` pointing at `.git/config`, a symlinked directory,
`wiki/../.git/config`, an absolute path or a `[[wikilink]]` spelling all
end the same way. The check applies at every read and write:

- page arguments (`timeline read|append|lint`, name and path mode), exit 2;
- `Walk`, and therefore `find`, `search`, name resolution and vault-wide lint;
- `lint --changed` and `--hook`, which silently skip non-content;
- every `search` hit before it is printed, so a stale cache entry for a
  path that is no longer content is never served.

Files named in config (`log.path`, `find.index`, `lint.baseline_path`)
are not pages, so they are exempt from the `.md`/`dirs` rule but not from
the rest. They must be relative, stay inside the root, and have no hidden
directory segment; only the file name may be hidden, as in
`.vaulty-baseline.json`. `config.Validate` checks this, and
`vault.ConfigFile` checks it again after resolving symlinks. `dirs` entries
must be relative, inside the root, and free of hidden segments.

Choices:

- **Hidden dirs, not just `.git`.** VCS metadata (`.git`, `.hg`, `.jj`),
  CI config and editor state are never knowledge. `Walk` already skipped
  them, and reads now match. A vault that keeps sources in a hidden dir
  (such as `.raw/`) reads them with its own tools, not vaulty.
- **Outside `dirs` is out.** Root-level files like `hot.md` or `README.md`
  are not content unless `dirs` includes `.`. Before this, path mode read
  any file under the root.
- **`exclude` is part of the boundary.** Excluded means "not content",
  for reads as well as scans.
- **Non-markdown is out.** `timeline read wiki/data.txt` is refused.

---

## 4. Config: `.vaulty.yml`

### 4.1 Schema and defaults

Every key is optional and absent keys keep their defaults. Slices replace
the default wholesale; the `severity`, `search.boosts` and
`search.field_aliases` maps merge (a partial override only changes the keys
it names). Unknown keys are an error (`yaml.Decoder.KnownFields(true)`). The
schema is implemented in `internal/config/config.go`; `examples/vaulty.yml`
documents every key.

```yaml
version: 1
dirs: [wiki, me, now, archive]
exclude: []
frontmatter:
  updated_key: updated
timeline:
  heading: "## Timeline"
  divider: "---"
  entry_gap: auto          # auto | 0 | 1
lint:
  hook_paths: ["wiki/**"]
  page_checks:
    paths: ["wiki/**"]
    compiled_truth_max_tokens: 3000
    checklist: true
    table_keywords: [unit, units, step, steps, stap, stappen]
    heading_keywords: ["agent log"]
  severity: {}             # e.g. {TL006: error, TL008: off}
  baseline_path: .vaulty-baseline.json  # ratchet file (§6.1a), relative to the vault root
  overrides: []            # per-path severity/ratchet exemptions (§6.1a)
log:
  path: log.md             # `log append|last|lint` target, relative to the root
fields:                    # shared by find and search (§18, §19)
  type: type                # frontmatter key holding the page type (--type, facets)
  title: title               # frontmatter key holding the page title (title-display fallback)
find:
  index: index.md          # vault-relative path `find` reads index summaries from;
                           # a missing file is skipped silently (§18)
  fields:                  # scoring sources, in tie-break order (§18.2)
    - { source: slug,        weight: 100, match: token }  # exact slug match keeps a bonus, §18.2
    - { source: frontmatter, key: title,   weight: 40, match: token }
    - { source: frontmatter, key: aliases, weight: 40, match: token }
    - { source: frontmatter, key: tags,    weight: 25, match: token }
    - { source: index,       weight: 20, match: token }
    - { source: h1,          weight: 20, match: token }
    - { source: body,        weight: 5,  match: token }   # last-resort fallback, `--body` only
search:
  analyzers: [standard]    # language analyzers text is indexed with (§19.2)
  boosts:                  # query-time weight per content field group (§19.2)
    title: 5.0
    aliases: 5.0
    slug: 5.0
    h1: 3.0
    index: 3.0
    tags: 2.0
    body: 1.0
  field_aliases: { tag: tags }  # query-string `key:value` field name -> frontmatter key (§19.1)
```

`find`'s `--only <dir|glob>` flag (repeatable/comma-separated, §18.1) has
no config key — it's a per-invocation narrowing, not a vault-wide setting,
and is implemented generically in the `vault` package (`OnlyPatterns`,
`FilterOnly`), which `search` (§19) uses for the same flag.

`fields.type`/`fields.title` name the frontmatter keys `find` and `search`
read a page's type and title from — the only two frontmatter keys either
command treats as "well-known" across both of them (`--type` filtering, the
type facet, the title-display fallback). Every other frontmatter key either
command cares about (aliases, tags, an id, or any vault-specific key) is
named directly, per field, via `find.fields`/`search.boosts` below rather
than through a shared renaming layer like this one. A config error
(missing/empty) exits 2.

`find.fields` (DESIGN.md §18.2) replaces the built-in weight table wholesale
when given — a vault that lists its own `find.fields` opts fully out of the
defaults shown above, rather than appending to them. Each entry: `source` is
`slug`, `frontmatter` (`key` required, read generically via
`page.FrontmatterValues` — a scalar or list of scalars, stringified; nested
maps and non-scalar list elements skipped), `index` (the index.md summary),
`h1` or `body` (the compiled-truth text, matched only as a last-resort
fallback for a term no other field matched, and only with `find --body` —
regardless of this field's weight). `weight` must be `> 0`. `match` is
`token` (tokenize both term and value, §18.1's contiguous-prefix-window
rule) or `exact` (the whole normalized value must equal the whole
normalized term, no tokenizing — for values like ids containing `/` that
tokenizing would otherwise split on; normalizes by trimming and
case-folding only). For one term, the single field with the highest weight
that matches counts; a weight tie breaks toward the earlier entry in this
list. `source: slug` keeps an exact-match bonus as a fixed property of that
source, not a separately configurable knob: a full token-sequence match
scores `weight`, a mere substring/prefix-window match scores `weight/2` —
the built-in 100/50 default. Every other source scores the full `weight` on
any match. A config error (unknown `source`, missing `key` for
`frontmatter`, non-positive `weight`, or an unrecognized `match`) exits 2.

`search.boosts` weights each of the seven content field groups at query
time (§19.2); a higher boost ranks a match in that field higher. It is a
query-time weight only — it never changes what gets indexed, so changing it
alone never forces a cache rebuild (§19.3). The Timeline group (searched
only with `--timeline`) is not in this map: its boost is fixed at `1.0`,
since it's off by default. `search.field_aliases` maps a query-string field
name (the `key:value` syntax, §19.1) onto the frontmatter key it actually
filters; the default `tag: tags` lets `search tag:billing` filter on the
`tags` frontmatter key. Both maps merge onto the default like
`lint.severity` above. A config error (an unknown `search.boosts` key, a
non-positive boost, or an empty `search.field_aliases` key/value) exits 2.

`search.analyzers` lists the analyzers every text field is indexed with, one
sub-field each. The default `[standard]` is language-neutral (unicode words,
lowercased, English stop words, no stemming). A vault in one or more stemmed
languages lists them by code, e.g. `[nl, en]` for a bilingual Dutch/English
vault; accepted names are `standard`, `simple` and bleve's language analyzers
`ar cjk ckb da de en es fa fi fr hi hr hu it nl no pl pt ro ru sv tr`
(`config.SearchAnalyzers`). Unknown or duplicate names are a config error.
Every extra analyzer grows the index and the query; changing the list
rebuilds the cached index (§19.3).

`lint.overrides` is a list of glob-scoped exemptions, layered on top of
`severity`/the baseline rather than replacing them:

```yaml
lint:
  overrides:
    - paths: ["now/tracking/**"]
      severity:
        TL006: warning       # same values as top-level severity: error|warning|off
      ratchet:
        TL006: false         # exclude this code from the ratchet entirely, for matching paths
        TL008: false
```

- `paths` (required, non-empty): globs, same matching as everywhere else
  (§4.3) — `MatchAny` against the page's vault-relative path.
- `severity` works exactly like the top-level `lint.severity` map, but only
  for pages matching `paths`; later-matching overrides win over earlier ones
  and over the global map.
- `ratchet`, keyed by diag code, `false` for a matching path removes that
  code from the ratchet there: `lint --write-baseline` never records debt
  for it, `applyRatchet` never promotes it to error there (even though an
  un-baselined page is normally treated as "new debt = error", §6.1a), and
  vault-mode `count baseline-stale` never counts it there. The code's
  default/overridden severity still applies — this only opts a path out of
  the ratchet, not out of linting. Absent or `true` is the default (ratchet
  applies normally). Multiple overrides may match one path; for both
  `severity` and `ratchet`, later entries win per code.

This is generic — not specific to any one path — so a vault can carve out
its own working layer (e.g. `now/tracking/**`, a `type: work` layer that
isn't compiled knowledge and churns constantly) without ever feeding that
churn into the ratchet baseline, while every other page keeps the full
TL006/TL008 ratchet. Implemented in `internal/config/config.go`
(`config.Override`) and consumed by `internal/lint/baseline.go`
(`ratchetDisabled`) and `internal/lint/lint.go` (`applyOverrides`,
`pageDebtCounts`).

The defaults equal Peep's vault, which therefore needs no config file (ship
one anyway for clarity). `me-template` has the same top-level layout
(`wiki me now archive`); its templates currently carry no Timeline
convention. Peep's decision (2026-09-15, answers Q5 in §15): me-template
adopts the Timeline convention too — a change to its templates, not to this
binary, since the defaults already handle a vault with Timelines with no
config needed (and would have handled the no-Timeline case just as well:
pages without one simply produce no TL findings).

### 4.2 Root discovery (`vault.Open(flagRoot, start)`)

`start` is the cwd, except in `lint --hook` mode, where it is the directory
of the edited file.

The root is the first match in this order:

1. `--vault`
2. `$VAULTY_ROOT`
3. the nearest ancestor of `start` containing `.vaulty.yml`
4. the nearest ancestor containing `.git` (a directory or a file, so
   worktrees count)
5. `start` itself

The root is then `EvalSymlinks`-resolved. If `.vaulty.yml` exists at the
root, it is loaded; otherwise `Default()` is used and `ConfigPath` is `""`.

Worktree note: Peep runs long vault sessions in `.claude/worktrees/<x>/`
inside the vault. Discovering the root from the edited file's path finds the
worktree's own root first, which is correct. Never use `$CLAUDE_PROJECT_DIR`
for this; it points at the main checkout.

### 4.3 Globs and walking

- A pattern ending in `/**` matches every path below that prefix.
- Any other pattern uses `path.Match` on the vault-relative slash path.
- These are implemented as `vault.MatchGlob` and `vault.MatchAny`.

`Walk` returns sorted vault-relative `.md` files under the given dirs
(default: `config.dirs`):

- missing dirs are skipped;
- directories whose name starts with `.` are skipped;
- only vault content (§3.5) is returned: hidden files, `exclude` matches,
  and symlinks whose target is not content are skipped.

---

## 5. Parser

### 5.1 `doc.Doc`

`doc.Doc` holds the raw bytes, a line index (`LineStarts`) and the
frontmatter span. It is implemented in `internal/doc/doc.go`.

Frontmatter is present when the file's first line is `---` (trailing
spaces/`\r` allowed) and a later line equals `---`. The span covers both
delimiter lines, including their newlines. An opening `---` without a
closing one sets `FMUnclosed` (FM001, warning).

Line handling:

- Lines are split on `\n`, and `\r` stays part of the line content.
- The heading, divider and forbidden-line regexes allow an optional
  trailing `\r`.
- Nothing is ever normalized.
- Line numbers are 1-based everywhere.

### 5.2 Blocks, divider, compiled truth (`timeline.Parse`)

**Blocks.** Every line matching `^` + QuoteMeta(`timeline.heading`) +
`[ \t]*\r?$` starts a block (oracle `findTimelineBlocks`).

- `Heading` is the heading line including its `\n`.
- `Body.Start` is the start of the next line, or `len(src)` if the heading
  is the last line.
- `Body.End` is the start of the next line matching `^#{1,2} ` after
  `Body.Start`, or `len(src)`.
- `NextHeadingLine` is that heading's line number, or 0 at EOF.

**Divider.** Walk up from `HeadingLine-1`, skipping blank lines
(`TrimSpace == ""`). If the first non-blank line, right-trimmed, equals
`timeline.divider` and lies after the frontmatter, that line is
`DividerLine`.

**Compiled truth** (uses the first block):

- `CompiledTruth = [Body.Start of the doc, X)`.
- X is the start of `DividerLine` when a divider exists.
- Otherwise X is the start of the first heading.
- With no block at all, X is `len(src)`.

**Page-level diagnostics:**

| Code | Condition | Reported at |
|---|---|---|
| TL001 | More than one block | Every heading after the first |
| TL002 | A block without `DividerLine` | Its heading line |
| TL003 | A block with `NextHeadingLine != 0` whose next heading is not another Timeline heading | `NextHeadingLine` |
| FM001 | `doc.FMUnclosed` | Line 1 |

### 5.3 Dates (`timeline.ParseDate`)

The regexes are tried in this order (already implemented in
`internal/timeline/date.go`). `Key` equals the oracle `parseDateToken`.
`From` and `To` are inclusive textual bounds; no calendar arithmetic is
needed, because string comparison is enough.

| form | example | Key | From | To | Precision |
|---|---|---|---|---|---|
| day range | `2026-09-10/12` | 2026-09-10 | 2026-09-10 | 2026-09-12 | day-range |
| decade | `2026-08-2x` | 2026-08-20 | 2026-08-20 | 2026-08-29 | decade |
| decade 0 | `2026-08-0x` | 2026-08-00 | 2026-08-01 | 2026-08-09 | decade |
| month range | `2021-02/03` | 2021-02-00 | 2021-02-01 | 2021-03-31 | month-range |
| full | `2026-09-10` | 2026-09-10 | = | = | day |
| month | `2026-07`, `2026-03 (heel maand)` | 2026-07-00 | 2026-07-01 | 2026-07-31 | month |
| other | `juli/augustus 2026` | not parseable | | | |

- Month-only entries sort as the first of the month: `-00` sorts before `-01`.
- `2x` sorts as day 20.
- Ranges sort by their earliest day.
- `Date.OnOrAfter(since) := To >= since`. So `2026-08` matches
  `--since 2026-08-01` and `--since 2026-08-15`, and `2026-07` does not match
  `--since 2026-08-01`.
- TL010 validity check: for day precision, `time.Parse("2006-01-02", Key)`
  must succeed. For the other forms, the month must be 01–12.

### 5.4 Block body parse (never stops)

Split `src[Body]` on `\n`, exactly like `bodyText.split('\n')`. A body that
ends in `\n` therefore yields a final `""`, which counts as a trailing blank.
Body line `i` (0-based) is file line `HeadingLine + 1 + i`.

"Blank" means `TrimSpace == ""`. The state is `entries`, `gaps`, `pending`
(a blank count) and `preamble`.

1. Consume leading blank lines into `LeadingBlanks`.
2. Loop over the remaining lines.

   **(a) Blank run.** Count it. If it reaches the end, set
   `TrailingBlanks = count` and stop. If there are no entries yet, append
   `count` empty strings to `Preamble` and continue. Otherwise add the
   count to `pending`.

   **(b) Forbidden line.** This is a line matching `^---\s*$`, `^#{3,} `, or
   `` ^``` ``. The `---` pattern is the literal oracle one, not the config
   divider. Emit TL004 (error, Blocking). Then treat the line as an
   attached line (see (d)), or as a preamble line if there are no entries
   yet. Nothing is dropped.

   **(c) Entry start** (`^- \*\*([^*]+)\*\*`). Run `ParseDate` on the
   captured text.
   - If it parses: when entries exist, push `pending` to `gaps` and reset
     `pending = 0`. Then push `Entry{Line, Date, Lines:[line]}`.
     Emit TL008 (warning) if the precision is not `day`, and TL010 (error)
     if the date is invalid.
   - If it doesn't parse: emit TL007 (error), message "unparseable date".
     With no entries yet, the line goes to `Preamble` and the diagnostic is
     Blocking. Otherwise, attach it as in (d); the diagnostic is not
     Blocking, matching the oracle's rule 3.

   **(d) Any other line.**
   - With no entries yet: append it to `Preamble` and emit TL007
     (error, Blocking), message "content before first entry".
   - Otherwise, attach it:
     1. flush `pending` empty strings into `prev.Lines`, then set `pending = 0`;
     2. append the line to `prev.Lines` and its line number to `prev.Attached`;
     3. if the line does not start with a space or tab, emit TL007 (error,
        not Blocking), message "loose line: continuation lines must be
        indented".

3. After the loop:
   - If there are no entries, emit TL009 (warning, Blocking; oracle "no
     entries found").
   - TL005 (error, not Blocking): for each `i > 0` with
     `entries[i].Key < entries[i-1].Key`, report at `entries[i].Line`:
     "out of order: KEY after PREVKEY".
   - TL006 (warning by default): format check per entry.
     - Build the joined text: `Lines[0]`, then the `TrimSpace` of each
       non-blank attached line, joined by `" "`.
     - Strip the leading `- **token**`. The remainder must match
       `^ \| (\S.*?) — (\S.*)$`.
     - The trimmed source must not start with `—`.
     - Report at `Entry.Line`: "entry must read '- **YYYY-MM-DD** | source —
       what'".

`Block.Sortable()` means no Blocking diagnostic exists. This must equal the
oracle's `parseBlock(...).ok` on every block (the parity requirement).
`Entry.Lines`, `Gaps`, `LeadingBlanks` and `TrailingBlanks` must equal the
oracle's values whenever it returns `ok`.

`Page.AllDiags()` returns page and block diagnostics sorted by
`(Line, Code)`, each with `Path` set.

### 5.5 Sort (port for parity and `migrate`)

`ClassifyOrder` is done. `SortAscending` is a line-for-line port of the
oracle's `sortAscending` + `tieBreakRanks`:

- **Descending:** reverse both the entries and the gaps.
- **Otherwise:**
  - For each distinct key, find the nearest neighbour whose key differs:
    first the neighbour before the key's first occurrence, falling back to
    the neighbour after it. Set `flip = prevKey > key`, or `nextKey < key`
    when only the fallback exists.
  - Each entry's rank is `total-1-seen` when its key flips, else `seen`.
  - Do a stable sort by `(Key, rank)`.
  - Reuse the gaps positionally (`gaps` unchanged).

### 5.6 Serialize

`SerializeBody(entries, gaps, lead, trail)` builds a string from:

1. `lead` empty strings;
2. each entry's `Lines`, with `gaps[i]` empty strings between entry `i` and
   entry `i+1`;
3. `trail` empty strings.

It then joins them with `"\n"`. For any Sortable block with no Preamble, the
round trip `SerializeBody(b.Entries, b.Gaps, b.LeadingBlanks,
b.TrailingBlanks) == src[b.Body]` is an invariant. Unit-test it over every
parity corpus file.

---

## 6. `vaulty timeline lint`

### 6.1 Checks and default severities

| code | what | default |
|---|---|---|
| TL001 | more than one `## Timeline` | error |
| TL002 | Timeline heading without the divider directly above (only blank lines between) | error |
| TL003 | a `#`/`##` heading after the Timeline (Timeline must be last) | error |
| TL004 | `###`, `---`, or code fence inside the block | error |
| TL005 | entries not ascending | error |
| TL006 | entry not `- **date** \| source — what` (checked on the joined multi-line text) | **warning** |
| TL007 | loose line: unindented non-entry line, unparseable date, or content before the first entry | error |
| TL008 | partial date (month-only, `2x`, ranges) | **warning** |
| TL009 | Timeline heading with no entries | warning |
| TL010 | impossible date (`2026-02-30`, month 13) | error |
| FM001 | frontmatter opened but never closed | warning |
| PG001 | work material above the divider | error in files mode; count in vault mode |
| PG002 | compiled truth > `compiled_truth_max_tokens` | error in files mode; count in vault mode |

TL006 and TL008 are warnings because legacy entries without a source or with
a partial date exist throughout the vault (mostly person pages written as
`| <what>`, and free-form work-log pages under `now/tracking/`). Fixing a
partial date would mean inventing information. `append` enforces the strict
form for every new entry. The `lint.severity` config can override any code:
`error`, `warning` or `off`.

**Peep's decision (2026-09-15), leading over the paragraph above where it
conflicts:** TL006 and TL008 stay warnings by default, but both — and
PG002 — are **baseline-ratcheted** (§6.1a): a page may keep its existing
debt, but may not grow it. `append` remains strict regardless (§8.1).

### 6.1a Baseline ratchet

Before this, PG002 was a hard error in files/hook mode with no way to
accept legacy debt: editing any of the 17 (now more — vault has grown)
oversized pages tripped `lint --hook` unconditionally, every time. The
ratchet fixes that for PG002 and generalizes it to TL006/TL008, which were
already warnings but had no protection against silently growing worse.

**File.** `lint.baseline_path` (default `.vaulty-baseline.json`, resolved
relative to the vault root) holds one JSON object:

```json
{"pages": {
  "wiki/vendors/sendcloud.md": {"tl006": 12, "tl008": 3, "pg002_tokens": 0},
  "wiki/systems/bluestone-api-integratie.md": {"tl006": 0, "tl008": 0, "pg002_tokens": 12517}
}}
```

Each entry is the last **accepted** count of TL006/TL008 findings and the
last accepted PG002 token count for that page. A page absent from `pages`
has an implicit `{0, 0, 0}` baseline. The file is optional; when it does not
exist, ratchet is inactive and every code behaves exactly as configured
(pre-U8 behavior) — this keeps `go test ./...` and the real vault (which
does not ship this file — generating it there is U10c, out of scope here)
unaffected until someone opts in.

**Rule, per page, per code (TL006, TL008 independently; PG002 by tokens):**

- current count/tokens `>` baseline → **error** (growth);
- current count/tokens `<=` baseline → the configured default severity
  (**warning**) — this includes shrinking, which is free: nothing needs to
  re-run `--write-baseline` just because a page got a little better;
- baseline entry absent for a path that a baseline file is otherwise active
  for → treated as baseline `{0,0,0}`: **any** finding on that page is new
  debt, hence an error. This is what makes the ratchet meaningful for
  brand-new pages, not just a one-time amnesty.

Ratchet severity is computed before `lint.severity` overrides, so an
explicit override in `.vaulty.yml` still wins.

**Writing/updating the baseline.** `vaulty timeline lint --write-baseline`
(vault mode only; no positional args) recomputes TL006/TL008 counts and the
PG002 token count for every page in `Walk()`.

**Peep's decision (2026-09-15):** with no baseline file yet, this is first
creation — the current state is written outright, growth included, exactly
as before. Once a baseline file exists, writing is **shrink-only by
default**: per page and per code independently, a lower recomputed value is
applied (the baseline tightens itself, free of ceremony) but a *higher*
recomputed value is refused — the old, lower value is kept, reported on
stderr (`<path> <code> grew <old> -> <new>: refused, kept at <old>`), and the
command exits nonzero. This closes the hole where an agent blocked by the
ratchet (e.g. via a `PostToolUse` hook) could run `--write-baseline` itself
through an already-allowed `Bash(vaulty:*)` permission and quietly rewrite
its own debt away instead of fixing it.

Deliberately accepting growth needs the separate `--accept-growth` flag: it
applies the higher value instead of refusing it, still reports every
increase on stderr (`... : accepted`), and exits 0. This is a human review
decision — something the CLI reports clearly (the baseline file's diff, plus
the stderr lines), not something an agent should ever pass on its own
judgment; the flag's own TTY gate (below) enforces that mechanically. Bare
`--write-baseline` (shrink-only) has no such gate at the CLI level — it's
safe to let an agent run — but the vault-level operational policy is
stricter still: docs/claude-code.md §8d has agents run neither flag at all,
fixing the flagged page or asking Peep instead.

**Peep's decision (2026-09-15), hardening the two bypasses above:**

1. *"old" is HEAD, not disk.* Growth is computed against the baseline as
   **committed at HEAD** (`git show HEAD:<baseline_path>`), never the
   on-disk file, whenever the vault is a git repo. This closes two ways
   the shrink-only rule above could otherwise be defeated by a plain
   `Bash(vaulty:*)`-permitted command: `rm .vaulty-baseline.json &&
   vaulty timeline lint --write-baseline` (no `old` on disk, so the naive
   rule treats it as first creation, growth included) and hand-raising a
   value on disk before running `--write-baseline` (the disk file would
   then be its own growth reference, so nothing looks like growth). A
   baseline that exists at HEAD but is missing on disk, or that is higher
   on disk than at HEAD, is refused outright (`ExitIO`) rather than
   silently reconciled — both are signs of tampering or an incomplete
   checkout, not a bootstrap. A real bootstrap (nothing at HEAD, nothing on
   disk) still writes outright, growth included, exactly as before.
   Outside git, the on-disk file is used directly (pre-existing behavior).
   Implemented as `resolveWriteBaselineOld` (`internal/cli/lint.go`) and
   `lint.ExceedsBaseline` (`internal/lint/baseline.go`). Whether HEAD has a
   commit at all, and whether the baseline path is tracked there, is
   decided by exit code (`git rev-parse --verify -q HEAD`, `git cat-file -e
   HEAD:./<path>`), not by pattern-matching `git show`'s stderr text: a
   vault with zero commits fails `git show` with "invalid object name
   'HEAD'", a message a first pass at this missed enumerating, which made
   `--write-baseline`/`--check-baseline` refuse instead of bootstrapping in
   a brand-new vault. Exit-code checks are also independent of git's output
   language (`LANG`/`LC_ALL`), which stderr-matching never was.
2. *`--accept-growth` needs a real terminal.* Because it is a plain flag,
   `Bash(vaulty:*)` already permits it — nothing stops a script or an
   agent from typing it. `--write-baseline --accept-growth` now refuses
   (`ExitUsage`) unless stdin is an interactive terminal (a character
   device), so it only ever runs from a human actually sitting at a
   prompt. The production binary itself reads no environment variable to
   decide this — that would just move the bypass to `VAULTY_STDIN_TTY=1
   vaulty ...`, which `Bash(vaulty:*)` permits exactly as freely as the
   flag it's meant to gate. Golden tests instead force the answer through
   an unexported package var (`stdinTTYOverride` in `internal/cli/tty.go`)
   that only `golden_test.go` ever sets, from a case's `env` file
   (`VAULTY_STDIN_TTY=1|0` there is a test-harness convention, not
   something the shipped binary looks at), so the `--accept-growth` merge
   logic can be exercised without a real terminal while still proving the
   gate refuses by default (a `bytes.Buffer` stdin, which the golden
   harness always uses, is never a TTY).

**Pre-commit backstop: `lint --check-baseline`.** Peep's decision
(2026-09-15, hardening round 3): this vaulty-level hardening only ever
catches a bypass that goes through `--write-baseline` itself. It cannot
stop a commit that hand-edits `.vaulty-baseline.json` directly and skips
the CLI entirely (a sloppy agent, not a malicious one — see
docs/claude-code.md §8 for the threat model this is scoped to).
`--check-baseline` is a read-only companion to `--write-baseline`: it reads
the baseline on disk and the one committed at HEAD, using the exact same
`resolveWriteBaselineOld` (so it shares every HEAD-resolution and
submap-safety fix `--write-baseline` has), and exits `ExitFindings` if the
disk copy is higher on any page/code — without writing anything. Meant to
run from a `pre-commit` hook, after staging, as the cheap independent
second check docs/claude-code.md §8c recommends.

**`--check-baseline --staged` (hardening round 4, 2026-09-15).** Plain
`--check-baseline` diffs the *working copy* against HEAD, not the index.
That's a gap in a pre-commit hook: staging a grown baseline and then
restoring the file on disk (`git add .vaulty-baseline.json` with the grown
value, then overwriting it back to the old value without re-adding) passes
`--check-baseline` with exit 0 while the commit itself still ships the
grown value from the index. `--staged` makes the comparison source the git
index instead (`git show :./<baseline_path>`, i.e. `resolveWriteBaselineOld`
called with `staged=true`), falling back to the working copy when the path
isn't staged at all — matching what a commit will actually contain rather
than whatever happens to sit on disk when the hook runs. `--write-baseline`
never takes `--staged`: it writes the working copy, so that's what it must
diff against. docs/claude-code.md §8c's pre-commit snippet always runs
`--check-baseline --staged` unconditionally now, rather than first
`grep`-gating on `git diff --cached --name-only` for the default baseline
filename — that gate silently no-ops on a vault-configured
`lint.baselinePath` elsewhere (e.g. `sub/.vaulty-baseline.json`), and the
check is cheap and read-only enough that gating it was never worth the
risk of skipping it.

A page with zero TL006/TL008 findings and no PG002-over-max finding gets no
entry at all (adding one would be a no-op: an absent page's implicit `{0,0,0}`
baseline already tolerates zero findings), which keeps the file limited to
pages that actually carry debt.

**Stale baseline (shrink gap).** Because shrinking is free and never
enforced, nothing forces a re-run of `--write-baseline` after a page
improves — its baseline entry can sit above its current debt indefinitely.
Vault-mode `lint` counts and reports this: `count baseline-stale N (pages
below baseline; run --write-baseline to tighten)`, shown whenever a baseline
file is active (even at `N=0`, mirroring the always-shown PG001/PG002 count
lines). It is informational only — it never affects severity or exit code.

A baseline entry whose path **no longer exists** in the current `Walk()` —
deleted, or renamed without updating the baseline — also counts as stale
(this only applies to a full, unrestricted vault walk: `lint` with no
path/dir args and no `--changed`, never a partial `lint <dir>`, which would
otherwise flag every baselined page outside the requested subset as
"vanished"). Each such path is also listed on its own line, hinting at a
possible rename, since there is no current page to point an agent at —
only `--write-baseline` (drops it) or a manual rename fixes it:

```
count baseline-stale 1 (pages below baseline; run --write-baseline to tighten)
  wiki/old-name.md: no longer in the vault (deleted, or renamed and not repointed) — possible rename, or run --write-baseline to drop it
```

**Per-path overrides.** `lint.overrides` (§4.1) can exempt specific paths
from the ratchet entirely (`ratchet: false` per code) while leaving their
severity as configured — e.g. `now/tracking/**` (the `type: work` layer,
which churns constantly and isn't compiled knowledge) keeps TL006/TL008 as
plain warnings forever, never promoted to error for being un-baselined,
and never written into the baseline by `--write-baseline`.

An existing baseline entry that a newly-added (or newly-matching) override
now exempts disappears from `--write-baseline`'s output the same way a
genuine shrink would (see above) — `BuildBaseline` never counts a
ratchet-disabled finding, so the fresh recompute is 0 regardless of what's
still on the page. `--write-baseline` reports this distinctly from an
ordinary shrink, one stderr line per vanishing path/code (`<path> <code>:
lint.overrides disabled the ratchet for this path — baseline entry (was
<old>) dropped, not a shrink`), so it's never mistaken for the page having
actually improved. It also reports, once per override entry, when that
entry's `paths` glob matches nothing anywhere in the vault (`lint.overrides
[<i>] paths <globs> match no files in the vault`) — almost always a typo or
a stale path left behind after a rename.

**PG001 work material.** Applies only when the path matches
`lint.page_checks.paths`. Scan the compiled-truth span line by line, skipping
lines inside fenced code blocks (between lines starting with ```` ``` ```` or
`~~~`). Report one finding per construct, at its line:

- **Checklist** (if `checklist`): `^\s*[-*+] \[[ xX]\]( |$)`.
  Message: "checklist item above the divider — move to a now/tracking/ work file".
- **Unit/step table.** A line matching `^\s*\|.*\|\s*$`, immediately
  followed by a separator row matching `^\s*\|?\s*:?-{3,}`. Split the header
  on `|`, trim each cell, lowercase it and strip `*` and backticks. The
  check fires if the first word of any cell is in `table_keywords`. Report at
  the header line.
- **Heading.** A line matching `^#{1,6} (.*)` whose lowercased text starts
  with any entry of `heading_keywords`.

**PG002 size.** Applies only when the path matches `page_checks.paths`.
`tokens = ceil(len(compiled truth bytes)/4)`, which is `lint.EstimateTokens`.
The compiled truth is measured without frontmatter, up to the divider. If
`tokens > max`, report at the first body line: "compiled truth ~N tokens >
MAX — move history to Timeline, work to a work file, then compress".

### 6.2 Modes

| Invocation | Mode | Files | PG handling |
|---|---|---|---|
| `lint` (no args) | vault | `Walk(config.dirs)` | counted: pages with ≥1 PG001, pages with PG002 |
| `lint DIR...` | vault | `Walk(DIR...)` | counted |
| `lint FILE...` | files | exactly those | findings, error |
| `lint --changed[=REF]` (default `main`) | files | union of `git diff --name-only --diff-filter=AMR REF...HEAD`, `git diff --name-only HEAD`, `git ls-files --others --exclude-standard`; kept if `.md`, under `config.dirs`, not excluded, and still existing | findings, error |
| `lint --hook` | files (hook) | one file from stdin JSON (§6.4) | findings, error |
| `lint --write-baseline` | n/a (recompute + write, then exit) | `Walk(config.dirs)`, always the whole vault | writes `lint.baseline_path` (§6.1a), shrink-only unless `--accept-growth`; no findings printed |
| `lint --check-baseline` | n/a (read-only, then exit) | reads `lint.baseline_path` on disk and at HEAD only, no `Walk` | never writes; exits `ExitFindings` if the on-disk baseline is higher than HEAD's (§6.1a "Pre-commit backstop") |

Explicit file arguments may lie outside `page_checks.paths`; such files only
get TL checks. `--changed` with an unknown ref, or run outside git, exits 2.
`--write-baseline` rejects positional paths and `--changed` together (exit
2): it always covers the whole vault, by design — a partial baseline would
silently drop every un-walked page's existing debt back to an implicit
`{0,0,0}`, turning it into a false "new violation" on the next full lint.

### 6.3 Output

Human mode (stdout):

- Errors: one line each, `path:line: CODE severity: message`, sorted by
  path then line.
- Warnings: printed only with `--warnings`.
- Vault mode then adds count lines:
  `count PG001 work-material N` and `count PG002 compiled-truth-size N`.

On stderr, human mode always prints a summary:
`vaulty: E errors, W warnings in F files`.

`--json` output:

```json
{"mode":"files|vault","files_checked":140,
 "findings":[{"code":"TL005","severity":"error","path":"wiki/x.md","line":88,"message":"..."}],
 "counts":{"PG001":0,"PG002":17},"errors":2,"warnings":150}
```

In vault mode `findings` never contains PG codes. In files mode `counts` is
`{}`. JSON includes warnings regardless of `--warnings`.

Exit code: 1 if `errors > 0`, or if `--strict` and `warnings > 0`; else 0.

### 6.4 Hook mode (`--hook`)

1. Read stdin: the Claude Code PostToolUse JSON. Take
   `tool_input.file_path`, which Edit, Write and MultiEdit all set.
2. If stdin is empty, isn't JSON, or has no `file_path`, exit 0 silently.
3. Root discovery starts at `dir(file_path)` (§4.2).
4. Continue only if the file exists, is inside the root, ends in `.md`, and
   matches `lint.hook_paths`. Otherwise exit 0 silently.
5. Lint the file in files mode.
6. If there are error findings, write them to stderr: a header line
   `vaulty: <path> has N must-fix findings:`, one finding per line, then
   `Fix these before finishing (touched page rule).` Exit 2.
7. Otherwise exit 0 with no output. Warnings are never shown in hook mode.
8. Internal failure: exit 1 (§3.4).

`vaulty timeline append` runs through the Bash tool, not Edit or Write, so
it does not trigger the hook. That is intended: appending a Timeline line is
not a touch of compiled truth.

### 6.5 Expected numbers on the real vault (step 3 acceptance)

Vault-wide `lint` on a scratch copy of `cf1b6ae` should give:

| Finding | Expected |
|---|---|
| TL003 | 1 (`wiki/stances/tool-saas-selection.md`, `## Self-hosted tools` after Timeline) |
| TL007 | 1 (an unparseable-date line, currently in `wiki/systems/ground-truth-platform.md`) |
| TL001, TL002, TL004, TL005 | 0 each |
| TL006 warnings | about 81, **now ~100 in 24 files** (fix-round A remeasurement, 2026-09-15) |
| TL008 warnings | 67 |
| `count PG001` | 0 |
| `count PG002` | 17 |

The exit code should be 1.

**TL006 deviation, explained (fix-round A, review item 8).** The vault at
`cf1b6ae` and at current `HEAD` (14 commits later) both measure ~99–100
TL006 warnings in 24 files, not ~81 in 20 — i.e. the gap predates
`cf1b6ae` too; it isn't drift since that commit. Manual inspection of every
flagged file confirms each finding is a real `| what`-only entry with no
`—` separator (the join/shape logic is correct — verified against
`entryShapeOK`/`joinedText` line by line for the largest offenders). The
biggest single contributor is `now/tracking/po-proactief.md` (27 entries):
a `now/tracking/` work-log page written entirely as free-form
`- **date** | what.` lines, a legacy-format category the original "mostly
person pages" description in §6.1 didn't anticipate — `now/tracking/` work
files evidently accrue this shape naturally and should be read as included
in TL006's warning-by-design, not as an outlier to fix. The remaining
difference is ordinary vault growth (new vendor/system pages picked up more
`| what`-only entries over time, e.g. `wiki/vendors/sendcloud.md`,
`wiki/vendors/bluestone.md`). No parser bug found; the "~81 in 20 files"
figure in §6.1 was simply a point-in-time count from before this repo
existed, not the join being wrong. The baseline ratchet (§6.1a) is exactly
built to stop this number growing further at the tail from here.

TL008 stayed exactly 67, unaffected.

---

## 7. `vaulty timeline read <page>`

**Default mode** prints the compiled-truth span with leading and trailing
blank lines trimmed, followed by one final `\n`. With `--frontmatter`, the
frontmatter span is printed verbatim first. A page without a Timeline prints
its whole body without frontmatter.

**Timeline mode** is enabled by `--timeline`, `--since`, or `--last`.

- The entries are all blocks' entries in file order.
- `--since DATE` keeps entries with `Date.OnOrAfter(since)`. It accepts
  `YYYY-MM-DD` or `YYYY-MM` (read as `-01`); anything else exits 2.
- `--last N` keeps the last N entries after the since-filter. `N < 0` exits
  2; `N = 0` means all.
- Each entry prints its `Lines` minus blank lines, verbatim. There is no
  heading and no blank line between entries, to keep token cost low.
- A page without a Timeline prints nothing and exits 0.

`read` is best effort. It always exits 0 once the page is resolved. It never
prints diagnostics in human mode; the reader agent should not spend tokens
on them.

`--json` output:

```json
{"path":"wiki/x.md",
 "frontmatter":"...",            // only with --frontmatter
 "compiled_truth":"...",         // default mode only
 "entries":[{"line":88,"date":{"raw":"2026-08","key":"2026-08-00","from":"2026-08-01","to":"2026-08-31","precision":"month"},"text":"- **2026-08** | ...\n  wrapped"}],  // timeline mode only
 "diags":[...]}
```

### 7.1 `--headings`

Lists every ATX heading (`#`..`######`) found in the page's body
(frontmatter excluded), in file order, skipping anything inside a fenced
code block (`` ``` `` or `~~~`). It is its own mode — independent of the
default/Timeline/section modes above and of `--max-bytes` — for a reader
agent to scan a large page's structure before deciding what to read next
(`internal/doc.Headings`).

Each heading's reported size is its whole section: from the heading line
through the byte before the first of (a) the next heading of level ≤ its
own, (b) a standalone `---` divider line, or (c) EOF — nested sub-headings
count towards their parent's size, matching `--section` below exactly.
The divider stop applies regardless of heading level: it closes *every*
currently-open heading, not just ones at its own level, since it's the
vault's own hard content boundary (the line directly above `## Timeline`,
§1) — without it, a page's very first heading (level 1, with no later
heading at level ≤ 1 to close it) would otherwise report a size running
all the way through the Timeline block at the bottom of the file. A `---`
inside a fenced code block is not a divider and doesn't count.
`--headings` and `--section` don't otherwise know anything about Timeline
semantics; this one rule exists purely to stop the divider from leaking
into the section above it.

Human output, one line per heading, tab-separated, no header row (kept
terse like Timeline mode): `<line>\t<### text>\t<lines>\t<bytes>`, e.g.:

```
9	## Background	9	102
14	### Background Detail	4	44
```

`--json`: `{"path":"...", "headings":[{"line":9,"level":2,"text":"Background","lines":9,"bytes":102}], "diags":[...]}`.

A page with no headings prints nothing (human) / an empty `headings` array
(json), exit 0 — same best-effort contract as the rest of `read`.
`--headings` does not combine with `--frontmatter` or `--max-bytes` (exit
2 if either is given) — neither means anything for a heading listing, and
staying silent about a flag that does nothing is worse than telling the
caller their combination doesn't apply.

### 7.2 `--section <heading text>`

Prints one section: from the matching heading's line through the byte
before the next heading of level ≤ its own, or EOF (i.e. exactly the span
`--headings` reports for it), with leading/trailing blank lines trimmed —
nested sub-headings are included, since they're part of that section.

The query is matched against heading text with any leading `#`s and
surrounding whitespace the caller included stripped first, so `--section
Background`, `--section "## Background"` and `--section "  Background  "`
all match the same heading. Matching is exact and case-sensitive on the
remaining text; the first match in file order wins when a vault has two
identically-named headings (rare — same convention as page names being
unique by convention, §3.3; a golden fixture pins first-match-wins).

No match: exit 2, with a suggestion instead of a bare "not found" whenever
one is available (`sectionNotFoundError`, `internal/cli/read.go`) — an
agent that got the heading text slightly wrong should not have to re-run
`--headings` to find out why:

1. an exact case-insensitive match (query differs from a real heading only
   in case): `no such section: "background" (case-sensitive; did you mean
   "Background"?)`;
2. else any heading whose text contains the query, or vice versa,
   case-insensitively, in file order, capped at 5: `no such section:
   "Back" (closest matches: "B", "Background", "Background Detail")`;
3. else the plain `no such section: "<query>"`.

`--headings` and `--section` are mutually exclusive (exit 2 if both are
given). `--section` combines with `--frontmatter` (frontmatter still prints
first, in full) and with `--max-bytes` (§7.3); it does **not** combine
with `--timeline`/`--since`/`--last` (exit 2 if any is given alongside
`--section`) — those flags mean something specific to the Timeline block,
and a section is not that block, so silently ignoring them (the original
behavior) risked masking a caller's mistaken assumption that they'd
somehow narrow the section output.

`--json`: adds `"section":{"line":9,"heading":"Background","text":"..."}`
in place of `compiled_truth`/`entries`.

### 7.3 `--max-bytes N`

Caps the byte size of the one piece of content `read` would otherwise print
(the compiled truth in default mode, the joined Timeline entries in
Timeline mode, or the section text in `--section` mode) at N bytes,
counting from the start and backing off to the nearest UTF-8 rune boundary
so a multi-byte character is never split. `N` must be a positive integer;
`--max-bytes 0` (or any N ≤ 0) is a usage error (exit 2) — a caller that
wants "everything" simply omits the flag, rather than every read command
needing to special-case an explicit zero.

Human mode: the (possibly truncated) content prints as usual, followed by
one marker line reporting exactly how much was cut:

```
[... 842 bytes / 21 lines truncated ...]
```

The marker itself is not counted against the N-byte budget — it is metadata
about the cut, not more of the capped content, the same way `wc -l`'s own
output isn't part of the file it measures. This is the single place the
omission is reported; there is no separate stderr notice, keeping `read`'s
"never prints diagnostics in human mode" contract (above) intact while
still surfacing exactly what a script needs (grep the marker, or parse the
byte/line counts out of it).

`--json`: when truncation actually happened, adds a sibling `"truncated":
{"bytes":842,"lines":21}` field next to whichever content field was capped
(`compiled_truth` or `section.text`); the field is entirely absent when
`--max-bytes` was not given, or was given but nothing needed cutting.
**`--max-bytes` has no effect on `--json --timeline` output** — Timeline
entries are already a structured, ordered list bounded by `--since`/
`--last`; cutting into that array mid-entry would corrupt its structure for
no benefit a JSON consumer doesn't already get by slicing the array itself.
(Human-mode `--timeline --max-bytes` is unaffected by this: there the
output is one text stream like any other, and gets capped the same way.)

`--section X --max-bytes N` cuts inside the section's own text — this is
the combination the agent workflow in the problem statement needs: pick a
section with `--headings`, then read it in size-bounded chunks.

---

## 8. `vaulty timeline append <page> "<entry>"`

**CLI arg order.** `<page> "<entry>"` are positional and `--touch`/
`--dry-run` may appear before or after them in any combination — `append
<page> "<entry>" --touch` and `append --touch <page> "<entry>"` are
equivalent. This needs help from `Execute` (`internal/cli/root.go`,
`normalizeAppendArgs`): the entry conventionally starts with `- `, which
pflag misreads as a shorthand-flag cluster regardless of
`SetInterspersed`, so `append`'s own flag parsing keeps
`SetInterspersed(false)` (flags must precede every positional) and
`normalizeAppendArgs` floats `--touch`/`--dry-run` in front of the
positionals before cobra ever parses, whichever side of them the caller
wrote. A literal `--` separator (getopt/git convention) ends this
reordering and all flag scanning — everything after it is positional
verbatim, for the rare entry that must itself start with `--`.

### 8.1 Entry validation (`ValidateEntry`; failure exits 3)

1. Trim trailing whitespace and newlines from the whole input. If it starts
   with `**`, prepend `- `. Split on `\n`.
2. Line 1 must match `^- \*\*(\d{4}-\d{2}-\d{2})\*\* \| `, and the date must
   pass `time.Parse`. Partial dates are rejected, with the message "append
   needs a full date".
3. Lines 2 and later must be non-blank and start with a space or tab. None
   of them may match an entry start or a forbidden pattern (§5.4b).
4. The joined text must pass the TL006 shape.
5. If the date is later than today (`VAULTY_TODAY` or the local date), emit
   a stderr warning `date is in the future`, but still append.

### 8.2 Preconditions (refuse with exit 3, printing the blocking diagnostics)

The page must resolve; if it doesn't, exit 2. Refuse when any of these
hold:

- TL001 (several blocks);
- TL002 (Timeline without a divider);
- TL003 (content after the Timeline);
- TL005 (the block is unsorted: never insert into an unsorted block);
- the block is not Sortable, unless it is the empty-block case (0 entries,
  Preamble contains only blank strings, no TL004);
- FM001 combined with `--touch`;
- round-trip mismatch: `SerializeBody(...) != src[Body]`. This means a
  parser bug, so refuse and say so.

### 8.3 Duplicate

If an existing entry's non-blank `Lines`, each right-trimmed, equal the new
entry's lines, the entry is `AlreadyPresent`. Write nothing and exit 0.

### 8.4 Insertion

**No block (new section).** Let `s = src`.

1. If `s` is non-empty and doesn't end in `\n`, append `\n`.
2. If the last non-blank line of `s` equals the divider and lies after the
   frontmatter: add `\n` if `s` does not already end in `\n\n` (or is
   empty), then `heading + "\n\n" + entry + "\n"`.
3. Otherwise: add `\n` under the same condition, then
   `divider + "\n\n" + heading + "\n\n" + entry + "\n"`.
4. Position: `new-section`. Existing bytes are never changed, only added
   to.

**Empty block.** Replace `src[Body]` with `"\n" + entry + "\n"`. If the
heading line had no `\n` (EOF), prepend one. Position: `end`.

**Block with entries.**

1. Let `j` be the first index whose `Key` is greater than the new key, or
   `n` if none. Same-date entries therefore go after the existing ones.
   Normally `j == n`.
2. Choose the gap style:
   - `entry_gap` is `0` or `1`: use that value.
   - `auto`: use the most frequent value in `b.Gaps`; ties go to the value
     that occurs last. With no gaps, use 0.
3. Build the new gaps:
   - `j == n`: `gaps + [style]`.
   - `j == 0`: `[style] + gaps`.
   - otherwise, with `g = gaps[j-1]`: `gaps[:j-1] + [g, g] + gaps[j:]`.
4. If the block ends at EOF and `TrailingBlanks == 0`, use 1, so the file
   ends in `\n`.
5. The new body is `SerializeBody(entries with new inserted at j, gaps',
   Leading, Trailing')`, and the new source is
   `src[:Body.Start] + newBody + src[Body.End:]`.
6. Position: `end`, `start` or `middle`.

The new entry's line number is computed from the new content.

### 8.5 `--touch`

The key is `frontmatter.updated_key`. The frontmatter is edited after the
block edit; it comes before the block, so the block offsets stay valid.

- **Key present.** Find the first frontmatter line matching `^KEY:`. Apply
  `^(KEY:[ \t]*)(["']?)(\d{4}-\d{2}-\d{2})?(["']?)(.*)$` and replace group 3
  with today, keeping the quotes and any trailing `# comment`. If the
  existing date already reads exactly today, leave the line unchanged
  (`Touched=false`).
- **Key missing.** Insert `KEY: TODAY` directly before the closing `---`.
- **No frontmatter.** Emit the stderr warning `no frontmatter; --touch
  ignored`.
- **Unclosed frontmatter.** Refused (§8.2).

**Peep's decision (2026-09-15, answers Q4 in §15).** `--touch` always sets
`updated:` to today, full stop — including overriding a date that is
already later than today. The earlier "leave unchanged if already ≥ today"
rule (meant to avoid *lowering* a future-dated value) is gone: a future
`updated:` is itself either a typo or someone pre-dating a planned change,
and either way `--touch` recording "I touched this today" as today is the
correct, boring behavior. The only no-op case left is "already reads
exactly today".

### 8.6 Safety check (`safety.Verify`; mandatory before every write, including dry-run)

Inputs: `orig`, `next`, and `Expect{RegionStart, RegionEnd, NewRegionEnd,
Added, AllowUpdatedLine, PreserveBlankCount}`.

For append:

- The region is `[Body.Start, Body.End)` of the block.
- For a new section, it is `[len(orig), len(orig))`. Newlines added at EOF
  count as inside the region.
- `Added` is the new entry's non-blank lines.

`delta` is the frontmatter length change. The checks, in order:

1. **Frontmatter.** It must be byte-equal. With `AllowUpdatedLine`, it must
   be equal line by line except for exactly one line starting with `KEY:`,
   which may be changed or added.
2. **Before the region.** `orig[FM.End:RegionStart]` must equal
   `next[FM'.End:RegionStart+delta]`.
3. **After the region.** `orig[RegionEnd:]` must equal
   `next[NewRegionEnd+delta:]`.
4. **Non-blank lines.** `multiset(nonblank(next))` must equal
   `multiset(nonblank(orig)) − {old KEY line if changed} + {new KEY line} +
   Added`.
5. **Blank count.** Only with `PreserveBlankCount` (migrate): the blank-line
   counts must be equal.
6. **Reparse of `next`.** There must be exactly one block. It must be
   Sortable and `ClassifyOrder == Ascending`, with no TL001, TL002 or TL003.
   The new entry's first line must sit at the reported line.

Any failure is `ErrRefused` ("safety check failed: <check>"), exits 3 and
writes nothing.

### 8.7 Write

1. Re-read the file. If its bytes differ from `orig`, refuse with "file
   changed during append" (exit 3).
2. Write `.<basename>.vaulty-tmp-<pid>` in the same directory with the
   original file mode, fsync it, and rename it over the original.
3. Any error exits 4.

### 8.8 Output

Human mode (stdout):

| Outcome | Output |
|---|---|
| Appended | `appended wiki/x.md:123 (end)` |
| Duplicate | `already present wiki/x.md:120` |
| `--dry-run` | The new Timeline block (heading through the end of the block) on stdout, and `vaulty: dry-run, nothing written` on stderr |

`--json` output:

```json
{"path":"wiki/x.md","line":123,"position":"end","already_present":false,
 "created_section":false,"touched":true,"dry_run":false}
```

---

## 9. Diagnostic taxonomy

The type is `diag.Diag{Code, Severity, Path, Line, Message, Blocking}`
(`internal/diag/diag.go`). There are three families:

- **Parse diagnostics** (TL004, TL007, TL009 and so on) come from
  `timeline.Parse`. `Blocking` marks the oracle abort conditions. Blocking
  diagnostics stop `append` and future `migrate`; they never stop `read` or
  `lint`.
- **Structural page diagnostics** are TL001, TL002, TL003 and FM001.
- **Lint-only diagnostics** are PG001 and PG002. Their severity depends on
  the mode.

`lint` applies the `lint.severity` overrides after collection: `off` drops
the finding, and `error`/`warning` replaces the severity. Blocking is
unaffected by overrides.

---

## 10. Tests

### 10.1 Unit tests

- **config:** defaults, overlay, unknown key, invalid values.
- **vault:** discovery precedence, worktree `.git` file, the three
  resolution modes, ambiguous, outside, Walk with excludes.
- **doc:** frontmatter variants, CRLF, no trailing newline, `LineOf`.
- **timeline:**
  - a date table: the expected keys in
    `internal/timeline/testdata/date-keys.json` are generated once by
    `node scripts/parity/date-keys.mjs` from the oracle over every distinct
    token in the vault, plus edge cases (`0x`, `3x`, `/1`, junk);
  - one parse test per rule in §5.4;
  - a port of the U4 sort cases (pure descending, mixed with same-date
    groups in descending and ascending contexts);
  - round-trip serialize;
  - `OnOrAfter`.
- **safety:** a negative test per check. Take a valid append result, then
  drop a line, change a byte before or after the region, alter frontmatter
  without the flag, and add a second `KEY:` change. Each must be refused.

### 10.2 Golden CLI tests

Location: `testdata/golden/<case>/`. Each case holds:

- `args.json`: a JSON array of CLI arguments; the harness adds
  `--vault <tmp>`;
- `vault/`: the input tree, copied to `t.TempDir()`, optionally with a
  `.vaulty.yml`;
- optional `stdin`;
- optional `env`, lines of `K=V` (always set `VAULTY_TODAY=2026-09-15`);
- `want.stdout` and `want.exit`;
- optional `want.stderr`;
- optional `want.vault/`: the full expected tree after the command, compared
  file by file.

The harness runs `cli.Execute` in-process. `go test ./... -update` rewrites
the `want.*` files.

**Fixture content is synthetic only.** The repo is private (§15 Q3), but
that doesn't relax this: the real vault is candid, so never copy real vault
pages or entries into `testdata/`. Reproduce the real shapes with invented
text:

- hard-wrapped entries with 2-space continuation lines;
- month-only, `2x` and range dates;
- blocks with gap 1;
- a stance page with a heading after its Timeline;
- a `juli/augustus`-style bullet;
- the initiative template's `{{…}}` placeholder text inside the Timeline;
- a page over 3k tokens;
- checklist, unit table and "Agent log" above the divider, plus the same
  inside a code fence and below the divider (neither of those may be
  flagged).

Minimum cases:

- **lint:** clean page; each TL code; each PG001 kind; PG002 over and under
  a custom threshold; vault-mode counts; `--warnings`; `--strict`; severity
  override; `--changed` (the harness `git init`s the tmp vault); hook match
  giving exit 2, hook non-matching path giving 0, hook garbage stdin giving
  0; `--json`.
- **read:** default; `--frontmatter`; `--timeline`; `--since` with a
  month-only entry that matches and one that doesn't; `--last`;
  `--since`+`--last`; name, path and `[[wikilink]]` resolution; ambiguous
  name giving exit 2; not found giving exit 2; `--json`.
- **append:**
  - positions: end; middle; start; same date goes after existing entries;
    gap-style 0 and 1 and `auto` tie; new section without a divider; new
    section after an existing trailing divider; empty block; file without a
    trailing newline; duplicate;
  - `--touch` variants: plain, quoted value, trailing comment, missing key,
    a future `updated:` (overwritten to today — Peep's decision, 2026-09-15;
    §8.5), no frontmatter;
  - refusals giving exit 3: unsorted, multiple blocks, missing divider,
    content after, placeholder preamble, bad format, partial date, future
    date (warning only, not a refusal); `--dry-run` that would itself be
    refused (safety.Verify runs before dry-run returns, never after; §8.6).

### 10.3 Vault round-trip check (`scripts/parity/roundtrip_test.go`, live proof)

Peep's scope call (2026-09-15): the done-criterion for parser correctness
against the real vault is a **round-trip identity check**, not a Node-oracle
diff. Over a copy of the vault at HEAD, for every `.md` file under
`wiki me now archive`:

The file is reconstructed **independently** from its parsed model, not by
copying the source and splicing an already-verified-equal region back into
itself (that construction can never disagree with the source once its own
per-block check passed — see the fix-round A finding this replaced).
`vault.Walk()` (the default config's dirs) lists the files; for each:

1. `doc.Parse` + `timeline.Parse` the file with the default config (the real
   vault ships no `.vaulty.yml`).
2. Copy the frontmatter and compiled-truth span verbatim (this tool never
   restructures either).
3. For each Timeline block: copy its heading line verbatim; if the block is
   `Sortable`, reconstruct its body from the parsed **original** (unsorted)
   `Entries`, `Gaps`, `LeadingBlanks`, `TrailingBlanks` via `SerializeBody`
   — this is the one place model-derived (not literal source-slice) bytes
   enter the reconstruction, and the only place a parser bug can hide;
   otherwise (not `Sortable`) copy the body verbatim and count it as
   skipped — not round-trippable by construction. Copy any bytes between
   blocks, or after the last one (TL003's "content after" case), verbatim.
4. The independently reconstructed file must be byte-identical to the
   source. A mismatch confined to a block's `SerializeBody` output is a
   block mismatch; any other mismatch (a span/boundary bug) is a file
   mismatch. Both are reported for every file, never short-circuited.

This is a Go test, `TestVaultRoundTrip` in package `parity`
(`scripts/parity/roundtrip_test.go` — not `internal/timeline`, corrected
2026-09-15; it lives outside the internal graph so it can freely import
`internal/vault` alongside `doc`/`timeline`/`config`), gated on the
`VAULTY_PARITY_ROOT` env var (skipped when unset, so `go test ./...` stays
green without the private vault). Run it against a scratch copy, never the
live vault:

```
git -C /var/www/personal/me worktree add /path/to/scratch HEAD   # read-only copy
VAULTY_PARITY_ROOT=/path/to/scratch go test ./scripts/parity/... -run TestVaultRoundTrip -v
```

It reports files scanned, blocks checked, blocks skipped (non-sortable),
block mismatches and file mismatches (both must be 0). On the real vault at
`HEAD` (2026-09-15): 719 files, 140 blocks checked, 0 skipped, 0 mismatches
of either kind — the `juli/augustus 2026` block once mentioned here as the
sole non-`Sortable` case now parses as `Sortable` (its stray unparseable
date attaches to the previous entry rather than blocking the block); the
file still trips TL007 (§6.5), it just no longer prevents round-tripping.

Second live proof, on the same scratch copy: `vaulty timeline append` on a
handful of representative pages (one plain, one with month-only entries, one
with no Timeline yet), each followed by `vaulty timeline lint`, must show no
lost content and the entry landing in the right (ascending) position. This
is a manual/scripted smoke test, not a `go test` target (§14 step 4's
done-when). Verified 2026-09-15 on the three page shapes above (a gap-1
page wasn't hunted down separately — none of the ~140 blocks needed it to
demonstrate the property; unit/golden coverage already exercises gap-1
explicitly, §10.1–10.2).

**Dropped from scope:** the two-ref Node-diff (`HEAD` vs `9e7bffa^`), any
`make parity` target, and the Node-oracle dumper itself. Removed
2026-09-15: `scripts/parity/oracle-dump.mjs`, `scripts/parity/run.sh` and
the hidden `vaulty timeline dump` command (`internal/timeline/dump.go`) —
Node parity is fully out of scope now, not just de-prioritized, so keeping
dead code and a hidden CLI surface around to debug a diff nobody runs isn't
worth it. If a parser disagreement ever needs manual dumping again, write a
throwaway script against `internal/timeline` directly; don't wire a command
for it.

---

## 11. Release and install

The repo is public (2026-09). Releases are versioned and changelogged by
[release-please](https://github.com/googleapis/release-please), and
binaries are built by goreleaser and downloadable with no GitHub auth.

- **Versioning.** `release-please-config.json` (release-type `go`,
  `include-v-in-tag: true`, `bump-minor-pre-major: true`,
  `packages["."].initial-version: "0.1.0"`) + `.release-please-manifest.json`
  (`"." : "0.0.0"`) drive `CHANGELOG.md` at the repo root. The two settings
  do different jobs, checked against release-please's own source
  (`src/strategies/base.ts`, `src/manifest.ts`), not guessed: with the
  manifest at exactly `"0.0.0"` release-please does **not** synthesize a
  fake prior release from it (that value is special-cased to mean "no
  release yet"), so the first release PR has no `latestRelease` to bump
  from and skips the versioning-strategy bump entirely, falling straight
  to `initial-version` — hence `0.1.0`, not a semver-bump result and not
  the library-default `1.0.0`. With no prior release sha, "commits since
  last release" is the *entire* history, so the first changelog covers
  everything merged before release-please was added, including anything
  merged while this PR is in flight (no `bootstrap-sha`/`last-release-sha`
  is set, on purpose, so later commits aren't excluded). From the second
  release onward, a real `latestRelease` exists (found via the GitHub
  release/tag, not the manifest) and normal bumping resumes:
  `bump-minor-pre-major` means a `feat`/breaking commit bumps the minor
  (not major) version while pre-1.0. Changelog sections: `feat`→Features,
  `fix`→Bug Fixes, `perf`→Performance, `refactor`→Code Refactoring,
  `docs`→Documentation; `chore`/`ci`/`test` are recorded but hidden from
  the rendered changelog.
- **PR titles are the commit history.** Merges are squash-merged, so the PR
  title becomes the single commit subject release-please reads —
  `.github/workflows/pr-title.yml` (`amannn/action-semantic-pull-request`)
  rejects a non-conventional-commit PR title (`feat:`, `fix:`, `feat!:` /
  `BREAKING CHANGE` footer for a major bump, etc.) before merge.
- **Release flow.** `.github/workflows/release-please.yml` runs on push to
  `main`: the `release-please` job maintains a standing "release PR" that
  accumulates changelog entries; merging it is the release — release-please
  tags `vX.Y.Z` and publishes a GitHub Release (with changelog notes) via
  the API. A second job, `goreleaser`, gated on that job's
  `release_created` output, checks out the new tag and runs
  `go test ./...` then goreleaser. It has to live in the same workflow
  run: a tag/release created through `GITHUB_TOKEN` does not fire other
  workflows, so a tag-triggered workflow would never see it.
  `.goreleaser.yaml` sets `changelog.disable: true` and
  `release.mode: keep-existing` — goreleaser only attaches archives +
  `checksums.txt` to the release release-please already created, it never
  writes its own notes.
- **Manual path.** `.github/workflows/release.yml` (tag-push triggered)
  still exists for a hand-pushed `vX.Y.Z` tag. It first checks
  (`gh release view`) whether a release already exists for that tag and
  skips goreleaser if so — the one case that guards against, a tag someone
  re-pushes after release-please already cut it — so it can never
  double-release.
- **Build.** goreleaser builds `linux,darwin × amd64,arm64`, CGO disabled,
  with `-trimpath`, and `main.version` set via ldflags to the tag with no
  leading `v` (so `vaulty version` prints e.g. `0.3.1` for tag `v0.3.1`).
- **Assets.** Versionless names: `vaulty_<os>_<arch>.tar.gz` plus
  `checksums.txt`, at the stable URL
  `https://github.com/toppynl/vaulty/releases/latest/download/vaulty_linux_amd64.tar.gz`
  (or `/releases/download/vX.Y.Z/...` for a pinned version).
- **CI** (`ci.yml`) runs gofmt, vet, test and build on pushes and PRs,
  unchanged.

`scripts/install.sh`:

1. Downloads `vaulty_<os>_<arch>.tar.gz` + `checksums.txt` with `curl`
   (falling back to `wget`) from `releases/latest/download/...`, or from
   `releases/download/$VAULTY_VERSION/...` when `VAULTY_VERSION` is set.
   No `gh`, no token.
2. Verifies the archive against `checksums.txt` (`sha256sum -c` or
   `shasum -a 256 -c`) before extracting.
3. Extracts the binary to a temp dir and reads its version (bare, no `v`)
   to compare against the currently-installed `vaulty version`. Equal and
   not `--force` → exits 0, nothing to do. Otherwise installs atomically
   (write alongside the target, then rename) to
   `${VAULTY_INSTALL_DIR:-$HOME/.local/bin}`. Rerunning the script is the
   upgrade path.
4. Falls back to `go install github.com/toppynl/vaulty/cmd/vaulty@<version>`
   only when neither `curl` nor `wget` is present.
5. Supports piping: `curl -fsSL
   https://raw.githubusercontent.com/toppynl/vaulty/main/scripts/install.sh
   | bash`.

For me-template, the README gets the same one-liner. No repo-access step
is needed anymore now that the repo is public (closes the former open
question about private-repo access for template users).

---

## 12. Claude Code wiring

The snippets live in `docs/claude-code.md`, written in step 5. Applying them
to the vault is a separate orchestrator unit. This repo never edits the
vault.

- **Allowlist.** Add `"Bash(vaulty:*)"` to `.claude/settings.json`
  `permissions.allow`, for both the vault and me-template.
- **PostToolUse hook** (the enforcement point for touched pages):

```json
"PostToolUse": [
  { "matcher": "Edit|Write|MultiEdit",
    "hooks": [ { "type": "command",
      "command": "command -v vaulty >/dev/null 2>&1 || exit 0; vaulty timeline lint --hook" } ] }
]
```

  The file filter lives in `vaulty` (`lint.hook_paths`), not in the matcher,
  because matchers only see tool names. A missing binary is a no-op.

- **vault-reader agent.** Add `Bash` to its tools. In its rules, replace
  "Read the page, stop at the divider" with:
  - `vaulty timeline read <name>` for compiled truth;
  - `vaulty timeline read <name> --since YYYY-MM-DD` or `--last N` only when
    the question asks for history or "wanneer".

  For large pages, grep for headings and read by offset, until a `--section`
  option exists.
- **ingest skill, step 5,** plus brief-watch, correction-capture, the weekly
  review, and every "append a Timeline line" instruction: use
  `vaulty timeline append <page> "- **YYYY-MM-DD** | [[P]] — reason"`. Add
  `--touch` only where the skill wants `updated:` bumped, which is not the
  case for backlink log lines.
- **lint skill.** In the "work material" section, run
  `vaulty timeline lint --changed` for touched pages (findings must be
  fixed) and `vaulty timeline lint` for the vault-wide counts.

---

## 13. Obsidian CLI: what we borrowed

We used the official Obsidian CLI (<https://obsidian.md/help/cli>) as
inspiration only. It needs the desktop app running, so `vaulty` does not
depend on it or wrap it. We borrowed:

- **Addressing notes by name or by path.** Obsidian has a wikilink-style
  `file=` and an exact `path=`. `<page>` in `vaulty` accepts a name, a path
  or `[[wikilink]]` (§3.3), and both styles are unambiguous.
- **Verbs** `read` and `append`, plus a later `search`/`backlinks`/`tasks`.
- **An opt-in machine format** (`format=json` in Obsidian, `--json` here),
  with human text as the default.
- **Count-only summaries** (Obsidian's `total`/`counts` flags). Vault-mode
  `lint` prints `count` lines instead of per-page lists.

We kept POSIX `--flags` and cobra subcommands (`timeline append`) rather than
`key=value` and `ns:verb`, to match `gh`, `clickup` and the permission
patterns in Claude Code (`Bash(vaulty:*)`).

---

## 14. Implementation plan (one sonnet agent, about 150k tokens)

Each step ends with `gofmt`, `go vet`, `go test ./...` green, and one
commit. Out of scope for every step: editing `/var/www/personal/me`,
creating the GitHub remote, and adding commands beyond §3.1.

1. **Foundations (about 20k).**
   - Build `vault.Open`, `Resolve` and `Walk`, and `config print`, with its
     JSON output.
   - Add `internal/cli` helpers: a vault loader, an output writer, JSON
     encoding and today resolution.
   - Write the unit tests from §10.1 for config, vault and doc.
2. **Parser and parity (about 40k).**
   - Build `timeline.Parse` (§5.2–5.4), `AllDiags`, `SortAscending` and
     `SerializeBody` (§5.5–5.6).
   - Write the date-key test (`internal/timeline/testdata/date-keys.json`,
     generated once by `scripts/parity/date-keys.mjs` from the oracle).
   - Write `scripts/parity/roundtrip_test.go`'s `TestVaultRoundTrip` (§10.3).
   - Done when: `TestVaultRoundTrip` is zero-mismatch on a scratch copy of
     the vault at HEAD. Report the file, block and mismatch/skip counts.
3. **Lint (about 30k).**
   - Build the §6 checks, all modes (`--changed`, `--hook`), `--warnings`,
     `--strict`, severity overrides and both output formats.
   - Write the golden harness (§10.2) and the lint goldens.
   - Done when: the numbers in §6.5 are reproduced on a scratch copy of the
     vault.
4. **Read, append and safety (about 40k).**
   - Build §7, §8 and `safety.Verify`, with an atomic write.
   - Write the read and append goldens and the safety negative tests.
   - Done when: all goldens are green; an append with `--dry-run` and then a
     real append on a scratch copy of 5 real pages (one with month-only
     entries, one with gap 1, one without a Timeline) gives the expected
     diff, with `lint` clean before and after.
5. **Release, install and wiring docs (about 15k).**
   - Check `.goreleaser.yaml` with
     `go run github.com/goreleaser/goreleaser/v2@latest check`, and run a
     snapshot build if feasible.
   - Write `scripts/install.sh`, `docs/claude-code.md` (the §12 snippets
     plus the §11 bootstrap block) and a short `README.md`.
6. **Hardening (about 5k).**
   - Run the full test suite and a `TestVaultRoundTrip` rerun.
   - Check that `vaulty --version` shows ldflags output.
   - Grep that no Go file hard-codes the name outside `internal/name`.
   - Report the round-trip counts, the lint numbers and any deviations.

---

## 15. Open questions (for Peep)

- **Q1 — Legacy entry format. Answered 2026-09-15.** TL006 (no source) and
  TL008 (partial date) stay warnings, not errors — backfilling would mean
  inventing information, and `| what`-only entries on person pages and
  `now/tracking/` work logs are an accepted shape, not a defect to clear.
  What changes instead: both are now baseline-ratcheted (§6.1a), so the
  existing ~100/67 legacy findings are grandfathered but a page can't
  quietly accrue more of them. `append` stays strict for every new entry
  regardless (§8.1).
- **Q2 — Oversized legacy pages. Answered 2026-09-15.** Not a grace list
  (`lint.page_checks.exclude`) — that would hide the pages from PG002
  entirely, including future growth. Instead: PG002 is baseline-ratcheted
  (§6.1a) exactly like Q1. A legacy oversized page lints as a warning as
  long as it doesn't grow past its baselined token count; growing past it
  is an error. This keeps the pressure (don't make it worse) without
  blocking every unrelated edit on a page U6 hasn't reached yet.
- **Q3 — Private repo vs. shareable. Answered 2026-09-15.** The repo stays
  private. That doesn't relax §10.2's fixture rule: golden vaults and
  `internal/timeline/testdata/date-keys.json` stay fully synthetic
  regardless — no real names, brands, amounts or date lists copied out of
  the vault, private repo or not.
- **Q4 — `--touch` date. Answered 2026-09-15.** Today, unconditionally —
  see §8.5. Not the entry's date: `--touch` records "this page was worked
  on today", independent of which date the new Timeline entry itself
  carries (an entry can legitimately be backdated).
- **Q5 — me-template convention. Answered 2026-09-15.** `me-template`
  adopts the Timeline convention: its page templates get a `## Timeline`
  section like the main vault's, rather than staying convention-free. This
  is a change to `me-template`'s own templates, not to this binary — the
  built-in defaults (§4.1) already handle it with no config needed.

---

## 16. Shard lint checks (SH001-SH005)

Hosted in `vaulty timeline lint` (there is no standalone `vaulty lint` yet —
§3.1 reserves that name; PG001/PG002 already set the precedent of hosting a
non-Timeline hygiene check here rather than waiting for the umbrella
command). Implemented in `internal/lint/shard.go`; codes in
`internal/diag/diag.go`.

### 16.1 The convention these checks enforce

The ingest skill's "Sharding" section (`.claude/skills/ingest/SKILL.md`):
when a page's compiled truth above the divider outgrows ~3k tokens and has
genuinely separable parts, it splits into a **hub** `wiki/<type>/<x>.md`
(short, current state, one line + link per child; `## Timeline` stays here)
plus **children** `wiki/<type>/<x>/<x>-<part>.md` (flat frontmatter, hub
named in `related:`, each under the token budget, no `## Timeline` of their
own). Example: `wiki/systems/bluestone-api-integratie.md` plus
`wiki/systems/bluestone-api-integratie/*.md`.

### 16.2 What counts as a hub directory

Not every subdirectory in the vault is a shard — `now/tracking/` is a
work-file layer, `archive/` holds superseded pages, and a vault might have
no shards at all. `lint.shard.type_dirs` (default `["wiki/*"]`) names the
*type* folders (`wiki/systems`, `wiki/vendors`, ...) whose immediate
subdirectories are hub-directory candidates: a directory `D` is a
hub-directory candidate iff `path.Dir(D)` matches one of `type_dirs`
(`vault.MatchAny`, the same glob rules as everywhere else). This is
deliberately the only place "what counts as a shard" is decided
(`lint.IsHubDirCandidate`), so a vault with a different layout — or none of
this convention at all (`type_dirs: []` disables every SH check) —
configures it instead of the binary hard-coding a fixed depth or name. A
page's own directory being a hub-directory candidate is also how a page is
recognized as a *child* (§16.4): `wiki/systems/x.md` itself never qualifies,
because `path.Dir("wiki/systems")` is `"wiki"`, which `"wiki/*"` does not
match.

### 16.3 Checks and severities

| code | what | severity | scope |
|---|---|---|---|
| SH001 | a hub-directory candidate has no sibling hub page `<x>.md` | error | hub directory |
| SH002 | a child's frontmatter `related:` does not list its hub | error | child page |
| SH003 | a hub page does not link one of its children (`[[child]]` anywhere in the hub file, frontmatter or body) | error | hub page |
| SH004 | a child's compiled truth exceeds `lint.page_checks.compiled_truth_max_tokens` (the same estimate/threshold as PG002) | error | child page |
| SH005 | a child page carries its own `## Timeline` | error | child page |

All five are always full findings in both files and vault mode — unlike
PG001/PG002 they are not expected to be noisy at vault scale (one hit per
broken shard, not per legacy page), so there is no vault-mode count
collapse and no SH-specific baseline ratchet: the sharding convention is
new enough that there is no legacy debt to grandfather, unlike PG002's
oversized pages that predate the 3k-token rule. SH004 is the one exception,
in severity only — see "SH004 vs. PG002" below.

**SH004 vs. PG002.** Both apply the identical token estimate and default
threshold to the same compiled-truth span, and both fire on an oversized
child under the default config (`wiki/**` is in both `page_checks.paths`
and matches a hub-directory candidate's child). This is intentional
duplication, not a bug: PG002 is the generic "this page has outgrown a
single page" signal (its fix could be *shard it*), while SH004 is the
shard-specific signal that a page *already inside* a shard needs to split
further or shed history/work — a different next action worth its own line.
`lint.severity: {SH004: off}` turns off the shard-specific one for a vault
that finds it redundant with PG002.

Because the two measure the exact same number for the exact same page,
SH004's *severity* follows PG002's existing ratchet rather than keeping a
second, SH-specific one: `checkShardChild` reads the page's baselined
`pg002_tokens` (via the same `pg002Severity` PG002 itself calls) and
downgrades SH004 to a warning under the identical condition — tokens at or
below the baselined value. SH004 never writes its own baseline entry
(`--write-baseline` only ever records `pg002_tokens`, driven by PG002's
own `page_checks.paths` match); it only reads the one PG002 already
maintains. Without a baseline (ratchet inactive) or without an entry for
that page (never accepted as oversized), SH004 is an error, same as
before. This keeps a child that's already been accepted as oversized via
`--write-baseline` from flipping back to an error — and a `--hook` exit
2 — purely because SH004 restates a number PG002 already downgraded.

`lint.severity` and the per-path `severity` half of `lint.overrides` apply
to SH001-SH005 exactly as they do to every other code (both are generic
over `diag.Code`, requiring no SH-specific plumbing). The `ratchet` half of
`lint.overrides` has no effect on SH001-SH005: they are never ratcheted, so
there is nothing to exempt.

### 16.4 `lint <path>`, `--changed`, `--hook`

- **Child-level checks (SH002, SH004, SH005)** need no I/O beyond the page
  already being linted: `internal/lint.checkShardChild` runs inside the
  normal per-file `CheckPage` pass, so they appear for exactly the files a
  given `lint` invocation already covers — a single `--hook` file, a
  `--changed` list, an explicit `lint <path>`, or a full vault walk — with
  no extra scoping logic.
- **Hub-level checks (SH001, SH003)** need a directory listing (is there a
  sibling hub file; does the hub link every file physically present in the
  directory), so they run once per `Run()` call via
  `internal/lint.CheckShardDirs(v, files)` rather than inside the per-file
  loop. `files` is exactly the file list that call already resolved (whole
  vault, a directory subset, explicit files, `--changed`, or the one
  `--hook` file) — a hub directory's findings are kept only when that scope
  *touches* it: its hub file, or at least one of its children, is in
  `files`. This makes `vaulty timeline lint wiki/systems/x.md` or a
  `--hook` run on one child feel exactly as scoped as every other check,
  instead of a single-file lint suddenly reporting on hub directories
  elsewhere in the vault. A hub directory's children, for this check, are
  whatever `.md` files sit directly in it *on disk* — not filtered to
  `files` — since SH001/SH003 are about the directory's actual shape, not
  about which of its files happen to be part of the current lint run.
- `lint --write-baseline` / `--check-baseline` are unaffected: SH001-SH005
  carry no baseline entries of their own (§16.3) — SH004 reads PG002's
  entry for the same page at check time, but nothing about
  `--write-baseline`/`--check-baseline` itself changes for SH004.
- A hub-level finding (SH003) reached only through a child in scope — the
  hub file itself is not among `files`, only one of its children is —
  names that child in the message (`... (via child <path>)`), so a
  `--hook`/`--changed` run on one child doesn't read as an unprompted hit
  on an unrelated hub file.

### 16.5 Link-target normalization (SH002/SH003)

SH002 (does a child's `related:` name its hub) and SH003 (does the hub
link each child) both compare `[[...]]` wikilink targets against a plain
page name, but a real link can spell that target several ways: an
escaped-pipe alias inside a markdown table cell (`[[x\|alias]]` — the pipe
is escaped so it doesn't end the table cell, which otherwise leaves a
trailing backslash on the captured target), a path-form link
(`[[type/hub/x]]`), an explicit `.md` suffix (`[[x.md]]`), or different
casing (`[[X]]`). `internal/lint.normalizeWikilinkTarget` is the single
place that collapses all four to the same bare, lowercase name; both
`wikilinkTargets` (SH003, hub → children) and `relatedListsHub` (SH002,
child → hub) go through it, so the two directions can never disagree on
what counts as the same link. `relatedField`'s own scan (finding the
`related:` block in frontmatter) additionally treats a column-0 `- ` YAML
list item as part of the block, not just indented or blank continuation
lines — `related:` followed by an unindented list is valid YAML — and
matches the `related:` key itself exactly (not `related_extra:` or any
other key sharing the prefix).

---

## 17. `vaulty log append|last|lint`

A second, separate append-only convention from Timeline blocks (§1): one
file per vault (`log.md` by default, `log.path` in `.vaulty.yml`, resolved
relative to the root — `internal/config.Log`), holding a flat operation
log rather than per-page history. Header format, fixed:

```
## [YYYY-MM-DD] <op> | <title>
<body>            (optional, free-form, any number of lines)
```

`log append` always adds at the end of the file (append-only). That is
**not** the same as "chronological order": the real vault's `log.md` has
~41 points where a later-in-file entry's date is earlier than the entry
before it (added by hand or other tooling, not exclusively by this
command). So file order must never be assumed to equal date order — `log
last` sorts before taking the tail (§17.3) and `log lint` reports the
gap as a warning (§17.4), rather than either command silently trusting
file position. Implemented in `internal/vaultlog` (parse/format/validate/
sort, package-level, no vault dependency) plus `internal/cli/log.go` +
`internal/cli/logrun.go` (command tree + CLI glue, mirroring the
`timeline` command's file split).

### 17.1 Parsing (`vaultlog.Parse`)

Never stops and never errors, mirroring `timeline.Parse`'s "every problem
becomes a diagnostic, no line is dropped" stance (§5, chosen here because
the real vault's `log.md` has ~1300 headings written by hand/other tooling
over time, not exclusively by this command — a handful can be malformed
and `log last`/`log lint` must survive that, not crash).

A line is a candidate heading iff it starts with `"## "`. It parses as a
well-formed `Entry` iff, in order: it has a `[...]` right after `"## "`;
the bracketed text is a valid `time.Parse("2006-01-02", ...)` date; a
single space follows `]`; the remainder contains the literal separator
`" | "`; the text before that separator (`op`), trimmed, is non-empty; the
text after it (`title`), trimmed, is non-empty. Anything else on a
candidate line — missing brackets, an invalid calendar date, no `" | "`
separator, an empty op or title — makes it `Malformed` (`{Line, Text,
Reason}`) instead, and parsing continues with the next candidate line.

A bare `|` inside `op` with no surrounding spaces (seen in the wild:
`decision|update`) does **not** trigger the separator search early — only
the exact three-byte sequence `" | "` does — so that shape parses as a
normal, well-formed entry with `op = "decision|update"`.

A well-formed entry's `Body` is every line between its heading and the
next candidate heading (well-formed or malformed) or EOF, joined and
trimmed of leading/trailing blank lines — so the real vault's often-blank-
line-then-bullets bodies parse as one `Body` string regardless of how many
lines or what markdown they use; only `log append` itself is limited to a
single body line (§17.2).

`vaultlog.FindOutOfOrder(entries)` is a separate pass over `Parse`'s
well-formed entries (never mixed into `Parse` itself, or into
`Malformed` — a date going backwards is not a format defect, the heading
parses fine): it reports every `i` where `entries[i].Date <
entries[i-1].Date`, `{Line, Date, PrevLine, PrevDate}`. Consumed by `log
lint` (§17.4). `vaultlog.SortByDate(entries)` stable-sorts a copy by
`Date` ascending, keeping file order among entries sharing a date; `log
last` (§17.3) uses it before applying `--n`.

### 17.2 `log append <op> <title> [--body TEXT] [--date YYYY-MM-DD]`

`--date` defaults to today (`$VAULTY_TODAY` or the local date, like
`--touch`, §8.5); given explicitly it must be a full `YYYY-MM-DD` date or
the command exits 2 (usage — same class of error as `timeline read
--since` with a bad date).

`op` and `title` are trimmed, then `op`, `title` and `--body` (when given)
are each validated by `vaultlog.ValidateField` before anything is
written — **rejected**, never silently stripped further, since silently
mutating what an agent asked to write is worse than a clear refusal it can
retry:

- non-empty after trimming;
- no `\n` or `\r` anywhere — a newline inside any of these three fields
  would corrupt the one-line heading format (or, for `--body`, break the
  "single body line" contract §17 promises for entries this command
  writes) on the very next parse;
- `op` additionally must match `^[a-z0-9-]+$` (`vaultlog.opPattern`) —
  lowercase letters, digits and hyphens only. Stricter than merely banning
  `|` (an earlier pass at this rule): a bare `|` (tolerated when *reading*
  a legacy entry, §17.1's `decision|update` example) would make an op this
  command just wrote ambiguous with the heading's real separator on every
  future parse, but so would other shapes that aren't pipes at all —
  `a+b`, `x/y`, `two words` are all refused too, keeping op the short,
  stable, greppable category it's meant to be. This is intentionally
  stricter than what `Parse` accepts: append is strict, read stays
  lenient — the same asymmetry Timeline `append`/`lint` already have
  (§8.1 vs. the ratchet, §6.1a). Because this is enforced at write time,
  there's no separate "op looks irregular" lint warning to also maintain —
  an op written through this command is canonical from the start;
- `body` additionally must not contain a line that is itself a real ATX
  heading — `doc.IsATXHeadingLine` (`internal/doc`'s heading grammar,
  §7: 1-6 `#`'s then a space or end of line), not merely "starts with
  `#`". A body like `"## [2026-01-02] fake | injected"` (or a bare `"#
  fake"`) would forge a second, unrelated-looking log entry inside the
  body of the one the caller asked for, rather than staying inside it —
  but an issue reference like `"#123 fixed"` isn't a heading (no space
  after the `#`s) and is fine as body text.

A validation failure exits 3 (refused — same code timeline `append` uses
for `ValidateEntry` failures, §8.1) and writes nothing.

**Write mechanism.** Unlike Timeline `append` (§8.7, tmpfile + rename —
`atomicWrite`), `log append` never rewrites or renames the file: renaming
a new file over `log.md` would silently replace a symlinked `log.md` with
a plain file (breaking whatever the symlink pointed at), and a full
read-modify-write invites two concurrent appenders computing the same
"end of file" offset and clobbering each other. Instead
(`internal/cli/logrun.go`'s `appendLogEntry`): open (creating if needed)
with `O_RDWR|O_CREATE`, take an exclusive `flock` for the rest of the
call, `Stat` for the current size, read the existing bytes only to work
out the separator via `vaultlog.SeparatorFor` (0, 1 or 2 newlines,
depending on the current ending) — then `WriteAt` *only*
`separator + vaultlog.Format(...)` at that size offset. Existing bytes on
disk are never read back and rewritten, matching the append-only
guarantee Timeline blocks already have (§8.4: "existing bytes are never
changed, only added to") — a crash mid-write can corrupt at most the
entry being added, never history already on disk. The flock also
serializes concurrent appenders so two writers never compute the same
offset.

On success: parent directories are created if needed, then the entry is
appended as above. Human output: `appended <path>:<line>\n` to stdout,
where `<line>` is computed from the pre-append byte/newline counts plus
the separator's newline count (no re-read/re-parse needed — see the
comment beside `appendLogEntry`). `--json`:
`{"path":...,"line":...,"date":...,"op":...,"title":...}`.

### 17.3 `log last [-n|--n N] [--op OP] [--since DATE]`

Best-effort and scriptable, like `timeline read`: parses the whole file,
prints one stderr warning per malformed heading found (`<path>:<line>:
malformed log entry, skipped: <reason>`) and otherwise ignores them — never
a fatal error. A missing log file behaves as zero entries (not an error);
an unreadable one (permission, or a directory at that path) is exit 4 (I/O).
`-n`/`--n` (both forms; `-n` is the short flag) accepts the same value.

Filters apply first: `--op OP` keeps exact (case-sensitive) op matches;
`--since DATE` (same `YYYY-MM-DD`/`YYYY-MM` parsing as `timeline read
--since`) keeps `Date >= since` by plain ISO string comparison (valid
since the format is fixed-width `YYYY-MM-DD`). The result is then
**stable-sorted by date** (`vaultlog.SortByDate`, ascending, file order
preserved among entries sharing a date) **before** `--n` takes the tail —
file order is not reliably chronological (§17, the ~41 out-of-order
points in the real vault), so skipping this sort would make "last N" mean
"N entries nearest the end of the file", not "N most recent by date"; a
query like `--op lint -n 1` needs the latter to return the actual most
recent `lint` entry rather than whichever happened to be filed last.
`--n` (default 10) keeps the last N after sorting — `0` means all,
matching `timeline read --last`'s convention; `N < 0` is a usage error
(exit 2).

Human output reprints each kept entry exactly as `vaultlog.Format` would
write it (heading + body), blank-line-separated — log entries are read as
a short journal, not folded into one token-lean stream the way Timeline
entries are (§7), so the extra readability is worth the bytes here.
`--json`: `{"path":...,"entries":[{"line":...,"date":...,"op":...,
"title":...,"body":...}],"malformed_count":N}` — a count, not the
malformed entries themselves (already on stderr; `log lint` is the command
for their detail, §17.4).

### 17.4 `log lint`

Reports two independent kinds of finding, both from one parse of the log
file:

- **malformed headings** (`vaultlog.Parse`'s `Malformed`, §17.1) — a
  format defect, and the only thing that fails the exit code (below);
- **out-of-order entries** (`vaultlog.FindOutOfOrder`, §17.1) — a
  well-formed entry whose date is earlier than the one before it in the
  file. This is a **warning only, with no exit-code effect**: the real
  vault's `log.md` has ~41 of these in its legitimate append-only
  history (entries added by hand or other tooling over time, not a
  defect — §17), so treating it as a failure would leave `log lint`
  permanently red there with nothing to actually fix. It's still
  reported — worth knowing about, e.g. before trusting file order for
  something — just never fatal, and never folded into `Malformed`.

Human mode, one line per finding on stdout: malformed as `<path>:<line>:
malformed: <reason>: <text>` (mirroring `timeline lint`'s `path:line: CODE
severity: message` shape, §6.3); out-of-order as `<path>:<line>: warning
out-of-order: entry dated <date> appears after <prev_date> (line
<prev_line>)` (the `warning` token makes the exit-code asymmetry legible
in the output itself, not just in this doc). `--json`: `{"path":...,
"malformed":[{"line":...,"reason":...,"text":...}],
"out_of_order":[{"line":...,"date":...,"prev_line":...,"prev_date":...}]}`
— two separate arrays for the same reason: a consumer that only cares
about real defects can ignore `out_of_order` entirely.

Exit 1 only if `malformed` is non-empty (`ExitFindings`, same convention
as `timeline lint`); exit 0 otherwise, `out_of_order` findings included —
including when the file doesn't exist yet. A separate subcommand rather
than folding this into `log last` because `last`'s job is best-effort
reading (never fail the read over data quality — it already corrects for
out-of-order dates itself, §17.3) while `lint`'s job is exactly the
opposite: surface every finding as the primary result, for a periodic
vault-health pass (alongside `timeline lint`) rather than every `log
last` call.

## 18. `vaulty find [<term>...]`

Replaces raw `grep -r`/`find` as the LLM reader agent's discovery step over
the vault: term(s) in, ranked vault-relative page paths out, ready to pass
to `vaulty timeline read`. Implemented in `internal/find` (scoring, pure of
any CLI concerns) plus `internal/cli/find.go` (command tree + rendering),
mirroring the `timeline`/`log` package split.

### 18.1 Scope and tokenization

Scope is `vault.Walk()` — `config.dirs` minus the global `exclude` (§4.3),
the same "never scan this" list every other command respects — and nothing
narrower by default: `find` searches the whole vault. `--only <dir|glob>`
(repeatable, also comma-separated) restricts a single invocation further:
a bare name with no glob metacharacter (`*`, `?`, `[`) means "everything
under that directory" (`--only wiki` → `wiki/**`, `--only now/actions` →
`now/actions/**`); anything containing one is used as a glob exactly as
given (`MatchGlob` semantics). A file is kept if it matches *any* `--only`
pattern. `--only` pointing outside `config.dirs` (or at a path `exclude`
already dropped) simply yields nothing for that invocation — never an
error, since a caller narrowing to the wrong place should see "no
matches", not a crash. Implemented generically in the `vault` package
(`OnlyPatterns`, `FilterOnly`) rather than inside `internal/find`, since
`search` (§19) takes the same `--only` flag rather than inventing its own
notion of scope.

Matching tokenizes both the term and every candidate field the same way
(`internal/find/find.go`'s `tokenize`): lowercase, split on every run of
non-letter/non-digit characters (`-`, `_`, whitespace, and other
punctuation like `.`, `/`, `(`, `:` — Unicode-aware, so Dutch vault text
tokenizes correctly). A term's token sequence matches a field when it
appears contiguously in the field's token sequence with every term token a
*prefix* of the token it aligns with; the match is exact when the two
token sequences are equal outright, substring otherwise. So `po agent`
(`["po","agent"]`) matches `po-agent`/`po_agent` (also `["po","agent"]`,
exactly), `dam` matches `dam-cutoff`, and `stock` matches `stocky` — but
`ai` never matches `payment-failed` or `email`, because `ai` is only ever
a prefix of a *whole* token, never of a run of letters spanning two real
words; splitting on token boundaries first is what plain substring
matching over a separator-collapsed string got wrong.

### 18.2 Fields, weights and per-term scoring

Scoring sources are `find.fields` (§4.1), a config-driven list — the
built-in defaults reproduce what used to be a fixed table:

| Field | Weight | Match |
|---|---|---|
| slug (basename without `.md`) | 100 (50 on a mere substring) | token |
| frontmatter `title` | 40 | token |
| any frontmatter `aliases` entry | 40 | token |
| frontmatter `tags` entry | 25 | token |
| index summary (§18.3) | 20 | token |
| first H1 heading text | 20 | token |
| compiled-truth body line (`--body` only, §18.4) | 5 | token |

Terms are OR'ed. For each term independently, the page's *single*
best-matching field counts (the highest weight; ties break toward the
earlier entry in `find.fields` — e.g. an exact `title` and an exact `alias`
both score 40 by default, and `title` wins the tie only for the purpose of
which field name is reported, not the score). The page's total score is the
sum of each term's best-field weight (0 for a term that matches nothing). A
page with a total score of 0 (no term matched anything, and no `--where`/
`--type` filter given either) is not a result at all.

A `find.fields` entry's `source` names where its value(s) come from (`slug`,
`frontmatter` with `key`, `index`, `h1` or `body`); `match: token` runs the
tokenized matching described above, `match: exact` instead compares the
whole normalized value to the whole normalized term with no tokenizing at
all (trim + case-fold only) — for a value like an id containing `/` that
tokenizing would otherwise split apart. `source: slug` is the one field
that keeps an exact-match bonus as a fixed property of that source (not a
separately configurable knob): a full token-sequence match scores its
configured weight, a mere substring/prefix-window match scores half that —
the built-in 100/50 shown above. Every other source scores the full
configured weight on any match, exact or not. A vault that sets its own
`find.fields` replaces the table above wholesale (§4.1); a vault-defined
`frontmatter` key not among `title`/`aliases`/`tags` reports its own key
name as the matched field (e.g. `id`), rather than one of the built-in
labels (`title`/`alias`/`tag`).

Body is deliberately checked last and only as a fallback: a term already
satisfied by a higher-weight field never triggers a body scan for that
term, which keeps `--body` cheap on the common case (most terms hit
metadata) even though it means whichever body match a page needed is
what's actually reported (§18.4) — a term that also happens to appear in
the body of a page it already matched by slug never shows a body
snippet, since body never had to run for that term.

`--type TYPE` filters on frontmatter `type` (exact match) before scoring.
`type` comes from frontmatter only; a page with no frontmatter, or
frontmatter that fails to parse, has `type == ""` and is excluded by any
`--type` filter (but still eligible for every other flag combination).

`--where KEY=VALUE` (repeatable) is the same exact, case-sensitive,
AND'ed frontmatter filter as `search --where` (§19.1), splitting on the
first `=` only: a list-valued key matches when the list contains the
value. It is shared with `search` via the `internal/filter` package (both
the `KEY=VALUE` parser and the match function), so the two commands parse
and match identically; `find --where` never applies the `tag:`→`tags`
alias — that's a `search`-only query-string convenience (§19.1), not part
of `--where` on either command. `--where` combines with `--type` and
`--only` (AND'ed with both). Unlike a bare `find` invocation, `find` with
at least one `--where` or `--type` is valid with **no terms at all**: every
page matching the filter(s) is a result, sorted by path ascending, score 0
(§18.5). With no terms and no `--where`/`--type` either, it's still the
usual usage error (`vaulty: find: no search terms given`, exit 2).

### 18.3 Frontmatter and the index

Frontmatter is parsed leniently (`yaml.Unmarshal`, no `KnownFields`): a
page with broken frontmatter YAML still matches on slug, H1 and body — it
never fails the command, and its `type`/`title`/`aliases`/`tags` are just
empty. `aliases`/`tags` accept either a YAML list or a single bare scalar.

The index summary comes from `find.index` (default `index.md`, the
vault's own catalog convention: `- [[name]] — <rest of line>` lines).
`find` reads it once per invocation, maps `name -> summary` (the wikilink
normalization — cut at the first `|` or `#` — matches `vault.Resolve`,
§3.3), and looks up each page by `basename == name`. The summary is
everything after the first ` — `; a single trailing parenthetical is
stripped from it, but only when that parenthetical contains a full
`YYYY-MM-DD` date somewhere inside it — covering both the common
`(YYYY-MM-DD)` form and status-plus-date forms like `(DRAFT,
2026-06-13)`/`(seed, 2026-09-11)` — so a line with no trailing date at all
(or a non-date trailing parenthetical) still counts, keeping its full
remainder as the summary rather than silently being skipped. A missing
index file is skipped silently (empty map, not an error); a line that
doesn't match the `- [[name]] — ...` shape at all is ignored. Sharded
child pages (`wiki/<type>/<x>/<x>-<part>.md`) are never index entries by
convention and so never get an index-summary match — they still match on
their own slug/frontmatter/H1/body.

### 18.4 `--body`

Scans the page's compiled-truth span (`timeline.Parse`'s `CompiledTruth`,
§5.2 — the same span `timeline read`'s default mode prints, i.e. above the
Timeline divider, frontmatter excluded) line by line, for whichever terms
didn't already match a higher-weight field (§18.2). For each such term,
scanning stops at its first matching line (one body match per term, not
every line it appears on); recording additionally stops once the page has
3 body matches total, whichever limit is hit first — enough to show why a
body-only hit exists without flooding the output — as `{line, text}`,
`text` trimmed and capped at 120 bytes, backed off to the nearest UTF-8
rune boundary so a multi-byte rune is never split.

### 18.5 Output

`--limit` (default 10, `0` = unlimited) applies after sorting by score
descending, then path ascending. `total` (JSON) / the pre-limit match
count is always the full count, independent of `--limit`.

Human mode, one tab-separated line per result on stdout: `path\ttype\t
score\tmatched-fields\tsummary-or-title`, where `matched-fields` is a
comma list of the field names that contributed to the score (deduplicated,
first-contributed order — e.g. `slug,alias,index`), and the last column is
the index summary when the page has one, else the frontmatter title, else
empty. With `--body`, each result is followed by one indented line per
recorded body match: `  L<n>: <snippet>`.

No terms given (or every term blank after normalization) and no `--where`/
`--type` given either exits 2 (`vaulty: find: no search terms given`) — a
caller mistake, not "found nothing". With `--where`/`--type` and no terms,
every page is scored 0 and sorted by path instead (§18.1's `--where`
paragraph). No pages match (valid terms, zero results): nothing on stdout,
`vaulty: no pages match` on stderr, **exit 0** — this is a normal, useful
answer for a discovery tool, not a usage error.

`--json`:

```json
{"terms":["po agent"],
 "results":[{"path":"wiki/initiatives/po-agent.md","type":"initiative",
   "title":"PO Agent","summary":"knowledge base for the PO agent",
   "score":100,"matched":["slug"],
   "body":[{"line":12,"text":"..."}]}],
 "total":1}
```

`results` is always an array, even when empty (never `null` — unlike most
other `--json` array fields elsewhere in this tool, because an empty
result set is find's normal "nothing found" answer, not an edge case a
JSON consumer should have to special-case with an extra nil check).
`body` is present only on a result that actually recorded a body match
(§18.4); it's absent (not an empty array) on every other result, including
when `--body` was passed but that page matched entirely through metadata.

## 19. `vaulty search <query...>`

Content search for the LLM reader agent: a topic question in ("what do we
know about delivery reliability"), ranked pages with a couple of highlighted
source lines out, without reading whole pages. `find` (§18) is discovery by
name and metadata; `search` is BM25-ranked full text. Implemented in
`internal/search` (index, query, cache, snippets) plus
`internal/cli/search.go` (flags and rendering); page metadata comes from the
same `internal/page` helpers `find` uses (lenient frontmatter, index.md
summaries, first H1).

The corpus is `vault.Walk()` (`config.dirs` minus `exclude`), one index
document per page. `--only` (same expansion as `find`, §18.1) is applied at
query time as a non-scoring filter; the index always covers the whole
configured corpus, so `--only` never triggers re-indexing and never changes
scores.

### 19.1 Query syntax

The positional arguments are joined with spaces and parsed as one query
string:

| Form | Meaning |
|---|---|
| `word` | Plain term. Analyzed with every configured analyzer plus the raw analyzer; terms are OR'ed and pages matching more of them rank higher (bleve's coord factor). |
| `"exact phrase"` | Words in this order, against the unstemmed raw sub-fields. |
| `term~` | Fuzzy, against the raw sub-fields: edit distance 1 for terms under 6 runes, 2 from 6 runes on. `term~1`/`term~2` pin the distance; any other `~N` is a query error. |
| `term*` | Prefix, against the raw sub-fields. |
| `-term`, `-"phrase"`, `-term~`, `-term*` | Exclude pages matching it (in the searched fields). |
| `key:value`, `key:"quoted value"` | Exact frontmatter filter on any key (§19.2). `tag:` is an alias for `tags:` (`search.field_aliases`, §4.1). AND'ed with every other filter. |
| `-key:value` | Exclude pages whose frontmatter has that value. |

A `key:value` token is a filter when `key` starts with a letter or `_` and
continues with letters, digits, `_`, `-`, `.`; anything else containing a
colon is an ordinary term. A fuzzy/prefix/phrase stem that the raw analyzer
splits into several tokens (`po-agent~`) applies to each token, OR'ed.
Terms with nothing searchable in them (pure punctuation) are dropped; a
query whose terms all drop out (`vaulty search '!!!'`) is a query error
(exit 2), even alongside filters.

`--type T` is `--where type=T`. `--where key=value` (repeatable) splits on the
first `=` only, so values may contain `=` and `/`
(`--where thread=spaces/AAA/threads/BBB`); every `--where`, `--type` and
`key:value` is AND'ed. A list-valued key matches when the list contains the
value. Matching is exact and case-sensitive. The `key=value` parser and the
match function are shared with `find --where` (§18) via the
`internal/filter` package; `search`'s own `--where` never applies
`search.field_aliases` (only the `key:value` syntax below does).

Negated terms are ordinary arguments: `vaulty search delivery -hookdeck`.
`search` has no shorthand flags, so every argument after `search` that
starts with a single `-` (other than `-h`) is a query term
(`normalizeSearchArgs`); a literal `--` turns this off.

A query with filters but no ranked terms (`vaulty search --type decision`,
`vaulty search status:active`) is valid: every matching page, sorted by
path, score 0, no snippets.

### 19.2 Fields, analyzers and boosts

Each page is indexed as eight text groups plus keyword/stored fields. Seven
are content groups, each its own field so it can carry its own
configurable boost (`search.boosts`, §4.1); the eighth, `timeline`, is
searched only with `--timeline` and its boost is fixed, not
config-driven:

| Group | Content | Boost (default) |
|---|---|---|
| `title` | frontmatter `title` | 5.0 |
| `aliases` | every `aliases` entry | 5.0 |
| `slug` | slug (basename without `.md`) | 5.0 |
| `h1` | first H1 heading text | 3.0 |
| `index` | index summary (§18.3) | 3.0 |
| `tags` | every `tags` entry, as text | 2.0 |
| `body` | compiled truth (§5.2: after frontmatter, above the Timeline divider) | 1.0 |
| `timeline` | from the Timeline divider (or heading) to EOF; searched only with `--timeline` | 1.0 (fixed, not in `search.boosts`) |

`search.boosts` (§4.1) is a query-time weight only, applied when building
the query (`SetBoost`); it never changes what gets indexed, so changing it
alone never forces a cache rebuild (§19.3) — only a change to which groups
exist or how they're analyzed does (a mapping-schema change, §19.3). Every
group is indexed once per `search.analyzers` entry
(`<group>_<analyzer>`, §4.1) and once with the raw analyzer (`<group>_raw`:
unicode tokenizer + lowercase, no stop words, no stemming). A plain word
queries all of them; phrase, fuzzy and prefix queries use only the raw
sub-fields, because stemming breaks them (`bluestne~1` never reaches an
`en`-stemmed `blueston`). Term vectors are stored on raw sub-fields only
(phrases need positions).

Frontmatter is indexed generically: every top-level key whose value is a
scalar (string, number, bool, date — stringified) or a list of scalars
becomes one exact term `key=value` per value in the keyword field `fm`
(`page.FrontmatterValues`); nested maps and non-scalar list elements are
skipped. Filters are term queries on `fm`. `type`, `title` and the index
summary are also stored for display, and `type` feeds the type facet.

Scoring is bleve's BM25 (`ScoringModel = "bm25"`; bleve's default is
TF-IDF). The query is built programmatically, not with bleve's query-string
parser: each term becomes a disjunction of per-sub-field match/phrase/
fuzzy/prefix queries carrying the group boost, the terms form the should
clause of a boolean query, exclusions its must-not clause, and filters plus
the `--only` document-id set its non-scoring filter. bleve keeps
Lucene-classic query normalization and coord factors on top of BM25, so raw
scores are tiny and depend on how many sub-queries a query expanded into;
`score` is therefore reported relative to the best hit of the query (the top
hit is `1.00`), in bleve's rank order (score desc, then path asc).

### 19.3 Index cache

Layout, one directory per vault, never inside the vault:

```
$VAULTY_CACHE_DIR/<sha256(abs vault root)[:16]>/     (default base: os.UserCacheDir()/vaulty)
  index/           bleve scorch index
  manifest.json    what the index holds (below)
  lock             flock target
```

`manifest.json` records the index format (`search.FormatVersion`; 2 since
the boosts/fields follow-up split the `name`/`head` groups into their own
per-field groups, §19.2), a hash of the index mapping (fields, analyzers),
a hash of the config that decides what is indexed and how pages split
(vault root, `dirs`, `exclude`, `find.index`, `timeline.heading`,
`timeline.divider`, `search.analyzers`, `fields.type`, `fields.title`), and
per page: size, mtime (ns), content sha256 and index summary; plus the
stats below. `search.boosts`/`search.field_aliases` are query-time only
and never part of this hash (they don't change what gets indexed).

Every call (unless `--no-cache`):

1. Take `lock` (exclusive `flock`, polled every 20 ms for up to **2 s**).
2. **Full rebuild** when `--rebuild` is given, the manifest is missing or
   unreadable, or its format version, mapping hash or config hash differs,
   or the index won't open (corrupt): delete manifest and index, index every
   page, write the manifest.
3. Otherwise **incremental**: walk the corpus and stat every file. Same
   size, mtime and index summary as the manifest: unchanged, no read.
   Anything else: read and hash. Same hash and summary (a touch): only the
   manifest entry is refreshed. Different: re-index the page. Manifest pages
   no longer in the corpus (deleted, excluded, unreadable) are deleted from
   the index. All changes go in one bleve batch.
4. If anything changed, write the manifest atomically (temp file in the
   cache dir, fsync, rename), only after the index update succeeded — a
   crash in between leaves the old manifest, and the next call re-applies
   the same idempotent updates. A manifest write that fails is ignored for
   the same reason: the index is current and is used for this call. A call
   that changes nothing writes nothing.
5. Query, release the lock.

Index-format changes bump `FormatVersion` (`internal/search/mapping.go`);
format 1 is the initial schema.

**Fallback.** When the cache can't be used — no user cache dir, the directory
can't be created, the lock can't be taken in 2 s, the old manifest can't be
removed before a rebuild, rebuilding or updating the index fails, or the
query against the cached index fails — the call indexes the corpus in memory
(the same scorch engine and scoring) and prints exactly one stderr line (an index that failed to build, update or
query is deleted so the next call rebuilds; one that was merely not
reachable, e.g. a read-only cache dir, is kept):
`vaulty: search cache unavailable (<reason>), indexing in memory`. A cache
problem never fails the command. `--no-cache` always indexes in memory,
silently, and never touches the cache (golden tests use it). `--rebuild`
forces step 2 and keeps using the cache afterwards.

Measured on the real vault (735 pages): full rebuild about 0.8 s; a cached
call with nothing changed about 10 ms; in-memory about 0.8 s and 135 MB RSS.

**`--stats`** reports on the cached index without locking or updating it
(the query is ignored), one `key: value` per line (or `--json`):
`cache` (the vault's cache dir), `pages`, `index_bytes`, `last_update`
(RFC 3339, last time the index content changed, or `never`),
`last_check_updated` (pages indexed or deleted by the most recent update
that changed the index; the page count after a full rebuild),
`last_update_kind` (`full` or `incremental`), and `full_rebuilds`. Calls that
find nothing to do don't change these. With no cached index yet, `pages: 0` and
`last_update_kind: none (no cached index yet)`.

### 19.4 Output and exit codes

`--limit` (default 10, `0` = unlimited) caps the results; totals and facets
count every hit.

Human mode, per hit one tab-separated line on stdout, `path\ttype\t
score\tsummary-or-title` (score with 2 decimals; the index summary when the
page has one, else the title), then up to 2 snippet lines
`  L<n>: <text>`. Snippet lines come from the compiled truth (plus the
Timeline with `--timeline`): the lines matching the most distinct query
terms, earliest first on ties, printed in line order. `<n>` is the line
number in the source file. Matches are found with the same analyzers as the
index (so `leveringen` highlights `levering`) and wrapped in `**`; the
source's own `**` bold markers are dropped from the line first. A line longer
than 160 bytes is cut to a window starting up to 40 bytes before its first
match, with `…` at each cut; the 160 bytes include markup and ellipses, and
a cut never splits a UTF-8 rune or a `**` pair.

After the results, exactly one stderr line:
`vaulty: N hits (type counts: decision 4, system 2, (none) 1)`, types by
count desc then name asc, `(none)` for pages without a type, and
`type counts: none` when N is 0.

`--json` (no summary line on stderr):

```json
{"query":"delivery reliability",
 "total":38,
 "facets":{"type":{"initiative":4,"(none)":4}},
 "results":[{"path":"wiki/initiatives/x.md","type":"initiative","title":"X",
   "summary":"...","score":1,
   "snippets":[{"line":20,"text":"# DAM asset **delivery** (CDN)"}]}]}
```

`results` and `snippets` are always arrays; `score` is rounded to 4
decimals.

| Exit | When |
|---|---|
| 0 | Results printed, or no hits (nothing on stdout, `vaulty: 0 hits (type counts: none)` on stderr) |
| 2 | Empty query (no terms, `key:value`, `--where` or `--type`), unparseable query (unterminated quote, lone `-`, `*`/`~` without a term, `~N` other than 1/2, `key:` without a value, terms with nothing searchable), malformed `--where`, invalid config (including unknown `search.analyzers`) |
| 4 | The corpus can't be walked, or the in-memory index can't be built |

## 20. `vaulty setup <target>...`

Installs the skills and agents that ship in this repo (embedded in the
binary by the root `vaulty` package, so a binary always installs the
matching release's files) into the locations agent harnesses read.
Implemented in `internal/setup` plus `internal/cli/setup.go`.

### 20.1 Targets

| target | aliases | project root | `--global` root | contents |
|---|---|---|---|---|
| `agents` | `codex`, `gemini`, `opencode` | `.agents` | `~/.agents` | `skills/*` |
| `claude` | | `.claude` | `~/.claude` | `skills/*`, `agents/*` |
| `pi` | | `.pi` | `~/.pi/agent` | `skills/*` |

`.agents/skills` is the shared Agent Skills location: Codex reads it (repo
and `$HOME`), and Gemini CLI, OpenCode and pi read it next to their own
directories. Only Claude Code gets the `vault-reader` agent: the other
harnesses either have no subagents or use a different agent format.
Duplicate-target aliases (`codex gemini`) install once. The project dir is
the vault root (normal discovery, §3.2), `--dir <path>` (must exist), or the
home dir with `--global`; `--dir` and `--global` are mutually exclusive.

### 20.2 Ownership and upgrades

Every target root keeps `.vaulty-setup.json`:
`{"version": "<vaulty version>", "files": {"skills/vaulty-read/SKILL.md": "<sha256>", ...}}`.
Per file:

| on disk | status | written |
|---|---|---|
| missing | `created` | yes |
| same bytes | `unchanged` | no |
| differs, sha256 matches the manifest | `updated` | yes |
| differs, not in the manifest, or edited since | `conflict` | only with `--force` |
| not a regular file (symlink, dir) | `conflict` | only with `--force` (replaced, never written through) |

The plan for every target is built before anything is written; any conflict
without `--force` writes nothing. Files removed from a later release are
left in place. `--dry-run` prints the plan and writes nothing.

Output: per target a `<target> (<harnesses>):` line, then one
`  <status> <path relative to the project dir>` line per file (`would be
created`/`would be updated` under `--dry-run` or a refusal). `--json`:
`{"dry_run": bool, "targets": [{"target", "root", "files": [{"path",
"dest", "status", "reason"?}]}]}`; `dry_run` is also true when conflicts
blocked the write.

| Exit | When |
|---|---|
| 0 | Installed, or dry run without conflicts |
| 2 | No or unknown target, `--dir` not a directory, `--dir` with `--global`, no vault root found without `--dir`/`--global` |
| 3 | Conflicts without `--force` (nothing written) |
| 4 | Read/write failure, unreadable manifest |
