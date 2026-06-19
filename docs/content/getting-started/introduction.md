---
title: "Introduction"
description: "Why ami separates the seed from the fetch, and what a WARC plus Parquet capture buys you."
weight: 10
---

A crawler is really two jobs glued together: deciding which URLs to fetch, and fetching them.
Most tools fuse the two, so the only way to re-fetch a known list of URLs is to re-run a whole crawler and let it rediscover them.
ami pulls the two apart.
It does not discover URLs.
It takes a seed (a list of URLs you already have) and re-fetches every one as fast as one machine sustains, then packs the results.

Say you have a frontier of a few million URLs, or a sitemap, or a `url` column in a Parquet dataset.
Hand it to ami and you get the bytes behind every one, archived in a standard format and indexed so you can find any single response again:

```bash
ami crawl urls.txt
```

ami treats a crawl as three steps in order.

## 1. Read the seed

A seed is just a list of URLs.
ami reads it from one of several formats, inferred from the path or set with `--from`:

- **lines**: one URL per line in a text file (`-` means stdin);
- **jsonl**: newline-delimited JSON objects, each with a `url` field (any other fields ride along as per-capture metadata);
- **parquet**: a Parquet file with a `url` column;
- **sitemap**: an XML sitemap or sitemap index, fetched over HTTP.

The same fetch engine runs behind all four, so switching seeds never changes how the crawl behaves.

## 2. Fetch

ami runs a large pool of workers (2000 by default) over sharded keep-alive transport pools.
Per-host connection caps and a per-domain failure threshold keep one slow or hostile host from soaking up the pool, and a `--mode` switch picks between a minimal header profile for raw throughput and a browser-like one that bot-detection WAFs are less likely to block.
When a seed carries a prior digest, ami compares it against the sha1 of the fetched body and records an unchanged response as a revisit instead of re-storing it.

## 3. Pack

Every response is written to a gzipped WARC file, the ISO archival format, capped at a target size so a long run rolls over into `ami-00000.warc.gz`, `ami-00001.warc.gz`, and so on.
Alongside them ami writes `captures.parquet`, one row per fetch, carrying the URL, host, status, content type, body length, content digest, and a pointer (file, offset, length) back into the WARC.
That index is the fast path: you can answer "which URLs 404ed" or "where is the response for this URL" without reopening a single archive.

## Then what?

The output is two standard things: WARC files any archival tool reads, and a Parquet table DuckDB, pandas, or `ami inspect` reads.
`ami inspect captures.parquet` gives you a quick summary and a sample of rows without a Parquet tool installed.

Next: [install ami](/getting-started/installation/).
