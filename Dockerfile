# syntax=docker/dockerfile:1

# --- build stage -----------------------------------------------------------
# Keep the tag in step with the `go` line in go.mod.
FROM golang:1.26.7 AS build
WORKDIR /src

# Module download is its own layer so source edits don't re-fetch dependencies.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# Pure Go (disgo and dave-go need no cgo), so the binary is fully static.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sonnie ./cmd/bot

# --- runtime stage ---------------------------------------------------------
# distroless/static ships CA certificates (for the Discord gateway over TLS)
# and runs as non-root (uid 65532). The bot only makes outbound connections:
# it listens on no port, so there is no EXPOSE and no health endpoint.
#
# NOTE: a future TTS step may need ffmpeg to convert engine output to 48 kHz
# Opus (see README "Host requirements"). distroless has no package manager, so
# that would mean switching this stage to a base with ffmpeg (or copying in a
# static ffmpeg build).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sonnie /sonnie

# Configuration comes only from environment variables (DISCORD_BOT_TOKEN, TTS_*;
# see .env.example). No .env file is baked in; a missing one is ignored.
ENTRYPOINT ["/sonnie"]
