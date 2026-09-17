// Package setup installs the embedded agent skills (and, where the harness
// supports it, the vault-reader agent) into a project or home directory for
// one agent harness. DESIGN.md §20.
//
// Each install root keeps a manifest (.vaulty-setup.json) with the sha256 of
// every file it wrote. A rerun updates a file only when it still matches the
// manifest (nobody edited it since); anything else that differs is a conflict
// and nothing is written unless the caller forces it.
package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestFile is the manifest's name inside an install root.
const ManifestFile = ".vaulty-setup.json"

// Target is one harness family: where its skills (and agents) live.
type Target struct {
	Name    string   // canonical name, e.g. "claude"
	Aliases []string // other names accepted on the command line
	For     string   // harnesses served, for help and output
	Project string   // install root relative to the project dir
	Global  string   // install root relative to the home dir
	Agents  bool     // also install agents/<name>.md into <root>/agents
}

// Targets are the supported harness families, in help order.
var Targets = []Target{
	{
		Name: "agents", Aliases: []string{"codex", "gemini", "opencode"},
		For:     "Codex, Gemini CLI, OpenCode, pi (the shared .agents/skills location)",
		Project: ".agents", Global: ".agents",
	},
	{
		Name: "claude", For: "Claude Code (skills and the vault-reader agent)",
		Project: ".claude", Global: ".claude", Agents: true,
	},
	{
		Name: "pi", For: "pi (its own .pi/skills location)",
		Project: ".pi", Global: filepath.Join(".pi", "agent"),
	},
}

// Lookup resolves a target by name or alias (case-insensitive).
func Lookup(name string) (Target, bool) {
	name = strings.ToLower(name)
	for _, t := range Targets {
		if t.Name == name {
			return t, true
		}
		for _, a := range t.Aliases {
			if a == name {
				return t, true
			}
		}
	}
	return Target{}, false
}

// Names lists every accepted target name, aliases included.
func Names() []string {
	var out []string
	for _, t := range Targets {
		out = append(out, t.Name)
		out = append(out, t.Aliases...)
	}
	return out
}

// Status is what happens (or would happen) to one file.
type Status string

const (
	Created   Status = "created"
	Updated   Status = "updated"
	Unchanged Status = "unchanged"
	Conflict  Status = "conflict" // exists, differs, not written by setup (or edited since)
)

// File is one planned file.
type File struct {
	Rel     string `json:"path"`   // relative to the install root, slash-separated
	Dest    string `json:"dest"`   // absolute destination
	Status  Status `json:"status"` // planned outcome
	Reason  string `json:"reason,omitempty"`
	content []byte
}

// Plan is the planned install of one target into one root.
type Plan struct {
	Target Target `json:"-"`
	Name   string `json:"target"`
	Root   string `json:"root"`
	Files  []File `json:"files"`
}

// Conflicts returns the files that block a non-forced install.
func (p *Plan) Conflicts() []File {
	var out []File
	for _, f := range p.Files {
		if f.Status == Conflict {
			out = append(out, f)
		}
	}
	return out
}

type manifest struct {
	Version string            `json:"version"`
	Files   map[string]string `json:"files"` // rel path -> sha256 hex
}

// NewPlan compares the embedded files for t with what is on disk under root.
func NewPlan(src fs.FS, t Target, root string) (*Plan, error) {
	want, err := wanted(src, t)
	if err != nil {
		return nil, err
	}
	man, err := readManifest(root)
	if err != nil {
		return nil, err
	}
	p := &Plan{Target: t, Name: t.Name, Root: root}
	for _, rel := range sortedKeys(want) {
		f := File{Rel: rel, Dest: filepath.Join(root, filepath.FromSlash(rel)), content: want[rel]}
		f.Status, f.Reason = classify(f.Dest, want[rel], man.Files[rel])
		p.Files = append(p.Files, f)
	}
	return p, nil
}

// wanted maps install-root-relative paths to embedded contents.
func wanted(src fs.FS, t Target) (map[string][]byte, error) {
	out := map[string][]byte{}
	add := func(dir string) error {
		return fs.WalkDir(src, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(src, p)
			if err != nil {
				return err
			}
			out[p] = b
			return nil
		})
	}
	if err := add("skills"); err != nil {
		return nil, err
	}
	if t.Agents {
		if err := add("agents"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func classify(dest string, content []byte, recorded string) (Status, string) {
	st, err := os.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return Created, ""
	}
	if err != nil {
		return Conflict, err.Error()
	}
	if !st.Mode().IsRegular() {
		return Conflict, "not a regular file"
	}
	cur, err := os.ReadFile(dest)
	if err != nil {
		return Conflict, err.Error()
	}
	if string(cur) == string(content) {
		return Unchanged, ""
	}
	if recorded != "" && sum(cur) == recorded {
		return Updated, ""
	}
	if recorded == "" {
		return Conflict, "exists and was not installed by vaulty setup"
	}
	return Conflict, "edited since vaulty setup installed it"
}

// Apply writes the plan. With force, conflicts are overwritten; without it,
// a plan with conflicts writes nothing and returns an error.
func Apply(p *Plan, version string, force bool) error {
	if c := p.Conflicts(); len(c) > 0 && !force {
		return fmt.Errorf("%d file(s) would be overwritten; rerun with --force to replace them", len(c))
	}
	man, err := readManifest(p.Root)
	if err != nil {
		return err
	}
	for i, f := range p.Files {
		if f.Status != Unchanged {
			if err := writeFile(f.Dest, f.content); err != nil {
				return err
			}
			if f.Status == Conflict {
				p.Files[i].Status = Updated
			}
		}
		man.Files[f.Rel] = sum(f.content)
	}
	man.Version = version
	return writeManifest(p.Root, man)
}

func writeFile(dest string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// Replace, never write through, whatever is there (e.g. a symlink).
	if err := os.Remove(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(dest, content, 0o644)
}

func readManifest(root string) (*manifest, error) {
	m := &manifest{Files: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(root, ManifestFile), err)
	}
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	return m, nil
}

func writeManifest(root string, m *manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(root, ManifestFile), append(b, '\n'))
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return path.Clean(out[i]) < path.Clean(out[j]) })
	return out
}
