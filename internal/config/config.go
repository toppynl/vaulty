// Package config loads the per-vault .vaulty.yml (DESIGN.md §4). Every key is
// optional; absent keys keep the defaults, which match Robin's vault.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/toppynl/vaulty/internal/name"
)

// FileName is looked up at the vault root (derived from name.Binary).
const FileName = name.ConfigFile

// CurrentVersion is the only schema version this binary understands.
const CurrentVersion = 1

type Config struct {
	Version     int         `yaml:"version" json:"version"`
	Dirs        []string    `yaml:"dirs" json:"dirs"`
	Exclude     []string    `yaml:"exclude" json:"exclude"`
	Frontmatter Frontmatter `yaml:"frontmatter" json:"frontmatter"`
	Timeline    Timeline    `yaml:"timeline" json:"timeline"`
	Lint        Lint        `yaml:"lint" json:"lint"`
}

type Frontmatter struct {
	UpdatedKey string `yaml:"updated_key" json:"updated_key"`
}

type Timeline struct {
	Heading  string `yaml:"heading" json:"heading"`     // full heading line, e.g. "## Timeline"
	Divider  string `yaml:"divider" json:"divider"`     // divider line above the heading
	EntryGap string `yaml:"entry_gap" json:"entry_gap"` // "auto" | "0" | "1"
}

type Lint struct {
	HookPaths  []string          `yaml:"hook_paths" json:"hook_paths"`
	PageChecks PageChecks        `yaml:"page_checks" json:"page_checks"`
	Severity   map[string]string `yaml:"severity" json:"severity"` // code -> error|warning|off
	// BaselinePath is where `lint --write-baseline` writes, and where lint
	// reads the ratchet baseline from (DESIGN.md §6.1a). Resolved relative
	// to the vault root. Ratchet is inactive when this file doesn't exist.
	BaselinePath string `yaml:"baseline_path" json:"baseline_path"`
}

type PageChecks struct {
	Paths                  []string `yaml:"paths" json:"paths"`
	CompiledTruthMaxTokens int      `yaml:"compiled_truth_max_tokens" json:"compiled_truth_max_tokens"`
	Checklist              bool     `yaml:"checklist" json:"checklist"`
	TableKeywords          []string `yaml:"table_keywords" json:"table_keywords"`
	HeadingKeywords        []string `yaml:"heading_keywords" json:"heading_keywords"`
}

// Default returns the built-in configuration (Robin's vault layout).
func Default() *Config {
	return &Config{
		Version:     CurrentVersion,
		Dirs:        []string{"wiki", "me", "now", "archive"},
		Exclude:     []string{},
		Frontmatter: Frontmatter{UpdatedKey: "updated"},
		Timeline:    Timeline{Heading: "## Timeline", Divider: "---", EntryGap: "auto"},
		Lint: Lint{
			HookPaths: []string{"wiki/**"},
			PageChecks: PageChecks{
				Paths:                  []string{"wiki/**"},
				CompiledTruthMaxTokens: 3000,
				Checklist:              true,
				TableKeywords:          []string{"unit", "units", "step", "steps", "stap", "stappen"},
				HeadingKeywords:        []string{"agent log"},
			},
			Severity:     map[string]string{},
			BaselinePath: ".vaulty-baseline.json",
		},
	}
}

// Load reads path and overlays it on Default(). Unknown keys are an error.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks value ranges. Called by Load; call it on hand-built configs too.
func (c *Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported version %d (want %d)", c.Version, CurrentVersion)
	}
	if len(c.Dirs) == 0 {
		return errors.New("dirs must not be empty")
	}
	if !strings.HasPrefix(c.Timeline.Heading, "#") {
		return fmt.Errorf("timeline.heading %q must be a markdown heading", c.Timeline.Heading)
	}
	if strings.TrimSpace(c.Timeline.Divider) == "" {
		return errors.New("timeline.divider must not be empty")
	}
	switch c.Timeline.EntryGap {
	case "auto", "0", "1":
	default:
		return fmt.Errorf("timeline.entry_gap %q: want auto, 0 or 1", c.Timeline.EntryGap)
	}
	if c.Lint.PageChecks.CompiledTruthMaxTokens <= 0 {
		return errors.New("lint.page_checks.compiled_truth_max_tokens must be > 0")
	}
	if strings.TrimSpace(c.Lint.BaselinePath) == "" {
		return errors.New("lint.baseline_path must not be empty")
	}
	for code, sev := range c.Lint.Severity {
		switch sev {
		case "error", "warning", "off":
		default:
			return fmt.Errorf("lint.severity.%s %q: want error, warning or off", code, sev)
		}
	}
	return nil
}
