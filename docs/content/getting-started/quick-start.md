---
title: "Quick start"
description: "From a list of URLs to a WARC archive and a Parquet index you can query."
weight: 30
---

This walks the core loop: build a small seed, crawl it, and look at what landed on disk.

## 1. Make a seed

A seed is just a list of URLs, one per line:

```bash
cat > urls.txt <<'EOF'
https://example.com/
https://example.com/about
https://www.iana.org/help/example-domains
EOF
```

## 2. Crawl it

```bash
ami crawl urls.txt
```

ami infers the seed format from the path (a `.txt` file is `lines`), spins up its worker pool, and re-fetches every URL.
A live counter on stderr shows pages per second, throughput, and the running tally of outcomes; the final summary reports where the output landed:

```
ami: crawling urls.txt -> ami-out
ami: 3/3 done | 9 pages/s | 0.2 MiB/s | ok=3 unchanged=0 fail=0 skip=0
ami: done in 1s | 3 ok, 0 unchanged, 0 redirect, 0 4xx, 0 5xx, 0 failed, 0 skipped | 3 pages/s, 0.2 MiB/s
```

## 3. Look at what landed

```bash
ls ami-out
```

```
ami-00000.warc.gz   # the responses, in gzipped WARC
captures.parquet    # one row per fetch, pointing back into the WARC
```

The WARC opens in any WARC tool.
The Parquet index opens in DuckDB, pandas, or ami's own `inspect`:

```bash
ami inspect ami-out/captures.parquet
```

```
captures: 3 rows in ami-out/captures.parquet

STATUS  BYTES  HOST                 URL
200     1256   example.com          https://example.com/
200     1256   example.com          https://example.com/about
200     9417   www.iana.org         https://www.iana.org/help/example-domains
```

## 4. Crawl from stdin or a sitemap

The seed does not have to be a file.
Pipe URLs in, or point ami at a sitemap:

```bash
# One URL per line from stdin
printf 'https://example.com/\nhttps://example.com/about\n' | ami crawl --from lines -

# Every URL in a sitemap
ami crawl https://www.example.com/sitemap.xml
```

## Where to go next

- The [seed formats](/guides/seed-formats/) guide covers lines, JSONL, Parquet, and sitemaps in depth.
- [Tuning a crawl](/guides/tuning-a-crawl/) explains the throughput and politeness knobs.
- [Sharding a run](/guides/sharding-a-run/) splits a big seed across machines.
- The [CLI reference](/reference/cli/) lists every flag, and [configuration](/reference/configuration/) documents the output layout and the Parquet schema.
