package cli

import (
	"github.com/spf13/cobra"
)

func (a *app) newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Append to and read the vault's append-only log.md",
	}
	cmd.AddCommand(
		a.newLogAppendCmd(),
		a.newLogLastCmd(),
		a.newLogLintCmd(),
	)
	return cmd
}

// ---- append -------------------------------------------------------------

type logAppendOpts struct {
	body string
	date string
}

func (a *app) newLogAppendCmd() *cobra.Command {
	var o logAppendOpts
	cmd := &cobra.Command{
		Use:   "append <op> <title>",
		Short: "Append one entry to log.md (creates the file if missing)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLogAppend(o, args[0], args[1])
		},
	}
	cmd.Flags().StringVar(&o.body, "body", "", "one line of body text under the heading")
	cmd.Flags().StringVar(&o.date, "date", "", "entry date, YYYY-MM-DD (default: today)")
	return cmd
}

// ---- last -----------------------------------------------------------

type logLastOpts struct {
	n     int
	op    string
	since string
}

func (a *app) newLogLastCmd() *cobra.Command {
	o := logLastOpts{n: 10}
	cmd := &cobra.Command{
		Use:   "last",
		Short: "Print the most recent log.md entries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLogLast(o)
		},
	}
	cmd.Flags().IntVar(&o.n, "n", 10, "number of entries to print (0 = all)")
	cmd.Flags().StringVar(&o.op, "op", "", "only entries with this exact op")
	cmd.Flags().StringVar(&o.since, "since", "", "only entries on or after YYYY-MM-DD or YYYY-MM")
	return cmd
}

// ---- lint -----------------------------------------------------------

func (a *app) newLogLintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lint",
		Short: "Report log.md headings that don't match the entry format (exit 1 on findings)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLogLint()
		},
	}
}
