---
title: "CLI reference"
description: "Every ami command and flag."
weight: 10
---

```
ami [command] [flags]
```

Two commands: `crawl` re-fetches every URL in a seed and packs the results, and `inspect` summarises a capture index.
Run `ami <command> --help` for the canonical, up-to-date list.

## ami crawl

```
ami crawl <seed> [flags]
```

Reads a seed (a list of URLs), re-fetches each one concurrently, and writes WARC files plus a `captures.parquet` index under `--out`.
The seed format is inferred from the path, or set with `--from`.

### Seed

| Flag | Default | Meaning |
|------|---------|---------|
| `--from` | infer from path | Seed format: `lines`, `jsonl`, `parquet`, or `sitemap` |

The seed argument is a path or URL.
`-` reads a `lines` seed from stdin.
See [seed formats](/guides/seed-formats/) for the inference rules and the shape of each format.

### Output

| Flag | Default | Meaning |
|------|---------|---------|
| `-o, --out` | `ami-out` | Output directory for the WARC files and the capture index |
| `--run-id` | | Subdirectory under `--out` for this run |
| `--warc-size` | `1073741824` | Target bytes per WARC file before rolling over (1 GiB) |

### Concurrency

| Flag | Default | Meaning |
|------|---------|---------|
| `--workers` | `2000` | Worker pool size and the ceiling on in-flight requests |
| `--min-inflight` | `32` | Floor on the adaptive in-flight request limit |
| `--start-inflight` | `64` | Initial in-flight limit before the controller adapts |
| `--transport-shards` | `64` | Number of keep-alive transport pools |

The number of requests actually in flight floats between `--min-inflight` and `--workers` under a congestion controller that tracks what the local uplink sustains, so a thin link is never asked to open more connections than it carries.
See [Tuning a crawl](../../guides/tuning-a-crawl/) for how it behaves.

### Politeness and limits

| Flag | Default | Meaning |
|------|---------|---------|
| `--timeout` | `5s` | Per-request ceiling timeout |
| `--per-host` | `8` | Max concurrent connections per host |
| `--domain-fail-threshold` | `3` | Host-attributable failures before a domain is skipped |
| `--max-retries` | `4` | Retries for a fetch the engine attributes to local congestion |
| `--max-body` | `2097152` | Maximum response body bytes to store (2 MiB) |
| `--mode` | `fast` | Header profile: `fast` (minimal) or `polite` (browser-like) |

### Storage behaviour

| Flag | Default | Meaning |
|------|---------|---------|
| `--store-unchanged` | `false` | Store the full body even when the digest is unchanged |

### Sharded distribution

| Flag | Default | Meaning |
|------|---------|---------|
| `--shard` | `0` | This process's partition index (0-based) |
| `--shards` | `1` | Total number of partitions for a distributed run |

See [sharding a run](/guides/sharding-a-run/) for how to split a seed across machines.

## ami inspect

```
ami inspect <captures.parquet> [flags]
```

Summarises a capture index and prints a sample of rows, so you can look at a crawl's output without a Parquet tool installed.
It reports the total row count and then a table of status, body length, host, and URL.

| Flag | Default | Meaning |
|------|---------|---------|
| `-n, --limit` | `10` | Number of sample rows to print |

```bash
ami inspect ami-out/captures.parquet -n 20
```
