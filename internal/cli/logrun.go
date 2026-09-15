package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/toppynl/vaulty/internal/name"
	"github.com/toppynl/vaulty/internal/vaultlog"
)

func (a *app) runLogAppend(o logAppendOpts, op, title string) error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	rel := v.Config.Log.Path
	full := filepath.Join(v.Root, filepath.FromSlash(rel))

	date := o.date
	if date == "" {
		date = a.today()
	} else if _, derr := parseLogDate(date); derr != nil {
		return &ExitError{Code: ExitUsage, Err: derr}
	}

	if err := vaultlog.ValidateField("op", op); err != nil {
		return &ExitError{Code: ExitRefused, Err: err}
	}
	if err := vaultlog.ValidateField("title", title); err != nil {
		return &ExitError{Code: ExitRefused, Err: err}
	}
	if o.body != "" {
		if err := vaultlog.ValidateField("body", o.body); err != nil {
			return &ExitError{Code: ExitRefused, Err: err}
		}
	}

	orig, err := os.ReadFile(full)
	if err != nil && !os.IsNotExist(err) {
		return &ExitError{Code: ExitIO, Err: err}
	}

	next := vaultlog.Append(orig, date, op, title, o.body)

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	if err := atomicWrite(full, next); err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	entries, _ := vaultlog.Parse(next)
	line := 0
	if len(entries) > 0 {
		line = entries[len(entries)-1].Line
	}

	if a.flags.json {
		return a.writeJSON(struct {
			Path  string `json:"path"`
			Line  int    `json:"line"`
			Date  string `json:"date"`
			Op    string `json:"op"`
			Title string `json:"title"`
		}{rel, line, date, op, title})
	}
	fmt.Fprintf(a.stdout, "appended %s:%d\n", rel, line)
	return nil
}

func (a *app) runLogLast(o logLastOpts) error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	if o.n < 0 {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--n must be >= 0")}
	}
	var since string
	if o.since != "" {
		since, err = normalizeSince(o.since)
		if err != nil {
			return &ExitError{Code: ExitUsage, Err: err}
		}
	}

	rel := v.Config.Log.Path
	full := filepath.Join(v.Root, filepath.FromSlash(rel))
	src, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			src = nil
		} else {
			return &ExitError{Code: ExitIO, Err: err}
		}
	}

	entries, malformed := vaultlog.Parse(src)
	for _, m := range malformed {
		fmt.Fprintf(a.stderr, "%s: %s:%d: malformed log entry, skipped: %s\n", name.Binary, rel, m.Line, m.Reason)
	}

	var filtered []vaultlog.Entry
	for _, e := range entries {
		if o.op != "" && e.Op != o.op {
			continue
		}
		if since != "" && e.Date < since {
			continue
		}
		filtered = append(filtered, e)
	}
	if o.n > 0 && len(filtered) > o.n {
		filtered = filtered[len(filtered)-o.n:]
	}

	if a.flags.json {
		type entryJSON struct {
			Line  int    `json:"line"`
			Date  string `json:"date"`
			Op    string `json:"op"`
			Title string `json:"title"`
			Body  string `json:"body,omitempty"`
		}
		out := struct {
			Path           string      `json:"path"`
			Entries        []entryJSON `json:"entries"`
			MalformedCount int         `json:"malformed_count"`
		}{Path: rel, MalformedCount: len(malformed)}
		for _, e := range filtered {
			out.Entries = append(out.Entries, entryJSON{Line: e.Line, Date: e.Date, Op: e.Op, Title: e.Title, Body: e.Body})
		}
		if out.Entries == nil {
			out.Entries = []entryJSON{}
		}
		return a.writeJSON(out)
	}

	for i, e := range filtered {
		if i > 0 {
			fmt.Fprintln(a.stdout)
		}
		a.stdout.Write([]byte(vaultlog.Format(e.Date, e.Op, e.Title, e.Body)))
	}
	return nil
}

func (a *app) runLogLint() error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	rel := v.Config.Log.Path
	full := filepath.Join(v.Root, filepath.FromSlash(rel))
	src, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			src = nil
		} else {
			return &ExitError{Code: ExitIO, Err: err}
		}
	}
	_, malformed := vaultlog.Parse(src)

	if a.flags.json {
		type malformedJSON struct {
			Line   int    `json:"line"`
			Reason string `json:"reason"`
			Text   string `json:"text"`
		}
		out := struct {
			Path      string          `json:"path"`
			Malformed []malformedJSON `json:"malformed"`
		}{Path: rel}
		for _, m := range malformed {
			out.Malformed = append(out.Malformed, malformedJSON{Line: m.Line, Reason: m.Reason, Text: m.Text})
		}
		if out.Malformed == nil {
			out.Malformed = []malformedJSON{}
		}
		if err := a.writeJSON(out); err != nil {
			return err
		}
	} else {
		for _, m := range malformed {
			fmt.Fprintf(a.stdout, "%s:%d: malformed: %s: %s\n", rel, m.Line, m.Reason, m.Text)
		}
	}
	if len(malformed) > 0 {
		return &ExitError{Code: ExitFindings, Err: nil}
	}
	return nil
}

// parseLogDate validates --date as a full YYYY-MM-DD calendar date.
func parseLogDate(s string) (string, error) {
	if _, err := time.Parse(vaultlog.DateFormat, s); err != nil {
		return "", fmt.Errorf("--date: invalid date %q (want YYYY-MM-DD)", s)
	}
	return s, nil
}
