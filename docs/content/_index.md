---
title: "ami"
description: "ami (網, net) re-fetches every URL in a seed as fast as one machine sustains, then packs the results into WARC files and a columnar Parquet index. The seed can be a text file, newline JSON, a Parquet column, a sitemap, or stdin, all from one pure-Go binary."
heroTitle: "Cast a net over a list of URLs"
heroLead: "ami reads a seed (a list of URLs), re-fetches every one concurrently, and writes the responses to standard WARC files plus a columnar Parquet index that points back into them. One machine, thousands of workers, one self-contained capture."
heroPrimaryURL: "/getting-started/quick-start/"
heroPrimaryText: "Get started"
---

You have a list of URLs and you want the bytes behind them: a crawl frontier someone else produced, a sitemap, a column lifted out of a dataset.
ami (網, "net") takes that list and re-fetches every URL as fast as one machine sustains, then packs what comes back into WARC files and a Parquet index you can query.

The seed is just a list of URLs.
Point ami at a text file and go:

```bash
ami crawl urls.txt
```

## What it does

- **Reads any seed.** A text file (one URL per line), newline-delimited JSON with a `url` field, a Parquet file with a `url` column, an XML sitemap, or stdin. The same engine drives them all.
- **Fetches concurrently.** Thousands of workers, sharded keep-alive transport pools, and per-host connection caps push the box hard while staying inside polite limits.
- **Writes standard WARC.** Every response lands in a gzipped WARC file, the ISO archival format, so the captures open in any WARC tool, not just ami.
- **Indexes into Parquet.** A `captures.parquet` carries one row per fetch with the URL, host, status, content type, digest, and a pointer (file, offset, length) back into the WARC, so you can find a response without reopening the archive.
- **Shards across machines.** Hand each process its partition with `--shard`/`--shards` and a big seed splits cleanly across a fleet.

## Where to go next

- New here? Start with the [introduction](/getting-started/introduction/), then the [quick start](/getting-started/quick-start/).
- Want to install it? See [installation](/getting-started/installation/).
- Looking for a specific task? The [guides](/guides/) cover the seed formats, tuning a crawl for throughput, and sharding a run across machines.
- Need every flag? The [CLI reference](/reference/cli/) is the full surface.
