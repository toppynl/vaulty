package setup

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func release(body string) fstest.MapFS {
	return fstest.MapFS{
		"skills/vaulty-read/SKILL.md": {Data: []byte("read " + body)},
		"agents/vault-reader.md":      {Data: []byte("agent " + body)},
	}
}

func statuses(t *testing.T, p *Plan) map[string]Status {
	t.Helper()
	out := map[string]Status{}
	for _, f := range p.Files {
		out[f.Rel] = f.Status
	}
	return out
}

func install(t *testing.T, src fstest.MapFS, target Target, root string, force bool) (*Plan, error) {
	t.Helper()
	p, err := NewPlan(src, target, root)
	if err != nil {
		t.Fatal(err)
	}
	return p, Apply(p, "test", force)
}

// A rerun with a newer release updates files setup wrote, but never silently
// replaces a file the user edited or a file setup didn't write.
func TestUpgradeUpdatesOwnFilesAndRefusesEdited(t *testing.T) {
	claude, _ := Lookup("claude")
	root := t.TempDir()
	if _, err := install(t, release("v1"), claude, root, false); err != nil {
		t.Fatal(err)
	}

	p, err := install(t, release("v2"), claude, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(t, p); got["skills/vaulty-read/SKILL.md"] != Updated || got["agents/vault-reader.md"] != Updated {
		t.Fatalf("upgrade statuses = %v, want both updated", got)
	}

	edited := filepath.Join(root, "skills", "vaulty-read", "SKILL.md")
	if err := os.WriteFile(edited, []byte("my notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	if err := os.MkdirAll(filepath.Join(foreign, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "agents", "vault-reader.md"), []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{root, foreign} {
		p, err = install(t, release("v3"), claude, dir, false)
		if err == nil || len(p.Conflicts()) != 1 {
			t.Fatalf("%s: err=%v conflicts=%v, want one conflict and a refusal", dir, err, p.Conflicts())
		}
	}
	if b, _ := os.ReadFile(edited); string(b) != "my notes" {
		t.Fatalf("edited file overwritten without --force: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "agents", "vault-reader.md")); string(b) != "agent v2" {
		t.Fatalf("refused install still wrote other files: %q", b)
	}

	if _, err := install(t, release("v3"), claude, root, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(edited); string(b) != "read v3" {
		t.Fatalf("--force did not replace edited file: %q", b)
	}
}

// --keep-existing (MarkKeptExisting) leaves a file vaulty didn't write
// byte-identical and out of the manifest, while everything else still
// installs normally.
func TestKeepExistingLeavesConflictsUntouched(t *testing.T) {
	claude, _ := Lookup("claude")
	root := t.TempDir()
	foreign := filepath.Join(root, "skills", "vaulty-read", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("my own notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := NewPlan(release("v1"), claude, root)
	if err != nil {
		t.Fatal(err)
	}
	p.MarkKeptExisting()
	if err := Apply(p, "test", false); err != nil {
		t.Fatal(err)
	}

	got := statuses(t, p)
	if got["skills/vaulty-read/SKILL.md"] != Kept {
		t.Fatalf("foreign file status = %v, want kept", got["skills/vaulty-read/SKILL.md"])
	}
	if got["agents/vault-reader.md"] != Created {
		t.Fatalf("agent file status = %v, want created", got["agents/vault-reader.md"])
	}
	if b, err := os.ReadFile(foreign); err != nil || string(b) != "my own notes" {
		t.Fatalf("kept file changed: %q, %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "agents", "vault-reader.md")); string(b) != "agent v1" {
		t.Fatalf("other file not applied: %q", b)
	}

	man, err := readManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := man.Files["skills/vaulty-read/SKILL.md"]; ok {
		t.Fatalf("kept file recorded in manifest, want it absent")
	}
}

// Only the claude target gets the agent; a symlinked destination is replaced,
// not written through.
func TestTargetsAndSymlink(t *testing.T) {
	agents, _ := Lookup("codex")
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "vaulty-read"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "skills", "vaulty-read", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	p, err := install(t, release("v1"), agents, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(t, p); len(got) != 1 || got["skills/vaulty-read/SKILL.md"] == "" {
		t.Fatalf("agents target files = %v, want only the skill", got)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Fatalf("wrote through symlink: %q", b)
	}
}
