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
# distroless/static ships CA certificates (for Discord and Google Cloud TTS
# over TLS) and runs as non-root (uid 65532). Google Cloud TTS returns 48 kHz
# Ogg/Opus directly and the bot demuxes it in pure Go, so neither cgo nor
# ffmpeg is needed at runtime. The bot only makes outbound connections: it
# listens on no port, so there is no EXPOSE and no health endpoint.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sonnie /sonnie

# Configuration comes only from environment variables (DISCORD_BOT_TOKEN, TTS_*;
# see .env.example). No .env file is baked in; a missing one is ignored.
ENTRYPOINT ["/sonnie"]
