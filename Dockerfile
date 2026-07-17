FROM --platform=$BUILDPLATFORM ghcr.io/crazy-max/osxcross:14.5-debian AS osxcross

########################################################################################################################
### Build xx (original image: tonistiigi/xx)
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/alpine:3.20 AS xx-build

# v1.9.0
ENV XX_VERSION=a5592eab7a57895e8d385394ff12241bc65ecd50

RUN apk add -U --no-cache git
RUN git clone https://github.com/tonistiigi/xx && \
    cd xx && \
    git checkout ${XX_VERSION} && \
    mkdir -p /out && \
    cp src/xx-* /out/

RUN cd /out && \
    ln -s xx-cc /out/xx-clang && \
    ln -s xx-cc /out/xx-clang++ && \
    ln -s xx-cc /out/xx-c++ && \
    ln -s xx-apt /out/xx-apt-get

# xx mimics the original tonistiigi/xx image
FROM scratch AS xx
COPY --from=xx-build /out/ /usr/bin/

########################################################################################################################
### Build Navidrome UI
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/node:lts-alpine AS ui
WORKDIR /app

# Install node dependencies
COPY ui/package.json ui/package-lock.json ./
COPY ui/bin/ ./bin/
RUN npm ci

# Build bundle
COPY ui/ ./
RUN npm run build -- --outDir=/build

FROM scratch AS ui-bundle
COPY --from=ui /build /build

########################################################################################################################
### Build Navidrome binary for Docker image (dynamic musl, enables native libwebp via dlopen)
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26-alpine AS build-alpine
COPY --from=xx / /

ARG TARGETPLATFORM

RUN apk add --no-cache clang lld file git
RUN xx-apk add --no-cache gcc musl-dev zlib-dev
RUN xx-verify --setup

WORKDIR /workspace

RUN --mount=type=bind,source=. \
    --mount=type=cache,target=/root/.cache \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

ARG GIT_SHA
ARG GIT_TAG

RUN --mount=type=bind,source=. \
    --mount=from=ui,source=/build,target=./ui/build,ro \
    --mount=type=cache,target=/root/.cache \
    --mount=type=cache,target=/go/pkg/mod <<EOT
    set -e
    xx-go --wrap
    export CGO_ENABLED=1
    BUILD_TAGS=$(./release/build-tags.sh)
    # -latomic is required on 32-bit arm (arm/v6, arm/v7) so SQLite's 64-bit atomics resolve.
    go build -tags="${BUILD_TAGS}" -ldflags="-w -s \
        -linkmode=external -extldflags '-latomic' \
        -X github.com/navidrome/navidrome/consts.gitSha=${GIT_SHA} \
        -X github.com/navidrome/navidrome/consts.gitTag=${GIT_TAG}" \
        -o /out/navidrome .
    # Fail the build if native libwebp (purego) leaked into a 32-bit binary (issue #5738).
    ./release/verify-binary.sh /out/navidrome
    # Fail the build if the binary is accidentally statically linked: dlopen (and
    # therefore native libwebp detection) only works with a dynamic interpreter.
    file /out/navidrome | grep -q "dynamically linked" || { echo "ERROR: /out/navidrome is not dynamically linked"; file /out/navidrome; exit 1; }
EOT

########################################################################################################################
### Build Navidrome binary for standalone distribution (static glibc, cross-compiled)
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26-trixie AS base
RUN apt-get update && apt-get install -y clang lld
COPY --from=xx / /
WORKDIR /workspace

FROM --platform=$BUILDPLATFORM base AS build

# Install build dependencies for the target platform
ARG TARGETPLATFORM

RUN xx-apt install -y binutils gcc g++ libc6-dev zlib1g-dev
RUN xx-verify --setup

RUN --mount=type=bind,source=. \
    --mount=type=cache,target=/root/.cache \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

ARG GIT_SHA
ARG GIT_TAG

RUN --mount=type=bind,source=. \
    --mount=from=ui,source=/build,target=./ui/build,ro \
    --mount=from=osxcross,src=/osxcross/SDK,target=/xx-sdk,ro \
    --mount=type=cache,target=/root/.cache \
    --mount=type=cache,target=/go/pkg/mod <<EOT
    set -e

    # Setup CGO cross-compilation environment
    xx-go --wrap
    export CGO_ENABLED=1
    cat "$(go env GOENV)" 2>/dev/null || true

    # Only Darwin (macOS) requires clang (default), Windows requires gcc, everything else can use any compiler.
    # So let's use gcc for everything except Darwin.
    if [ "$(xx-info os)" != "darwin" ]; then
        export CC=$(xx-info)-gcc
        export CXX=$(xx-info)-g++
        export LD_EXTRA="-extldflags '-static -latomic'"
    fi
    # GNU ld corrupts the R_ARM_IRELATIVE addends of libatomic's ifunc resolvers
    # (wrong address, Thumb bit lost) once .text outgrows the 16MB Thumb branch
    # range, making static arm binaries jump to garbage inside glibc's ifunc
    # resolution and crash before main() (issue #5738). Link 32-bit arm with LLD,
    # which emits correct addends.
    if [ "$(xx-info arch)" = "arm" ]; then
        export LD_EXTRA="-extldflags '-static -latomic -fuse-ld=lld'"
    fi
    if [ "$(xx-info os)" = "windows" ]; then
        export EXT=".exe"
    fi

    BUILD_TAGS=$(./release/build-tags.sh)
    go build -tags="${BUILD_TAGS}" -ldflags="${LD_EXTRA} -w -s \
        -X github.com/navidrome/navidrome/consts.gitSha=${GIT_SHA} \
        -X github.com/navidrome/navidrome/consts.gitTag=${GIT_TAG}" \
        -o /out/navidrome${EXT} .
    # Fail the build if native libwebp (purego) leaked into a 32-bit binary (issue #5738).
    ./release/verify-binary.sh /out/navidrome*
