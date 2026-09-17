// Package vaulty embeds the agent skills and agents that ship with the CLI,
// so `vaulty setup` installs exactly what the same release's Claude Code
// plugin ships.
package vaulty

import "embed"

// AgentFiles holds skills/<name>/SKILL.md and agents/<name>.md.
//
//go:embed skills agents
var AgentFiles embed.FS
