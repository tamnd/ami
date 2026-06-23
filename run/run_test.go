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
	URL          string `parquet:"url"`
	Status       int32  `parquet:"status"`
	Digest       string `parquet:"digest"`
	ETag         string `parquet:"etag"`
	LastModified string `parquet:"last_modified"`
	BodyLength   int64  `parquet:"body_length"`
	Unchanged    bool   `parquet:"unchanged"`
	Body         []byte `parquet:"body"`
	RespHeaders  string `parquet:"resp_headers"`
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

	// The default format is the parquet body store: no WARC, and the bodies live
	// inline in the capture rows.
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.warc.gz")); len(matches) != 0 {
		t.Fatalf("parquet format should write no WARC, found %v", matches)
	}

	// The index should hold 3 rows, all status 200 with a digest and an inline
	// body the reader can rebuild from.
	rows := readIndex(t, dir)
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
		if len(row.Body) == 0 {
			t.Errorf("row %s missing inline body", row.URL)
		}
		if row.RespHeaders == "" {
			t.Errorf("row %s missing reconstructed response head", row.URL)
		}
	}
}

// TestRunWARCFormat covers the opt-in warc format: bodies go to WARC files and
// the capture rows point at them instead of carrying the body inline.
func TestRunWARCFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(w, "body for %s", r.URL.Path)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := config.Default()
	cfg.OutDir = dir
	cfg.Workers = 4
	cfg.Format = "warc"

	r := New(cfg)
	if err := r.Run(context.Background(), sliceSource{[]seed.Seed{
		{URL: srv.URL + "/a"}, {URL: srv.URL + "/b"}, {URL: srv.URL + "/c"},
	}}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "ami-00000.warc.gz")); err != nil {
		t.Fatalf("warc missing: %v", err)
	}
	rows := readIndex(t, dir)
	if len(rows) != 3 {
		t.Fatalf("want 3 index rows, got %d", len(rows))
	}
	for _, row := range rows {
		if len(row.Body) != 0 {
			t.Errorf("row %s should not carry an inline body in warc format", row.URL)
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
	digest := readIndex(t, dir1)[0].Digest

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

// TestRunConditional304 proves the recrawl path: a first pass learns the
// origin's ETag, and a second pass seeded with it issues If-None-Match, gets a
// bodiless 304, and records the URL as unchanged with no body transferred. This
// is the mechanism that lets a warm recrawl run at the engine's request-rate
// ceiling instead of being bounded by egress bandwidth.
func TestRunConditional304(t *testing.T) {
	const etag = `"v1"`
	var sawConditional bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			sawConditional = true
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		_, _ = fmt.Fprint(w, "stable body")
	}))
	defer srv.Close()

	// First pass learns the ETag.
	dir1 := t.TempDir()
	cfg := config.Default()
	cfg.OutDir = dir1
	cfg.Workers = 2
	r1 := New(cfg)
	if err := r1.Run(context.Background(), sliceSource{[]seed.Seed{{URL: srv.URL}}}, nil); err != nil {
		t.Fatal(err)
	}
	first := readIndex(t, dir1)[0]
	if first.ETag != etag {
		t.Fatalf("first pass did not record etag: got %q", first.ETag)
	}

	// Second pass seeds the ETag: the server must see If-None-Match and answer 304.
	dir2 := t.TempDir()
	cfg.OutDir = dir2
	r2 := New(cfg)
	if err := r2.Run(context.Background(),
		sliceSource{[]seed.Seed{{URL: srv.URL, Digest: first.Digest, ETag: first.ETag}}}, nil); err != nil {
		t.Fatal(err)
	}
	if !sawConditional {
		t.Fatal("server never received If-None-Match on the recrawl")
	}
	if got := r2.Stats().Snapshot().NotMod; got != 1 {
		t.Fatalf("want 1 unchanged from a 304, got %d", got)
	}
	row := readIndex(t, dir2)[0]
	if row.Status != http.StatusNotModified {
		t.Errorf("want status 304, got %d", row.Status)
	}
	if !row.Unchanged {
		t.Error("304 row should be marked unchanged")
	}
	if row.BodyLength != 0 {
		t.Errorf("304 should transfer no body, got body_length %d", row.BodyLength)
	}
}

// readIndex reads every rotated capture file under dir and returns the rows in
// file order, so a test can assert against a run regardless of how many files it
// rotated into.
func readIndex(t *testing.T, dir string) []indexRow {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "captures-*.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no capture files under %s", dir)
	}
	var all []indexRow
	for _, path := range files {
		rows, err := parquet.ReadFile[indexRow](path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		all = append(all, rows...)
	}
	return all
}
