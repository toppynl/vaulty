package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toppynl/vaulty"
)

// setup installs every skill the plugin ships (the embed pattern can't drift
// from skills/) into the vault root by default.
func TestSetupInstallsShippedSkills(t *testing.T) {
	skills, err := fs.Glob(vaulty.AgentFiles, "skills/*/SKILL.md")
	if err != nil || len(skills) < 4 {
		t.Fatalf("embedded skills = %v (%v), want vaulty-read/-write/-maintain/-setup", skills, err)
	}
	v := t.TempDir()
	writeFile(t, filepath.Join(v, ".vaulty.yml"), "version: 1\n")

	stdout, stderr, exit := runCLI(t, v, "setup", "claude", "opencode")
	if exit != ExitOK {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	for _, rel := range skills {
		for _, root := range []string{".claude", ".agents"} {
			if _, err := os.Stat(filepath.Join(v, root, rel)); err != nil {
				t.Errorf("%s/%s not installed: %v\n%s", root, rel, err, stdout)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(v, ".claude", "agents", "vault-reader.md")); err != nil {
		t.Errorf("vault-reader agent not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v, ".agents", "agents")); err == nil {
		t.Errorf("agents target got Claude agent files")
	}

	_, stderr, exit = runCLI(t, v, "setup", "cursor")
	if exit != ExitUsage || !strings.Contains(stderr, "unknown target") {
		t.Fatalf("unknown target: exit %d, stderr %q", exit, stderr)
	}
}

// --keep-existing and --force are mutually exclusive, like --dir and --global.
func TestSetupKeepExistingAndForceMutuallyExclusive(t *testing.T) {
	v := t.TempDir()
	writeFile(t, filepath.Join(v, ".vaulty.yml"), "version: 1\n")

	_, stderr, exit := runCLI(t, v, "setup", "claude", "--keep-existing", "--force")
	if exit != ExitUsage || !strings.Contains(stderr, "--keep-existing") {
		t.Fatalf("keep-existing + force: exit %d, stderr %q, want %d and a mention of --keep-existing", exit, stderr, ExitUsage)
	}
}
