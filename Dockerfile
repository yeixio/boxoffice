# syntax=docker/dockerfile:1
# Box Office. Build from the repository root:
#   docker build .

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY i18n/ /src/i18n/
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH TARGETVARIANT
# Official images get the project's TMDB key from a build secret (kept out of image history).
RUN --mount=type=secret,id=tmdb_key \
    TMDB_KEY="$(cat /run/secrets/tmdb_key 2>/dev/null || true)" && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM="${TARGETVARIANT#v}" go build -trimpath \
    -ldflags "-s -w -X github.com/yeixio/boxoffice/internal/version.Version=${VERSION} -X github.com/yeixio/boxoffice/internal/config.DefaultTMDBKey=${TMDB_KEY}" \
    -o /out/boxoffice ./cmd/boxoffice \
    && mkdir -p /out/data

# Debian with FFmpeg, for Problem files: remuxing disc images into MKVs
# (DVDs through FFmpeg's dvdvideo demuxer, Blu-rays through libbluray) and
# joining movies split into parts. The build stops if this FFmpeg can't
# read either. Box Office runs as the same user (65532) as before.
FROM debian:trixie-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && ffmpeg -hide_banner -demuxers | grep -q dvdvideo \
    && ffmpeg -hide_banner -protocols | grep -q bluray \
    && groupadd -g 65532 nonroot && useradd -u 65532 -g 65532 -M -d /data -s /usr/sbin/nologin nonroot
COPY --from=server /out/boxoffice /boxoffice
COPY --from=server --chown=65532:65532 /out/data /data
USER 65532:65532
ENV BOXOFFICE_DATA_DIR=/data BOXOFFICE_ADDR=:7272
VOLUME /data
EXPOSE 7272
HEALTHCHECK --interval=30s --timeout=5s CMD ["/boxoffice", "healthcheck"]
ENTRYPOINT ["/boxoffice"]
