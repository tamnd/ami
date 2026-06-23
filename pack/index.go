package pack

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// Capture is one row of the columnar output: enough to find and describe a
// stored response, the response validators so the file doubles as a recrawl
// seed, and (in the parquet body-store format) the captured exchange itself.
type Capture struct {
	URL         string `parquet:"url"`
	Host        string `parquet:"host"`
	Status      int32  `parquet:"status"`
	FetchedAt   int64  `parquet:"fetched_at"` // unix millis
	ContentType string `parquet:"content_type"`
	BodyLength  int64  `parquet:"body_length"`
	Digest      string `parquet:"digest"`
	Unchanged   bool   `parquet:"unchanged"`

	// Response validators, so this output doubles as a recrawl seed: a later run
	// reads etag/last_modified back and issues conditional requests.
	ETag         string `parquet:"etag"`
	LastModified string `parquet:"last_modified"`

	// Pointer into the WARC (warc format only; zero in parquet format).
	WARCFile   string `parquet:"warc_file"`
	WARCOffset int64  `parquet:"warc_offset"`
	WARCLength int64  `parquet:"warc_length"`

	// Error text for failed fetches (empty on success).
	Error string `parquet:"error"`

	// MetaJSON carries the seed's Meta map verbatim as a JSON object string, so
	// arbitrary producer context survives without a fixed schema.
	MetaJSON string `parquet:"meta_json"`

	// Markdown is the body rendered to Markdown, populated only when the crawl ran
	// with --markdown and the response was HTML that yielded an article. It is
	// empty otherwise. MarkdownLength is its byte length, so a reader can size the
	// text column without decompressing it.
	Markdown       string `parquet:"markdown"`
	MarkdownLength int64  `parquet:"markdown_length"`

	// Timing and network metadata captured during the fetch.
	TTFBMS    int64  `parquet:"ttfb_ms"`         // time to first byte in milliseconds
	FetchDurMS int64  `parquet:"fetch_duration_ms"` // total fetch wall-clock in milliseconds
	FinalURL  string `parquet:"final_url"`         // URL after following redirects; empty if same as URL
	IPAddress string `parquet:"ip_address"`        // IP that served the response

	// The captured exchange, stored inline in the parquet body-store format and
	// left empty in warc format (where the bytes live in the WARC instead). The
	// header fields hold the reconstructed HTTP head text, so a reader can rebuild
	// the full request/response without a WARC.
	RespHeaders string `parquet:"resp_headers"`
	ReqHeaders  string `parquet:"req_headers"`
	Body        []byte `parquet:"body"`
}

// IndexWriter writes Capture rows to zstd-compressed Parquet, rotating to a new
// file once a target amount of uncompressed payload has accumulated. Rows are
// buffered and handed to the encoder in batches, so the hot path is one append
// rather than a one-element slice allocation and an encoder call per capture.
// The underlying file is wrapped in a large buffered writer, and the row-group
// size is bounded so a multi-million-row run keeps a steady memory footprint
// instead of buffering the whole file before the first flush.
//
// Each rotated file is finalized with its own footer, so it is independently
// readable, can be offloaded and deleted mid-run, and a crash loses only the
// open file. The body and header columns compress columnar with zstd, which
// packs thousands of similar pages together far more tightly than per-record
// gzip; the crawl is network-bound, so the heavier compression is effectively
// free.
type IndexWriter struct {
	dir       string
	base      string // file name without the .parquet extension
	batchRows int
	target    int64 // rotate after this many uncompressed payload bytes (0 = never)
	codec     *zstd.Codec

	seq         int
	f           *os.File
	bw          *bufio.Writer
	w           *parquet.GenericWriter[Capture]
	buf         []Capture
	accumulated int64 // uncompressed payload bytes in the current file
}

// NewIndexWriter creates a rotating capture writer under dir. name is the base
// file name; its .parquet extension is replaced by a -NNNNN.parquet sequence.
// batchRows is how many rows are buffered before a write to the encoder; a
// non-positive value falls back to a default. targetSize rotates the file once
// that many uncompressed payload bytes have been written, so a long run produces
// a series of bounded files; zero disables rotation (a single file).
func NewIndexWriter(dir, name string, batchRows int, targetSize int64) (*IndexWriter, error) {
	if batchRows <= 0 {
		batchRows = 2000
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	iw := &IndexWriter{
		dir:       dir,
		base:      strings.TrimSuffix(name, ".parquet"),
		batchRows: batchRows,
		target:    targetSize,
		// BetterCompression is roughly zstd level 7-8: a large ratio gain over the
		// default while still far faster than the network feeding it.
		codec: &zstd.Codec{Level: zstd.SpeedBetterCompression, Concurrency: 4},
		buf:   make([]Capture, 0, batchRows),
	}
	if err := iw.openFile(); err != nil {
		return nil, err
	}
	return iw, nil
}

// openFile starts the next rotated Parquet file.
func (iw *IndexWriter) openFile() error {
	name := fmt.Sprintf("%s-%05d.parquet", iw.base, iw.seq)
	iw.seq++
	f, err := os.Create(filepath.Join(iw.dir, name))
	if err != nil {
		return err
	}
	iw.f = f
	iw.bw = bufio.NewWriterSize(f, 256*1024)
	iw.w = parquet.NewGenericWriter[Capture](iw.bw,
		parquet.Compression(iw.codec),
		parquet.MaxRowsPerRowGroup(int64(iw.batchRows)*64),
	)
	iw.accumulated = 0
	return nil
}

// closeFile finalizes the current Parquet file (footer), flushes the buffer, and
// closes the OS file.
func (iw *IndexWriter) closeFile() error {
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

// Write appends one capture row, flushing the batch to the encoder once it is
// full and rotating to a new file once the payload target is reached.
func (iw *IndexWriter) Write(c Capture) error {
	iw.buf = append(iw.buf, c)
	iw.accumulated += approxPayloadBytes(c)
	if len(iw.buf) >= cap(iw.buf) {
		if err := iw.writeBatch(); err != nil {
			return err
		}
		if iw.target > 0 && iw.accumulated >= iw.target {
			if err := iw.closeFile(); err != nil {
				return err
			}
			if err := iw.openFile(); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeBatch hands the buffered rows to the Parquet encoder and resets the
// batch.
func (iw *IndexWriter) writeBatch() error {
	if len(iw.buf) == 0 {
		return nil
	}
	if _, err := iw.w.Write(iw.buf); err != nil {
		return err
	}
	iw.buf = iw.buf[:0]
	return nil
}

// Close flushes the pending batch and finalizes the open file.
func (iw *IndexWriter) Close() error {
	if err := iw.writeBatch(); err != nil {
		_ = iw.closeFile()
		return err
	}
	return iw.closeFile()
}

// approxPayloadBytes estimates a row's uncompressed footprint, dominated by the
// body, for the rotation threshold. It need not be exact.
func approxPayloadBytes(c Capture) int64 {
	return int64(len(c.Body) + len(c.RespHeaders) + len(c.ReqHeaders) + len(c.URL) + len(c.MetaJSON) + len(c.Markdown) + 128)
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
