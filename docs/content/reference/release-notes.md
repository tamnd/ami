---
title: "Release notes"
description: "What changed in each ami release."
weight: 40
---

The authoritative, commit-level history lives on the [releases page](https://github.com/tamnd/ami/releases). This page summarises each version.

## v0.1.0

The first release. ami re-fetches every URL in a seed as fast as one machine sustains, then packs the results into a compact Parquet store (or classic WARC) with a columnar index.

### Crawl

- **`ami crawl <seed>`** reads a seed (a list of URLs), re-fetches every one concurrently, and writes the captures plus an index under `--out`.
- **Four seed formats.** A text file (one URL per line, `-` for stdin), newline-delimited JSON with a `url` field, a Parquet file with a `url` column, or an XML sitemap, all driving the same fetch engine. A capture file from a prior run feeds straight back in as the seed for a recrawl.
- **`ami inspect`** summarises a capture index and samples its rows with no Parquet tool installed, over a single file or a directory of rotated ones.
- **Sharded runs.** `--shard`/`--shards` partition a seed deterministically across machines, so each process fetches a disjoint slice.

### Output

- **Parquet body store by default.** Bodies and their reconstructed request and response headers are stored inline in the capture Parquet, compressed column-wise with zstd. On real pages this packs several times smaller than per-record gzipped WARC. The writer rotates into `captures-NNNNN.parquet` bounded by `--capture-size`, each file independently readable, so you can offload and delete finished files mid-run and a crash loses only the open one.
- **WARC still available.** `--format warc` writes standard gzipped WARC 1.1 files (rolled at `--warc-size`) plus a metadata-only index whose `warc_*` columns point back into them.
- **Columnar index.** One row per fetch, with `url`, `host`, `status`, `fetched_at`, `content_type`, `body_length`, `digest`, `unchanged`, `etag`, `last_modified`, and `meta_json`, readable in DuckDB, pandas, or `ami inspect`.

### Engine

- **Sized for one fast machine.** Thousands of workers over sharded keep-alive transport pools, with per-host connection caps and a `fast`/`polite` header profile.
- **Adaptive in-flight limit.** The number of requests in flight floats between `--min-inflight` and `--workers` under a congestion controller, instead of a fixed wall of connections. A thin uplink is no longer oversubscribed into a timeout storm, and a fat pipe still climbs to the worker ceiling.
- **No false-skipped live hosts.** The dead-domain breaker skips a host only when it has proven the host cannot be reached: a name that does not resolve, or a network with no route. Every timeout, reset, and refused connection is treated as transient and retried up to `--max-retries` times, because each can be collateral from the crawler's own concurrency against a live host. A domain is immunised the moment it answers once or accepts a single connection. On a real crawl of 100,000 top domains over https this took the false-skip rate among skipped hosts from 34% to zero, verified by re-probing every skipped host, with throughput unchanged.
- **Resolver racing and domain collapse.** DNS queries race the system resolver against public resolvers in parallel, and a name is called dead only when all agree. Hosts collapse by effective top-level domain plus one, so a failure on one subdomain never condemns a whole public suffix.
- **Conditional recrawl.** When the seed carries an ETag or last-modified time, ami sends `If-None-Match`/`If-Modified-Since` and records a bodiless 304 as unchanged with no body transferred, so a warm recrawl runs at the request-rate ceiling instead of being bound by bandwidth.
- **Host-spread ordering.** The seed is spread across hosts (`--reorder`, on by default) so throughput does not depend on the order URLs arrive in. A host-clustered shard fed in order stalls the pool on the per-host cap; the spread keeps a wide host set in flight. On a 100,000-URL sample this measured about 4.3x.

### Packaging

- **Everywhere.** Archives for Linux, macOS, Windows, and FreeBSD, `.deb`/`.rpm`/`.apk` packages, a Homebrew cask, a Scoop manifest, a signed apt/dnf repository, a multi-arch GHCR image, checksums, SBOMs, and a cosign signature.
