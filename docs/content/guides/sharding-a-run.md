---
title: "Sharding a run"
description: "Split one big seed across several machines so each fetches a disjoint partition."
weight: 30
---

One machine has a ceiling: at some point the bottleneck is the box, not the code.
When a seed is large enough that one machine cannot finish it in the time you have, ami splits the work across several.
Each process reads the whole seed but fetches only its own partition, so the machines never duplicate a URL and never need to coordinate.

## How it works

`--shards` is the total number of partitions, and `--shard` is this process's 0-based index.
ami hashes each URL to a partition; a process keeps the URLs that fall in its shard and skips the rest.
The same seed and the same `--shards` value always partition the URLs the same way, so the split is deterministic.

## Run it

Give every machine the same seed and the same `--shards`, and a distinct `--shard`:

```bash
# machine 0 of 4
ami crawl urls.txt --shards 4 --shard 0 --out s0

# machine 1 of 4
ami crawl urls.txt --shards 4 --shard 1 --out s1

# machine 2 of 4
ami crawl urls.txt --shards 4 --shard 2 --out s2

# machine 3 of 4
ami crawl urls.txt --shards 4 --shard 3 --out s3
```

Each writes its own WARC files and `captures.parquet` under its `--out`.
Because the partitions are disjoint, the four indexes concatenate into the full capture set with no de-duplication needed.

## Keep runs apart with --run-id

When several runs share one output root, `--run-id` puts each under its own subdirectory of `--out`, so their WARC files and indexes do not collide:

```bash
ami crawl urls.txt --out captures --run-id 2026-06-19
# -> captures/2026-06-19/ami-00000.warc.gz, captures/2026-06-19/captures.parquet
```
