# ami

[![ci](https://github.com/tamnd/ami/actions/workflows/ci.yml/badge.svg)](https://github.com/tamnd/ami/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/tamnd/ami)](https://github.com/tamnd/ami/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/tamnd/ami.svg)](https://pkg.go.dev/github.com/tamnd/ami)
[![Go Report Card](https://goreportcard.com/badge/github.com/tamnd/ami)](https://goreportcard.com/report/github.com/tamnd/ami)
[![License](https://img.shields.io/github/license/tamnd/ami)](./LICENSE)

**ami** (網, "net") re-fetches every URL in a seed (a list of URLs) as fast as one machine sustains, then packs the results into WARC files and a columnar Parquet index.
The seed can be a text file, newline JSON, a Parquet column, a sitemap, or stdin.
They all feed the same fetch engine.

[Install](#install) • [Quick start](#quick-start) • [Seed formats](#seed-formats) • [Output](#output) • [Flags](#flags) • [Build](#building-from-source)

A crawler is two jobs glued together: deciding which URLs to fetch, and fetching them.
ami does only the second.
You bring a list of URLs (a frontier someone else produced, a sitemap, a column lifted out of a dataset) and ami re-fetches every one, archives the responses in a standard format, and indexes them so you can find any single response again.

Full docs and guides live at **[ami.tamnd.com](https://ami.tamnd.com)**.

## Install

```bash
go install github.com/tamnd/ami/cmd/ami@latest
```

Prefer a prebuilt binary? Grab an archive, a `.deb`/`.rpm`/`.apk`, or a checksum from [releases](https://github.com/tamnd/ami/releases). Or use the container image:

```bash
docker run -v "$PWD/out:/out" ghcr.io/tamnd/ami crawl --from lines /out/urls.txt
```

ami is a single pure-Go binary with no runtime dependency beyond CA roots.

Shell completion ships in the box: `ami completion bash|zsh|fish|powershell`.

## Quick start

```bash
# A list of URLs, one per line
cat > urls.txt <<'EOF'
https://example.com/
https://example.com/about
https://www.iana.org/help/example-domains
EOF

# Crawl it: writes WARC files and captures.parquet under ami-out/
ami crawl urls.txt

# Look at what landed, no Parquet tool needed
ami inspect ami-out/captures.parquet
```

```
captures: 3 rows in ami-out/captures.parquet

STATUS  BYTES  HOST                 URL
200     1256   example.com          https://example.com/
200     1256   example.com          https://example.com/about
200     9417   www.iana.org         https://www.iana.org/help/example-domains
```

The seed does not have to be a file. Pipe URLs in, or point ami at a sitemap:

```bash
printf 'https://example.com/\n' | ami crawl --from lines -
ami crawl https://www.example.com/sitemap.xml
```

## Seed formats

A seed is just a list of URLs. ami reads four formats, inferred from the path or set with `--from`:

| Format | Looks like | Shape |
| --- | --- | --- |
| `lines` | a `.txt` file, or `-` for stdin | one URL per line |
| `jsonl` | `.jsonl`, `.ndjson` | one JSON object per line, each with a `url` field |
| `parquet` | `.parquet`, `.pq` | a Parquet file with a `url` column |
| `sitemap` | an `http(s)://` URL, or `.xml` | an XML sitemap or sitemap index |

With JSONL, any field other than `url` rides along as per-capture metadata and lands in the index's `meta_json` column.

## Output

A crawl writes two kinds of output under `--out` (default `ami-out`):

```
ami-out/
├── ami-00000.warc.gz      # responses, gzipped WARC 1.1, rolled at --warc-size
├── ami-00001.warc.gz
└── captures.parquet       # one row per fetch, pointing back into the WARC
```

The WARC files open in any WARC tool. `captures.parquet` carries one row per fetch with these columns:

| Column | Type | Meaning |
| --- | --- | --- |
| `url` | string | the URL fetched |
| `host` | string | its host |
| `status` | int32 | HTTP status (0 if the fetch failed before a response) |
| `fetched_at` | int64 | completion time, Unix milliseconds |
| `content_type` | string | response `Content-Type` |
| `body_length` | int64 | stored body length in bytes |
| `digest` | string | SHA-1 of the body, for change detection |
| `unchanged` | bool | true when the digest matched the seed's prior digest |
| `warc_file` | string | WARC file holding the response |
| `warc_offset` | int64 | byte offset of the record in that file |
| `warc_length` | int64 | byte length of the record |
| `error` | string | failure text (empty on success) |
| `meta_json` | string | the seed's per-URL metadata as a JSON object (empty when none) |

The three `warc_*` columns are a direct pointer: open the file, seek to the offset, read the length, and you have the exact record. The index reads in DuckDB, pandas, or `ami inspect`.

## Flags

`ami crawl <seed>` takes:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--from` | infer | seed format: `lines`, `jsonl`, `parquet`, `sitemap` |
| `-o, --out` | `ami-out` | output directory for the WARC files and the index |
| `--run-id` | | subdirectory under `--out` for this run |
| `--workers` | `2000` | concurrent fetch workers |
| `--transport-shards` | `64` | keep-alive transport pools |
| `--timeout` | `5s` | per-request ceiling timeout |
| `--per-host` | `8` | max concurrent connections per host |
| `--domain-fail-threshold` | `3` | consecutive failures before a domain is skipped |
| `--store-unchanged` | `false` | store the full body even when the digest is unchanged |
| `--max-body` | `2097152` | max response body bytes to store (2 MiB) |
| `--warc-size` | `1073741824` | target bytes per WARC file before rolling over (1 GiB) |
| `--mode` | `fast` | header profile: `fast` or `polite` |
| `--shard` | `0` | this process's partition index (0-based) |
| `--shards` | `1` | total partitions for a distributed run |

`ami crawl --help` has the canonical list. `ami inspect <captures.parquet>` takes `-n/--limit` for the number of sample rows.

## Building from source

```bash
git clone https://github.com/tamnd/ami
cd ami
make build          # -> bin/ami (pure Go)
make test           # full suite
make test-short     # skip the tests that hit the network
```

The repo is split by concern:

```
cmd/ami/   thin main: hands off to cli.Execute
cli/       the cobra command tree and flag wiring
seed/      the seed readers: lines, jsonl, parquet, sitemap
fetch/     the concurrent fetch engine: workers, transport, header profiles
run/       the crawl loop that ties seed, fetch, and pack together
pack/      WARC writer and the Parquet capture index
config/    the tunables for one run
metrics/   the live progress snapshot
urlx/      URL helpers
docs/      the tago documentation site
```

## Releasing

Push a version tag and GitHub Actions runs GoReleaser, which builds the archives, the `.deb`/`.rpm`/`.apk` packages, a multi-arch GHCR image, checksums, SBOMs, and a cosign signature:

```bash
git tag v0.1.0
git push --tags
```

The image tag carries no `v` prefix (`ghcr.io/tamnd/ami:0.1.0`). The Homebrew and Scoop steps self-disable until their tokens exist, so the first release works with no extra secrets.

## License

MIT. See [LICENSE](LICENSE).
