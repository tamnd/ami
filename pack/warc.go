// Package pack writes a run's results to durable output: WARC/1.1 files holding
// the raw HTTP exchanges, and a columnar Parquet index describing every capture
// for fast analytics without reopening the WARCs.
package pack

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/gzip"
)

// WARCWriter appends WARC records and rotates to a new file once one passes the
// target size. Each record is its own gzip member (a "WARC record gzip"), so a
// reader can seek to any record's offset and inflate just that member.
type WARCWriter struct {
	dir        string
	prefix     string
	targetSize int64

	seq  int
	f    *os.File
	bw   *bufio.Writer
	size int64

	// Offset of the next record to be written, used to populate the index.
	offset int64
	file   string
}

// NewWARCWriter creates a writer that rotates files of about targetSize bytes
// under dir, naming them prefix-00000.warc.gz and so on.
func NewWARCWriter(dir, prefix string, targetSize int64) (*WARCWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &WARCWriter{dir: dir, prefix: prefix, targetSize: targetSize}
	if err := w.rotate(); err != nil {
		return nil, err
	}
	return w, nil
}

// rotate closes the current file (if any) and opens the next one with a
// warcinfo record.
func (w *WARCWriter) rotate() error {
	if w.bw != nil {
		if err := w.closeFile(); err != nil {
			return err
		}
	}
	name := fmt.Sprintf("%s-%05d.warc.gz", w.prefix, w.seq)
	w.seq++
	f, err := os.Create(filepath.Join(w.dir, name))
	if err != nil {
		return err
	}
	w.f = f
	w.bw = bufio.NewWriterSize(f, 64*1024)
	w.size = 0
	w.offset = 0
	w.file = name
	return w.writeWarcinfo()
}

// closeFile flushes and closes the open WARC file.
func (w *WARCWriter) closeFile() error {
	if err := w.bw.Flush(); err != nil {
		return err
	}
	return w.f.Close()
}

// Close finishes the current file.
func (w *WARCWriter) Close() error {
	if w.bw == nil {
		return nil
	}
	return w.closeFile()
}

// Location is where a record landed: which file and the byte offset of its
// gzip member, so the index can point straight at it.
type Location struct {
	File   string
	Offset int64
	Length int64
}

// writeMember writes one gzip member containing block and returns its location.
func (w *WARCWriter) writeMember(block []byte) (Location, error) {
	if w.size >= w.targetSize {
		if err := w.rotate(); err != nil {
			return Location{}, err
		}
	}
	start := w.offset
	gz, _ := gzip.NewWriterLevel(w.bw, gzip.BestSpeed)
	if _, err := gz.Write(block); err != nil {
		return Location{}, err
	}
	if err := gz.Close(); err != nil {
		return Location{}, err
	}
	// The compressed length is what advanced the buffered writer; track it via
	// the running size delta the caller flushes. We approximate the on-disk
	// member length from the buffered counter.
	loc := Location{File: w.file, Offset: start}
	// Flush so the file offset is exact, then read it back for the length.
	if err := w.bw.Flush(); err != nil {
		return Location{}, err
	}
	pos, err := w.f.Seek(0, 1)
	if err != nil {
		return Location{}, err
	}
	loc.Length = pos - start
	w.offset = pos
	w.size = pos
	return loc, nil
}

// writeWarcinfo emits the mandatory warcinfo record at the head of a file.
func (w *WARCWriter) writeWarcinfo() error {
	payload := "software: ami\r\nformat: WARC File Format 1.1\r\n"
	hdr := strings.Join([]string{
		"WARC/1.1",
		"WARC-Type: warcinfo",
		"WARC-Date: " + warcTime(time.Now()),
		"WARC-Record-ID: " + recordID(),
		"WARC-Filename: " + w.file,
		"Content-Type: application/warc-fields",
		fmt.Sprintf("Content-Length: %d", len(payload)),
	}, "\r\n")
	_, err := w.writeMember([]byte(hdr + "\r\n\r\n" + payload + "\r\n\r\n"))
	return err
}

// WriteResponse writes a paired request and response (or a revisit) record for
// one capture and returns the response record's location.
func (w *WARCWriter) WriteResponse(targetURI string, status int, reqHeader, respHeader http.Header, body []byte, revisit bool, refDigest string) (Location, error) {
	now := warcTime(time.Now())
	reqID := recordID()

	reqBlock := buildRequestRecord(targetURI, reqHeader, now, reqID)
	if _, err := w.writeMember(reqBlock); err != nil {
		return Location{}, err
	}

	var respBlock []byte
	if revisit {
		respBlock = buildRevisitRecord(targetURI, respHeader, now, reqID, refDigest)
	} else {
		respBlock = buildResponseRecord(targetURI, status, respHeader, body, now, reqID)
	}
	return w.writeMember(respBlock)
}

// recordID returns a urn:uuid WARC record id.
func recordID() string {
	return "<urn:uuid:" + uuid.NewString() + ">"
}

// warcTime formats a time in the WARC 14-digit profile.
func warcTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
