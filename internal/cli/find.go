package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/toppynl/vaulty/internal/find"
	"github.com/toppynl/vaulty/internal/name"
)

type findOpts struct {
	limit int
	typ   string
	body  bool
	only  []string
}

func (a *app) newFindCmd() *cobra.Command {
	o := findOpts{limit: 10}
	cmd := &cobra.Command{
		Use:   "find <term> [<term>...]",
		Short: "Rank vault pages matching term(s) (slug/title/aliases/tags/index/H1, optionally body)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runFind(o, args)
		},
	}
	cmd.Flags().IntVar(&o.limit, "limit", 10, "max results (0 = unlimited)")
	cmd.Flags().StringVar(&o.typ, "type", "", "only pages whose frontmatter type equals this")
	cmd.Flags().BoolVar(&o.body, "body", false, "also match compiled-truth body text (lowest-weight fallback field)")
	cmd.Flags().StringSliceVar(&o.only, "only", nil, "repeatable/comma-separated: restrict to a dir (\"wiki\", \"now/actions\") or glob (\"wiki/*.md\"); default is the whole vault")
	return cmd
}

func (a *app) runFind(o findOpts, rawTerms []string) error {
	terms := find.NormalizeTerms(rawTerms)
	if len(terms) == 0 {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("find: no search terms given")}
	}

	v, err := a.openVault()
	if err != nil {
		return err
	}

	results, err := find.Search(v, terms, find.Options{Body: o.body, Only: o.only, Type: o.typ})
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	total := len(results)
	if o.limit > 0 && len(results) > o.limit {
		results = results[:o.limit]
	}

	if a.flags.json {
		if results == nil {
			results = []find.Result{}
		}
		return a.writeJSON(findJSON{Terms: terms, Results: results, Total: total})
	}

	if len(results) == 0 {
		fmt.Fprintln(a.stderr, name.Binary+": no pages match")
		return nil
	}

	for _, r := range results {
		fmt.Fprintf(a.stdout, "%s\t%s\t%d\t%s\t%s\n", r.Path, r.Type, r.Score, strings.Join(r.Matched, ","), r.SummaryOrTitle())
		if o.body {
			for _, b := range r.Body {
				fmt.Fprintf(a.stdout, "  L%d: %s\n", b.Line, b.Text)
			}
		}
	}
	return nil
}

type findJSON struct {
	Terms   []string      `json:"terms"`
	Results []find.Result `json:"results"`
	Total   int           `json:"total"`
}
