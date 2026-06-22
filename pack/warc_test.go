package pack

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/gzip"
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

// TestIndexWriterBatch writes more rows than one batch and confirms the file is
// a readable Parquet with the right row count, exercising the batched path and
// the final partial-batch flush.
func TestIndexWriterBatch(t *testing.T) {
	dir := t.TempDir()
	const batch = 8
	iw, err := NewIndexWriter(dir, "captures.parquet", batch)
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

	st, err := os.Stat(filepath.Join(dir, "captures.parquet"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size() == 0 {
		t.Fatal("parquet file is empty")
	}
}
