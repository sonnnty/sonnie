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

Reading aloud is **opt-in per person** and lives in a **session**. `/tts join`
(run in the text channel you want read, while you are in a voice channel) joins
the session: from then on the bot reads **your** messages in that channel
aloud in the voice channel, and nobody else's. `/tts leave` takes you out
again. `/tts usage` shows this month's Google TTS usage, in characters or baht
(see [Usage cap and alerts](#usage-cap-and-alerts)). Each server has at most
one session.

### How a session looks in Discord

1. The **first** `/tts join` in a server makes the bot join your voice channel
   and post **one public session message** (a Thai embed) in the text channel:
   who takes part (as mentions, which do not notify anyone), the text and voice
   channel, when it started, and how to join and leave. You get a private
   (ephemeral) confirmation.
2. Anyone else in **the same voice channel** runs `/tts join` **in the same
   text channel**. They are added and the bot **edits** the session message;
   it never posts a second one. They get a private confirmation.
3. `/tts leave` removes you and edits the message. When the last participant
   leaves, the session ends.
4. When a session ends (last participant left, nobody but bots left in the
   voice channel, the bot was disconnected or moved, or the bot shuts down),
   the bot **deletes the messages the participants wrote** in that text channel
   during the session and turns the session message into a short summary that
   stays in the channel as a log:

   ```
   📝 จบการอ่านออกเสียง · ผู้เข้าร่วม: @a, @b · 25 นาที · ลบข้อความแล้ว 42 ข้อความ
   ```

Refusals are private and say what to do instead:

- `/tts join` from a **different voice channel** while the session's voice
  channel still has people in it: refused, naming the busy channel. If nobody
  is left there, the old session is ended (with clean-up) and the new one starts.
- `/tts join` from a **different text channel** while the session in your voice
  channel is bound to another one: refused, naming the bound text channel.
- `/tts leave` when you are not in the session, or no session runs.

What gets deleted at the end, exactly: messages in the session's text channel
by **anyone who took part at any time** (including people who joined and later
left), created **after that person first ran `/tts join` and before the session
ended**. Never touched: messages by people who did not join, what a participant
wrote before they joined, messages in other channels, anything from before the
session, and the bot's own session message. After a restart the join times are
not known, so a recovered session cleans from the session message on.
Messages younger than 14 days go through Discord's bulk delete (chunks of at
most 100); older ones are deleted one by one. The bot also remembers the
participants' messages as they arrive, and scans the channel history after the
session message when it ends, so a missed event or a restart does not leave
messages behind. Because anything a participant writes in the channel during
the session is deleted, `/tts join` tells people so.

**Manage Messages** is needed for the deletion. The bot checks it in the text
channel when the session starts. If it is missing, the session message turns
orange and says nothing will be deleted, and the summary says
`ไม่ได้ลบข้อความ`; the bot does not fail and reading works as usual.
Participants are told at the start whether their messages will be deleted, so
the bot does not delete anything if it did not have the permission then.

If the bot **restarts** during a session, the session is lost (it leaves
voice). The session message carries a hidden marker in its footer (like the
usage status message) listing the participants. When the bot comes back it
looks through the last 300 messages of each channel it can read for its own
unfinished session messages and finishes their clean-up and summary. A session
message that has been pushed out of that window by a busy channel is left as
it is.

Each session start, join, leave and end is logged as one line (user IDs and
counts) for `kubectl logs`, e.g. `tts: session end guild=… reason="last
participant left" users=… duration=25m0s deleted=42 failed=0`.

### Status

What works now:

- `/tts join` and `/tts leave`: opt-in sessions, one per server, with one
  session message and clean-up when it ends (see above). Only participants'
  messages are spoken.
- Message filtering and clean-up: bot, webhook and empty messages are
  ignored. Mentions become names (from cache) or are dropped. URLs become
  "ลิงก์" (or "link" when `TTS_LANG` isn't Thai). Custom emoji, timestamps,
  spoilers and code blocks are dropped, markdown is stripped, and messages
  are capped at `TTS_MAX_CHARS`.
- A per-server queue that speaks messages in order. When it is full the
  **oldest waiting message is dropped**, because in a live chat the newest
  messages matter most.
- The bot leaves automatically (ending the session, with clean-up) when nobody
  (other than bots) is left in the voice channel, or if it is disconnected from
  voice or moved to another channel.
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
  20 ms Opus frames in pure Go (no cgo, no `ffmpeg`). Gemini models are
  streamed over gRPC by default (`TTS_STREAMING`, see
  [Streaming](#streaming-gemini)): pages are demuxed as they arrive and the
  bot starts speaking before the sentence is fully synthesized.
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
   permissions in the voice channels it should join, and in the text channels
   where people run `/tts join` it needs **View Channel**, **Send Messages**,
   **Embed Links** (the session message is an embed), **Read Message History**
   (the clean-up scans the history) and **Manage Messages** (to delete the
   participants' messages when the session ends; without it reading works but
   nothing is deleted). When inviting the bot, include the `bot` and
   `applications.commands` scopes. This invite URL asks for exactly those
   permissions (`3238912` = `0x316C00`: View Channel `0x400`, Send Messages
   `0x800`, **Manage Messages `0x2000`**, Embed Links `0x4000`, Read Message
   History `0x10000`, Connect `0x100000`, Speak `0x200000`):

   ```
   https://discord.com/oauth2/authorize?client_id=<APPLICATION_ID>&scope=bot+applications.commands&permissions=3238912
   ```

   A bot that is already in the server does not need re-inviting: grant
   **Manage Messages** to its role (or to the channel) in the server settings.
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
| `GOOGLE_TTS_API_KEY` | *(empty)* | Google Cloud Text-to-Speech API key (Chirp/Neural2 voices)   |
| `GOOGLE_APPLICATION_CREDENTIALS` | *(empty)* | Service account JSON key file; required for Gemini-TTS, wins over the API key |
| `TTS_LANG`           | `th`    | Language passed to the speech engine (`th`, `en`, `ja`, ...)  |
| `TTS_VOICE`          | *(empty)* | Optional Google voice name override (e.g. `th-TH-Chirp3-HD-Achird`). With a Gemini model: the bare speaker name, default `Charon` |
| `TTS_MODEL`          | *(empty)* | Gemini-TTS model, e.g. `gemini-2.5-flash-tts`. Empty = Chirp/Neural2 as before (see [Gemini-TTS](#gemini-tts)) |
| `TTS_PROMPT`         | *(empty)* | Style prompt sent as `input.prompt`; Gemini models only, ignored otherwise |
| `TTS_STREAMING`      | `true` with a Gemini `TTS_MODEL`, else `false` | Gemini only: synthesize over the gRPC streaming API and start speaking at the first audio (see [Streaming](#streaming-gemini)). Needs `GOOGLE_APPLICATION_CREDENTIALS`. Ignored for Chirp/Neural2 |
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
- Authentication is a service account (`GOOGLE_APPLICATION_CREDENTIALS`, see
  setup below); API keys are rejected for Gemini models.
- The audio is the same 48 kHz Ogg/Opus (single 20 ms frames), so the voice
  pipeline needs no change.
- **Latency:** with the unary REST call (`TTS_STREAMING=false`) expect roughly
  **3–4 s more** per message than Chirp 3 HD before speech starts (about 4 s
  for one sentence vs about 1 s), because the whole clip is generated before
  it is returned. Streaming, the default, is meant to cut that; see below.
  Messages queue as before, so a busy chat falls further behind.

**Setup** (in addition to the Cloud Text-to-Speech steps above):

1. In the GCP project, enable the **Agent Platform API** (the Vertex AI /
   `aiplatform.googleapis.com` API) as well as the Cloud Text-to-Speech API.
2. Create a **service account** and grant it `roles/aiplatform.user`.
3. Create a **JSON key** for that service account
   (`gcloud iam service-accounts keys create sa.key --iam-account=<sa-email>`).
   API keys do not work here, not even ones bound to the service account:
   the Text-to-Speech endpoint answers `UNAUTHENTICATED API keys are not
   supported by this API` for Gemini models (checked 2026-11-04).
4. Set `GOOGLE_APPLICATION_CREDENTIALS` to the path of that file (it takes
   precedence over `GOOGLE_TTS_API_KEY`), plus the variables below. In k8s,
   mount it from a Secret.

Environment for the Gemini setup used here:

```
TTS_MODEL=gemini-2.5-flash-tts
TTS_VOICE=Charon
TTS_PROMPT=พูดแบบเป็นกันเอง เหมือนเพื่อนคุยกันในวงเกม
TTS_MONTHLY_BUDGET_THB=300
```

`TTS_MONTHLY_CHAR_LIMIT` is ignored while `TTS_MODEL` is a Gemini model.

### Streaming (Gemini)

With a Gemini `TTS_MODEL` and `GOOGLE_APPLICATION_CREDENTIALS`, the bot uses
the gRPC `StreamingSynthesize` call of Cloud Text-to-Speech
(`texttospeech.googleapis.com:443`) instead of `v1/text:synthesize`. It is on
by default; `TTS_STREAMING=false` goes back to the unary REST call. Chirp and
Neural2 voices always use REST.

- One call per message: a `StreamingSynthesizeConfig` (same voice, language
  and `model_name` as the REST request, `OGG_OPUS` at 48 kHz), then one
  `StreamingSynthesisInput` with the text and `TTS_PROMPT`. Same service
  account, same token source as the REST client.
- Each `audio_content` chunk goes straight into an incremental Ogg demuxer;
  every Ogg page's Opus packets are queued for sending as soon as the page is
  complete, and playback starts with the first one while the rest is still
  arriving. Pacing (20 ms), the queue and "one message at a time" are
  unchanged.
- `/tts leave`, the bot leaving on its own, or shutdown cancel the call in
  flight. A stream that sends no audio for 30 s is abandoned.
- **Fallback:** if the stream fails before any audio arrives (an error, or
  the call ends without audio), the bot logs
  `tts: google: stream failed before any audio, retrying once with REST: ...`
  and synthesizes that message once with the REST call. A failure after
  audio has started is reported and not retried.
- **Billing:** a streamed message is charged when its stream ends, for the
  audio actually received (see [Money](#money-gemini)), including a stream
  cut off by an error or by the bot leaving.

**Comparing latency.** Every message logs one line, whichever path it took:

```
tts: timing path=stream first_audio=412ms total=2.31s bytes=9123
tts: timing path=rest first_audio=3.9s total=3.9s bytes=9087
tts: timing path=rest-fallback first_audio=4.2s total=4.2s bytes=9101
```

`first_audio` is from sending the request to the first audio byte (what the
listener waits for, give or take one Ogg page); `total` is until the last
byte. For REST both are the same. `rest-fallback` includes the failed stream
attempt. To compare, run a while with each setting and read
`kubectl logs <pod> | grep 'tts: timing'`. On startup the bot logs
`tts: streaming gemini-2.5-flash-tts over gRPC (TTS_STREAMING)` when
streaming is active.

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

`/tts usage` replies (only to you) with a compact Discord embed showing this
month's progress bar, characters used, audio generated, status, and reset
timestamp.

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
- **Streamed messages** ([Streaming](#streaming-gemini)) are charged once,
  when the stream ends, from the complete Ogg pages received: granule of the
  last one minus the pre-skip. A stream that breaks or is cancelled halfway
  pays for the part that arrived; one that delivers no audio pays nothing.
  Audio Google generated but had not yet delivered when the bot cancelled is
  not visible to the bot and not counted.
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

`/tts usage` replies (only to you) with a compact Thai Discord embed, e.g.:

```
🔊 Google TTS · พฤศจิกายน 2026
▰▰▱▱▱▱▱▱▱▱  12%
฿36.20 / ฿300.00
เสียงที่สร้าง: 6.7 วินาที   สถานะ: 🟢 กำลังอ่าน   รีเซ็ต: <t:unix:R>
```

Embed color reflects percentage (green < 50%, yellow 50–79%, orange 80–99%, red ≥ 100%).

#### Alerts and persistence (both meters)

**Alerts.** Set `TTS_ALERT_CHANNEL_ID` to a text channel's ID (enable Developer
Mode in Discord, right-click the channel, *Copy Channel ID*). The bot posts a
notification preview line and a small embed card the first time usage crosses 50%,
80% and 100% in a month (colors match the thresholds):

```
⚠️ Google TTS ใช้ไป 80% ของงบเดือนนี้
[Embed: 🔊 Google TTS · พฤศจิกายน 2026 | ▰▰▰▰▰▰▰▰▱▱  80% | ฿240.00 / ฿300.00]
```

The 100% message announces that reading is paused until next month. It also fires
when a message is refused for not fitting in what is left, since usage may never
land exactly on 100%. Each threshold fires once per month, also across restarts.

**Persistence.** The deployment has no persistent volume, so the counter lives
in Discord: the bot keeps one status message in the alert channel as an embed
(no visible code block). The machine-readable marker is kept in the embed footer text
after `อัปเดตอัตโนมัติ · อย่าลบข้อความนี้`:

```
sonnie-tts-usage v2 month=2026-11 unit=chars value=12345 audio_ms=0 alerted=50,80
sonnie-tts-usage v2 month=2026-11 unit=thb_micro value=240000000 audio_ms=29000000 alerted=50,80
```

`unit=chars` counts characters; `unit=thb_micro` counts millionths of a baht
(`240000000` = ฿240.00). `audio_ms` is the audio generated this month.

On startup the bot scans the channel's last 100 messages for the newest such
message written by itself and restores the count from it (supporting both the
embed footer marker and legacy plain-content markers, migrating old-format messages
to the embed format on startup). A marker from a different month, or in a different
unit, means start at 0 (the reason is logged): the money meter starts at ฿0 when it
finds an old `v1` (characters) marker or a `unit=chars` one, e.g. right after
switching to Gemini, and the character meter does the same for a `unit=thb_micro`
one. A `v1` marker is still read by the character meter. The bot edits that message
at most once a minute, only when the count changed, and once more on shutdown. A
crash can lose up to a minute of counts. Do not delete the status message or let
100+ messages pile up after it in that channel; if it cannot be found the count
restarts at 0. Use a dedicated channel.

The bot needs **View Channel**, **Send Messages** and **Read Message History**
in the alert channel (no pinning, no other permissions). If
`TTS_ALERT_CHANNEL_ID` is not set, the count is kept in memory only, resets
when the bot restarts, and a warning is logged at startup.

### Host requirements

None beyond network access to Discord and `texttospeech.googleapis.com`
(HTTPS for REST; gRPC over HTTP/2 on the same host and port 443 for
streaming, so an egress proxy or firewall must allow HTTP/2 there).
Google Cloud TTS returns 48 kHz Ogg/Opus directly and SONNIE demuxes it in
pure Go, so neither cgo nor `ffmpeg` is needed at runtime and the container
image remains `distroless/static`.
