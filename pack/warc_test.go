package pack

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/gzip"
	"github.com/parquet-go/parquet-go"
)

// TestWARCMemberOffsets writes several responses and checks that every location
// the writer reports points at a self-contained gzip member: seeking to Offset
// and reading Length bytes must inflate cleanly on its own. This guards the
// optimized writer, where offsets come from the counting writer rather than a
// flush-and-seek after each record.
func TestWARCMemberOffsets(t *testing.T) {
	dir := t.TempDir()
	// Small target size so the run rotates across more than one file.
	w, err := NewWARCWriter(dir, "ami", 4*1024)
	if err != nil {
		t.Fatalf("NewWARCWriter: %v", err)
	}

	bodies := make([][]byte, 0, 24)
	locs := make([]Location, 0, 24)
	for i := 0; i < 24; i++ {
		body := bytes.Repeat([]byte{byte('a' + i%26)}, 200+i*37)
		h := http.Header{}
		h.Set("Content-Type", "text/html")
		loc, err := w.WriteResponse("http://example.com/"+string(rune('a'+i%26)), 200, http.Header{}, h, body, false, "")
		if err != nil {
			t.Fatalf("WriteResponse %d: %v", i, err)
		}
		bodies = append(bodies, body)
		locs = append(locs, loc)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for i, loc := range locs {
		f, err := os.Open(filepath.Join(dir, loc.File))
		if err != nil {
			t.Fatalf("open %s: %v", loc.File, err)
		}
		member := make([]byte, loc.Length)
		if _, err := f.ReadAt(member, loc.Offset); err != nil {
			_ = f.Close()
			t.Fatalf("record %d ReadAt(off=%d len=%d): %v", i, loc.Offset, loc.Length, err)
		}
		_ = f.Close()

		gr, err := gzip.NewReader(bytes.NewReader(member))
		if err != nil {
			t.Fatalf("record %d gzip.NewReader: %v", i, err)
		}
		got, err := io.ReadAll(gr)
		if err != nil {
			t.Fatalf("record %d inflate: %v", i, err)
		}
		if err := gr.Close(); err != nil {
			t.Fatalf("record %d gzip close: %v", i, err)
		}
		if !bytes.Contains(got, bodies[i]) {
			t.Fatalf("record %d: inflated member does not contain its body", i)
		}
		if !bytes.HasPrefix(got, []byte("WARC/1.1")) {
			t.Fatalf("record %d: member is not a WARC record", i)
		}
	}
}

// TestIndexWriterBatch writes more rows than one batch and confirms the run
// produces a readable Parquet with the right row count, exercising the batched
// path and the final partial-batch flush. With no rotation target the writer
// stays on a single file (captures-00000.parquet).
func TestIndexWriterBatch(t *testing.T) {
	dir := t.TempDir()
	const batch = 8
	iw, err := NewIndexWriter(dir, "captures.parquet", batch, 0)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	// 2.5 batches: forces two full flushes plus a partial flush on Close.
	const rows = batch*2 + batch/2
	for i := 0; i < rows; i++ {
		if err := iw.Write(Capture{URL: "http://example.com/", Status: 200}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	files := captureFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("got %d capture files, want 1 (no rotation target)", len(files))
	}
	if n := countRows(t, files[0]); n != rows {
		t.Fatalf("row count = %d, want %d", n, rows)
	}
}

// TestIndexWriterRotation drives the body store with a small rotation target and
// confirms the run spreads across several independently readable files, that
// every body round-trips, and that no row is lost across the rotation.
func TestIndexWriterRotation(t *testing.T) {
	dir := t.TempDir()
	const batch = 4
	const rows = 40
	// Each body is ~4 KiB; rotate every ~16 KiB so a few files result.
	body := bytes.Repeat([]byte("0123456789abcdef"), 256)
	iw, err := NewIndexWriter(dir, "captures.parquet", batch, 16*1024)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	for i := 0; i < rows; i++ {
		c := Capture{
			URL:         "http://example.com/" + string(rune('a'+i%26)),
			Status:      200,
			Body:        append([]byte(nil), body...),
			RespHeaders: "HTTP/1.1 200 OK\r\n\r\n",
		}
		c.Body = append(c.Body, byte(i)) // make each body distinct
		if err := iw.Write(c); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	files := captureFiles(t, dir)
	if len(files) < 2 {
		t.Fatalf("got %d capture files, want rotation into at least 2", len(files))
	}

	total := 0
	seen := make(map[byte]bool)
	for _, path := range files {
		caps, err := parquet.ReadFile[Capture](path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		for _, c := range caps {
			if len(c.Body) == 0 {
				t.Fatalf("%s: empty body in stored capture", path)
			}
			seen[c.Body[len(c.Body)-1]] = true
		}
		total += len(caps)
	}
	if total != rows {
		t.Fatalf("total rows across %d files = %d, want %d", len(files), total, rows)
	}
	if len(seen) != rows {
		t.Fatalf("distinct bodies = %d, want %d (a body did not round-trip)", len(seen), rows)
	}
}

// captureFiles returns the sorted captures-*.parquet paths under dir.
func captureFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "captures-*.parquet"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no capture files written")
	}
	return files
}

// countRows reads back a Parquet capture file and returns its row count.
func countRows(t *testing.T, path string) int {
	t.Helper()
	caps, err := parquet.ReadFile[Capture](path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return len(caps)
}
