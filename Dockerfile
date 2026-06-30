# ── Build ───────────────────────────────────────────────────────────────────
FROM golang:latest AS builder

WORKDIR /src
ENV GOTOOLCHAIN=auto

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bard .

# ── Runtime ─────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ffmpeg \
        curl \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# yt-dlp standalone binary — Nuitka-compiled, no Python required.
# yt-dlp_linux (amd64 default) and arch-specific variants are all self-contained ELFs.
ARG TARGETARCH
RUN set -eux; \
    case "${TARGETARCH}" in \
        arm64) BINARY="yt-dlp_linux_aarch64" ;; \
        arm)   BINARY="yt-dlp_linux_armv7l"  ;; \
        *)     BINARY="yt-dlp_linux"          ;; \
    esac; \
    curl -fsSL "https://github.com/yt-dlp/yt-dlp/releases/latest/download/${BINARY}" \
        -o /usr/local/bin/yt-dlp && \
    chmod a+rx /usr/local/bin/yt-dlp

COPY --from=builder /bard /usr/local/bin/bard

WORKDIR /app
ENTRYPOINT ["bard"]
