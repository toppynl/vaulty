# vaulty

A single static Go binary for LLM-maintained markdown vaults. First scope:
the `## Timeline` convention (`vaulty timeline lint|read|append`). See
[`DESIGN.md`](DESIGN.md) for the full spec.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/toppynl/vaulty/main/scripts/install.sh | bash
```

or, from a checkout: `scripts/install.sh`. Downloads the latest release
binary (no GitHub auth, no `gh`, no Go toolchain needed), verifies it
against `checksums.txt`, and installs to `${VAULTY_INSTALL_DIR:-$HOME/.local/bin}`.
Rerunning it upgrades in place (idempotent: no-op if already at the latest
version); `--force` reinstalls unconditionally, `VAULTY_VERSION=vX.Y.Z`
pins a specific release. Falls back to `go install` only when neither
`curl` nor `wget` is available. See [`docs/claude-code.md`](docs/claude-code.md)
for wiring vaulty into a vault's Claude Code setup (hook, permission
allowlist, skill snippets).

## Usage

```bash
vaulty timeline lint [paths...]         # check format + page hygiene + shard hygiene (SH*, DESIGN.md §16)
vaulty timeline lint --write-baseline   # recompute the TL006/TL008/PG002 ratchet baseline
vaulty timeline read <page> [--timeline] [--since D] [--last N]
vaulty timeline read <page> --headings                 # list section headings (line, lines, bytes)
vaulty timeline read <page> --section "<heading text>" # print just that section
vaulty timeline read <page> [...] --max-bytes N        # cap the printed content, report what was cut
vaulty timeline append <page> "- **YYYY-MM-DD** | source — what" [--touch] [--dry-run]

vaulty log append <op> <title> [--body TEXT] [--date YYYY-MM-DD]  # appends to log.md
vaulty log last [-n N] [--op OP] [--since D]                      # most recent entries
vaulty log lint                                                   # report malformed entries
```

`<page>` accepts a bare name (`toppy`), a path (`wiki/systems/toppy.md`), or
a `[[wikilink]]`. Run any subcommand with `--json` for machine-readable
output, or `vaulty timeline <cmd> --help` for the full flag list.

## Develop

```bash
go build ./...
go vet ./...
go test ./...
```

Golden CLI tests live in `internal/cli/testdata/golden/`; `go test ./... -update`
regenerates the `want.*` files after an intentional behavior change.

The vault round-trip check (`scripts/parity/roundtrip_test.go`, package
`parity`) needs a local copy of the real vault (`VAULTY_PARITY_ROOT=...`)
and is not part of `go test ./...` in CI — see DESIGN.md §10.3.

## Release

Releases are cut by [release-please](https://github.com/googleapis/release-please):
every PR title must be a conventional-commit subject (`feat:`, `fix:`,
`feat!:` for a breaking change, ...) — merges are squashed, so the PR
title becomes the commit release-please reads. `pr-title.yml` enforces
this on every PR.

On push to `main`, `release-please.yml` keeps an up-to-date "release PR"
that accumulates `CHANGELOG.md` entries from the merged PR titles.
Merging that release PR is the release: release-please tags `vX.Y.Z` and
publishes a GitHub Release with the changelog notes, then, in the same
workflow run, a `goreleaser` job checks out that tag and runs goreleaser
(`linux`/`darwin` × `amd64`/`arm64`) to attach the archives and
`checksums.txt` to it. (A tag/release created via `GITHUB_TOKEN` doesn't
trigger other workflows, which is why goreleaser runs as a second job in
the same workflow rather than depending on the tag-push `release.yml`.)

`release.yml` (tag-push triggered) remains for hand-pushed tags — a
manual escape hatch. It checks whether a release already exists for the
tag first and skips goreleaser if so, so it can never double-release a
tag release-please already cut.

Verify the goreleaser config directly:

```bash
go run github.com/goreleaser/goreleaser/v2@latest check
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
```
