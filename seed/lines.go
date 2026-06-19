package seed

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
)

// Lines reads one URL per line from a file (or stdin when path is "-"). Blank
// lines and lines beginning with # are skipped. It is the simplest source and
// the default.
type Lines struct {
	Path string
}

// Name implements Source.
func (l Lines) Name() string { return "lines:" + l.Path }

// Iterate implements Source.
func (l Lines) Iterate(ctx context.Context, yield func(Seed) error) error {
	r, closeFn, err := openInput(l.Path)
	if err != nil {
		return err
	}
	defer closeFn()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := yield(Seed{URL: line}); err != nil {
			return err
		}
	}
	return sc.Err()
}

// openInput opens path for reading, treating "-" as stdin. The returned close
// func is always safe to call.
func openInput(path string) (io.Reader, func(), error) {
	if path == "-" || path == "" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { _ = f.Close() }, nil
}
