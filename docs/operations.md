# SONNIE operations and maintainer reference

For the people who run and maintain SONNIE. Users read the Thai manual inside
the dashboard at [https://sonnie.tapb.ch/docs](https://sonnie.tapb.ch/docs)
(files in [`docs/manual/`](manual/)). The README is the short overview.

| Document | Covers |
|---|---|
| this file | architecture, environment variables, run with Docker, dashboard login and access, webhooks, logs, releases |
| [tts-operations.md](tts-operations.md) | `/tts` setup (Discord intent and permissions, invite URL, Google API key, Gemini service account), session behaviour, clean-up and recovery, Gemini streaming |
| [tts-cost-and-persistence.md](tts-cost-and-persistence.md) | prices, the monthly meter, alerts, the markers that keep state in Discord |
| [events-operations.md](events-operations.md) | `/event`: form, time formats, covers and licensing, Brave, reminders, restart behaviour |
| [events-concept.md](events-concept.md) | original design notes for events |
| [voice-transport.md](voice-transport.md) | voice gateway, AEAD and DAVE encryption |

## Architecture

- **Binary and runtime:** a static, CGO-free Go binary on
  `gcr.io/distroless/static-debian12:nonroot`, one replica. Entry point
  `cmd/bot`, wiring in `internal/bot`, settings from environment variables
  only in `internal/config`. There is no database and no persistent volume:
  state that must survive a restart lives in Discord messages (see
  [tts-cost-and-persistence.md](tts-cost-and-persistence.md)).
- **Inbound:** one HTTP server on `:8080` (`DASHBOARD_ADDR`):
  - `GET /healthz`: `ok` (200) while the Discord gateway is connected, 503
    otherwise. No login. For Kubernetes liveness/readiness probes (the image
    has no shell or curl for a Docker `HEALTHCHECK`).
  - `GET /`: the read-only dashboard behind Discord OAuth2.
  - `GET /docs`, `GET /docs/{slug}`: the user manual, rendered from the
    embedded Markdown, behind the same login and access rule.
  - `POST /hooks/{name}`: incoming webhooks.
  - `/auth/login`, `/auth/callback`, `/auth/logout`, `/api/panels`, `/static/`.
- **Outbound:** Discord gateway and REST (`discord.com`); Google Cloud
  Text-to-Speech over HTTPS and gRPC/HTTP/2 (`texttospeech.googleapis.com:443`);
  Vertex AI / Gemini (`aiplatform.googleapis.com`); cover libraries
  (`api.search.brave.com`, `api.unsplash.com`, `api.pexels.com`,
  `api.openverse.org`) and the hosts the pictures live on.

## Run

```bash
cp .env.example .env     # fill in DISCORD_BOT_TOKEN
go run ./cmd/bot
```

In a server the bot has been invited to, `/ping` should reply `pong`.

### Docker

The image is a static Go binary on a non-root distroless base. It reads its
configuration from environment variables only. Its one inbound port is the
HTTP server on 8080.

```bash
docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t sonnie .
docker run --rm --env-file .env -p 8080:8080 sonnie
```

`.env` is excluded from the build context (see `.dockerignore`), so the token
is never baked into the image — pass it at run time with `--env-file` or `-e`.
Without `DISCORD_BOT_TOKEN` the container exits immediately with a config
error. The dashboard works over plain HTTP on a LAN address (for example a
Kubernetes NodePort); behind HTTPS set `DASHBOARD_PUBLIC_URL` to the `https`
address so the session cookie is marked `Secure`. Kubernetes manifests live in
a separate repository (homelab-ops), not here.

### Releases

Releases use semantic versions. A release is a git tag `vX.Y.Z` on `main` and
an image `rg.tapb.ch/sonnie:X.Y.Z`, built with `--build-arg VERSION=X.Y.Z`
(the version is shown on the dashboard's bot panel; without the argument it
shows the git revision or `dev`):

```bash
git tag vX.Y.Z
docker build --build-arg VERSION=X.Y.Z -t rg.tapb.ch/sonnie:X.Y.Z .
```

Bump **MINOR** for a feature, **PATCH** for a fix, **MAJOR** for a breaking
configuration change. `v1.0.0` is the first versioned release (the dashboard).

## Environment variables

The bot is configured only through environment variables. Values marked
**secret** are redacted from the dashboard's log view. `.env.example` lists
them with comments.

### Core

| Variable | Default | Description |
|---|---|---|
| `DISCORD_BOT_TOKEN` | *(required)* | Bot token from the Discord Developer Portal (**secret**) |

### Read-aloud (`/tts`)

See [tts-operations.md](tts-operations.md) and
[tts-cost-and-persistence.md](tts-cost-and-persistence.md).

| Variable | Default | Description |
|---|---|---|
| `TTS_ENABLED` | `false` | Turns the feature on and requests the privileged Message Content intent (must be enabled in the Developer Portal, otherwise the gateway refuses with 4014) |
| `GOOGLE_TTS_API_KEY` | *(empty)* | Google Cloud Text-to-Speech API key (Chirp 3: HD / Neural2 voices only; **secret**) |
| `GOOGLE_APPLICATION_CREDENTIALS` | *(empty)* | Service account JSON key file; required for Gemini-TTS (API keys are rejected for Gemini), wins over the API key; also used for the Thai → English cover keywords |
| `TTS_MODEL` | *(empty)* | Gemini-TTS model, e.g. `gemini-2.5-flash-tts`. Empty = Chirp/Neural2 |
| `TTS_LANG` | `th` | Language passed to the speech engine (`th`, `en`, `ja`, …) |
| `TTS_VOICE` | *(empty)* | Fallback voice when no per-participant voice is attached: a bare speaker name with a Gemini model (default `Charon`), or a full name such as `th-TH-Chirp3-HD-Achird`; defaults to `th-TH-Neural2-C` / `en-US-Neural2-F` |
| `TTS_VOICE_POOL` | 16 Gemini male voices | Comma-separated pool of voices handed out per participant; bare names map to `<lang>-Chirp3-HD-<Name>` when `TTS_MODEL` is empty |
| `TTS_PROMPT` | *(empty)* | Natural-language style instruction (Gemini models only; billed as input on every message) |
| `TTS_STREAMING` | `true` with a Gemini model, else `false` | Gemini only: gRPC `StreamingSynthesize`, speech starts at the first audio chunk. Needs `GOOGLE_APPLICATION_CREDENTIALS` |
| `TTS_MONTHLY_BUDGET_THB` | `300` | Gemini only: money cap in baht per US/Pacific month; reading pauses at 100%. `0` = no cap |
| `TTS_USD_TO_THB` | `33` | Gemini only: exchange rate used to turn the USD price into baht |
| `TTS_MONTHLY_CHAR_LIMIT` | `1000000` | Chirp/Neural2 only (ignored when `TTS_MODEL` is set): characters per US/Pacific month. `0` = no cap |
| `TTS_MAX_CHARS` | `200` | Maximum characters read per message (longer text is cut, not skipped) |
| `TTS_QUEUE_SIZE` | `20` | Max messages waiting per server; the oldest is dropped when full |
| `TTS_DELETE_AFTER` | `8s` | Delete each participant message this long after it was read aloud or skipped. `0` = only clean up when the session ends |
| `TTS_SPEAK_AUTHOR` | `true` | Say the author's name before each message |
| `TTS_ALERT_CHANNEL_ID` | *(empty)* | Channel for usage alerts (50/80/100%) and the persisted usage counter. Empty = counter in memory only |

### Server events (`/event`)

See [events-operations.md](events-operations.md).

| Variable | Default | Description |
|---|---|---|
| `EVENT_CHANNEL_ID` | `TTS_ALERT_CHANNEL_ID` | Channel for event reminders and the Brave quota notice. Separate from the usage alert channel; falls back to it when unset. No channel = no reminders |
| `EVENT_REMIND_BEFORE` | `15m` | Reminder interval before the start (`0` = off) |
| `EVENT_AUTO_START` | `true` | Also start/end events created by people in Discord (the bot's own are always handled) |
| `BRAVE_SEARCH_API_KEY` | *(empty)* | Brave web image search — unlicensed images, private server only (**secret**) |
| `UNSPLASH_ACCESS_KEY` | *(empty)* | Unsplash access key (**secret**) |
| `PEXELS_API_KEY` | *(empty)* | Pexels API key (**secret**) |

### Dashboard, login and webhooks

| Variable | Default | Description |
|---|---|---|
| `DASHBOARD_ADDR` | `:8080` | HTTP listen address; empty or `off` turns the whole HTTP server off (dashboard, `/healthz`, webhooks) |
| `DASHBOARD_PUBLIC_URL` | *(empty)* | Browser-facing URL, e.g. `https://sonnie.tapb.ch`; the redirect URI is this + `/auth/callback` |
| `DISCORD_CLIENT_ID` | bot's application ID | OAuth2 client ID |
| `DISCORD_CLIENT_SECRET` | *(empty)* | OAuth2 client secret (**secret**) |
| `DASHBOARD_SESSION_KEY` | *(empty)* | ≥ 32 random bytes that seal the session cookie (**secret**) |
| `DASHBOARD_GUILD_ID` | *(empty)* | Only members of this server may log in |
| `DASHBOARD_ROLE_IDS` | *(empty)* | Comma-separated role IDs; members of `DASHBOARD_GUILD_ID` with any one of them get in. Needs `DASHBOARD_GUILD_ID`. **Replaces** the Administrator/Manage Server rule |
| `DASHBOARD_ALLOWED_USERS` | *(empty)* | Comma-separated user IDs always allowed in |
| `WEBHOOKS` | *(empty)* | `name=token@channelID,…` incoming webhooks (**secret**) |

## Dashboard

The bot runs a small HTTP server (`DASHBOARD_ADDR`, default `:8080`). The
dashboard at `/` is a read-only monitoring page behind Discord login, in Thai,
light/dark aware and usable on a phone. It refreshes every 5 seconds and has
one panel per feature, in this order: **บอท** (online, gateway ping, uptime,
version, Go version, goroutines, heap, OS memory, GC count, guilds),
**เซสชันอ่านแชท** (voice channel, text channels, participants with channel and
voice, start, queue, messages read and deleted), **งบ Google TTS** (as in the
Discord usage embed, plus recent time-to-first-audio), **อีเวนต์** (scheduler
state, reminder channel, auto-start mode, next wake, cover providers and
Brave's pause, upcoming events with their failures), **Webhooks** (hooks and
their recent calls) and **Log ล่าสุด** (the last 300 log lines with a filter
box). Each panel's snapshot is an in-memory, read-only status; panels never
call Discord. To add a panel for a new feature, see `CLAUDE.md`.

### Who can see the dashboard (and the manual)

A Discord user is let in when they are listed in `DASHBOARD_ALLOWED_USERS`,
**or** when they are a member of a server the bot is in and satisfy the
server rule:

- **With `DASHBOARD_ROLE_IDS` set** (production: members of the **TAPB**
  server with role **TABP**): having one of those roles is the whole rule.
  Owning the server or having Administrator / Manage Server does **not** grant
  access by itself (`GuildAccess.grants` in `internal/dashboard/access.go`);
  add such people to the role or to `DASHBOARD_ALLOWED_USERS`.
- **Without `DASHBOARD_ROLE_IDS`** (fallback): the server's owner, or anyone
  with **Administrator** or **Manage Server** (directly or through a role or
  @everyone), in any server the bot is in, or only in `DASHBOARD_GUILD_ID` when
  that is set.

`DASHBOARD_ROLE_IDS` without `DASHBOARD_GUILD_ID` is refused at startup. The
bot checks with its own token (guild roles and owner from the gateway, the
user's roles over REST), never the user's token; the Server Members
privileged intent is not needed. Anyone else gets a polite 403 page in Thai.
The decision is cached per user for up to 10 minutes (`DefaultAuthCacheTTL`),
so losing the role takes effect within 10 minutes. If Discord cannot be asked,
the last decision stands in for up to 1 hour (`staleAuthLimit`); with no
earlier decision the page answers 503 and retries. While the bot has not yet
loaded its guilds (just started or reconnecting) the check is indeterminate:
503, nothing cached.

### Setting up login

1. In the [Developer Portal](https://discord.com/developers/applications),
   open the bot's application → **OAuth2**.
2. Copy the **Client Secret** (Reset Secret if you never saved it) into
   `DISCORD_CLIENT_SECRET`. The **Client ID** on the same page is the default
   `DISCORD_CLIENT_ID`; you only need to set it if it differs from the bot's
   application.
3. Under **Redirects**, add `<DASHBOARD_PUBLIC_URL>/auth/callback` — in
   production `https://sonnie.tapb.ch/auth/callback` — and save. It must match
   exactly (scheme, address, port). The bot logs the URI it uses at startup
   (`dashboard: listening on … Discord login with redirect URI …`).
4. Set `DASHBOARD_PUBLIC_URL` to the address browsers use to reach the server,
   and `DASHBOARD_SESSION_KEY` to a random secret of at least 32 bytes:

   ```bash
   openssl rand -base64 48
   ```
5. Set `DASHBOARD_GUILD_ID` (the TAPB server's ID) and `DASHBOARD_ROLE_IDS`
   (the TABP role's ID), and optionally `DASHBOARD_ALLOWED_USERS`.

The login asks only for the `identify` scope. The session is an encrypted and
signed cookie (AES-256-GCM): `HttpOnly`, `SameSite=Lax`, `Secure` when the
public URL is https, and **valid for 7 days**. Changing the session key logs
everyone out. `/auth/logout` logs you out.

**Partial login configuration stops startup.** The three login settings
(`DISCORD_CLIENT_SECRET`, `DASHBOARD_PUBLIC_URL`, `DASHBOARD_SESSION_KEY`) must
be set together or not at all. Setting only some of them stops the bot at
startup with an error naming what is missing. With none of them the dashboard
pages say what is missing (503) while `/healthz` and the webhooks still work.

### Logs and secrets

The log panel shows the bot's own log (a ring of the last 300 lines), the same
lines as `kubectl logs`. The bot never logs its token or keys. As a second line
of defence every configured secret (bot token, client secret, session key,
Google TTS API key, Brave/Unsplash/Pexels keys, webhook tokens) is replaced
with `[redacted]` in the dashboard's copy (`redactSecrets` in
`internal/bot/dashboard.go`; add any new secret there).

## Webhooks

`WEBHOOKS` (a secret) lists the hooks, comma-separated, as
`name=token@channelID`:

```
WEBHOOKS=grafana=7f3c…long-random…@123456789012345678,ci=9a1e…@123456789012345679
```

Names are lowercase letters, digits, `-` and `_` (at most 32 characters, each
listed once). Tokens are at least 16 characters without spaces
(`openssl rand -hex 24`). The bot needs **View Channel** and **Send Messages**
(and **Embed Links** for embeds) in each channel. Webhooks are served by the
same HTTP server, so `DASHBOARD_ADDR` must not be off.

Call a hook with its token in `Authorization: Bearer <token>` or
`X-Sonnie-Token: <token>`:

```bash
# simple form: becomes one embed (footer "webhook · grafana")
curl -X POST https://sonnie.tapb.ch/hooks/grafana \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"title":"CPU สูง","text":"node1 95%","color":"#ff8800","url":"https://grafana.example/d/abc"}'

# Discord-webhook-like
curl -X POST https://sonnie.tapb.ch/hooks/ci \
  -H "X-Sonnie-Token: $TOKEN" -H "Content-Type: application/json" \
  -d '{"content":"build ok","embeds":[{"title":"main @ 1a2b3c","color":5763719}]}'

# plain text
curl -X POST https://sonnie.tapb.ch/hooks/ci -H "X-Sonnie-Token: $TOKEN" \
  -H "Content-Type: text/plain" --data 'deploy finished'
```

- **Success** is `200 {"ok":true,"message_id":"…","channel_id":"…"}`.
- **`401`**: an unknown hook and a wrong token both answer 401 (the token is
  compared in constant time and a dummy comparison runs for unknown names), so
  the answer never says which hooks exist.
- **`429`** with `Retry-After`: at most 30 calls per minute per hook (checked
  after authentication).
- **`413`**: body larger than 64 KB.
- **`400`**: bad JSON, an unreadable body, or nothing to post.
- **`415`**: a content type other than `application/json` (or `+json`) and
  `text/plain`. No content type is accepted: a body starting with `{` is
  read as JSON, anything else as UTF-8 text.
- **`502`**: Discord refused the message (for example missing permissions in
  the channel); the details are in the log.
- Embeds are rebuilt from the known fields and cut to Discord's limits: 10
  embeds, 6000 characters in total, http(s) URLs only; the message text is cut
  at 2000 characters.
- **Nothing pings anyone**: messages are sent with `allowed_mentions: {parse:
  []}`, so `@everyone`, roles and users stay plain text.
- Each call (time, hook, status, source IP, size; never the token or body) is
  listed on the dashboard's Webhooks panel and logged as `webhook: <hook> ->
  <status> from <ip> (<n> bytes)`.
