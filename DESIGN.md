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
internal/safety/          pre-write verification shared by every writer
internal/diag/            Diag type + codes
scripts/parity/           vault round-trip live proof (package parity, step 2)
scripts/install.sh        installer (step 5)
testdata/golden/          CLI golden cases (synthetic content only, §10.2)
examples/vaulty.yml       documented defaults
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
stack as `toppynl/clickup-cli`. Everything else is the standard library. The
binary is built with CGO disabled.

---

## 3. CLI

### 3.1 Command tree

```
vaulty [--vault DIR] [--json]
├── timeline
│   ├── lint   [paths...] [--hook] [--changed[=REF]] [--warnings] [--strict] [--write-baseline]
│   ├── read   <page> [--timeline] [--since DATE] [--last N] [--frontmatter]
│   └── append <page> "<entry>" [--touch] [--dry-run]
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
| `read --section` | Read one section of a page |
| `search`, `backlinks` | Search and backlink queries |

`timeline lint` hosts the two page checks (PG001, PG002) because both are
defined relative to the Timeline divider. The later umbrella `vaulty lint`
calls into the same `lint` package.

### 3.2 Global flags and environment

| Flag or variable | Meaning |
|---|---|
| `--vault DIR` | Vault root. Beats everything else (§4.2). |
| `--json` | One JSON document on stdout (schemas per command below). Diagnostics go into the JSON, never mixed into stdout. |
| `VAULTY_ROOT` | Root override, below `--vault`. |
| `VAULTY_TODAY=YYYY-MM-DD` | Pins "today" for `--touch`, future-date warnings and goldens. |

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
   `ErrOutside`.
3. Otherwise, name mode. Walk `config.dirs` (archive included, excludes
   applied) for files whose basename is exactly `arg + ".md"` (case-sensitive).
   - 1 match: use it.
   - 0 matches: `ErrNotFound`.
   - More than 1: `ErrAmbiguous`, and the message lists the matches.
     Filenames are unique vault-wide by convention, so ambiguity is a real
     error.

All three errors exit with code 2.

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

---

## 4. Config: `.vaulty.yml`

### 4.1 Schema and defaults

Every key is optional and absent keys keep their defaults. Slices replace
the default wholesale; the `severity` map merges. Unknown keys are an error
(`yaml.Decoder.KnownFields(true)`). The schema is implemented in
`internal/config/config.go`; `examples/vaulty.yml` documents every key.

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
```

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
- paths matching `exclude` are skipped.

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

- **Release.** Tagging `vX.Y.Z` runs `.github/workflows/release.yml`, which
  runs goreleaser. It builds `linux,darwin × amd64,arm64`, CGO disabled,
  with `-trimpath`, and `main.version` set via ldflags.
- **Assets.** They have versionless names: `vaulty_<os>_<arch>.tar.gz` plus
  `checksums.txt`. If the repo is ever public, the stable URL is
  `https://github.com/toppynl/vaulty/releases/latest/download/vaulty_linux_amd64.tar.gz`.
- **CI** (`ci.yml`) runs gofmt, vet, test and build on pushes and PRs.
- **Private repo.** Installing needs a token, so `gh` is preferred.

`scripts/install.sh` (step 5):

1. It is idempotent and exits 0 if `vaulty` is already on PATH, unless
   `--force` is given.
2. It detects the OS and architecture (`uname -s`/`-m`, mapping `x86_64` to
   `amd64` and `aarch64` to `arm64`).
3. If `gh` exists and a token is set, it installs with
   `gh release download --repo toppynl/vaulty --pattern "vaulty_${os}_${arch}.tar.gz" -O - | tar -xz -C "$DIR" vaulty`.
4. Otherwise, if `go` exists, it runs
   `GOPRIVATE=github.com/toppynl go install github.com/toppynl/vaulty/cmd/vaulty@latest`.
5. Otherwise it fails with a message.
6. `DIR` is `${VAULTY_INSTALL_DIR:-$HOME/.local/bin}`.

The block for `/var/www/personal/me/scripts/bootstrap-cloud.sh` goes after
the gh block, because it needs gh and `GH_TOKEN`:

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

For me-template, the README gets the same two options: the one-line `gh`
install, or `go install`. It also ships a `.vaulty.yml` and the settings
snippet below. With a private repo, template users need access to it
(open question Q3).

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
