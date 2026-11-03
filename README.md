# SONNIE

A general-purpose Discord bot written in Go, grown one command at a time.

## Setup

1. Copy `.env.example` to `.env` and fill in `DISCORD_BOT_TOKEN` with your
   bot's token from the [Discord Developer Portal](https://discord.com/developers/applications).
2. Run the bot:

   ```bash
   go run ./cmd/bot
   ```

3. In a server the bot has been invited to, run `/ping` — it should reply `pong`.

## Run with Docker

The image is a static Go binary on a non-root distroless base. It reads its
configuration from environment variables only and listens on no port.

```bash
docker build -t sonnie .
docker run --rm --env-file .env sonnie
```

`.env` is excluded from the build context (see `.dockerignore`), so the token
is never baked into the image — pass it at run time with `--env-file` or `-e`.
Without `DISCORD_BOT_TOKEN` the container exits immediately with a config
error.

## Adding a command

Add a new file under `internal/commands/`, following the pattern in
`ping.go`: define an `init()` that calls `register()` with a
`discordgo.ApplicationCommand` definition and a handler. No other file
needs to change.

Commands that need shared state (like `/tts`) still self-register in
`init()`. They get their dependencies through a small interface and a
setter that the bot calls once at startup (see `SetTTS` in `tts.go`).

## Read chat aloud (`/tts`)

`/tts join` makes the bot join your voice channel and read new messages
from the text channel you ran it in. `/tts leave` stops reading and leaves
the voice channel. Each server can have one active binding.

### Status

What works now:

- `/tts join` and `/tts leave`, one binding per server. Running `join`
  again moves the binding.
- Message filtering and clean-up: bot, webhook and empty messages are
  ignored. Mentions become names (from cache) or are dropped. URLs become
  "ลิงก์" (or "link" when `TTS_LANG` isn't Thai). Custom emoji, timestamps,
  spoilers and code blocks are dropped, markdown is stripped, and messages
  are capped at `TTS_MAX_CHARS`.
- A per-server queue that speaks messages in order. When it is full the
  **oldest waiting message is dropped**, because in a live chat the newest
  messages matter most.
- The bot leaves automatically when nobody (other than bots) is left in the
  voice channel, and drops the binding if it is disconnected from voice or
  moved to another channel.
- Voice transport: the bot joins through disgo's voice client, which uses
  voice gateway v8 and `aead_aes256_gcm_rtpsize`/`aead_xchacha20_poly1305_rtpsize`
  transport encryption. DAVE end-to-end encryption, which Discord requires
  in voice channels since March 2026, comes from dave-go. Both are pure Go,
  so no cgo. discordgo still handles everything else. Frames are paced at
  20 ms and held until the DAVE handshake is done. Joining and the DAVE/MLS
  handshake have been verified live in Discord. See
  [docs/voice-transport.md](docs/voice-transport.md) for the research, the
  options considered and why disgo and dave-go are pinned to untagged
  commits.
- Speech synthesis and Opus encoding: `tts.Google` calls the Google Cloud
  Text-to-Speech v1 REST API (`OGG_OPUS` at 48 kHz, default voices
  `th-TH-Neural2-C` for Thai and `en-US-Neural2-F` for English, overridable
  via `TTS_VOICE`), and `voice.OggOpusEncoder` demuxes the Ogg pages into
  20 ms Opus frames in pure Go (no cgo, no `ffmpeg`).
- Audio playback requires `GOOGLE_TTS_API_KEY`. When `TTS_ENABLED=true` but
  `GOOGLE_TTS_API_KEY` is not set, the bot logs a warning at startup and
  falls back to the unimplemented stubs so `/ping` and the rest of the bot
  continue to work.

### Setup

1. In the [Discord Developer Portal](https://discord.com/developers/applications),
   open your application, go to **Bot** and turn on
   **Message Content Intent**. It is a privileged intent. If it is not
   enabled, the bot cannot connect at all while `TTS_ENABLED=true`.
2. Make sure the bot has the **View Channel**, **Connect** and **Speak**
   permissions in the voice channels it should join, and **View Channel**
   and **Read Message History** in the text channels it should read. When
   inviting the bot, include the `bot` and `applications.commands` scopes.
3. Create a Google Cloud Text-to-Speech API key:
   - In the [Google Cloud Console](https://console.cloud.google.com/), create
     or select a GCP project with billing enabled.
   - Go to **APIs & Services → Library**, search for **Cloud Text-to-Speech API**
     (`texttospeech.googleapis.com`) and enable it.
   - Go to **APIs & Services → Credentials**, click **Create credentials → API key**,
     open the key's settings, and under **API restrictions** select
     **Restrict key** and restrict it to **Cloud Text-to-Speech API**.
4. Set `TTS_ENABLED=true` and `GOOGLE_TTS_API_KEY=<your-key>` in `.env` and
   restart the bot.

| Variable             | Default | Meaning                                                       |
|----------------------|---------|---------------------------------------------------------------|
| `TTS_ENABLED`        | `false` | Turns the feature (and the Message Content intent) on         |
| `GOOGLE_TTS_API_KEY` | *(empty)* | Google Cloud Text-to-Speech API key (required for audio)    |
| `TTS_LANG`           | `th`    | Language passed to the speech engine (`th`, `en`, `ja`, ...)  |
| `TTS_VOICE`          | *(empty)* | Optional Google voice name override (e.g. `th-TH-Chirp3-HD-Achird`) |
| `TTS_MAX_CHARS`      | `200`   | Max characters spoken per message                             |
| `TTS_QUEUE_SIZE`     | `20`    | Max messages waiting per server; oldest is dropped            |
| `TTS_SPEAK_AUTHOR`   | `true`  | Say the author's name before each message                     |

When `TTS_ENABLED` is off, `/tts` is still listed in Discord but replies
that the feature is not enabled. `/ping` is unaffected either way.

### Cost

Google Cloud Text-to-Speech includes a monthly free tier of **1,000,000
characters per month** for Neural2 (the default) and Chirp 3: HD voices, and
**4,000,000 characters per month** for Standard and WaveNet voices. Beyond
the free tier, Neural2 voices cost **$16 per 1 million characters**
($0.000016 per character; Chirp 3: HD is $30 per 1 million characters, and
Standard is $4 per 1 million characters). See the
[Google Cloud Text-to-Speech pricing page](https://cloud.google.com/text-to-speech/pricing)
for current rates.

### Host requirements

None beyond network access to Discord and `texttospeech.googleapis.com`.
Google Cloud TTS returns 48 kHz Ogg/Opus directly and SONNIE demuxes it in
pure Go, so neither cgo nor `ffmpeg` is needed at runtime and the container
image remains `distroless/static`.
