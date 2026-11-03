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
the voice channel. `/tts usage` shows this month's Google TTS usage, in
characters or baht (see [Usage cap and alerts](#usage-cap-and-alerts)). Each server can
have one active binding.

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
  via `TTS_VOICE`; or Gemini-TTS when `TTS_MODEL` is set, see
  [Gemini-TTS](#gemini-tts)), and `voice.OggOpusEncoder` demuxes the Ogg pages into
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
| `TTS_VOICE`          | *(empty)* | Optional Google voice name override (e.g. `th-TH-Chirp3-HD-Achird`). With a Gemini model: the bare speaker name, default `Charon` |
| `TTS_MODEL`          | *(empty)* | Gemini-TTS model, e.g. `gemini-2.5-flash-tts`. Empty = Chirp/Neural2 as before (see [Gemini-TTS](#gemini-tts)) |
| `TTS_PROMPT`         | *(empty)* | Style prompt sent as `input.prompt`; Gemini models only, ignored otherwise |
| `TTS_MONTHLY_BUDGET_THB` | `300` | Gemini only: money cap in baht per US/Pacific month; reading pauses once spend reaches it. `0` = no cap |
| `TTS_USD_TO_THB`     | `33`    | Gemini only: exchange rate used to turn the USD price into baht |
| `TTS_MAX_CHARS`      | `200`   | Max characters spoken per message                             |
| `TTS_QUEUE_SIZE`     | `20`    | Max messages waiting per server; oldest is dropped            |
| `TTS_SPEAK_AUTHOR`   | `true`  | Say the author's name before each message                     |
| `TTS_MONTHLY_CHAR_LIMIT` | `1000000` | Chirp/Neural2 only (no `TTS_MODEL`): max characters sent to Google per US/Pacific month; reading pauses when the next message would exceed it. `0` = no cap |
| `TTS_ALERT_CHANNEL_ID` | *(empty)* | Discord channel ID for usage alerts and the persisted counter (see below) |

When `TTS_ENABLED` is off, `/tts` is still listed in Discord but replies
that the feature is not enabled. `/ping` is unaffected either way.

### Gemini-TTS

Setting `TTS_MODEL` to a Gemini model (`gemini-2.5-flash-tts`) sends the same
Cloud Text-to-Speech `v1/text:synthesize` request with Gemini fields:

```json
{"input":{"text":"...","prompt":"<TTS_PROMPT>"},
 "voice":{"languageCode":"th-TH","name":"Charon","modelName":"gemini-2.5-flash-tts"},
 "audioConfig":{"audioEncoding":"OGG_OPUS","sampleRateHertz":48000}}
```

- `TTS_VOICE` is the bare speaker name (default `Charon`), not a
  `th-TH-...` voice name.
- `TTS_LANG` still picks `languageCode` (`th` → `th-TH`, `en` → `en-US`).
- `TTS_PROMPT` is a natural-language style instruction (e.g. `พูดแบบเป็นกันเอง
  เหมือนเพื่อนคุยกันในวงเกม`). It is sent only to Gemini models, never to
  Chirp/Neural2, which reject it. Its length is billed as input on every
  message, so keep it short.
- Authentication is unchanged: an API key in `X-Goog-Api-Key`.
- The audio is the same 48 kHz Ogg/Opus (single 20 ms frames), so the voice
  pipeline needs no change.
- **Latency:** expect roughly **3–4 s more** per message than Chirp 3 HD
  before speech starts, because Gemini generates the whole clip before
  returning it. Messages queue as before, so a busy chat falls further behind.

**Setup** (in addition to the Cloud Text-to-Speech steps above):

1. In the GCP project, enable the **Agent Platform API** (the Vertex AI /
   `aiplatform.googleapis.com` API) as well as the Cloud Text-to-Speech API.
2. Create a **service account** and grant it `roles/aiplatform.user`.
3. Create an **API key bound to that service account** (Credentials → Create
   credentials → API key → *Authenticate API calls through a service
   account*), and restrict it to the **Cloud Text-to-Speech API** and the
   **Agent Platform API**.
4. Set `GOOGLE_TTS_API_KEY` to that key, plus the variables below.

Environment for the Gemini setup used here:

```
TTS_MODEL=gemini-2.5-flash-tts
TTS_VOICE=Charon
TTS_PROMPT=พูดแบบเป็นกันเอง เหมือนเพื่อนคุยกันในวงเกม
TTS_MONTHLY_BUDGET_THB=300
```

`TTS_MONTHLY_CHAR_LIMIT` is ignored while `TTS_MODEL` is a Gemini model.

### Cost

Google Cloud Text-to-Speech includes a monthly free tier of **1,000,000
characters per month** for Neural2 (the default) and Chirp 3: HD voices, and
**4,000,000 characters per month** for Standard and WaveNet voices. Beyond
the free tier, Neural2 voices cost **$16 per 1 million characters**
($0.000016 per character; Chirp 3: HD is $30 per 1 million characters, and
Standard is $4 per 1 million characters). See the
[Google Cloud Text-to-Speech pricing page](https://cloud.google.com/text-to-speech/pricing)
for current rates.

**Gemini 2.5 Flash TTS** has no free tier and is billed per token (Cloud TTS
pricing page, read 2026-11-04):

| Part                    | Price                   |
|-------------------------|-------------------------|
| Text input              | $0.50 per 1M tokens     |
| Audio output            | $10.00 per 1M tokens    |
| Audio tokens per second | 25                      |

So one second of speech costs 25 × $10 / 1M = $0.00025, about ฿0.008 at 33
THB/USD. A 4 s message is roughly ฿0.035, so ฿300 is about 8,500 such messages.
The prices are named constants in `internal/usage/cost.go`; update them there
if Google changes them. The project's bill is in THB and is covered by about
฿329/month of Google credits, which is why the default budget is ฿300.

### Usage cap and alerts

One meter runs per process, chosen from config: with a Gemini `TTS_MODEL` it
meters **money** (below), otherwise **characters**.

#### Characters (Chirp 3: HD, Neural2; no `TTS_MODEL`)

The bot counts every character (Unicode code points, spaces included) it
sends to Google, and stops synthesizing once
`used + message length > TTS_MONTHLY_CHAR_LIMIT`. The default of 1,000,000 is
the Chirp 3: HD free tier, so the cap keeps the bill at $0. Skipped messages
are simply not spoken; the first refusal each month is logged once. The count
resets at the start of each calendar month in **US/Pacific** time, Google's
billing time zone. Set `TTS_MONTHLY_CHAR_LIMIT=0` to count without a cap.
Counting only happens when `GOOGLE_TTS_API_KEY` is set. Because the bot
counts what it sends, the meter is only accurate if nothing else uses the same
API key.

`/tts usage` replies (only to you) with this month's characters used, the
limit, the percentage and whether reading is paused.

#### Money (Gemini)

After every successful synthesis the bot prices the request:

```
cost = audio seconds × 25 × $10 / 1M  +  input tokens × $0.50 / 1M     (× TTS_USD_TO_THB)
```

- **Audio seconds** come from the Ogg granule position of the last page minus
  the pre-skip in `OpusHead`, at 48 kHz (not by counting packets; the two agree
  to within the pre-skip, and a test checks this on real fixtures). If the
  audio cannot be parsed, the bot bills an upper bound (size at 6 kbit/s) and
  logs it.
- **Input tokens** are estimated as the runes of the message plus the runes of
  `TTS_PROMPT`. Gemini's tokenizer is not available offline; one token per
  character over-counts Thai, so the estimate errs towards spending too
  much, and input is only about 1/20 of the audio cost anyway.
- Failed requests are not charged.
- **Budget check:** the cost is unknown until the audio comes back, so the bot
  can only check before a request that the spend so far has not yet reached
  `TTS_MONTHLY_BUDGET_THB`. The message that crosses the budget is spoken, so
  the month can overshoot by one message (a few satang), or one per request
  already in flight in other servers. After that, messages
  are skipped until next month. `TTS_MONTHLY_BUDGET_THB=0` counts without a cap.
- The month is still the US/Pacific calendar month, so it does not line up
  exactly with a THB invoice or credit period.
- The meter only sees this bot's requests; anything else using the same key is
  not counted.

`/tts usage` replies (only to you) with the spend, the budget, the percentage,
whether reading is paused, and the seconds of audio generated this month, e.g.
`Google TTS spend for 2026-11 (US/Pacific month): ฿240.00 / ฿300.00 budget
(80%). Reading is active. Audio generated: 29000.0 s.`

Alerts use the same 50/80/100% thresholds, in baht:
`⚠️ Google TTS ใช้ไป 80% ของงบเดือนนี้ (฿240.00 / ฿300.00)`.

#### Alerts and persistence (both meters)

**Alerts.** Set `TTS_ALERT_CHANNEL_ID` to a text channel's ID (enable Developer
Mode in Discord, right-click the channel, *Copy Channel ID*). The bot posts a
Thai message the first time usage crosses 50%, 80% and 100% in a month, e.g.
`⚠️ Google TTS ใช้ไป 80% ของโควตาเดือนนี้ (800,000 / 1,000,000 ตัวอักษร)`;
the 100% message says reading is paused until next month. It also fires when
a message is refused for not fitting in what is left, since usage may never
land exactly on 100%. Each threshold fires once per month, also across
restarts.

**Persistence.** The deployment has no persistent volume, so the counter lives
in Discord: the bot keeps one status message in the alert channel with a
machine-readable marker line in a code block:

```
sonnie-tts-usage v2 month=2026-11 unit=chars value=12345 audio_ms=0 alerted=50,80
sonnie-tts-usage v2 month=2026-11 unit=thb_micro value=240000000 audio_ms=29000000 alerted=50,80
```

`unit=chars` counts characters; `unit=thb_micro` counts millionths of a baht
(`240000000` = ฿240.00). `audio_ms` is the audio generated this month.

On startup the bot scans the channel's last 100 messages for the newest such
message written by itself and restores the count from it. A marker from a
different month, or in a different unit, means start at 0 (the reason is
logged): the money meter starts at ฿0 when it finds an old `v1` (characters)
marker or a `unit=chars` one, e.g. right after switching to Gemini, and the
character meter does the same for a `unit=thb_micro` one. A `v1` marker is
still read by the character meter. The bot edits that message at most once a
minute, only when the count changed, and once more on shutdown. A crash can
lose up to a minute of counts. Do not delete the status message or let
100+ messages pile up after it in that channel; if it cannot be found the
count restarts at 0. Use a dedicated channel.

The bot needs **View Channel**, **Send Messages** and **Read Message History**
in the alert channel (no pinning, no other permissions). If
`TTS_ALERT_CHANNEL_ID` is not set, the count is kept in memory only, resets
when the bot restarts, and a warning is logged at startup.

### Host requirements

None beyond network access to Discord and `texttospeech.googleapis.com`.
Google Cloud TTS returns 48 kHz Ogg/Opus directly and SONNIE demuxes it in
pure Go, so neither cgo nor `ffmpeg` is needed at runtime and the container
image remains `distroless/static`.
