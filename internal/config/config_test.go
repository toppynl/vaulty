package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() invalid: %v", err)
	}
}

func TestLoadOverlay(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vaulty.yml")
	os.WriteFile(p, []byte("version: 1\ndirs: [wiki]\nlint:\n  severity:\n    TL006: error\n"), 0o644)

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Dirs) != 1 || cfg.Dirs[0] != "wiki" {
		t.Errorf("dirs override not applied: %v", cfg.Dirs)
	}
	// Untouched default preserved.
	if cfg.Timeline.Heading != "## Timeline" {
		t.Errorf("timeline.heading default lost: %q", cfg.Timeline.Heading)
	}
	if cfg.Lint.Severity["TL006"] != "error" {
		t.Errorf("severity override not applied: %v", cfg.Lint.Severity)
	}
	// Merge, not replace: other page_checks defaults survive.
	if cfg.Lint.PageChecks.CompiledTruthMaxTokens != 3000 {
		t.Errorf("page_checks default lost: %+v", cfg.Lint.PageChecks)
	}
}

func TestLoadUnknownKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vaulty.yml")
	os.WriteFile(p, []byte("version: 1\nbogus: true\n"), 0o644)

	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestValidateInvalidValues(t *testing.T) {
	cases := []func(*Config){
		func(c *Config) { c.Version = 2 },
		func(c *Config) { c.Dirs = nil },
		func(c *Config) { c.Timeline.Heading = "Timeline" },
		func(c *Config) { c.Timeline.Divider = "  " },
		func(c *Config) { c.Timeline.EntryGap = "sometimes" },
		func(c *Config) { c.Lint.PageChecks.CompiledTruthMaxTokens = 0 },
		func(c *Config) { c.Lint.Severity = map[string]string{"TL006": "critical"} },
	}
	for i, mutate := range cases {
		c := Default()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
