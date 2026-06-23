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

## Homebrew (macOS)

```bash
brew install tamnd/tap/ami
```

The cask installs the prebuilt macOS binary. On Linux, use the repository or the packages below, or `go install`.

## Scoop (Windows)

```bash
scoop bucket add tamnd https://github.com/tamnd/scoop-bucket
scoop install ami
```

## Linux (apt and dnf)

A signed apt and dnf repository tracks every release, so `apt upgrade` and `dnf upgrade` keep ami current.

```bash
# Debian, Ubuntu
curl -fsSL https://tamnd.github.io/linux-repo/gpg.key \
  | sudo gpg --dearmor -o /usr/share/keyrings/tamnd.gpg
echo "deb [signed-by=/usr/share/keyrings/tamnd.gpg] https://tamnd.github.io/linux-repo/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/tamnd.list
sudo apt update && sudo apt install ami

# Fedora, RHEL
sudo dnf config-manager --add-repo https://tamnd.github.io/linux-repo/dnf/tamnd.repo
sudo dnf install ami
```

## Release archives and Linux packages

Every [release](https://github.com/tamnd/ami/releases) attaches `tar.gz` archives (and a `.zip` for Windows) for Linux, macOS, Windows, and FreeBSD, plus `.deb`, `.rpm`, and `.apk` packages and a `checksums.txt` with a cosign signature.
Download the one for your platform, extract `ami`, and put it on your `PATH`.
To install a package directly without the repository above:

```bash
# Debian/Ubuntu
sudo dpkg -i ami_*_amd64.deb

# Fedora/RHEL
sudo rpm -i ami-*.x86_64.rpm
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
