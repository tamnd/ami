package pack

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// Capture is one row of the columnar index: enough to find and describe a
// stored response without reopening the WARC, plus a pointer into the WARC for
// the full bytes.
type Capture struct {
	URL         string `parquet:"url,zstd"`
	Host        string `parquet:"host,zstd"`
	Status      int32  `parquet:"status"`
	FetchedAt   int64  `parquet:"fetched_at"` // unix millis
	ContentType string `parquet:"content_type,zstd"`
	BodyLength  int64  `parquet:"body_length"`
	Digest      string `parquet:"digest,zstd"`
	Unchanged   bool   `parquet:"unchanged"`

	// Response validators, so this index doubles as a recrawl seed: a later run
	// reads etag/last_modified back and issues conditional requests.
	ETag         string `parquet:"etag,zstd"`
	LastModified string `parquet:"last_modified,zstd"`

	// Pointer into the WARC.
	WARCFile   string `parquet:"warc_file,zstd"`
	WARCOffset int64  `parquet:"warc_offset"`
	WARCLength int64  `parquet:"warc_length"`

	// Error text for failed fetches (empty on success).
	Error string `parquet:"error,zstd"`

	// MetaJSON carries the seed's Meta map verbatim as a JSON object string, so
	// arbitrary producer context survives without a fixed schema.
	MetaJSON string `parquet:"meta_json,zstd"`
}

// IndexWriter buffers Capture rows and writes them to a zstd Parquet file. Rows
// accumulate in a slice and are handed to the Parquet writer in batches, so the
// hot path is one append rather than a one-element slice allocation and a writer
// call per capture. The underlying file is wrapped in a large buffered writer,
// and the row-group size is bounded so a multi-million-row run keeps a steady
// memory footprint instead of buffering the whole file before the first flush.
type IndexWriter struct {
	f   *os.File
	bw  *bufio.Writer
	w   *parquet.GenericWriter[Capture]
	buf []Capture
}

// NewIndexWriter creates the capture index file under dir. batchRows is how many
// rows are buffered before a write to the Parquet encoder; a non-positive value
// falls back to a sane default.
func NewIndexWriter(dir, name string, batchRows int) (*IndexWriter, error) {
	if batchRows <= 0 {
		batchRows = 2000
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	bw := bufio.NewWriterSize(f, 256*1024)
	w := parquet.NewGenericWriter[Capture](bw,
		parquet.Compression(&zstd.Codec{}),
		parquet.MaxRowsPerRowGroup(int64(batchRows)*64),
	)
	return &IndexWriter{f: f, bw: bw, w: w, buf: make([]Capture, 0, batchRows)}, nil
}

// Write appends one capture row, flushing the batch to the encoder once it is
// full.
func (iw *IndexWriter) Write(c Capture) error {
	iw.buf = append(iw.buf, c)
	if len(iw.buf) >= cap(iw.buf) {
		return iw.flushBatch()
	}
	return nil
}

// flushBatch hands the buffered rows to the Parquet encoder and resets the
// batch.
func (iw *IndexWriter) flushBatch() error {
	if len(iw.buf) == 0 {
		return nil
	}
	if _, err := iw.w.Write(iw.buf); err != nil {
		return err
	}
	iw.buf = iw.buf[:0]
	return nil
}

// Close flushes the pending batch and the Parquet footer, then the buffered
// writer, then closes the file.
func (iw *IndexWriter) Close() error {
	if err := iw.flushBatch(); err != nil {
		_ = iw.f.Close()
		return err
	}
	if err := iw.w.Close(); err != nil {
		_ = iw.f.Close()
		return err
	}
	if err := iw.bw.Flush(); err != nil {
		_ = iw.f.Close()
		return err
	}
	return iw.f.Close()
}

// MetaToJSON encodes a meta map to a compact JSON object string, returning ""
// for an empty map so the column stays cheap.
func MetaToJSON(meta map[string]string) string {
	if len(meta) == 0 {
		return ""
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(b)
}
