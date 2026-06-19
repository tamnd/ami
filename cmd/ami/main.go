// Command ami casts a net over a list of URLs: it re-fetches every one as fast
// as a single machine can sustain, then packs the results into WARC and a
// columnar Parquet index. The list is a seed, and ami does not care where it
// came from: a text file, a sitemap, a Parquet column, or stdin all feed the
// same engine.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/tamnd/ami/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		stop()
	}()

	os.Exit(cli.Execute(ctx))
}
