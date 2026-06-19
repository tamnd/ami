---
title: "Release notes"
description: "What changed in each ami release."
weight: 40
---

The authoritative, commit-level history lives on the [releases page](https://github.com/tamnd/ami/releases). This page summarises each version.

## v0.1.0

The first release. ami re-fetches every URL in a seed and packs the results into WARC files and a columnar Parquet index.

- **`ami crawl <seed>`** reads a seed (a list of URLs), re-fetches every one concurrently, and writes gzipped WARC files plus a `captures.parquet` index under `--out`.
- **Four seed formats.** A text file (one URL per line, `-` for stdin), newline-delimited JSON with a `url` field, a Parquet file with a `url` column, or an XML sitemap, all driving the same fetch engine.
- **A fetch engine sized for one fast machine.** Thousands of workers over sharded keep-alive transport pools, with per-host connection caps and a per-domain failure threshold, a `fast`/`polite` header profile, and a post-fetch digest comparison that records an unchanged response as a revisit.
- **Standard output.** WARC 1.1 files that open in any WARC tool, and a Parquet index with a row per fetch (`url`, `host`, `status`, `fetched_at`, `content_type`, `body_length`, `digest`, `unchanged`, `warc_file`, `warc_offset`, `warc_length`, `error`, `meta_json`) that points back into them.
- **`ami inspect`** summarises a capture index and samples its rows with no Parquet tool installed.
- **Sharded runs.** `--shard`/`--shards` partition a seed deterministically across machines, so each process fetches a disjoint slice.
- **Packaged everywhere.** Archives, `.deb`/`.rpm`/`.apk`, a multi-arch GHCR image, checksums, SBOMs, and a cosign signature.
