---
title: "Tuning a crawl"
description: "Push the box as hard as it goes with workers, transport shards, and per-host caps, or dial it back to be polite."
weight: 20
---

ami's defaults are sized for a fast machine on a fat pipe: 2000 workers across 64 keep-alive transport pools.
That is a lot for a laptop and too much for a single small site.
These flags move the crawl along the throughput-versus-politeness axis.

## Concurrency

`--workers` sets the size of the worker pool and the ceiling on concurrent fetches, and `--transport-shards` sets how many keep-alive connection pools they spread across (sharding the pool reduces lock contention at high worker counts):

```bash
# Ease off on a laptop
ami crawl urls.txt --workers 200 --transport-shards 8

# Push a well-provisioned box
ami crawl urls.txt --workers 4000 --transport-shards 128
```

## Adaptive in-flight limit

The number of requests actually in flight is not fixed at `--workers`.
A controller floats it between `--min-inflight` and `--workers` based on what the local uplink sustains, the same idea as TCP congestion control: each completed response grows the limit, and a broad burst of timeouts from hosts that had been answering shrinks it.

This matters because a fixed wall of connections oversubscribes a thin uplink, the handshakes congest, requests time out before the first byte, and a naive crawler then blames the hosts and skips them.
The controller keeps the link out of that collapse, so live hosts are not false-skipped, while a fat pipe with no losses still climbs straight to the `--workers` ceiling.
On a fast link the controller costs nothing; you only notice it on a link too thin for the worker count you asked for.

```bash
# Start gentle and let it climb; never drop below 64 in flight
ami crawl urls.txt --workers 3000 --start-inflight 128 --min-inflight 64
```

`--start-inflight` is where the controller begins before it has learned the link (default 64), and `--min-inflight` is the floor it will never drop below (default 32).
A request the engine attributes to local congestion rather than the host is retried up to `--max-retries` times (default 4) with a short backoff, and only counted as a failure if it still cannot get through.

## Per-host limits

A seed that hammers one host is both rude and slow, since the host throttles you.
Cap the connections any single host gets, and give up on a host that keeps failing so it stops eating worker slots:

```bash
ami crawl urls.txt --per-host 4 --domain-fail-threshold 5
```

`--per-host` is the ceiling on concurrent connections to one host; `--domain-fail-threshold` is how many host-attributable failures a domain may rack up before ami skips the rest of its URLs.
Only failures the host owns count toward that threshold: a refused connection, a reset, an unresolvable name.
A timeout the engine reads as local congestion does not, and a domain that has answered even once is never skipped, so a slow-but-live host is never given up on by mistake.

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
