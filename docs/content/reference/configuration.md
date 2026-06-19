---
title: "Configuration"
description: "The layout of a crawl's output on disk, and the columns of the Parquet capture index."
weight: 20
---

ami is configured entirely through command-line flags (see the [CLI reference](/reference/cli/)).
A crawl writes two kinds of output: gzipped WARC files and a Parquet index.

## Output layout

A crawl writes everything under `--out` (default `ami-out`).
With `--run-id` set, the output lands one level deeper, under `<out>/<run-id>/`:

```
ami-out/
├── ami-00000.warc.gz      # responses, gzipped WARC, rolled at --warc-size
├── ami-00001.warc.gz      # the next file once the first hits the size target
├── ...
└── captures.parquet       # one row per fetch, pointing back into the WARC
```

The WARC files are standard [WARC](https://iso.org/standard/68004.html) 1.1, so they open in any WARC tool.
Each rolls over to the next sequence number once it reaches `--warc-size`, and each carries a `warcinfo` record naming ami as the software that wrote it.

Inside a file ami writes a `request` and a `response` record for every captured URL, and compresses each record as its own gzip member (the convention WARC tools call "record-at-a-time" gzip).
That per-record framing is what makes the index's byte offsets useful: a reader seeks to a record's offset and inflates that one member, instead of decompressing the whole file to reach the last record.

When a fetched body's digest matches the digest the seed already carried, ami writes a `revisit` record in place of a second copy of the body, using the standard identical-payload-digest profile.
Pass `--store-unchanged` to write the full body every time regardless.

## The capture index

`captures.parquet` holds one row per fetch.
It is the fast path: it lets you find or describe any stored response, and locate its bytes in the WARC, without reopening an archive.
The columns:

| Column | Type | Meaning |
|--------|------|---------|
| `url` | string | The URL that was fetched |
| `host` | string | The host of that URL |
| `status` | int32 | HTTP status code (0 when the fetch failed before a response) |
| `fetched_at` | int64 | When the fetch completed, Unix milliseconds |
| `content_type` | string | The response `Content-Type` |
| `body_length` | int64 | Stored response body length in bytes |
| `digest` | string | SHA-1 hex of the response body, for change detection |
| `unchanged` | bool | True when `digest` matched the seed's prior digest (stored as a revisit) |
| `warc_file` | string | Name of the WARC file holding this response |
| `warc_offset` | int64 | Byte offset of the record within that WARC file |
| `warc_length` | int64 | Byte length of the record |
| `error` | string | Failure text for a fetch that did not complete (empty on success) |
| `meta_json` | string | The seed's per-URL metadata, verbatim as a JSON object (empty when none) |

The three `warc_*` columns are a direct pointer into the archive: open `warc_file`, seek to `warc_offset`, read `warc_length` bytes, and you have the exact record without scanning.
Every text column is zstd-compressed inside the Parquet file.

### meta_json

When the seed is JSONL, any fields on a line other than `url` are kept and written into `meta_json` as a compact JSON object, so producer context survives the crawl.
A line of `{"url": "...", "source": "sitemap", "depth": 1}` lands with `meta_json` set to `{"source":"sitemap","depth":"1"}`.
Seeds that carry no extra fields leave the column empty.

### Following a row back to its bytes

The three `warc_*` columns are a direct pointer into the archive: open `warc_file`, seek to `warc_offset`, read `warc_length` bytes, and you have the exact response record's gzip member without scanning.
Inflate it and the original `request`/`response` exchange is there, headers and body intact.
Any WARC library does this for you; the index just saves the linear scan.

A failed fetch still produces a row: `status` is `0`, `error` carries the reason, and the `warc_*` columns are empty.
The index is therefore a complete record of the run, so a retry pass can select exactly the rows that need another attempt.

## Reading the index

Anything that reads Parquet reads the capture index.
The quickest look needs no other tool:

```bash
# A summary and a sample of rows
ami inspect ami-out/captures.parquet -n 20
```

For real queries, the index is an ordinary Parquet file, so DuckDB reads it directly:

```sql
-- status breakdown for a run
SELECT status, count(*) AS n
FROM 'ami-out/captures.parquet'
GROUP BY status ORDER BY n DESC;

-- the hosts that contributed the most bytes
SELECT host, sum(body_length) AS bytes, count(*) AS pages
FROM 'ami-out/captures.parquet'
WHERE error = ''
GROUP BY host ORDER BY bytes DESC LIMIT 25;

-- everything that failed, with the reason
SELECT url, error FROM 'ami-out/captures.parquet' WHERE error <> '';

-- pull a seed's passthrough metadata back out
SELECT url, json_extract_string(meta_json, '$.source') AS source
FROM 'ami-out/captures.parquet' WHERE meta_json <> '';
```

When a seed was [sharded across machines](/guides/sharding-a-run/), each machine writes its own `captures.parquet`.
Collect the directories side by side and the indexes union cleanly, because DuckDB globs them:

```sql
SELECT count(*) FROM 'runs/*/captures.parquet';
```

pandas reads it too:

```bash
python -c "import pandas; print(pandas.read_parquet('ami-out/captures.parquet').head())"
```
