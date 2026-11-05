# SONNIE Operations & Maintainer Reference

This document contains operational, architectural, and configuration details for maintainers and operators of SONNIE.
For user-facing guides in Thai, see the manual inside the dashboard at [https://sonnie.tapb.ch/docs](https://sonnie.tapb.ch/docs) or files in [`docs/manual/`](manual/).

---

## Architecture Overview

- **Binary & Runtime**: Static, CGO-free Go binary running on `gcr.io/distroless/static:nonroot`. Single replica deployment.
- **Entry point**: `cmd/bot`, wiring in `internal/bot`, configuration parsed strictly from environment variables in `internal/config`.
- **Inbound Ports**: Single HTTP server on `:8080` (or `DASHBOARD_ADDR`) serving:
  - `/healthz`: Kubernetes liveness and readiness probe (reports 200 when Discord gateway is connected, 503 otherwise).
  - `/`: Read-only monitoring dashboard behind Discord OAuth2.
  - `/docs` and `/docs/{slug}`: Embedded user manual rendered server-side.
  - `POST /hooks/{name}`: Generic authenticated incoming webhooks.
- **Outbound Egress**:
  - Discord Gateway & REST: WebSocket & HTTPS (`discord.com`).
  - Google Cloud Text-to-Speech: gRPC over HTTP/2 and HTTPS (`texttospeech.googleapis.com:443`).
  - Vertex AI / Gemini: HTTPS (`aiplatform.googleapis.com`).
  - Cover search APIs: Brave (`api.search.brave.com`), Unsplash (`api.unsplash.com`), Pexels (`api.pexels.com`), Openverse (`api.openverse.org`), and image CDN hosts.

---

## Complete Environment Variable Reference

### Core & Discord
| Variable | Default | Description |
|---|---|---|
| `DISCORD_BOT_TOKEN` | *(required)* | Bot token from the Discord Developer Portal (**secret**) |

### Read-Aloud / TTS (`/tts`)
| Variable | Default | Description |
|---|---|---|
| `TTS_ENABLED` | `false` | Enable read-aloud feature (requires Message Content privileged intent) |
| `GOOGLE_APPLICATION_CREDENTIALS` | *(empty)* | Path to GCP Service Account JSON key (required for Gemini-TTS; takes precedence over API key) |
| `GOOGLE_TTS_API_KEY` | *(empty)* | Google Cloud TTS API key (used for legacy Chirp/Neural2 voices) |
| `TTS_MODEL` | *(empty)* | Gemini-TTS model (e.g. `gemini-2.5-flash-tts`). Empty = Chirp/Neural2 |
| `TTS_VOICE` | *(empty)* | Default speaker voice name (e.g. `Charon` for Gemini, or `th-TH-Neural2-C`) |
| `TTS_VOICE_POOL` | 16 Gemini male voices | Comma-separated pool of speaker voices assigned per participant |
| `TTS_LANG` | `th` | Language code passed to speech engine (`th`, `en`, ...) |
| `TTS_PROMPT` | *(empty)* | Natural-language style instruction prompt sent to Gemini models |
| `TTS_STREAMING` | `true` with Gemini | Stream gRPC `StreamingSynthesize` and start playback on first chunk |
| `TTS_MONTHLY_BUDGET_THB` | `300` | Monthly money budget in THB; reading pauses at 100%. `0` = no cap |
| `TTS_USD_TO_THB` | `33` | Exchange rate used to convert USD Gemini pricing to THB |
| `TTS_MONTHLY_CHAR_LIMIT` | `1000000` | Legacy Chirp character limit (ignored when `TTS_MODEL` is set) |
| `TTS_MAX_CHARS` | `200` | Maximum characters synthesized per message |
| `TTS_QUEUE_SIZE` | `20` | Max messages in queue per server; drops oldest message when full |
| `TTS_DELETE_AFTER` | `8s` | Delay before deleting participant message after playback |
| `TTS_SPEAK_AUTHOR` | `true` | Speak author display name before reading message content |
| `TTS_ALERT_CHANNEL_ID` | *(empty)* | Discord text channel ID for budget alerts & usage marker persistence |

### Server Events (`/event`)
| Variable | Default | Description |
|---|---|---|
| `EVENT_CHANNEL_ID` | `TTS_ALERT_CHANNEL_ID` | Channel for upcoming event reminders and Brave quota alerts |
| `EVENT_REMIND_BEFORE` | `15m` | Reminder interval before event start (`0` to disable) |
| `EVENT_AUTO_START` | `true` | Auto start and end scheduled events created in Discord |
| `BRAVE_SEARCH_API_KEY` | *(empty)* | Brave Search API key for web image search (**secret**) |
| `UNSPLASH_ACCESS_KEY` | *(empty)* | Unsplash Access Key for high-res photography (**secret**) |
| `PEXELS_API_KEY` | *(empty)* | Pexels API Key (**secret**) |

### Dashboard, Auth & Webhooks
| Variable | Default | Description |
|---|---|---|
| `DASHBOARD_ADDR` | `:8080` | HTTP listen address; empty or `off` disables HTTP server |
| `DASHBOARD_PUBLIC_URL` | *(empty)* | Public browser URL, e.g. `https://sonnie.tapb.ch` |
| `DISCORD_CLIENT_ID` | *(bot app ID)* | OAuth2 Client ID |
| `DISCORD_CLIENT_SECRET` | *(empty)* | OAuth2 Client Secret (**secret**) |
| `DASHBOARD_SESSION_KEY` | *(empty)* | ≥ 32 random bytes for AES-256-GCM session cookie (**secret**) |
| `DASHBOARD_GUILD_ID` | *(empty)* | Server ID restricted for dashboard access |
| `DASHBOARD_ROLE_IDS` | *(empty)* | Comma-separated role IDs in `DASHBOARD_GUILD_ID` allowed access |
| `DASHBOARD_ALLOWED_USERS` | *(empty)* | Comma-separated user IDs unconditionally allowed access |
| `WEBHOOKS` | *(empty)* | Comma-separated `name=token@channelID` entries (**secret**) |

---

## Read-Aloud & Voice Operations

### Gemini-TTS & gRPC Streaming
When `TTS_MODEL=gemini-2.5-flash-tts` and `GOOGLE_APPLICATION_CREDENTIALS` are configured:
- Synthesizes audio using gRPC `StreamingSynthesize` over HTTP/2 (`texttospeech.googleapis.com:443`).
- Audio arrives in incremental Ogg/Opus chunks (48 kHz). Frames are demuxed in pure Go without `ffmpeg` and paced at 20 ms.
- Playback starts immediately on the first delivered audio chunk (cutting latency from ~4s to ~400ms).
- If streaming fails before any audio is received, the bot logs a fallback notice and retries once with unary REST `v1/text:synthesize`.
- Latency metrics are logged per message:
  ```
  tts: timing path=stream first_audio=412ms total=2.31s bytes=9123
  ```

### Voice Transport & DAVE E2EE
- Voice connection is managed via disgo voice gateway v8.
- Transport encryption uses `aead_aes256_gcm_rtpsize` or `aead_xchacha20_poly1305_rtpsize`.
- End-to-end encryption (DAVE MLS protocol, required by Discord since March 2026) is handled in pure Go by `dave-go`.
- Frames are held until the DAVE key exchange handshake completes.

### Budget & Cost Accounting
- Gemini 2.5 Flash TTS pricing:
  - Text input: $0.50 per 1M tokens.
  - Audio output: $10.00 per 1M tokens (25 audio tokens per second).
  - Exchange rate: `TTS_USD_TO_THB` (default 33).
- Month boundary follows Google billing zone (**US/Pacific**), resetting on the 1st of each month at 00:00 US/Pacific.
- Threshold alerts fire at 50%, 80%, and 100% of `TTS_MONTHLY_BUDGET_THB` in `TTS_ALERT_CHANNEL_ID`.
- At 100%, reading pauses until the next calendar month begins.

### State Persistence & Crash Recovery
The bot runs without persistent disks. State is maintained across restarts using Discord embed URL markers:
- **TTS Session State (v2 Marker)**:
  - Hidden in the session card embed URL (`https://sonnie.invalid/tts#<base64url>`).
  - Serializes active participants, text channel bindings, join timestamps, and voice assignments.
  - On startup, the bot inspects the last 300 messages of text channels, finds unfinished cards, finishes pending clean-ups, and turns cards into session summaries.
- **Usage Counter State**:
  - Hidden in the alert channel usage message URL (`https://sonnie.invalid/usage#<base64url>`).
  - Stores current month, unit (`thb_micro` or `chars`), spent value, audio duration, and alerted thresholds.
  - Edited at most once per minute and on graceful shutdown. Restored on startup by scanning the alert channel's last 100 messages.

---

## Server Events & Cover Image Pipeline

### Modal Form Handling
`/event create` without arguments opens a Discord modal. Because discordgo v0.29 cannot unmarshal Label components (component type 18), SONNIE parses the submitted modal payload from the raw gateway interaction event.

### Cover Finder Pipeline
1. **Gemini Keywords Translation**: Thai event names are translated to 2–4 English search keywords via Vertex AI (`gemini-2.5-flash-lite`, using `GOOGLE_APPLICATION_CREDENTIALS`). Times out after 3 seconds with fallback to the raw event name.
2. **Provider Priority**:
   - `Brave Search API`: Unlicensed web images.
   - `Unsplash API`: Free high-resolution photos.
   - `Pexels API`: Stock photos.
   - `Openverse`: Free-culture CC-licensed media (CC0, PDM, CC BY, CC BY-SA).
3. **Brave Quota Pause**:
   - If Brave returns HTTP 402 or 429 with monthly quota exhausted, Brave search is paused until the reset timestamp (from `X-RateLimit-Reset` or next month 1st UTC).
   - An alert is posted once to `EVENT_CHANNEL_ID`.
   - The cover pipeline automatically falls back to Unsplash/Openverse without failing user requests.
4. **Image Processing**:
   - Pictures are cropped to 5:2 ratio centered, scaled to 800×320 JPEG using pure Go (`golang.org/x/image`), staying under 10 MB.
   - Attribution is automatically formatted and appended to the event description.

---

## Dashboard & Security Operations

### Discord OAuth2 Setup
1. In Discord Developer Portal -> **OAuth2**:
   - Set Redirect URI to `<DASHBOARD_PUBLIC_URL>/auth/callback`.
   - Copy Client Secret into `DISCORD_CLIENT_SECRET`.
2. Generate session key:
   ```bash
   openssl rand -base64 48
   ```
   Set into `DASHBOARD_SESSION_KEY`.

### Access Authorization
- Access decisions are cached in-memory for up to 10 minutes (`DefaultAuthCacheTTL`).
- If Discord API experiences temporary outages, the last cached decision stands in for up to 1 hour (`staleAuthLimit`).
- Session cookies are sealed with AES-256-GCM, set with `HttpOnly`, `SameSite=Lax`, and `Secure` when public URL is HTTPS.

### Webhook Specifications
- Call with Header `Authorization: Bearer <TOKEN>` or `X-Sonnie-Token: <TOKEN>`.
- Both unknown hook names and invalid tokens return HTTP 401 (constant-time comparison).
- Mentions are sanitized with `allowed_mentions: {parse: []}`.
- Max payload size: 64 KB (HTTP 413).
- Rate limit: 30 requests per minute per hook (HTTP 429 with `Retry-After`).
- Process log ring buffer holds the last 300 lines with secrets automatically masked (`[redacted]`).
