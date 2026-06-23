package pack

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// ResponseHead reconstructs the HTTP response head (status line plus headers) as
// text, ending with the blank line that separates head from body. The parquet
// body store keeps this alongside the body so a reader can rebuild the full
// response without a WARC.
func ResponseHead(status int, header http.Header) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	writeHeaders(&sb, header)
	sb.WriteString("\r\n")
	return sb.String()
}

// RequestHead reconstructs the HTTP request head ami sent, as text.
func RequestHead(targetURI string, header http.Header) string {
	var sb strings.Builder
	sb.WriteString("GET ")
	sb.WriteString(pathOf(targetURI))
	sb.WriteString(" HTTP/1.1\r\n")
	writeHeaders(&sb, header)
	sb.WriteString("\r\n")
	return sb.String()
}

// buildRequestRecord serializes a WARC request record reconstructed from the
// request headers ami sent.
func buildRequestRecord(targetURI string, header http.Header, date, id string) []byte {
	return warcEnvelope("request", targetURI, date, id, "application/http; msgtype=request", "", nil, []byte(RequestHead(targetURI, header)))
}

// buildResponseRecord serializes a WARC response record with a reconstructed
// HTTP status line, headers, and the captured body.
func buildResponseRecord(targetURI string, status int, header http.Header, body []byte, date, id string) []byte {
	// Pass the reconstructed HTTP head and the body as separate parts so the
	// envelope writes the body straight into its buffer once, instead of first
	// concatenating head+body into a throwaway slice (a second copy of the body).
	digest := payloadDigest(body)
	return warcEnvelope("response", targetURI, date, id, "application/http; msgtype=response", digest, nil, []byte(ResponseHead(status, header)), body)
}

// buildRevisitRecord serializes a WARC revisit record (server-not-modified
// profile) referencing the prior capture's payload digest.
func buildRevisitRecord(targetURI string, header http.Header, date, id, refDigest string) []byte {
	var sb strings.Builder
	sb.WriteString("HTTP/1.1 304 Not Modified\r\n")
	writeHeaders(&sb, header)
	sb.WriteString("\r\n")

	extra := []string{
		"WARC-Profile: http://netpreserve.org/warc/1.1/revisit/identical-payload-digest",
	}
	if refDigest != "" {
		extra = append(extra, "WARC-Payload-Digest: sha1:"+refDigest)
	}
	return warcEnvelope("revisit", targetURI, date, id, "application/http; msgtype=response", "", extra, []byte(sb.String()))
}

// warcEnvelope wraps payload parts in WARC headers. payloadDigestVal, when
// non-empty, is emitted as WARC-Payload-Digest. Extra header lines are appended
// verbatim. The payload is passed in parts (for a response that is the HTTP head
// then the body) so the body is copied into the buffer once and never
// pre-concatenated; the buffer is grown to the exact final size up front.
func warcEnvelope(typ, targetURI, date, id, contentType, payloadDigestVal string, extra []string, payload ...[]byte) []byte {
	payloadLen := 0
	for _, p := range payload {
		payloadLen += len(p)
	}

	lines := []string{
		"WARC/1.1",
		"WARC-Type: " + typ,
		"WARC-Target-URI: " + targetURI,
		"WARC-Date: " + date,
		"WARC-Record-ID: " + id,
		"Content-Type: " + contentType,
	}
	if payloadDigestVal != "" {
		lines = append(lines, "WARC-Payload-Digest: sha1:"+payloadDigestVal)
	}
	lines = append(lines, extra...)
	lines = append(lines, "Content-Length: "+strconv.Itoa(payloadLen))
	head := strings.Join(lines, "\r\n")

	var buf bytes.Buffer
	buf.Grow(len(head) + 4 + payloadLen + 4)
	buf.WriteString(head)
	buf.WriteString("\r\n\r\n")
	for _, p := range payload {
		buf.Write(p)
	}
	buf.WriteString("\r\n\r\n")
	return buf.Bytes()
}

// writeHeaders writes HTTP headers in a stable order so output is reproducible.
func writeHeaders(sb *strings.Builder, header http.Header) {
	keys := make([]string, 0, len(header))
	for k := range header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range header[k] {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
	}
}

// payloadDigest returns the lowercase hex sha1 of the body.
func payloadDigest(body []byte) string {
	sum := sha1.Sum(body)
	return hex.EncodeToString(sum[:])
}

// pathOf returns the path+query of a URL for the request line, defaulting to /.
func pathOf(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if j := strings.IndexByte(raw, '/'); j >= 0 {
		return raw[j:]
	}
	return "/"
}
