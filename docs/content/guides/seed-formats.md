---
title: "Seed formats"
description: "Feed ami a list of URLs as a text file, newline JSON, a Parquet column, a sitemap, or stdin."
weight: 10
---

A seed is just a list of URLs.
ami reads four formats, and the same fetch engine runs behind all of them, so the format only decides how the URLs get in.
ami infers the format from the path, or you set it explicitly with `--from`.

## lines

One URL per line in a plain text file.
This is the default for any path that does not look like one of the other formats:

```bash
ami crawl urls.txt
ami crawl --from lines urls.txt
```

Blank lines are skipped. A single `-` reads the list from stdin, so ami sits at the end of a pipe:

```bash
grep example.com all-urls.txt | ami crawl --from lines -
```

## jsonl

Newline-delimited JSON, one object per line, each with a `url` field.
Any other fields on the object ride along as per-capture metadata and are written verbatim into the `meta_json` column of the index, so producer context (a source id, a priority, a discovered-at timestamp) survives the crawl:

```bash
ami crawl frontier.jsonl
ami crawl --from jsonl frontier.jsonl
```

```jsonl
{"url": "https://example.com/", "source": "sitemap", "depth": 0}
{"url": "https://example.com/about", "source": "link", "depth": 1}
```

## parquet

A Parquet file with a `url` column.
This is the natural fit when your URLs already live in a dataset or came out of a previous ami run:

```bash
ami crawl frontier.parquet
ami crawl --from parquet frontier.parquet
```

## sitemap

An XML sitemap or a sitemap index, fetched over HTTP.
A bare `http://` or `https://` argument is treated as a sitemap by default:

```bash
ami crawl https://www.example.com/sitemap.xml
ami crawl --from sitemap https://www.example.com/sitemap_index.xml
```

A sitemap index is followed to the child sitemaps, so a site that splits its URLs across many files still seeds in one command.

## Inference rules

When you omit `--from`, ami picks the format from the reference:

| Reference looks like | Inferred format |
|----------------------|-----------------|
| starts with `http://` or `https://` | `sitemap` |
| ends with `.jsonl` or `.ndjson` | `jsonl` |
| ends with `.parquet` or `.pq` | `parquet` |
| ends with `.xml` or `.xml.gz` | `sitemap` |
| anything else (including `-`) | `lines` |

Set `--from` whenever the extension does not match the contents, for example a `lines` list with no `.txt` suffix.
