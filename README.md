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

## Read chat aloud (`/tts`) — work in progress

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
  20 ms and held until the DAVE handshake is done. This has not been tested
  against a live voice channel yet. See
  [docs/voice-transport.md](docs/voice-transport.md) for the research, the
  options considered and why disgo and dave-go are pinned to untagged
  commits.

What is stubbed, so **no audio plays yet**:

- Speech synthesis: `tts.Unimplemented` fails every request, so each
  message logs `tts: no synthesizer configured`.
- Opus encoding: `voice.UnimplementedEncoder`.

Once both are replaced, audio is expected to reach listeners. The first
live test should confirm that `/tts join` is not closed with code 4016 or
4017, and that speech is heard in a normal desktop client.

### Setup

1. In the [Discord Developer Portal](https://discord.com/developers/applications),
   open your application, go to **Bot** and turn on
   **Message Content Intent**. It is a privileged intent. If it is not
   enabled, the bot cannot connect at all while `TTS_ENABLED=true`.
2. Make sure the bot has the **View Channel**, **Connect** and **Speak**
   permissions in the voice channels it should join, and **View Channel**
   and **Read Message History** in the text channels it should read. When
   inviting the bot, include the `bot` and `applications.commands` scopes.
3. Set `TTS_ENABLED=true` in `.env` and restart the bot.

| Variable           | Default | Meaning                                              |
|--------------------|---------|------------------------------------------------------|
| `TTS_ENABLED`      | `false` | Turns the feature (and the Message Content intent) on |
| `TTS_LANG`         | `th`    | Language passed to the speech engine                 |
| `TTS_MAX_CHARS`    | `200`   | Max characters spoken per message                    |
| `TTS_QUEUE_SIZE`   | `20`    | Max messages waiting per server; oldest is dropped   |
| `TTS_SPEAK_AUTHOR` | `true`  | Say the author's name before each message            |

When `TTS_ENABLED` is off, `/tts` is still listed in Discord but replies
that the feature is not enabled. `/ping` is unaffected either way.

### Host requirements (once finished)

Depending on which speech engine is chosen, the bot will probably need
`ffmpeg` on the host to convert engine output to 48 kHz Opus. It is not
needed if the engine can output Ogg/Opus directly. A local engine such as
Piper or eSpeak NG would also need its binary and voice models installed.
