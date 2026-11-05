# syntax=docker/dockerfile:1

# --- build stage -----------------------------------------------------------
# Keep the tag in step with the `go` line in go.mod.
FROM golang:1.26.7 AS build
WORKDIR /src

# Module download is its own layer so source edits don't re-fetch dependencies.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# VERSION is shown on the dashboard, e.g.
#   docker build --build-arg VERSION=$(git rev-parse --short HEAD) .
ARG VERSION=dev
# Pure Go (disgo and dave-go need no cgo), so the binary is fully static.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X github.com/Sonty15/SONNIE/internal/buildinfo.Version=${VERSION}" \
      -o /out/sonnie ./cmd/bot

# --- runtime stage ---------------------------------------------------------
# distroless/static ships CA certificates (for Discord and Google Cloud TTS
# over TLS) and runs as non-root (uid 65532). Google Cloud TTS returns 48 kHz
# Ogg/Opus directly and the bot demuxes it in pure Go, so neither cgo nor
# ffmpeg is needed at runtime. The one inbound port is the HTTP server
# (DASHBOARD_ADDR, default :8080): the dashboard, the webhooks and GET
# /healthz for Kubernetes probes (the image has no shell or curl for a
# Docker HEALTHCHECK).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sonnie /sonnie
EXPOSE 8080

# Configuration comes only from environment variables (DISCORD_BOT_TOKEN, TTS_*,
# EVENT_*, PEXELS_API_KEY, UNSPLASH_ACCESS_KEY, DASHBOARD_*, WEBHOOKS;
# see .env.example). No .env file is baked in; a missing one is ignored.
ENTRYPOINT ["/sonnie"]
