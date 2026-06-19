package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/parquet-go/parquet-go"
	"github.com/spf13/cobra"
)

// newInspectCmd builds `ami inspect`, a quick look at a capture index without a
// Parquet tool installed.
func newInspectCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "inspect <captures.parquet>",
		Short: "Summarize a capture index and print a sample of rows",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return inspect(args[0], limit)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "number of sample rows to print")
	return cmd
}

// captureRow mirrors pack.Capture for read-back; kept local so inspect does not
// import the writer's internal layout.
type captureRow struct {
	URL        string `parquet:"url"`
	Host       string `parquet:"host"`
	Status     int32  `parquet:"status"`
	BodyLength int64  `parquet:"body_length"`
	WARCFile   string `parquet:"warc_file"`
	Error      string `parquet:"error"`
}

func inspect(path string, limit int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return err
	}
	total := pf.NumRows()
	fmt.Printf("captures: %d rows in %s\n\n", total, path)

	reader := parquet.NewGenericReader[captureRow](pf)
	defer func() { _ = reader.Close() }()

	rows := make([]captureRow, limit)
	n, _ := reader.Read(rows)

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "STATUS\tBYTES\tHOST\tURL")
	for i := 0; i < n; i++ {
		r := rows[i]
		status := fmt.Sprintf("%d", r.Status)
		if r.Error != "" {
			status = "ERR"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", status, r.BodyLength, r.Host, r.URL)
	}
	return tw.Flush()
}
