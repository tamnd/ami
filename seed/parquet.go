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
	digestCol, hasDigest := idxByName["digest"]

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
				s := rowToSeed(buf[i], cols, urlCol, digestCol, hasDigest)
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

// rowToSeed projects one Parquet row into a Seed, copying every non-null string
// column other than url/digest into Meta.
func rowToSeed(row parquet.Row, cols [][]string, urlCol, digestCol int, hasDigest bool) Seed {
	s := Seed{}
	for _, v := range row {
		col := v.Column()
		if col < 0 || col >= len(cols) {
			continue
		}
		if v.IsNull() || v.Kind() != parquet.ByteArray {
			continue
		}
		str := v.String()
		switch {
		case col == urlCol:
			s.URL = str
		case hasDigest && col == digestCol:
			s.Digest = str
		default:
			name := strings.Join(cols[col], ".")
			if s.Meta == nil {
				s.Meta = make(map[string]string)
			}
			s.Meta[name] = str
		}
	}
	return s
}
