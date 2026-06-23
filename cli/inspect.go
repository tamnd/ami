package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/parquet-go/parquet-go"
	"github.com/spf13/cobra"
)

// newInspectCmd builds `ami inspect`, a quick look at a capture index without a
// Parquet tool installed.
func newInspectCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "inspect <dir|captures-NNNNN.parquet>",
		Short: "Summarize a capture index and print a sample of rows",
		Long: "inspect summarizes a crawl's capture index. Point it at an output\n" +
			"directory to aggregate every rotated captures-NNNNN.parquet file, or at\n" +
			"a single capture file.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return inspect(args[0], limit)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "number of sample rows to print")
	return cmd
}

// captureFiles resolves the inspect argument to the capture files to read. A
// directory expands to every rotated captures-*.parquet inside it; anything else
// is taken as a single file path.
func captureFiles(arg string) ([]string, error) {
	info, err := os.Stat(arg)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{arg}, nil
	}
	files, err := filepath.Glob(filepath.Join(arg, "captures-*.parquet"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no captures-*.parquet files in %s", arg)
	}
	return files, nil
}

// captureRow mirrors pack.Capture for read-back; kept local so inspect does not
// import the writer's internal layout.
type captureRow struct {
	URL            string `parquet:"url"`
	Host           string `parquet:"host"`
	Status         int32  `parquet:"status"`
	BodyLength     int64  `parquet:"body_length"`
	MarkdownLength int64  `parquet:"markdown_length"`
	WARCFile       string `parquet:"warc_file"`
	Error          string `parquet:"error"`
}

func inspect(arg string, limit int) error {
	files, err := captureFiles(arg)
	if err != nil {
		return err
	}

	// Total rows across every rotated file, plus a sample drawn from the files in
	// order until the limit is filled.
	var total int64
	var sample []captureRow
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return err
		}
		pf, err := parquet.OpenFile(f, info.Size())
		if err != nil {
			_ = f.Close()
			return err
		}
		total += pf.NumRows()

		if len(sample) < limit {
			reader := parquet.NewGenericReader[captureRow](pf)
			rows := make([]captureRow, limit-len(sample))
			n, _ := reader.Read(rows)
			sample = append(sample, rows[:n]...)
			_ = reader.Close()
		}
		_ = f.Close()
	}

	if len(files) == 1 {
		fmt.Printf("captures: %d rows in %s\n\n", total, files[0])
	} else {
		fmt.Printf("captures: %d rows across %d files in %s\n\n", total, len(files), arg)
	}

	// Only show the markdown column when at least one sampled row carries it, so a
	// crawl run without --markdown keeps the terse layout.
	hasMarkdown := false
	for _, r := range sample {
		if r.MarkdownLength > 0 {
			hasMarkdown = true
			break
		}
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	if hasMarkdown {
		_, _ = fmt.Fprintln(tw, "STATUS\tBYTES\tMD\tHOST\tURL")
	} else {
		_, _ = fmt.Fprintln(tw, "STATUS\tBYTES\tHOST\tURL")
	}
	for _, r := range sample {
		status := fmt.Sprintf("%d", r.Status)
		if r.Error != "" {
			status = "ERR"
		}
		if hasMarkdown {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\n", status, r.BodyLength, r.MarkdownLength, r.Host, r.URL)
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", status, r.BodyLength, r.Host, r.URL)
		}
	}
	return tw.Flush()
}
