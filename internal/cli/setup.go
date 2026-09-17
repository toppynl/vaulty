package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/toppynl/vaulty"
	"github.com/toppynl/vaulty/internal/setup"
)

type setupOpts struct {
	global       bool
	dir          string
	force        bool
	dryRun       bool
	keepExisting bool
}

func (a *app) newSetupCmd() *cobra.Command {
	var o setupOpts
	var help strings.Builder
	for _, t := range setup.Targets {
		name := t.Name
		if len(t.Aliases) > 0 {
			name += " (" + strings.Join(t.Aliases, ", ") + ")"
		}
		fmt.Fprintf(&help, "  %-36s %s/, %s\n", name, t.Project, t.For)
	}
	cmd := &cobra.Command{
		Use:   "setup <target>...",
		Short: "Install vaulty's agent skills for Claude Code, Codex, Gemini CLI, OpenCode or pi",
		Long: "Install the vaulty skills (and for Claude Code the vault-reader agent) into the\n" +
			"vault root, or the home directory with --global. Targets:\n\n" + help.String() +
			"\nReruns update files vaulty installed; files that exist but differ are only\n" +
			"replaced with --force, or left alone (status \"kept\") with --keep-existing.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSetup(o, args)
		},
	}
	cmd.Flags().BoolVar(&o.global, "global", false, "install into the home directory instead of the vault root")
	cmd.Flags().StringVar(&o.dir, "dir", "", "install into this project directory instead of the vault root")
	cmd.Flags().BoolVar(&o.force, "force", false, "replace existing files that differ")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would be written, write nothing")
	cmd.Flags().BoolVar(&o.keepExisting, "keep-existing", false, "leave conflicting files untouched instead of refusing or overwriting them")
	return cmd
}

func (a *app) runSetup(o setupOpts, args []string) error {
	if o.global && o.dir != "" {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("setup: --global and --dir are mutually exclusive")}
	}
	if o.force && o.keepExisting {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("setup: --force and --keep-existing are mutually exclusive")}
	}
	var targets []setup.Target
	seen := map[string]bool{}
	for _, arg := range args {
		t, ok := setup.Lookup(arg)
		if !ok {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("setup: unknown target %q (want one of %s)", arg, strings.Join(setup.Names(), ", "))}
		}
		if !seen[t.Name] {
			seen[t.Name] = true
			targets = append(targets, t)
		}
	}

	base, err := a.setupBase(o)
	if err != nil {
		return err
	}
	var plans []*setup.Plan
	for _, t := range targets {
		root := filepath.Join(base, t.Project)
		if o.global {
			root = filepath.Join(base, t.Global)
		}
		p, err := setup.NewPlan(vaulty.AgentFiles, t, root)
		if err != nil {
			return &ExitError{Code: ExitIO, Err: err}
		}
		if o.keepExisting {
			p.MarkKeptExisting()
		}
		plans = append(plans, p)
	}

	conflicts := 0
	for _, p := range plans {
		conflicts += len(p.Conflicts())
	}
	blocked := conflicts > 0 && !o.force
	if !o.dryRun && !blocked {
		for _, p := range plans {
			if err := setup.Apply(p, a.version, o.force); err != nil {
				return &ExitError{Code: ExitIO, Err: err}
			}
		}
	}

	if a.flags.json {
		if err := a.writeJSON(struct {
			DryRun bool          `json:"dry_run"`
			Plans  []*setup.Plan `json:"targets"`
		}{o.dryRun || blocked, plans}); err != nil {
			return err
		}
	} else {
		a.printSetup(plans, base, o.dryRun || blocked)
	}
	if blocked {
		return &ExitError{Code: ExitRefused, Err: fmt.Errorf("setup: %d file(s) exist and differ; nothing written. Rerun with --force to replace them", conflicts)}
	}
	return nil
}

// setupBase is the directory target roots are joined onto.
func (a *app) setupBase(o setupOpts) (string, error) {
	switch {
	case o.global:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", &ExitError{Code: ExitIO, Err: err}
		}
		return home, nil
	case o.dir != "":
		abs, err := filepath.Abs(o.dir)
		if err != nil {
			return "", &ExitError{Code: ExitUsage, Err: err}
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return "", &ExitError{Code: ExitUsage, Err: fmt.Errorf("setup: --dir %s is not a directory", o.dir)}
		}
		return abs, nil
	default:
		v, err := a.openVault()
		if err != nil {
			return "", err
		}
		return v.Root, nil
	}
}

func (a *app) printSetup(plans []*setup.Plan, base string, dry bool) {
	for _, p := range plans {
		fmt.Fprintf(a.stdout, "%s (%s):\n", p.Name, p.Target.For)
		for _, f := range p.Files {
			rel, err := filepath.Rel(base, f.Dest)
			if err != nil {
				rel = f.Dest
			}
			status := string(f.Status)
			if dry && f.Status != setup.Unchanged && f.Status != setup.Conflict && f.Status != setup.Kept {
				status = "would be " + status
			}
			line := fmt.Sprintf("  %-20s %s", status, rel)
			if f.Reason != "" {
				line += " (" + f.Reason + ")"
			}
			fmt.Fprintln(a.stdout, line)
		}
	}
}
