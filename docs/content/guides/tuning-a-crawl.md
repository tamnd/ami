---
title: "Tuning a crawl"
description: "Push the box as hard as it goes with workers, transport shards, and per-host caps, or dial it back to be polite."
weight: 20
---

ami's defaults are sized for a fast machine on a fat pipe: 2000 workers across 64 keep-alive transport pools.
That is a lot for a laptop and too much for a single small site.
These flags move the crawl along the throughput-versus-politeness axis.

## Concurrency

`--workers` sets how many fetches run at once, and `--transport-shards` sets how many keep-alive connection pools they spread across (sharding the pool reduces lock contention at high worker counts):

```bash
# Ease off on a laptop
ami crawl urls.txt --workers 200 --transport-shards 8

# Push a well-provisioned box
ami crawl urls.txt --workers 4000 --transport-shards 128
```

## Per-host limits

A seed that hammers one host is both rude and slow, since the host throttles you.
Cap the connections any single host gets, and give up on a host that keeps failing so it stops eating worker slots:

```bash
ami crawl urls.txt --per-host 4 --domain-fail-threshold 5
```

`--per-host` is the ceiling on concurrent connections to one host; `--domain-fail-threshold` is how many consecutive failures a domain may rack up before ami skips the rest of its URLs.

## Timeouts and body size

`--timeout` is the hard ceiling on a single request.
`--max-body` caps how many bytes of a response body ami stores, so one giant download cannot blow up a WARC:

```bash
ami crawl urls.txt --timeout 10s --max-body 4194304
```

## Header profile

`--mode fast` (the default) sends a minimal header set for the highest throughput.
`--mode polite` sends a full browser-like header set, which a bot-detection WAF is less likely to fingerprint and block:

```bash
ami crawl urls.txt --mode polite
```

## Unchanged responses

When a seed carries the digest from a prior capture, ami compares it against the SHA-1 of the body it just fetched.
A match means the content is unchanged, so ami records a revisit instead of re-storing the body.
To store the full body every time regardless, pass `--store-unchanged`:

```bash
ami crawl urls.txt --store-unchanged
```

## WARC roll-over

Long runs roll their output across multiple WARC files.
`--warc-size` sets the target bytes per file before ami opens the next one:

```bash
# Roll over every 256 MiB instead of the 1 GiB default
ami crawl urls.txt --warc-size 268435456
```
