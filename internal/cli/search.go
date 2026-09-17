package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/toppynl/vaulty/internal/name"
	"github.com/toppynl/vaulty/internal/search"
	"github.com/toppynl/vaulty/internal/vault"
)

type searchOpts struct {
	only     []string
	typ      string
	where    []string
	limit    int
	timeline bool
	noCache  bool
	rebuild  bool
	stats    bool
}

func (a *app) newSearchCmd() *cobra.Command {
	o := searchOpts{limit: 10}
	cmd := &cobra.Command{
		Use:   "search <query...>",
		Short: "Full-text search over page content: ranked pages with line snippets",
		Long: `Full-text, BM25-ranked search over the vault (DESIGN.md §19).

Query syntax: plain words (OR'ed), "exact phrase", term~ (fuzzy), term* (prefix),
-term (exclude), key:value (exact frontmatter filter; tag: means tags:).`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSearch(o, args)
		},
	}
	cmd.Flags().StringSliceVar(&o.only, "only", nil, "repeatable/comma-separated: restrict to a dir (\"wiki\") or glob (\"wiki/*.md\"); default is the whole vault")
	cmd.Flags().StringVar(&o.typ, "type", "", "only pages whose frontmatter type equals this (same as --where type=T)")
	cmd.Flags().StringArrayVar(&o.where, "where", nil, "repeatable: exact frontmatter filter key=value (split on the first \"=\"; list keys match if they contain the value)")
	cmd.Flags().IntVar(&o.limit, "limit", 10, "max results (0 = unlimited)")
	cmd.Flags().BoolVar(&o.timeline, "timeline", false, "also search the ## Timeline section")
	cmd.Flags().BoolVar(&o.noCache, "no-cache", false, "index in memory for this call; never read or write the cache")
	cmd.Flags().BoolVar(&o.rebuild, "rebuild", false, "force a full rebuild of the cached index")
	cmd.Flags().BoolVar(&o.stats, "stats", false, "print cache path, page count, index size and last update, then exit")
	return cmd
}

func (a *app) runSearch(o searchOpts, args []string) error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	if o.stats {
		return a.searchStats(v)
	}

	raw := strings.Join(args, " ")
	q, err := search.ParseQuery(raw, v.Config.Search.FieldAliases)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("search: %w", err)}
	}
	var where []search.Filter
	if o.typ != "" {
		where = append(where, search.Filter{Key: "type", Value: o.typ})
	}
	for _, w := range o.where {
		f, err := search.ParseFilter(w)
		if err != nil {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("search: %w", err)}
		}
		where = append(where, f)
	}
	if !q.Positive() && len(q.Filters) == 0 && len(where) == 0 {
		return &ExitError{Code: ExitUsage, Err: errors.New("search: empty query (give search terms, key:value, --where or --type)")}
	}

	resp, err := search.Run(v, q, search.Options{
		Only: o.only, Where: where, Limit: o.limit,
		Timeline: o.timeline, NoCache: o.noCache, Rebuild: o.rebuild,
	})
	if err != nil {
		if errors.Is(err, search.ErrQuery) {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("search: %w", err)}
		}
		return &ExitError{Code: ExitIO, Err: err}
	}
	if resp.CacheNote != "" {
		fmt.Fprintln(a.stderr, name.Binary+": "+resp.CacheNote)
	}

	if a.flags.json {
		return a.writeJSON(searchJSON{
			Query:   raw,
			Total:   resp.Total,
			Facets:  searchFacets{Type: resp.TypeCounts},
			Results: resp.Results,
		})
	}

	for _, r := range resp.Results {
		fmt.Fprintf(a.stdout, "%s\t%s\t%.2f\t%s\n", r.Path, r.Type, r.Score, r.SummaryOrTitle())
		for _, s := range r.Snippets {
			fmt.Fprintf(a.stdout, "  L%d: %s\n", s.Line, s.Text)
		}
	}
	counts := "none"
	if len(resp.TypeCounts) > 0 {
		counts = strings.Join(search.SortedTypeCounts(resp.TypeCounts), ", ")
	}
	fmt.Fprintf(a.stderr, "%s: %d hits (type counts: %s)\n", name.Binary, resp.Total, counts)
	return nil
}

func (a *app) searchStats(v *vault.Vault) error {
	st, err := search.ReadStats(v)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: fmt.Errorf("search --stats: %w", err)}
	}
	if a.flags.json {
		return a.writeJSON(st)
	}
	last := "never"
	if !st.LastUpdate.IsZero() {
		last = st.LastUpdate.Format(time.RFC3339)
	}
	kind := st.LastKind
	if !st.Exists {
		kind = "none (no cached index yet)"
	}
	fmt.Fprintf(a.stdout, "cache: %s\npages: %d\nindex_bytes: %d\nlast_update: %s\nlast_check_updated: %d\nlast_update_kind: %s\nfull_rebuilds: %d\n",
		st.CachePath, st.Pages, st.IndexBytes, last, st.LastChecked, kind, st.FullRebuilds)
	return nil
}

type searchFacets struct {
	Type map[string]int `json:"type"`
}

type searchJSON struct {
	Query   string          `json:"query"`
	Total   int             `json:"total"`
	Facets  searchFacets    `json:"facets"`
	Results []search.Result `json:"results"`
}
