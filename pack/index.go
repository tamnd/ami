package pack

import (
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

// IndexWriter buffers Capture rows and writes them to a zstd Parquet file.
type IndexWriter struct {
	f *os.File
	w *parquet.GenericWriter[Capture]
}

// NewIndexWriter creates the capture index file under dir.
func NewIndexWriter(dir, name string) (*IndexWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	w := parquet.NewGenericWriter[Capture](f, parquet.Compression(&zstd.Codec{}))
	return &IndexWriter{f: f, w: w}, nil
}

// Write appends one capture row.
func (iw *IndexWriter) Write(c Capture) error {
	_, err := iw.w.Write([]Capture{c})
	return err
}

// Close flushes the Parquet footer and closes the file.
func (iw *IndexWriter) Close() error {
	if err := iw.w.Close(); err != nil {
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
