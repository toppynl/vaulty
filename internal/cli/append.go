package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/safety"
	"github.com/toppynl/vaulty/internal/timeline"
)

func (a *app) runTimelineAppend(o appendOpts, pageArg, entry string) error {
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
	orig, err := os.ReadFile(full)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	d := doc.Parse(rel, orig)
	page := timeline.Parse(d, v.Config.Timeline)

	res, err := timeline.Append(page, entry, timeline.AppendOptions{Touch: o.touch, Today: a.today()}, v.Config)
	if err != nil {
		if errors.Is(err, timeline.ErrRefused) {
			return &ExitError{Code: ExitRefused, Err: err}
		}
		return &ExitError{Code: ExitIO, Err: err}
	}

	if res.FutureDate {
		fmt.Fprintln(a.stderr, "vaulty: date is in the future")
	}

	if res.AlreadyPresent {
		if a.flags.json {
			return a.writeJSON(appendJSON(rel, res, false))
		}
		fmt.Fprintf(a.stdout, "already present %s:%d\n", rel, res.Line)
		return nil
	}

	// Safety check runs before every write, including --dry-run (DESIGN.md
	// §8.6): a dry-run that would in fact be refused must exit 3, not 0.
	expect := safety.Expect{
		RegionStart:      res.RegionStart,
		RegionEnd:        res.RegionEnd,
		NewRegionEnd:     res.NewRegionEnd,
		Added:            res.Added,
		AllowUpdatedLine: res.AllowUpdatedLine,
		NewFirstLine:     res.Line,
		NewFirstLineText: res.AddedFirstLine,
	}
	if err := safety.Verify(orig, res.New, expect, v.Config); err != nil {
		return &ExitError{Code: ExitRefused, Err: err}
	}

	if o.dryRun {
		if a.flags.json {
			return a.writeJSON(appendJSON(rel, res, true))
		}
		a.stdout.Write(dryRunBlockText(res.New, v.Config))
		fmt.Fprintln(a.stderr, "vaulty: dry-run, nothing written")
		return nil
	}

	// Re-read to detect a concurrent change (DESIGN.md §8.7).
	current, err := os.ReadFile(full)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	if string(current) != string(orig) {
		return &ExitError{Code: ExitRefused, Err: fmt.Errorf("refused: file changed during append")}
	}

	if err := atomicWrite(full, res.New); err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	if a.flags.json {
		return a.writeJSON(appendJSON(rel, res, false))
	}
	fmt.Fprintf(a.stdout, "appended %s:%d (%s)\n", rel, res.Line, res.Position)
	return nil
}

type appendResultJSON struct {
	Path           string            `json:"path"`
	Line           int               `json:"line"`
	Position       timeline.Position `json:"position"`
	AlreadyPresent bool              `json:"already_present"`
	CreatedSection bool              `json:"created_section"`
	Touched        bool              `json:"touched"`
	DryRun         bool              `json:"dry_run"`
}

func appendJSON(path string, res *timeline.AppendResult, dryRun bool) appendResultJSON {
	return appendResultJSON{
		Path:           path,
		Line:           res.Line,
		Position:       res.Position,
		AlreadyPresent: res.AlreadyPresent,
		CreatedSection: res.CreatedSection,
		Touched:        res.Touched,
		DryRun:         dryRun,
	}
}

// dryRunBlockText extracts the sole Timeline block's bytes (heading through
// the end of the block) from the candidate content, for --dry-run.
func dryRunBlockText(next []byte, cfg *config.Config) []byte {
	d := doc.Parse("", next)
	page := timeline.Parse(d, cfg.Timeline)
	if len(page.Blocks) == 0 {
		return next
	}
	b := page.Blocks[0]
	return next[b.Heading.Start:b.Body.End]
}

// atomicWrite writes data to a temp file in the same directory, fsyncs it,
// and renames it over path (DESIGN.md §8.7).
func atomicWrite(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode()
	}
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.vaulty-tmp-%d", filepath.Base(path), os.Getpid()))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
