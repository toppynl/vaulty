---
name: vaulty-write
description: "Record changes in a markdown vault with the vaulty CLI: append dated entries to a page's ## Timeline, bump updated:, and append operation entries to log.md. Use whenever you add history to a vault page or log an operation, instead of editing Timeline sections or log.md by hand."
---

# Writing to a vault with vaulty

Pages in a vault keep **compiled truth** (the current state, rewritten
freely) above an append-only **`## Timeline`** of dated entries. vaulty
writes the append-only parts, so ordering, format and dates stay valid.

## Timeline entries

```bash
vaulty timeline append <page> "- **YYYY-MM-DD** | <who> — <what changed and why>"
```

- vaulty inserts the entry in date order and creates the divider and
  `## Timeline` heading if the page has none. The leading `- ` is optional.
- `--touch` also sets the frontmatter `updated:` date to today. Use it when
  the entry reflects a meaningful change to the page, not for trivial
  backlinks.
- `--dry-run` prints the resulting Timeline without writing.
- A refusal (exit 3) means the page or entry is malformed: fix what the
  message names, don't hand-edit around it.
- Write claims, not transcript: one line with the outcome and its reason.
- Compiled truth above the Timeline is edited normally (Edit/Write). Keep
  history out of it; history belongs in the Timeline.

## Log entries

Every operation on the vault gets one log entry:

```bash
vaulty log append <op> "<title>" [--body "<one line>"] [--date YYYY-MM-DD]
```

`<op>` is a short verb the vault uses consistently (e.g. `ingest`,
`update`, `query`). Check the ones in use with `vaulty log last -n 20`, or
filter with `--op <op>` / `--since YYYY-MM`. Never edit `log.md` by hand.

## After writing

- Run `vaulty timeline lint <page>` on pages you changed (or
  `vaulty timeline lint --changed`) and fix errors before committing.
- Follow the vault's own conventions (its CLAUDE.md or README) for index
  updates and commits. vaulty doesn't do those.
