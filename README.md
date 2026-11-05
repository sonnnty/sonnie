# SONNIE

A general-purpose Discord bot written in Go, grown one command at a time. It builds into a static, CGO-free binary on distroless base and runs as a single replica.

---

## Features

- **อ่านแชทออกเสียง (Read-Aloud / TTS)**: Natural Thai speech powered by Gemini-TTS (`gemini-2.5-flash-tts`) over gRPC streaming. Assigns distinct male voices to each participant in the voice channel, supports voice choice autocomplete (`voice:`), automatically cleans up messages 8 seconds after reading, and enforces a ฿300/month budget cap with alerts.
- **จัดการอีเวนต์ (/event)**: Create and manage Discord Scheduled Events with a Thai modal form (`/event create`). Automatically discovers high-quality covers from Brave Web Search, Unsplash, Pexels, and Openverse with a cover picker (`🔄 เปลี่ยนรูป`), 15-minute reminders, and automatic start/completion.
- **แดชบอร์ดตรวจสอบสถานะ (Web Dashboard)**: Read-only monitoring dashboard at [https://sonnie.tapb.ch](https://sonnie.tapb.ch) behind Discord OAuth2 login. Shows real-time bot health, active TTS sessions, monthly Google Cloud spend, upcoming events, webhook logs, and process ring buffer logs with secrets redacted.
- **คู่มือผู้ใช้ในตัว (/docs)**: Full Thai user manual embedded directly in the dashboard at `/docs` (rendered server-side from Markdown with table of contents and anchor navigation).
- **Incoming Webhooks**: Secure endpoint `POST /hooks/{name}` to route notifications from GitHub Actions, Grafana, or scripts into Discord channels. Supports simple embeds, Discord-compatible payloads, and plain text without pinging users (`allowed_mentions: {parse: []}`).

---

## Documentation

- **คู่มือผู้ใช้ (User Manual)**:
  - บนแดชบอร์ด: [https://sonnie.tapb.ch/docs](https://sonnie.tapb.ch/docs)
  - ไฟล์ Markdown ในคลังโค้ด: [`docs/manual/`](docs/manual/)
- **คู่มือผู้ดูแลระบบและคอนฟิก (Operations Reference)**:
  - ข้อมูลสถาปัตยกรรม, ตัวแปรสภาพแวดล้อมทั้งหมด, การกู้คืนสถานะ, และรายละเอียดเทคนิค: [`docs/operations.md`](docs/operations.md)
- **Voice Transport & DAVE Encryption**:
  - รายละเอียดเอนจินเสียงและการเข้ารหัส E2EE: [`docs/voice-transport.md`](docs/voice-transport.md)

---

## Quick Start

1. Copy `.env.example` to `.env` and fill in `DISCORD_BOT_TOKEN`:
   ```bash
   cp .env.example .env
   ```
2. Run the bot:
   ```bash
   go run ./cmd/bot
   ```
3. In a Discord server where SONNIE is invited, type `/ping` — it should reply `pong`.

---

## Essential Configuration

The bot is configured entirely via environment variables. For the full list of options, see [`docs/operations.md`](docs/operations.md).

| Variable | Default | Description |
|---|---|---|
| `DISCORD_BOT_TOKEN` | *(required)* | Bot token from Discord Developer Portal (**secret**) |
| `TTS_ENABLED` | `false` | Enable read-aloud TTS (requires Message Content privileged intent) |
| `GOOGLE_APPLICATION_CREDENTIALS` | *(empty)* | Path to GCP Service Account JSON key (for Gemini-TTS and keywords) |
| `TTS_MODEL` | *(empty)* | Set to `gemini-2.5-flash-tts` for natural Gemini voices |
| `TTS_MONTHLY_BUDGET_THB` | `300` | Monthly money cap in THB; reading pauses when reached |
| `TTS_ALERT_CHANNEL_ID` | *(empty)* | Discord text channel for budget alerts and state persistence |
| `EVENT_CHANNEL_ID` | `TTS_ALERT_CHANNEL_ID` | Discord channel for 15-minute event reminders |
| `BRAVE_SEARCH_API_KEY` | *(empty)* | Optional Brave Search API key for web cover image search |
| `DASHBOARD_ADDR` | `:8080` | Listen address for HTTP server (dashboard, `/healthz`, webhooks) |
| `DASHBOARD_PUBLIC_URL` | *(empty)* | Browser URL, e.g. `https://sonnie.tapb.ch` |
| `DISCORD_CLIENT_SECRET` | *(empty)* | OAuth2 client secret for dashboard login (**secret**) |
| `DASHBOARD_SESSION_KEY` | *(empty)* | ≥ 32 random bytes for AES-256-GCM session cookie (**secret**) |
| `DASHBOARD_ROLE_IDS` | *(empty)* | Role IDs in `DASHBOARD_GUILD_ID` allowed to view dashboard (e.g. TABP role) |
| `WEBHOOKS` | *(empty)* | `name=token@channelID,...` incoming webhook definitions (**secret**) |

---

## Build & Run with Docker

The container runs as non-root on distroless base:

```bash
docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t sonnie .
docker run --rm --env-file .env -p 8080:8080 sonnie
```

---

## Adding Commands & Features

- **New slash commands**: Add a file under `internal/commands/` calling `register(...)` in `init()` (see `ping.go` or `event.go`).
- **Dashboard panels & documentation rule**: Every feature must register a status panel in `internal/panels/` and update `docs/manual/` and `docs/operations.md` in the same change (see [`CLAUDE.md`](CLAUDE.md)).
