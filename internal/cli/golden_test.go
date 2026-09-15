package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Golden CLI harness (DESIGN.md §10.2). Each case lives in
// testdata/golden/<case>/: args.json (CLI args; --vault <tmp> is appended),
// vault/ (input tree, copied to t.TempDir()), optional stdin, optional env
// (lines of K=V; VAULTY_TODAY=2026-09-15 is always set), want.stdout,
// want.exit, optional want.stderr, optional want.vault/ (full expected tree
// after the command). `go test ./... -update` rewrites the want.* files.

var update = flag.Bool("update", false, "update golden files")

func TestGolden(t *testing.T) {
	root := "testdata/golden"
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("no golden cases yet")
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) { runGoldenCase(t, filepath.Join(root, name)) })
	}
}

func runGoldenCase(t *testing.T, dir string) {
	t.Helper()

	argsB, err := os.ReadFile(filepath.Join(dir, "args.json"))
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(argsB, &args); err != nil {
		t.Fatalf("args.json: %v", err)
	}

	tmp := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, "vault")); err == nil {
		if err := copyDir(filepath.Join(dir, "vault"), tmp); err != nil {
			t.Fatal(err)
		}
	}

	// Optional git.sh: shell commands run in tmp (cwd) before the CLI, for
	// cases that need real git state (e.g. --changed). Own commit identity
	// via -c flags — the sandbox has no configured git user. Resolved to an
	// absolute path since cmd.Dir is tmp, not this package's directory.
	if script, err := filepath.Abs(filepath.Join(dir, "git.sh")); err == nil {
		if _, statErr := os.Stat(script); statErr == nil {
			cmd := exec.Command("sh", script)
			cmd.Dir = tmp
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git.sh: %v\n%s", err, out)
			}
		}
	}

	t.Setenv("VAULTY_TODAY", "2026-09-15")
	t.Setenv("VAULTY_ROOT", "")
	stdinTTYOverride = nil
	t.Cleanup(func() { stdinTTYOverride = nil })
	if envB, err := os.ReadFile(filepath.Join(dir, "env")); err == nil {
		for _, line := range strings.Split(strings.TrimRight(string(envB), "\n"), "\n") {
			if line == "" {
				continue
			}
			kv := strings.SplitN(line, "=", 2)
			if len(kv) != 2 {
				continue
			}
			// VAULTY_STDIN_TTY is a golden-test-only hook (tty.go's
			// stdinTTYOverride): it forces the answer for cases that
			// exercise --accept-growth's merge logic itself, without a
			// real terminal. It is NOT an env var the production binary
			// reads — never set it outside this harness.
			if kv[0] == "VAULTY_STDIN_TTY" {
				b := kv[1] == "1"
				stdinTTYOverride = &b
				continue
			}
			t.Setenv(kv[0], kv[1])
		}
	}

	var stdin bytes.Buffer
	if b, err := os.ReadFile(filepath.Join(dir, "stdin")); err == nil {
		stdin.WriteString(strings.ReplaceAll(string(b), "{{VAULT}}", tmp))
	}

	for i, a := range args {
		args[i] = strings.ReplaceAll(a, "{{VAULT}}", tmp)
	}
	// --vault goes first: some subcommands (append) disable flag/positional
	// interspersion because their own positional args start with "-", so a
	// flag appended after them would be swallowed as a positional instead.
	full := append([]string{"--vault", tmp}, args...)
	var stdout, stderr bytes.Buffer
	exit := Execute("test", full, &stdin, &stdout, &stderr)

	if *update {
		os.WriteFile(filepath.Join(dir, "want.stdout"), stdout.Bytes(), 0o644)
		os.WriteFile(filepath.Join(dir, "want.exit"), []byte(strconv.Itoa(exit)), 0o644)
		if _, err := os.Stat(filepath.Join(dir, "want.stderr")); err == nil {
			os.WriteFile(filepath.Join(dir, "want.stderr"), stderr.Bytes(), 0o644)
		}
		if _, err := os.Stat(filepath.Join(dir, "want.vault")); err == nil {
			os.RemoveAll(filepath.Join(dir, "want.vault"))
			copyDir(tmp, filepath.Join(dir, "want.vault"))
		}
		return
	}

	wantStdout, err := os.ReadFile(filepath.Join(dir, "want.stdout"))
	if err != nil {
		t.Fatalf("want.stdout: %v", err)
	}
	if stdout.String() != string(wantStdout) {
		t.Errorf("stdout mismatch:\n got: %q\nwant: %q", stdout.String(), string(wantStdout))
	}

	wantExitB, err := os.ReadFile(filepath.Join(dir, "want.exit"))
	if err != nil {
		t.Fatalf("want.exit: %v", err)
	}
	wantExit, err := strconv.Atoi(strings.TrimSpace(string(wantExitB)))
	if err != nil {
		t.Fatalf("want.exit: %v", err)
	}
	if exit != wantExit {
		t.Errorf("exit = %d, want %d (stderr: %s)", exit, wantExit, stderr.String())
	}

	if wantStderrB, err := os.ReadFile(filepath.Join(dir, "want.stderr")); err == nil {
		if stderr.String() != string(wantStderrB) {
			t.Errorf("stderr mismatch:\n got: %q\nwant: %q", stderr.String(), string(wantStderrB))
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "want.vault")); err == nil {
		diffTrees(t, filepath.Join(dir, "want.vault"), tmp)
	}
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func diffTrees(t *testing.T, want, got string) {
	t.Helper()
	wantFiles := map[string][]byte{}
	filepath.WalkDir(want, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(want, p)
		b, _ := os.ReadFile(p)
		wantFiles[rel] = b
		return nil
	})
	gotFiles := map[string][]byte{}
	filepath.WalkDir(got, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(got, p)
		b, _ := os.ReadFile(p)
		gotFiles[rel] = b
		return nil
	})
	var keys []string
	for k := range wantFiles {
		keys = append(keys, k)
	}
	for k := range gotFiles {
		if _, ok := wantFiles[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		w, wok := wantFiles[k]
		g, gok := gotFiles[k]
		if !wok {
			t.Errorf("unexpected file in output vault: %s", k)
			continue
		}
		if !gok {
			t.Errorf("missing file in output vault: %s", k)
			continue
		}
		if string(w) != string(g) {
			t.Errorf("vault file mismatch %s:\n got: %q\nwant: %q", k, string(g), string(w))
		}
	}
}
