package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/frontmatter"
	"github.com/toppynl/vaulty/internal/vault"
)

func (a *app) newFrontmatterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "frontmatter",
		Short: "Read and edit a page's flat YAML frontmatter, line by line",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "get <page> [key...]",
			Short: "Print the frontmatter block, or the given keys as written",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.runFrontmatterGet(args[0], args[1:])
			},
		},
		a.newFrontmatterWriteCmd("set <page> key=value...", "Set scalar keys (split on the first '=')", 2, frontmatter.OpSet),
		a.newFrontmatterWriteCmd("add <page> <key> <value>...", "Append items to a list key, skipping ones already present", 3, frontmatter.OpAdd),
		a.newFrontmatterWriteCmd("remove <page> <key> <value>...", "Remove items from a list key", 3, frontmatter.OpRemove),
		a.newFrontmatterWriteCmd("unset <page> <key>...", "Remove keys", 2, frontmatter.OpUnset),
	)
	return cmd
}

type frontmatterOpts struct {
	touch  bool
	dryRun bool
}

func (a *app) newFrontmatterWriteCmd(use, short string, minArgs int, kind frontmatter.OpKind) *cobra.Command {
	var o frontmatterOpts
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(minArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			ops, err := frontmatterOps(kind, args[1:])
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: err}
			}
			return a.runFrontmatterWrite(o, args[0], ops)
		},
	}
	cmd.Flags().BoolVar(&o.touch, "touch", false, "also set frontmatter updated: to today")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print the resulting frontmatter block, write nothing")
	return cmd
}

// frontmatterOps turns a write subcommand's arguments (after <page>) into
// ops, refusing keys that aren't simple top-level identifiers.
func frontmatterOps(kind frontmatter.OpKind, args []string) ([]frontmatter.Op, error) {
	var ops []frontmatter.Op
	switch kind {
	case frontmatter.OpSet:
		for _, arg := range args {
			k, v, ok := strings.Cut(arg, "=")
			if !ok {
				return nil, fmt.Errorf("set: %q is not key=value", arg)
			}
			ops = append(ops, frontmatter.Op{Kind: kind, Key: k, Values: []string{v}})
		}
	case frontmatter.OpAdd, frontmatter.OpRemove:
		ops = append(ops, frontmatter.Op{Kind: kind, Key: args[0], Values: args[1:]})
	case frontmatter.OpUnset:
		for _, k := range args {
			ops = append(ops, frontmatter.Op{Kind: kind, Key: k})
		}
	}
	for _, op := range ops {
		if !frontmatter.ValidKey(op.Key) {
			return nil, fmt.Errorf("invalid key %q (want [A-Za-z0-9_-]+)", op.Key)
		}
	}
	return ops, nil
}

// readFrontmatterPage resolves pageArg like timeline append (DESIGN.md §3.3, §3.5).
func (a *app) readFrontmatterPage(pageArg string) (v *vault.Vault, rel, full string, src []byte, err error) {
	v, err = a.openVault()
	if err != nil {
		return nil, "", "", nil, err
	}
	rel, err = v.Resolve(pageArg)
	if err != nil {
		return nil, "", "", nil, &ExitError{Code: ExitUsage, Err: err}
	}
	full, err = v.ContentFile(rel)
	if err != nil {
		return nil, "", "", nil, &ExitError{Code: ExitUsage, Err: err}
	}
	src, err = os.ReadFile(full)
	if err != nil {
		return nil, "", "", nil, &ExitError{Code: ExitIO, Err: err}
	}
	return v, rel, full, src, nil
}