EOT

# Verify if the binary was built for the correct platform and it is statically linked
RUN xx-verify --static /out/navidrome*

FROM scratch AS binary
COPY --from=build /out /

########################################################################################################################
### Build Final Image
#
# Debian (glibc) instead of upstream Navidrome's Alpine base: the "Youtube Download"
# feature's onetagger-cli dependency only ships a glibc-linked binary (no musl build),
# and Alpine's gcompat glibc shim is incomplete (fails to resolve __res_init) so it
# can't run onetagger-cli even with the compat package installed. Since this stage
# needs glibc anyway, it reuses the static-glibc navidrome binary the "build" stage
# below already produces for standalone distribution, rather than build-alpine's
# musl one.
FROM public.ecr.aws/docker/library/debian:bookworm-slim AS final
LABEL maintainer="deluan@navidrome.org"
LABEL org.opencontainers.image.source="https://github.com/navidrome/navidrome"

ARG TARGETARCH
ARG YTDLP_VERSION=2026.07.04
ARG ONETAGGER_VERSION=1.7.0

# Install runtime dependencies:
# - ffmpeg/mpv/sqlite3: same as upstream's Alpine image
# - libwebp7 + symlink: enables native WebP encoding via purego/dlopen
# - libasound2: required by onetagger-cli (links libasound.so.2)
# - yt-dlp: official standalone binary (curl'd below), not the stale apt package
# - onetagger-cli: auto-tags files downloaded via the Youtube Download feature;
#   upstream only publishes an amd64 build, so it's skipped (with a warning) on
#   other architectures rather than failing the whole image build
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg mpv sqlite3 libwebp7 libasound2 ca-certificates curl && \
    target=$(ls /usr/lib/*/libwebp.so.* 2>/dev/null | head -1) && \
    [ -n "$target" ] && ln -sf "$target" "$(dirname "$target")/libwebp.so"; \
    case "${TARGETARCH}" in \
        amd64) YTDLP_ASSET=yt-dlp_linux ;; \
        arm64) YTDLP_ASSET=yt-dlp_linux_aarch64 ;; \
        *) echo "ERROR: no yt-dlp binary published for ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -sL -o /usr/local/bin/yt-dlp \
        "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/${YTDLP_ASSET}" && \
    chmod +x /usr/local/bin/yt-dlp && \
    if [ "${TARGETARCH}" = "amd64" ]; then \
        curl -sL "https://github.com/Marekkon5/onetagger/releases/download/${ONETAGGER_VERSION}/OneTagger-linux-cli.tar.gz" \
            | tar xz -C /usr/local/bin && \
        chmod +x /usr/local/bin/onetagger-cli; \
    else \
        echo "WARNING: no onetagger-cli build published for ${TARGETARCH}; the Youtube Download tagging step will be unavailable." >&2; \
    fi && \
    apt-get purge -y curl && apt-get autoremove -y && \
    rm -rf /var/lib/apt/lists/*

# Copy navidrome binary (static glibc build, compatible with this Debian-based image)
COPY --from=build /out/navidrome /app/

VOLUME ["/data", "/music"]
ENV ND_MUSICFOLDER=/music
ENV ND_DATAFOLDER=/data
ENV ND_CONFIGFILE=/data/navidrome.toml
# onetagger-cli writes its log/settings to $HOME/.config/OneTagger. The container
# typically runs as a numeric UID with no /etc/passwd entry (see docker-compose.yml's
# `user:`), which makes Docker default HOME to "/" — not writable by that UID. /tmp is
# writable by any user, and OneTagger doesn't need these files to persist since every
# invocation passes its options via CLI flags.
ENV HOME=/tmp
ENV ND_PORT=4533
RUN touch /.nddockerenv

EXPOSE ${ND_PORT}
WORKDIR /app
ENV PATH="/app:${PATH}"

ENTRYPOINT ["/app/navidrome"]

