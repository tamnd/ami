---
title: "Installation"
description: "Install ami from Go, Homebrew, a release archive, a Linux package, or the container image."
weight: 20
---

ami is a single pure-Go binary with no runtime dependency beyond CA roots.
Pick whichever channel suits you.

## Go

```bash
go install github.com/tamnd/ami/cmd/ami@latest
```

## Homebrew

```bash
brew install tamnd/tap/ami
```

## Release archives and Linux packages

Every [release](https://github.com/tamnd/ami/releases) attaches `tar.gz` archives (and a `.zip` for Windows) for Linux, macOS, Windows, and FreeBSD, plus `.deb`, `.rpm`, and `.apk` packages and a `checksums.txt` with a cosign signature.
Download the one for your platform, extract `ami`, and put it on your `PATH`.

```bash
# Debian/Ubuntu
sudo dpkg -i ami_*_linux_amd64.deb

# Fedora/RHEL
sudo rpm -i ami_*_linux_amd64.rpm
```

## Container

The image is a minimal Alpine with CA roots, so it needs nothing else.
Mount a volume at `/out` to keep the WARC and Parquet a run produces:

```bash
docker run -v "$PWD/out:/out" ghcr.io/tamnd/ami crawl --from lines /out/urls.txt
```

## Shell completion

Completion scripts ship in the binary:

```bash
ami completion bash|zsh|fish|powershell
```

Next: [the quick start](/getting-started/quick-start/).
