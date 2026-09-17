package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/name"
	"github.com/toppynl/vaulty/internal/safety"
	"github.com/toppynl/vaulty/internal/section"
	"github.com/toppynl/vaulty/internal/timeline"
)

type writeOpts struct {
	section string
	after   string
	ifHash  string
	append  bool
	touch   bool
	dryRun  bool
}

func (a *app) newWriteCmd() *cobra.Command {
	var o writeOpts
	cmd := &cobra.Command{
		Use:   `write <page> (--section "<heading>" (--if-hash <hash> | --append) | --after "<heading>") < content`,
		Short: "Replace, append to or insert a compiled-truth section; content comes from stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runWrite(o, args[0])
		},
	}
	cmd.Flags().StringVar(&o.section, "section", "", "the section to replace (with --if-hash; stdin includes its heading line) or append to (with --append)")
	cmd.Flags().StringVar(&o.after, "after", "", "insert stdin as a new section after this heading's section (stdin starts with the new heading)")
	cmd.Flags().StringVar(&o.ifHash, "if-hash", "", "refuse unless the section's current hash (from read --section) matches; required to replace")
	cmd.Flags().BoolVar(&o.append, "append", false, "with --section: append stdin to the end of the section")
	cmd.Flags().BoolVar(&o.touch, "touch", false, "also set frontmatter updated: to today")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print the resulting section, write nothing")
	return cmd
}

func (a *app) runWrite(o writeOpts, pageArg string) error {
	var mode section.Mode
	query := o.section
	switch {
	case o.section != "" && o.after != "":
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--section and --after are mutually exclusive")}
	case o.after != "":
		if o.append {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--append only applies with --section")}
		}
		mode, query = section.ModeAfter, o.after
	case o.section == "":
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("need --section \"<heading>\" or --after \"<heading>\"")}
	case o.append:
		mode = section.ModeAppend
	case o.ifHash == "":
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("replacing a section needs --if-hash: run `%s read %s --section %q` first and pass the hash it prints (or use --append)", name.Binary, pageArg, o.section)}
	default:
		mode = section.ModeReplace
	}

	v, err := a.openVault()
	if err != nil {
		return err
	}
	rel, err := v.Resolve(pageArg)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}
	full, err := v.ContentFile(rel)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}

	in, err := io.ReadAll(a.stdin)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	content := trimBlankEdges(string(in))
	if strings.TrimSpace(content) == "" {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("no content on stdin")}
	}

	orig, err := os.ReadFile(full)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	d := doc.Parse(rel, orig)
	page := timeline.Parse(d, v.Config.Timeline)

	// Exact heading match only, and never first-match: writing the wrong
	// one of two same-named sections is not recoverable from the output.
	hs := doc.Headings(d)
	h, err := matchHeading(hs, query)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}

	region, writable := section.Region(d, page, h)
	if !writable {
		return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: %q is not in compiled truth; use `%s timeline append` for Timeline entries", h.Text, name.Binary)}
	}
	if o.ifHash != "" {
		if now := section.Hash(section.Text(d, region)); now != o.ifHash {
			return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: section changed since read (hash %s, now %s)", o.ifHash, now)}
		}
	}

	edit, err := section.Build(page, h, mode, content)
	if err != nil {
		if errors.Is(err, section.ErrRefused) {
			return &ExitError{Code: ExitRefused, Err: err}
		}
		return &ExitError{Code: ExitIO, Err: err}
	}

	next, touched := edit.New, false
	if o.touch {
		if d.FMUnclosed {
			return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: frontmatter opened but never closed")}
		}
		next, touched, err = timeline.Touch(edit.New, d, v.Config.Frontmatter.UpdatedKey, a.today())
		if err != nil {
			return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: touch: %v", err)}
		}
	}

	// Safety check runs before every write, including --dry-run (DESIGN.md
	// §8.6): a dry-run that would in fact be refused must exit 3, not 0.
	expect := safety.SectionExpect{
		RegionStart:      edit.RegionStart,
		RegionEnd:        edit.RegionEnd,
		NewRegion:        edit.NewRegion,
		HeadingOffset:    edit.HeadingOffset,
		SectionText:      edit.SectionText,
		AllowUpdatedLine: o.touch,
	}
	if err := safety.VerifySection(orig, next, expect, v.Config); err != nil {
		return &ExitError{Code: ExitRefused, Err: err}
	}

	delta := len(next) - len(edit.New) // --touch only ever changes the frontmatter
	res := writeResultJSON{
		Path:    rel,
		Mode:    edit.Mode,
		Heading: edit.Heading,
		Line:    doc.Parse(rel, next).LineOf(edit.HeadingOffset + delta),
		Hash:    section.Hash(edit.SectionText),
		Touched: touched,
		DryRun:  o.dryRun,
	}

	if o.dryRun {
		if a.flags.json {
			return a.writeJSON(res)
		}
		fmt.Fprintln(a.stdout, edit.SectionText)
		fmt.Fprintln(a.stderr, "vaulty: dry-run, nothing written")
		return nil
	}

	// Re-read to detect a concurrent change (DESIGN.md §8.7).
	current, err := os.ReadFile(full)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	if string(current) != string(orig) {
		return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: file changed during write")}
	}
	if err := atomicWrite(full, next); err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	if a.flags.json {
		return a.writeJSON(res)
	}
	fmt.Fprintf(a.stdout, "wrote %s:%d (%s %q)\n", rel, res.Line, res.Mode, res.Heading)
	return nil
}

type writeResultJSON struct {
	Path    string       `json:"path"`
	Mode    section.Mode `json:"mode"`
	Heading string       `json:"heading"`
	Line    int          `json:"line"`
	Hash    string       `json:"hash"`
	Touched bool         `json:"touched"`
	DryRun  bool         `json:"dry_run"`
}
