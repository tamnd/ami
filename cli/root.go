// Package cli wires ami's command surface: the cobra tree, the global flags,
// and the fang-rendered help and errors. The real work lives in the seed,
// fetch, pack, and run packages; this layer only parses flags and prints
// progress.
package cli

import (
	"context"
	"fmt"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

// Execute builds the root command and runs it through fang. main passes the
// signal-aware context so Ctrl-C cancels the in-flight crawl and flushes the
// output files. It returns the process exit code.
func Execute(ctx context.Context) int {
	root := newRoot()
	opts := []fang.Option{
		fang.WithVersion(Version),
	}
	if err := fang.Execute(ctx, root, opts...); err != nil {
		return 1
	}
	return 0
}

// newRoot assembles the command tree.
func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "ami",
		Short: "Cast a net over a list of URLs and archive every one",
		Long: "ami (網, \"net\") re-fetches every URL in a seed as fast as one machine\n" +
			"sustains, then packs the results into WARC files and a columnar Parquet\n" +
			"index. The seed is just a list of URLs: a text file, newline JSON, a\n" +
			"Parquet column, a sitemap, or stdin all feed the same engine.",
		Version:       fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newCrawlCmd())
	root.AddCommand(newInspectCmd())
	return root
}
