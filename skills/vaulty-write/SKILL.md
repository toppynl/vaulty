---
name: vaulty-write
description: "Change a markdown vault with the vaulty CLI: replace, append to or insert one compiled-truth section (vaulty write), edit frontmatter fields (vaulty frontmatter), append dated entries to a page's ## Timeline, and append operation entries to log.md. Use whenever you edit a vault page or log an operation, instead of reading whole pages and hand-editing sections, YAML, Timelines or log.md."
---

# Writing to a vault with vaulty

Pages in a vault keep **compiled truth** (the current state, rewritten
freely) above an append-only **`## Timeline`** of dated entries. vaulty
edits one section, one frontmatter key or one entry at a time, so you
never read or rewrite a whole page to change a part of it, and the rest
of the page is verified byte-identical before anything is written.

## Editing a section

1. **Find the page** with `vaulty find` / `vaulty search`. If you don't
   know the exact heading, list them: `vaulty read <page> --headings`.
2. **Read just that section:**

   ```bash
   vaulty read <page> --section "Owners"
   ```

   stderr ends with `vaulty: section hash <12 hex>`. Note the hash.
   Don't pass `--max-bytes` here: never replace a section you only saw
   truncated.
3. **Write it back**, content on stdin through a quoted heredoc
   (`<<'EOF'`, so `$` and backticks stay literal):

   ```bash
   # replace: stdin is the whole section, heading line included, same level (renaming is fine)
   vaulty write <page> --section "Owners" --if-hash <hash> --touch <<'EOF'
   ## Owners

   - Finance team (Jan)
   EOF

   # append body text to the end of a section (no hash needed)
   vaulty write <page> --section "Owners" --append <<'EOF'
   Support covers weekends since September.
   EOF

   # insert a new section after an existing one
   vaulty write <page> --after "Owners" <<'EOF'
   ## Risks

   - Single payment provider.
   EOF
   ```

   - `--append` adds a blank line plus stdin at the end of the section's
     whole region: on a section with subheadings that is the end of its
     last subsection. Appended text may contain only headings deeper than
     the section's. To add an item inside an existing list, replace the
     section instead.
   - `--after` inserts after the whole region of that heading. The new
     heading's level must be the same or deeper, and its text must not
     exist on the page yet. Keep subheading text unique too; duplicate
     headings can't be targeted later.
   - `--touch` sets frontmatter `updated:` to today; use it for
     meaningful changes. `--dry-run` prints the resulting section.
   - Never read the whole page just to edit one section.
4. **Exit 3 is a refusal; nothing was written.** On `section changed since
   read`, read the section again and redo the edit on the new text. Don't
   work around it. Other refusals name the reason: the section is the
   Timeline (use `timeline append`), the content contains a standalone
   `---` line or a heading that would end the section early, or the new
   heading already exists. Exit 2 `ambiguous section` means two headings
   share that text: tell the user, don't guess.

A small free-form change inside one section may still use your own edit
tool, after reading just that section with `vaulty read --section`.

## Frontmatter

Change fields with `vaulty frontmatter`, never by hand-editing the YAML.
It keeps the page's quoting, list style, comments and key order:

```bash
vaulty frontmatter get <page> status related      # keys as written (--json: decoded values)
vaulty frontmatter set <page> status=mature --touch
vaulty frontmatter add <page> related "[[billing]]"  # list item; skipped if present; creates the list
vaulty frontmatter remove <page> related "[[billing]]"
vaulty frontmatter unset <page> aliases
```

- `set` is for scalars and refuses a list key; `add`/`remove` are for
  lists. Several `key=value` pairs or values fit in one call.
- A value starting with `-` needs `--` before it:
  `vaulty frontmatter add <page> aliases -- -legacy`.
- `--touch` only bumps `updated:` when something else changed; a call
  that changes nothing prints `unchanged`.
- Exit 3 on a shape vaulty won't edit (nested maps, block scalars,
  multi-line flow lists, invalid YAML, duplicate keys): tell the user
  rather than rewriting the frontmatter yourself.

## Timeline entries

```bash
vaulty timeline append <page> "- **YYYY-MM-DD** | <who> — <what changed and why>"
```

- vaulty inserts the entry in date order and creates the divider and
  `## Timeline` heading if the page has none. The leading `- ` is optional.
- `--touch` also sets `updated:` to today; use it when the entry reflects
  a meaningful change to the page, not for trivial backlinks.
- `--dry-run` prints the resulting Timeline without writing.
- A refusal (exit 3) means the page or entry is malformed: fix what the
  message names, don't hand-edit around it.
- Write claims, not transcript: one line with the outcome and its reason.
- Keep history out of compiled truth; history belongs in the Timeline.

## Log entries

Every operation on the vault gets one log entry:

```bash
vaulty log append <op> "<title>" [--body "<one line>"] [--date YYYY-MM-DD]
```

`<op>` is a short verb the vault uses consistently (e.g. `ingest`,
`update`, `query`). Check the ones in use with `vaulty log last -n 20`, or
filter with `--op <op>` / `--since YYYY-MM`. Never edit `log.md` by hand.

## After writing

- Run `vaulty lint <path>` on pages you changed, with the file path
  (`wiki/systems/billing.md`, not a bare name), or `vaulty lint --changed`;
  fix errors before committing.
- Follow the vault's own conventions (its CLAUDE.md or README) for index
  updates and commits. vaulty doesn't do those.
