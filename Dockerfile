# Consumed by GoReleaser: it copies the already cross-compiled binary out of the
# build context rather than compiling, so the image build is fast and uses the
# same static binary every other artifact ships.
#
# ami is a pure-Go network crawler with no runtime dependency beyond CA roots, so
# the image is a minimal Alpine with ca-certificates and tzdata. Point a volume
# at /out to keep the WARC and Parquet a run produces.
#
# GoReleaser builds one multi-platform image with buildx and stages each
# platform's binary under a $TARGETPLATFORM directory (e.g. linux/amd64/) in the
# build context, so the COPY line selects the right one through the automatic
# TARGETPLATFORM build arg.
FROM alpine:3.21

ARG TARGETPLATFORM

RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 ami \
 && mkdir -p /out \
 && chown ami:ami /out

COPY $TARGETPLATFORM/ami /usr/bin/ami

USER ami
WORKDIR /out

# A run writes its output under the working directory by default:
#
#   docker run -v "$PWD/out:/out" ghcr.io/tamnd/ami crawl --from lines /out/urls.txt
#
ENV HOME=/out

VOLUME ["/out"]

ENTRYPOINT ["/usr/bin/ami"]
