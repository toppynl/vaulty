# vaulty

A single static Go binary for LLM-maintained markdown vaults. First scope:
the `## Timeline` convention (`vaulty timeline lint|read|append`). See
[`DESIGN.md`](DESIGN.md) for the full spec.

## Install

```bash
scripts/install.sh
```

Prefers `gh release download` (private repo — needs `gh` auth with access to
`toppynl/vaulty`); falls back to `go install` when `go` is available. See
[`docs/claude-code.md`](docs/claude-code.md) for wiring vaulty into a
vault's Claude Code setup (hook, permission allowlist, skill snippets).

## Usage

```bash
vaulty timeline lint [paths...]         # check format + page hygiene
vaulty timeline lint --write-baseline   # recompute the TL006/TL008/PG002 ratchet baseline
vaulty timeline read <page> [--timeline] [--since D] [--last N]
vaulty timeline append <page> "- **YYYY-MM-DD** | source — what" [--touch] [--dry-run]
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

Tag `vX.Y.Z` to trigger `.github/workflows/release.yml` (goreleaser,
`linux`/`darwin` × `amd64`/`arm64`). Verify the config first:

```bash
go run github.com/goreleaser/goreleaser/v2@latest check
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
```
