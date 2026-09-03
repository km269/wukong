# Wukong Docker image for website cloning.
# Multi-stage build: build Go binary → package with a browser.
#
# Chrome flavor (build ARG):
#   CHROME_FLAVOR=official  (default) Debian + google-chrome-stable.
#     Official Google-signed binary: real Chrome TLS (JA3) fingerprint,
#     proprietary codecs (h264/aac), full font set. ~+130MB over slim.
#   CHROME_FLAVOR=slim      Debian + distro Chromium.
#     Smaller, but codec-stripped and version-lagged: anti-bot
#     downgrade mode (see docs/ANTIBOT_GUIDE.md §0.5).
#
# Build:
#   docker build -t wukong .
#   docker build --build-arg CHROME_FLAVOR=slim -t wukong:slim .
#
# Run clone:
#   docker run --rm -v "$PWD/out:/out" wukong apps clone https://example.com
#
# Run session:
#   docker run --rm -v "$PWD/config:/root/.config/wukong" wukong session

# ---------------------------------------------------------------------------
# Stage 1: Build the Go binary
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /wukong ./cmd/wukong

# ---------------------------------------------------------------------------
# Stage 2: Runtime with a browser
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim

ARG CHROME_FLAVOR=official

# Install the browser and a realistic font set (font fingerprinting
# reads metric noise; a near-empty font set is a container tell).
# xvfb enables headful mode in the container — headless is itself a
# detection signal, so for hardened targets run:
#   docker run ... wukong:latest xvfb-run -a --server-args="-screen 0 1920x1080x24" \
#     wukong ...   (with browser.headless=false)
RUN set -eux; \
    apt-get update; \
    if [ "$CHROME_FLAVOR" = "official" ]; then \
        apt-get install -y --no-install-recommends \
            ca-certificates curl gnupg fonts-liberation xvfb; \
        install -m 0755 -d /etc/apt/keyrings; \
        curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
            | gpg --dearmor -o /etc/apt/keyrings/google-chrome.gpg; \
        echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" \
            > /etc/apt/sources.list.d/google-chrome.list; \
        apt-get update; \
        apt-get install -y --no-install-recommends google-chrome-stable; \
    else \
        apt-get install -y --no-install-recommends \
            chromium fonts-liberation ca-certificates xvfb; \
    fi; \
    rm -rf /var/lib/apt/lists/*

# Unify the binary path so CHROME_BIN does not depend on the flavor.
RUN ln -sf "$(command -v google-chrome || command -v chromium)" /usr/local/bin/wukong-browser

# Tell wukong where to find the browser.
ENV CHROME_BIN=/usr/local/bin/wukong-browser
ENV KAGE_CHROME=/usr/local/bin/wukong-browser

# Browsers need a non-root user in Docker (--no-sandbox handled by wukong).
ENV CHROMIUM_FLAGS="--disable-dev-shm-usage"

COPY --from=builder /wukong /usr/local/bin/wukong

# Default data dir for clone output.
RUN mkdir -p /data /out
ENV HOME=/root

ENTRYPOINT ["wukong"]
CMD ["session"]
