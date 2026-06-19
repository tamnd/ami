package pack

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// buildRequestRecord serializes a WARC request record reconstructed from the
// request headers ami sent.
func buildRequestRecord(targetURI string, header http.Header, date, id string) []byte {
	var sb strings.Builder
	sb.WriteString("GET ")
	sb.WriteString(pathOf(targetURI))
	sb.WriteString(" HTTP/1.1\r\n")
	writeHeaders(&sb, header)
	sb.WriteString("\r\n")
	payload := sb.String()

	return warcEnvelope("request", targetURI, date, id, "application/http; msgtype=request", "", []byte(payload))
}

// buildResponseRecord serializes a WARC response record with a reconstructed
// HTTP status line, headers, and the captured body.
func buildResponseRecord(targetURI string, status int, header http.Header, body []byte, date, id string) []byte {
	var sb strings.Builder
	fmt.Fprintf(&sb, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	writeHeaders(&sb, header)
	sb.WriteString("\r\n")

	payload := append([]byte(sb.String()), body...)
	digest := payloadDigest(body)
	return warcEnvelope("response", targetURI, date, id, "application/http; msgtype=response", digest, payload)
}

// buildRevisitRecord serializes a WARC revisit record (server-not-modified
// profile) referencing the prior capture's payload digest.
func buildRevisitRecord(targetURI string, header http.Header, date, id, refDigest string) []byte {
	var sb strings.Builder
	sb.WriteString("HTTP/1.1 304 Not Modified\r\n")
	writeHeaders(&sb, header)
	sb.WriteString("\r\n")
	payload := []byte(sb.String())

	extra := []string{
		"WARC-Profile: http://netpreserve.org/warc/1.1/revisit/identical-payload-digest",
	}
	if refDigest != "" {
		extra = append(extra, "WARC-Payload-Digest: sha1:"+refDigest)
	}
	return warcEnvelope("revisit", targetURI, date, id, "application/http; msgtype=response", "", payload, extra...)
}

// warcEnvelope wraps a payload in WARC headers. payloadDigest, when non-empty,
// is emitted as WARC-Payload-Digest. Extra header lines are appended verbatim.
func warcEnvelope(typ, targetURI, date, id, contentType, payloadDigestVal string, payload []byte, extra ...string) []byte {
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
	lines = append(lines, fmt.Sprintf("Content-Length: %d", len(payload)))

	var buf bytes.Buffer
	buf.WriteString(strings.Join(lines, "\r\n"))
	buf.WriteString("\r\n\r\n")
	buf.Write(payload)
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
	return fmt.Sprintf("%x", sum)
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
