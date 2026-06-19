package seed

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func collect(t *testing.T, s Source) []Seed {
	t.Helper()
	var out []Seed
	if err := s.Iterate(context.Background(), func(sd Seed) error {
		out = append(out, sd)
		return nil
	}); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	return out
}

func TestLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "urls.txt")
	body := "https://a.com\n# comment\n\nhttps://b.com\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collect(t, Lines{Path: path})
	if len(got) != 2 || got[0].URL != "https://a.com" || got[1].URL != "https://b.com" {
		t.Fatalf("unexpected seeds: %+v", got)
	}
}

func TestJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seed.jsonl")
	body := `{"url":"https://a.com","digest":"d1","lang":"en"}` + "\n" +
		`{"url":"https://b.com"}` + "\n" + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collect(t, JSONL{Path: path})
	if len(got) != 2 {
		t.Fatalf("want 2 seeds, got %d", len(got))
	}
	if got[0].Digest != "d1" || got[0].Meta["lang"] != "en" {
		t.Fatalf("meta not carried: %+v", got[0])
	}
}

func TestOpenInfer(t *testing.T) {
	cases := map[string]string{
		"x.jsonl":             "jsonl:x.jsonl",
		"x.parquet":           "parquet:x.parquet",
		"https://h/sitemap":   "sitemap:https://h/sitemap",
		"plain.txt":           "lines:plain.txt",
	}
	for ref, wantName := range cases {
		s, err := Open("", ref)
		if err != nil {
			t.Fatalf("Open(%q): %v", ref, err)
		}
		if s.Name() != wantName {
			t.Errorf("Open(%q).Name() = %q, want %q", ref, s.Name(), wantName)
		}
	}
}
