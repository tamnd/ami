package seed

import (
	"fmt"
	"strings"
)

// Open builds a Source from a format name and a path or URL. The format names
// match the --from flag: lines, jsonl, parquet, sitemap. An empty format is
// inferred from the path extension, defaulting to lines.
func Open(format, ref string) (Source, error) {
	if format == "" {
		format = infer(ref)
	}
	switch format {
	case "lines", "txt":
		return Lines{Path: ref}, nil
	case "jsonl", "ndjson":
		return JSONL{Path: ref}, nil
	case "parquet", "pq":
		return Parquet{Path: ref}, nil
	case "sitemap", "xml":
		return Sitemap{URL: ref}, nil
	default:
		return nil, fmt.Errorf("unknown seed format %q (want lines, jsonl, parquet, or sitemap)", format)
	}
}

// infer guesses a format from the reference's extension.
func infer(ref string) string {
	switch {
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return "sitemap"
	case strings.HasSuffix(ref, ".jsonl"), strings.HasSuffix(ref, ".ndjson"):
		return "jsonl"
	case strings.HasSuffix(ref, ".parquet"), strings.HasSuffix(ref, ".pq"):
		return "parquet"
	case strings.HasSuffix(ref, ".xml"), strings.HasSuffix(ref, ".xml.gz"):
		return "sitemap"
	default:
		return "lines"
	}
}
