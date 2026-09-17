---
name: vaulty-read
description: "Find and read pages in a markdown knowledge vault with the vaulty CLI instead of grep/find/cat. Use when answering a question from a vault (a repo with a .vaulty.yml, or a wiki/notes repo the user calls their vault/knowledge base), looking up a person/system/decision page, or reading a page's compiled truth or Timeline history."
---

# Reading a vault with vaulty

`vaulty` knows which files are vault content, ranks pages, and reads the
right part of a page. Use it instead of `grep`, `find`, `ls` or `cat` on
the vault: it is faster, cheaper in tokens, and never guesses paths.

If `vaulty` is not on PATH, tell the user and suggest:
`curl -fsSL https://raw.githubusercontent.com/toppynl/vaulty/main/scripts/install.sh | bash`.
Do not fall back to grep silently.

## 1. Find candidates

Pick by what you know:

- **A name** (person, system, project, id, alias): `vaulty find <term> [<term>...]`.
  It ranks by file name, title, aliases, tags, index summary and first heading.
  Pass several spellings in one call: `vaulty find "acme corp" acme`.
  Add `--body` only when nothing matches.
- **A topic or question**: `vaulty search <words>`. Full-text, ranked, with
  1–2 matching lines per hit, which is often enough to choose without
  reading. Syntax: `"exact phrase"`, `term~` (typos), `term*` (prefix),
  `-term` (exclude), `key:value` (frontmatter). Add `--timeline` only for
  history questions. The first search in a fresh checkout builds an index
  (about a second); later calls are instant.
- **An exact frontmatter value**: `vaulty find --where key=value`
  (no terms lists every match), or `--type <type>` on either command.

Narrow with `--only <dir>[,<dir>]` (dirs or globs) when the question is
clearly about one part of the vault. Keep `--limit` at its default of 10.
Add `--json` when you need to process results.

Neither finds anything useful: say the vault does not have it. Don't widen
into grep.

## 2. Read only what bears on the question

Pass the path `find`/`search` printed (a bare page name also works):

```bash
vaulty timeline read <page> --max-bytes 25000   # compiled truth: the default
```

- Compiled truth is everything above the page's `## Timeline` section,
  without frontmatter. It answers almost every "what is / who / current
  state" question. Add `--frontmatter` when you need fields like status or
  owner.
- History or "when": `--last N` or `--since YYYY-MM-DD` (both imply
  `--timeline`). Never pair a bare `--timeline` with `--max-bytes`: entries
  are oldest first, so the cap cuts the newest ones.
- The output ends with a truncation marker: run `--headings`, then
  `--section "<exact heading text>"` for the part you need. Copy the
  heading text exactly; don't guess.

Read the 3–5 most relevant pages, not more. Prefer fewer, better pages.

## Rules

- Read-only: never run `timeline append`, `log append`, or any `lint`
  write flag while reading.
- vaulty only serves vault content: `.md` files under the configured
  `dirs`, outside hidden folders, not excluded. A refusal (`path is not
  vault content`) is deliberate. Don't read that file another way unless
  the user asks.
- `vaulty config print` shows the vault root, content dirs and index file
  when you need to orient.
