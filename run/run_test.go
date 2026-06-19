package run

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"

	"github.com/tamnd/ami/config"
	"github.com/tamnd/ami/seed"
)

// sliceSource is an in-memory Source for tests.
type sliceSource struct{ seeds []seed.Seed }

func (s sliceSource) Name() string { return "test" }
func (s sliceSource) Iterate(ctx context.Context, yield func(seed.Seed) error) error {
	for _, sd := range s.seeds {
		if err := yield(sd); err != nil {
			return err
		}
	}
	return nil
}

type indexRow struct {
	URL    string `parquet:"url"`
	Status int32  `parquet:"status"`
	Digest string `parquet:"digest"`
}

func TestRunEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(w, "body for %s", r.URL.Path)
	}))
	defer srv.Close()

	seeds := []seed.Seed{
		{URL: srv.URL + "/a"},
		{URL: srv.URL + "/b"},
		{URL: srv.URL + "/c"},
	}

	dir := t.TempDir()
	cfg := config.Default()
	cfg.OutDir = dir
	cfg.Workers = 4

	r := New(cfg)
	if err := r.Run(context.Background(), sliceSource{seeds}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	final := r.Stats().Snapshot()
	if final.OK != 3 {
		t.Fatalf("want 3 OK, got %d (%+v)", final.OK, final)
	}

	// The WARC file should exist.
	if _, err := os.Stat(filepath.Join(dir, "ami-00000.warc.gz")); err != nil {
		t.Fatalf("warc missing: %v", err)
	}

	// The index should hold 3 rows, all status 200 with a digest.
	rows := readIndex(t, filepath.Join(dir, "captures.parquet"))
	if len(rows) != 3 {
		t.Fatalf("want 3 index rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.Status != 200 {
			t.Errorf("row %s status %d", row.URL, row.Status)
		}
		if row.Digest == "" {
			t.Errorf("row %s missing digest", row.URL)
		}
	}
}

func TestRunUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "stable")
	}))
	defer srv.Close()

	// First pass to learn the digest.
	dir1 := t.TempDir()
	cfg := config.Default()
	cfg.OutDir = dir1
	cfg.Workers = 2
	r1 := New(cfg)
	if err := r1.Run(context.Background(), sliceSource{[]seed.Seed{{URL: srv.URL}}}, nil); err != nil {
		t.Fatal(err)
	}
	digest := readIndex(t, filepath.Join(dir1, "captures.parquet"))[0].Digest

	// Second pass with the digest seeded should report unchanged.
	dir2 := t.TempDir()
	cfg.OutDir = dir2
	r2 := New(cfg)
	if err := r2.Run(context.Background(), sliceSource{[]seed.Seed{{URL: srv.URL, Digest: digest}}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := r2.Stats().Snapshot().NotMod; got != 1 {
		t.Fatalf("want 1 unchanged, got %d", got)
	}
}

func readIndex(t *testing.T, path string) []indexRow {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	reader := parquet.NewGenericReader[indexRow](pf)
	defer func() { _ = reader.Close() }()
	rows := make([]indexRow, pf.NumRows())
	n, _ := reader.Read(rows)
	return rows[:n]
}