func (a *app) runFrontmatterGet(pageArg string, keys []string) error {
	for _, k := range keys {
		if !frontmatter.ValidKey(k) {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("invalid key %q (want [A-Za-z0-9_-]+)", k)}
		}
	}
	_, rel, _, src, err := a.readFrontmatterPage(pageArg)
	if err != nil {
		return err
	}
	d := doc.Parse(rel, src)
	if d.FMUnclosed {
		return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: frontmatter is not closed")}
	}
	if !d.HasFM {
		return &ExitError{Code: ExitFindings, Err: fmt.Errorf("no frontmatter in %s", rel)}
	}
	inner := frontmatter.Inner(d)

	if a.flags.json {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(inner), &m); err != nil {
			return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: frontmatter is not valid YAML: %v", err)}
		}
		out := map[string]any{}
		for k, v := range m {
			out[k] = jsonValue(v)
		}
		if len(keys) > 0 {
			picked := map[string]any{}
			for _, k := range keys {
				if v, ok := out[k]; ok {
					picked[k] = v
				} else {
					fmt.Fprintf(a.stderr, "vaulty: no key %s\n", k)
				}
			}
			if len(picked) == 0 {
				return &ExitError{Code: ExitFindings}
			}
			out = picked
		}
		return a.writeJSON(struct {
			Path        string         `json:"path"`
			Frontmatter map[string]any `json:"frontmatter"`
		}{rel, out})
	}

	if len(keys) == 0 {
		fmt.Fprint(a.stdout, inner)
		return nil
	}
	b := frontmatter.Parse(inner)
	found := 0
	for _, k := range keys {
		e := b.Find(k)
		if e == nil {
			fmt.Fprintf(a.stderr, "vaulty: no key %s\n", k)
			continue
		}
		found++
		fmt.Fprintln(a.stdout, strings.Join(b.Lines[e.Start:e.End], "\n"))
	}
	if found == 0 {
		return &ExitError{Code: ExitFindings}
	}
	return nil
}

// jsonValue makes a decoded YAML value JSON-friendly: dates become strings
// and nested maps/lists are converted recursively.
func jsonValue(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, it := range t {
			out[i] = jsonValue(it)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, it := range t {
			out[k] = jsonValue(it)
		}
		return out
	case nil, string, bool, int, int64, uint64, float64:
		return t
	default:
		s, _ := frontmatter.Stringify(t)
		return s
	}
}

func (a *app) runFrontmatterWrite(o frontmatterOpts, pageArg string, ops []frontmatter.Op) error {
	v, rel, full, orig, err := a.readFrontmatterPage(pageArg)
	if err != nil {
		return err
	}
	touchKey := ""
	if o.touch {
		touchKey = v.Config.Frontmatter.UpdatedKey
	}

	res, err := frontmatter.Apply(orig, ops, touchKey, a.today())
	if err != nil {
		if errors.Is(err, frontmatter.ErrRefused) {
			return &ExitError{Code: ExitRefused, Err: err}
		}
		return &ExitError{Code: ExitIO, Err: err}
	}

	if len(res.Changed) == 0 {
		if a.flags.json {
			return a.writeJSON(frontmatterJSON(rel, res, false))
		}
		fmt.Fprintf(a.stdout, "unchanged %s\n", rel)
		return nil
	}

	// Safety check runs before every write, including --dry-run.
	if err := frontmatter.Verify(orig, res.New, ops, touchKey, a.today(), res.Touched); err != nil {
		return &ExitError{Code: ExitRefused, Err: err}
	}

	if o.dryRun {
		if a.flags.json {
			return a.writeJSON(frontmatterJSON(rel, res, true))
		}
		nd := doc.Parse(rel, res.New)
		a.stdout.Write(res.New[nd.Frontmatter.Start:nd.Frontmatter.End])
		fmt.Fprintln(a.stderr, "vaulty: dry-run, nothing written")
		return nil
	}

	// Re-read to detect a concurrent change (DESIGN.md §8.7).
	current, err := os.ReadFile(full)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	if string(current) != string(orig) {
		return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: file changed during frontmatter edit")}
	}
	if err := atomicWrite(full, res.New); err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	if a.flags.json {
		return a.writeJSON(frontmatterJSON(rel, res, false))
	}
	fmt.Fprintf(a.stdout, "updated %s: %s\n", rel, strings.Join(res.Changed, ", "))
	return nil
}

type frontmatterResultJSON struct {
	Path      string   `json:"path"`
	Changed   []string `json:"changed"`
	Unchanged []string `json:"unchanged"`
	Touched   bool     `json:"touched"`
	DryRun    bool     `json:"dry_run"`
}

func frontmatterJSON(path string, res *frontmatter.Result, dryRun bool) frontmatterResultJSON {
	return frontmatterResultJSON{
		Path:      path,
		Changed:   append([]string{}, res.Changed...),
		Unchanged: append([]string{}, res.Unchanged...),
		Touched:   res.Touched,
		DryRun:    dryRun,
	}
}
