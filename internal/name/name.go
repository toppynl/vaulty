// Package name is the single source of the tool's name. Renaming the tool =
// change Binary here + sed the module path (go.mod, imports) + rename
// cmd/<name>/ + .goreleaser.yaml + examples/ + DESIGN.md (see DESIGN.md §2).
package name

import "strings"

// Binary is the executable and cobra root command name.
const Binary = "vaulty"

// ConfigFile is looked up at the vault root.
const ConfigFile = "." + Binary + ".yml"

// Environment variables, derived so there is one literal.
var (
	EnvRoot  = strings.ToUpper(Binary) + "_ROOT"  // overrides root discovery
	EnvToday = strings.ToUpper(Binary) + "_TODAY" // pins "today" for --touch and tests
)
