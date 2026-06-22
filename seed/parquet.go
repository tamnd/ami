package seed

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// Parquet reads seeds from a Parquet file with a "url" column. An optional
// "digest" column carries a prior body's SHA-1 for change detection; every other
// string-typed leaf column is carried into Meta under its column name. The schema
// is read dynamically so any producer can hand ami a Parquet file without a
// shared Go type.
type Parquet struct {
	Path string
}

// Name implements Source.
func (p Parquet) Name() string { return "parquet:" + p.Path }

// Iterate implements Source.
func (p Parquet) Iterate(ctx context.Context, yield func(Seed) error) error {
	osf, err := os.Open(p.Path)
	if err != nil {
		return err
	}
	defer func() { _ = osf.Close() }()
	info, err := osf.Stat()
	if err != nil {
		return err
	}
	f, err := parquet.OpenFile(osf, info.Size())
	if err != nil {
		return err
	}

	// Map leaf column name -> column index, and remember which leaves are the
	// string columns we want to surface as Meta.
	schema := f.Schema()
	cols := schema.Columns()
	idxByName := make(map[string]int, len(cols))
	for i, path := range cols {
		idxByName[strings.Join(path, ".")] = i
	}
	urlCol, ok := idxByName["url"]
	if !ok {
		return fmt.Errorf("seed parquet %q: no \"url\" column", p.Path)
	}
	sc := seedCols{urlCol: urlCol, cols: cols}
	sc.digestCol, sc.hasDigest = idxByName["digest"]
	sc.etagCol, sc.hasETag = idxByName["etag"]
	sc.modCol, sc.hasMod = idxByName["last_modified"]

	const batch = 4096
	for _, rg := range f.RowGroups() {
		rows := rg.Rows()
		buf := make([]parquet.Row, batch)
		for {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			n, readErr := rows.ReadRows(buf)
			for i := 0; i < n; i++ {
				s := rowToSeed(buf[i], sc)
				if s.URL == "" {
					continue
				}
				if err := yield(s); err != nil {
					_ = rows.Close()
					return err
				}
			}
			if readErr != nil {
				break
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

// seedCols records which leaf columns carry the fields a Seed understands, so a
// prior run's capture index can be read straight back as a recrawl seed.
type seedCols struct {
	cols                       [][]string
	urlCol, digestCol          int
	etagCol, modCol            int
	hasDigest, hasETag, hasMod bool
}

// rowToSeed projects one Parquet row into a Seed, mapping url/digest/etag/
// last_modified to their fields and copying every other non-null string column
// into Meta.
func rowToSeed(row parquet.Row, sc seedCols) Seed {
	s := Seed{}
	for _, v := range row {
		col := v.Column()
		if col < 0 || col >= len(sc.cols) {
			continue
		}
		if v.IsNull() || v.Kind() != parquet.ByteArray {
			continue
		}
		str := v.String()
		switch {
		case col == sc.urlCol:
			s.URL = str
		case sc.hasDigest && col == sc.digestCol:
			s.Digest = str
		case sc.hasETag && col == sc.etagCol:
			s.ETag = str
		case sc.hasMod && col == sc.modCol:
			s.ModTime = str
		default:
			name := strings.Join(sc.cols[col], ".")
			if s.Meta == nil {
				s.Meta = make(map[string]string)
			}
			s.Meta[name] = str
		}
	}
	return s
}
