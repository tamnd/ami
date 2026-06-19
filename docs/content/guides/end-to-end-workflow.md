---
title: "An end-to-end run"
description: "Take a list of URLs from raw seed to a queryable WARC archive: prepare the seed, crawl it, and read the results back."
weight: 5
---

This guide walks a seed all the way through: from however your URLs arrive, to a crawl, to answering questions about what came back.
It ties together the [seed formats](/guides/seed-formats/), [tuning](/guides/tuning-a-crawl/), and [sharding](/guides/sharding-a-run/) guides into one path.

## 1. Get your URLs into a seed

A seed is just a list of URLs.
ami reads four shapes and infers the format from the path, so most of the time there is nothing to convert:

- a text file, one URL per line (`urls.txt`)
- newline-delimited JSON with a `url` field per line (`seed.jsonl`)
- a Parquet file with a `url` column (`seed.parquet`)
- an XML sitemap, local or remote (`https://example.com/sitemap.xml`)

If a producer hands you URLs alongside other context, reach for JSONL or Parquet.
Any field beyond `url` rides along into the capture index, so producer metadata survives the crawl:

```jsonl
{"url": "https://example.com/a", "source": "frontier", "depth": 1}
{"url": "https://example.com/b", "source": "frontier", "depth": 2}
```

Two fields are special.
A `digest` is the SHA-1 of a body from a previous capture; supplying it lets ami detect unchanged content and record a revisit rather than a second copy.
Everything else is kept verbatim in `meta_json`.
See [seed formats](/guides/seed-formats/) for the full rules.

## 2. Crawl it

Point ami at the seed:

```bash
ami crawl seed.parquet -o run-out
```

That re-fetches every URL with the default 2000 workers and writes the results under `run-out`.
On a laptop, or against a handful of hosts, ease off so you are not rude or rate-limited:

```bash
ami crawl seed.parquet -o run-out --workers 200 --per-host 4
```

On a well-provisioned box, push harder:

```bash
ami crawl seed.parquet -o run-out --workers 4000 --transport-shards 128
```

The [tuning guide](/guides/tuning-a-crawl/) covers every knob on the throughput-versus-politeness axis.
While the run goes, ami prints a live line of pages per second, bytes per second, and the status breakdown.

## 3. See what came back

Every run writes WARC files plus a `captures.parquet` index under `-o`.
The fastest look needs no other tool:

```bash
ami inspect run-out/captures.parquet -n 20
```

For anything real, the index is an ordinary Parquet file.
DuckDB answers the usual questions directly:

```sql
-- how did the run go?
SELECT status, count(*) AS n
FROM 'run-out/captures.parquet'
GROUP BY status ORDER BY n DESC;

-- what failed, and why?
SELECT url, error FROM 'run-out/captures.parquet' WHERE error <> '';
```

The bytes themselves live in the WARC files, in the standard format, so they open in any WARC tool.
Each index row carries a `warc_file`, `warc_offset`, and `warc_length` that point straight at the response record, so you never scan the archive to find one capture.
The [configuration reference](/reference/configuration/) documents every column.

## 4. Retry just the failures

Because a failed fetch still produces a row (status `0`, with the reason in `error`), the index is a complete record of the run.
Select the rows that need another attempt, write them back out as a seed, and crawl that smaller list:

```bash
duckdb -noheader -csv \
  -c "SELECT url FROM 'run-out/captures.parquet' WHERE error <> ''" \
  > retry.txt
ami crawl retry.txt -o run-out --run-id retry --mode polite
```

`--run-id retry` keeps the retry's output beside the first pass under the same directory, and `--mode polite` gives the stragglers a browser-like header set that a bot-detection WAF is less likely to block.

## Scaling out

When one machine is not enough, the same seed splits across a fleet: each process takes `--shard i --shards N` and crawls its slice.
The [sharding guide](/guides/sharding-a-run/) covers the partitioning and how the per-machine indexes union back into one logical run.
