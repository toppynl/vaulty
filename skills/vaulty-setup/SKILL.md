---
name: vaulty-setup
description: "Set up vaulty for a markdown vault by talking it through with the user: inspect the repo, ask about content dirs, languages, page layout and lint scope, then write .vaulty.yml and verify it with vaulty config print and lint. Use when the user wants to start using vaulty in a repo, create or change .vaulty.yml, or asks why vaulty doesn't see their pages."
---

# Setting up vaulty for a vault

Goal: a `.vaulty.yml` at the vault root that matches how this vault is
actually laid out. Every key is optional, so write only what differs from
the defaults. Keep the file short.

## 1. Look before asking

Find out what you can yourself, so you only ask what the repo can't tell:

- Is vaulty installed? `vaulty version`. If not, point the user to
  https://github.com/toppynl/vaulty#install and stop.
- Existing config? `vaulty config print --json` shows the root and the
  effective values (defaults when there is no `.vaulty.yml`).
- Layout: list the top-level dirs and count `.md` files in each
  (`ls`, `find <dir> -name '*.md' | wc -l`). Note hidden dirs, `node_modules`,
  build output, attachments and template dirs; they are never content.
- Page shape: open two or three typical pages. Note the frontmatter keys
  (type, title, aliases, tags, updated, an id?), whether pages have a
  `---` + `## Timeline` section at the bottom, and the language(s) of the
  prose.
- An `index.md` catalog or `log.md` operation log at the root?

## 2. Ask the user (one short round)

Propose answers based on what you found; let the user confirm or correct.
Ask only the questions whose answer isn't obvious:

1. **Content dirs** (`dirs`): which dirs hold pages? `.` means the whole
   root. Only `.md` files under these dirs are vault content; vaulty
   refuses to read anything else. Default: `[wiki, me, now, archive]`.
2. **Never scan** (`exclude`): subtrees inside those dirs to skip, e.g.
   `templates/**`, `archive/**`, work logs.
3. **Languages** (`search.analyzers`): `[en]`, `[nl, en]`, ... for
   stemming; `[standard]` (default) when mixed or unsure. Supported codes:
   ar, cjk, ckb, da, de, en, es, fa, fi, fr, hi, hr, hu, it, nl, no, pl, pt,
   ro, ru, sv, tr (plus `standard`, `simple`).
4. **Frontmatter keys**, only if they differ from the defaults:
   `fields.type` (default `type`), `fields.title` (default `title`),
   `frontmatter.updated_key` (default `updated`). A page id worth finding
   by exact value can be added to `find.fields` (see below).
5. **Timeline**: do pages use a `## Timeline` heading under a `---`
   divider? If the heading differs (e.g. `## History`), set
   `timeline.heading`.
6. **Lint scope**: which paths should lint check on edit
   (`lint.hook_paths`) and for page hygiene (`lint.page_checks.paths`)?
   Default `wiki/**` for both. Usually the knowledge dirs, not work logs.
7. **Catalog and log**: `find.index` (default `index.md`) and `log.path`
   (default `log.md`) if they live elsewhere.

Don't ask about boosts, find weights or lint severities unless the user
brings them up.

## 3. Write `.vaulty.yml`

Show the file to the user before writing it. Start with `version: 1` and
add only the keys that changed. Example for a Dutch/English wiki kept in
`notes/` with its templates excluded:

```yaml
version: 1
dirs: [notes]
exclude: ["notes/_templates/**"]
search:
  analyzers: [nl, en]
lint:
  hook_paths: ["notes/**"]
  page_checks:
    paths: ["notes/**"]
```

Adding an exact-match id field replaces the whole `find.fields` list, so
repeat the defaults you want to keep:

```yaml
find:
  fields:
    - { source: slug, weight: 100, match: token }
    - { source: frontmatter, key: id, weight: 60, match: exact }
    - { source: frontmatter, key: title, weight: 40, match: token }
    - { source: frontmatter, key: aliases, weight: 40, match: token }
    - { source: index, weight: 20, match: token }
    - { source: h1, weight: 20, match: token }
    - { source: body, weight: 5, match: token }
```

All config paths are relative to the root and may not contain `..` or a
hidden directory. `vaulty` rejects an invalid config with exit 2 and says
which key is wrong.

## 4. Verify

```bash
vaulty config print                 # loads and validates the new config
vaulty find <a known page name>     # the page is found with the right path
vaulty search <a word from a page>  # builds the index, returns hits
vaulty lint --warnings              # whole-vault findings
```

If a known page is missing, check `dirs` and `exclude` first. If lint
reports many findings on existing pages, that's legacy debt: suggest the
user freezes it with `vaulty lint --write-baseline` (the user
runs it, not you) and commits `.vaulty-baseline.json`.

## 5. Next steps (suggest, don't apply)

- Agent skills for other harnesses: `vaulty setup <claude|agents|pi>`
  installs the vaulty skills into the project (`--global` for the home
  dir).
- Permissions, lint-on-edit hook and pre-commit check: see the
  vaulty-maintain skill.
