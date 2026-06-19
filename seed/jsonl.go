package seed

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
)

// JSONL reads newline-delimited JSON objects. Each object must carry a "url"
// field; "digest" is optional, and any remaining string-valued fields become
// Meta. Non-string fields are ignored so the format stays forgiving.
type JSONL struct {
	Path string
}

// Name implements Source.
func (j JSONL) Name() string { return "jsonl:" + j.Path }

// Iterate implements Source.
func (j JSONL) Iterate(ctx context.Context, yield func(Seed) error) error {
	r, closeFn, err := openInput(j.Path)
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
		if line == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return err
		}
		s := seedFromMap(raw)
		if s.URL == "" {
			continue
		}
		if err := yield(s); err != nil {
			return err
		}
	}
	return sc.Err()
}

// seedFromMap pulls url/digest out of a decoded object and folds remaining
// string fields into Meta.
func seedFromMap(raw map[string]any) Seed {
	s := Seed{}
	for k, v := range raw {
		str, ok := v.(string)
		if !ok {
			continue
		}
		switch k {
		case "url":
			s.URL = str
		case "digest":
			s.Digest = str
		default:
			if s.Meta == nil {
				s.Meta = make(map[string]string)
			}
			s.Meta[k] = str
		}
	}
	return s
}
