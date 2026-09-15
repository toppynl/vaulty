// Command vaulty is a CLI for LLM-maintained markdown knowledge vaults.
// See DESIGN.md for the command tree and semantics.
package main

import (
	"os"

	"github.com/toppynl/vaulty/internal/cli"
)

// version is set at build time by goreleaser (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	os.Exit(cli.Execute(version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
