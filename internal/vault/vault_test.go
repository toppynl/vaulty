package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/toppynl/vaulty/internal/config"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPrecedence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	sub := filepath.Join(root, "wiki", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// 4: nearest ancestor containing .git
	v, err := Open("", sub)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, _ := filepath.EvalSymlinks(root); v.Root != resolved {
		t.Errorf("git-root discovery: got %s want %s", v.Root, resolved)
	}
	if v.ConfigPath != "" {
		t.Errorf("expected no config path, got %s", v.ConfigPath)
	}

	// 3: nearest ancestor containing .vaulty.yml beats .git
	writeFile(t, filepath.Join(root, "wiki", config.FileName), "version: 1\ndirs: [wiki]\n")
	v2, err := Open("", sub)
	if err != nil {
		t.Fatal(err)
	}
	wikiResolved, _ := filepath.EvalSymlinks(filepath.Join(root, "wiki"))
	if v2.Root != wikiResolved {
		t.Errorf("config-root discovery: got %s want %s", v2.Root, wikiResolved)
	}
	if v2.ConfigPath == "" {
		t.Error("expected config path to be set")
	}

	// 1: --vault flag beats everything
	other := t.TempDir()
	v3, err := Open(other, sub)
	if err != nil {
		t.Fatal(err)
	}
	otherResolved, _ := filepath.EvalSymlinks(other)
	if v3.Root != otherResolved {
		t.Errorf("--vault: got %s want %s", v3.Root, otherResolved)
	}

	// 2: $VAULTY_ROOT beats ancestor discovery
	t.Setenv(EnvRoot, other)
	v4, err := Open("", sub)
	if err != nil {
		t.Fatal(err)
	}
	if v4.Root != otherResolved {
		t.Errorf("VAULTY_ROOT: got %s want %s", v4.Root, otherResolved)
	}
}

func TestOpenWorktreeGitFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git"), "gitdir: /elsewhere/.git/worktrees/x\n")
	v, err := Open("", root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(root)
	if v.Root != resolved {
		t.Errorf("got %s want %s", v.Root, resolved)
	}
}

func TestOpenFallsBackToStart(t *testing.T) {
	dir := t.TempDir()
	// Use a fresh subdir with no .git/.vaulty.yml anywhere above it within
	// the temp tree (t.TempDir() itself has no .git ancestor in CI/sandbox).
	v, err := Open("", dir)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	if v.Root != resolved && !hasAncestorMarker(dir) {
		t.Errorf("got %s want %s", v.Root, resolved)
	}
}

func hasAncestorMarker(dir string) bool {
	_, ok := findAncestorWith(dir, ".git")
	if ok {
		return true
	}
	_, ok = findAncestorWith(dir, config.FileName)
	return ok
}

func newTestVault(t *testing.T, root string) *Vault {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return &Vault{Root: resolved, Config: config.Default()}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "wiki", "systems", "widget.md"), "# Widget\n")
	writeFile(t, filepath.Join(root, "now", "priorities.md"), "# Priorities\n")
	writeFile(t, filepath.Join(root, "wiki", "dup1", "acme.md"), "# Acme 1\n")
	writeFile(t, filepath.Join(root, "wiki", "dup2", "acme.md"), "# Acme 2\n")
	v := newTestVault(t, root)

	cases := []struct {
		arg     string
		want    string
		wantErr error
	}{
		{"widget", "wiki/systems/widget.md", nil},
		{"[[widget]]", "wiki/systems/widget.md", nil},
		{"[[widget|Widget display]]", "wiki/systems/widget.md", nil},
		{"[[widget#section]]", "wiki/systems/widget.md", nil},
		{"wiki/systems/widget.md", "wiki/systems/widget.md", nil},
		{"wiki/systems/widget", "wiki/systems/widget.md", nil},
		{"nope-not-here", "", ErrNotFound},
		{"acme", "", ErrAmbiguous},
	}
	for _, c := range cases {
		got, err := v.Resolve(c.arg)
		if c.wantErr != nil {
			if err == nil || !isErr(err, c.wantErr) {
				t.Errorf("Resolve(%q): got err %v, want %v", c.arg, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("Resolve(%q): unexpected error %v", c.arg, err)
			continue
		}
		if got != c.want {
			t.Errorf("Resolve(%q) = %q, want %q", c.arg, got, c.want)
		}
	}
}

func TestResolveOutside(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "wiki", "a.md"), "# A\n")
	outsideDir := t.TempDir()
	writeFile(t, filepath.Join(outsideDir, "b.md"), "# B\n")
	v := newTestVault(t, root)

	rel, err := filepath.Rel(root, filepath.Join(outsideDir, "b.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Resolve(rel)
	if err == nil || !isErr(err, ErrOutside) && !isErr(err, ErrNotFound) {
		t.Errorf("expected outside/not-found error, got %v", err)
	}
}

func isErr(err, target error) bool {
	for e := err; e != nil; e = unwrap(e) {
		if e == target {
			return true
		}
	}
	return false
}

func unwrap(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

func TestWalkExcludes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "wiki", "a.md"), "")
	writeFile(t, filepath.Join(root, "wiki", "archive-ish.md"), "")
	writeFile(t, filepath.Join(root, "wiki", ".hidden", "b.md"), "")
	writeFile(t, filepath.Join(root, "wiki", "skip", "c.md"), "")
	writeFile(t, filepath.Join(root, "now", "d.md"), "")
	writeFile(t, filepath.Join(root, "missing-dir-is-fine"), "") // not a dir under config.dirs

	v := newTestVault(t, root)
	v.Config.Dirs = []string{"wiki", "now", "nonexistent"}
	v.Config.Exclude = []string{"wiki/skip/**"}

	files, err := v.Walk()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"now/d.md", "wiki/a.md", "wiki/archive-ish.md"}
	if len(files) != len(want) {
		t.Fatalf("got %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("got %v, want %v", files, want)
			break
		}
	}
}

func TestMatchGlob(t *testing.T) {
	if !MatchGlob("wiki/**", "wiki/a/b.md") {
		t.Error("wiki/** should match wiki/a/b.md")
	}
	if MatchGlob("wiki/**", "now/a.md") {
		t.Error("wiki/** should not match now/a.md")
	}
	if !MatchGlob("wiki/*.md", "wiki/a.md") {
		t.Error("wiki/*.md should match wiki/a.md")
	}
	if MatchGlob("wiki/*.md", "wiki/a/b.md") {
		t.Error("wiki/*.md should not match wiki/a/b.md")
	}
}
