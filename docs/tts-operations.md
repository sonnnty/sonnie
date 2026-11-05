# Read-aloud (`/tts`): setup, behaviour and recovery

Operator and maintainer reference for the read-aloud feature. Users read the
Thai manual instead (`docs/manual/10-tts.md`, served at `/docs/10-tts`).

Related: [operations.md](operations.md) (environment variable reference,
dashboard, webhooks, releases), [tts-cost-and-persistence.md](tts-cost-and-persistence.md)
(prices, the monthly meter, alerts and the markers that survive restarts),
[voice-transport.md](voice-transport.md) (voice gateway and DAVE).

Reading aloud is **opt-in per person** and lives in a **session**. `/tts join`
(run in any text channel while you are in a voice channel) joins the session:
from then on the bot reads **your** messages **in the channel where you ran
it** aloud in the voice channel, and nobody else's. Every participant picks
their own text channel, so a session can read several text channels at once,
but there is one voice channel per server (Discord allows one voice
connection per bot per server). `/tts leave` takes you out again. `/tts usage`
shows this month's Google TTS usage (see
[tts-cost-and-persistence.md](tts-cost-and-persistence.md)). Each server has
at most one session.

When `TTS_ENABLED` is off, `/tts` is still listed in Discord but replies that
the feature is not enabled, and the dashboard's session panel says so.
`/ping` is unaffected either way.

## Setup

1. **Message Content intent.** In the
   [Discord Developer Portal](https://discord.com/developers/applications),
   open the application, go to **Bot** and turn on **Message Content Intent**.
   It is a privileged intent. `TTS_ENABLED=true` makes the bot request it
   (together with the guild-messages and voice-state intents); if it is not
   switched on in the portal, Discord closes the gateway connection with
   **close code 4014 (disallowed intents)** and the bot cannot connect at all
   while `TTS_ENABLED=true`. With `TTS_ENABLED=false` the intent is not
   requested and nothing needs to be switched on.
2. **Bot permissions.** In the voice channels it should join the bot needs
   **View Channel**, **Connect** and **Speak**. In the text channels where
   people run `/tts join` it needs **View Channel**, **Send Messages**,
   **Embed Links** (the session message is an embed), **Read Message History**
   (the clean-up scans the history) and **Manage Messages** (to delete the
   participants' messages, after they are read and when the session ends;
   without it reading works but nothing is deleted). When inviting the bot,
   include the `bot` and `applications.commands` scopes. This invite URL asks
   for exactly those permissions (`3238912` = `0x316C00`: View Channel
   `0x400`, Send Messages `0x800`, **Manage Messages `0x2000`**, Embed Links
   `0x4000`, Read Message History `0x10000`, Connect `0x100000`, Speak
   `0x200000`):

   ```
   https://discord.com/oauth2/authorize?client_id=<APPLICATION_ID>&scope=bot+applications.commands&permissions=3238912
   ```

   A bot that is already in the server does not need re-inviting: grant
   **Manage Messages** to its role (or to the channel) in the server settings.
3. **Google Cloud Text-to-Speech API key** (Chirp 3: HD and Neural2 voices;
   skip this for Gemini-TTS, which needs the service account below):
   - In the [Google Cloud Console](https://console.cloud.google.com/), create
     or select a GCP project with billing enabled.
   - Go to **APIs & Services → Library**, search for **Cloud Text-to-Speech
     API** (`texttospeech.googleapis.com`) and enable it.
   - Go to **APIs & Services → Credentials**, click **Create credentials →
     API key**, open the key's settings, and under **API restrictions** select
     **Restrict key** and restrict it to **Cloud Text-to-Speech API**.
   - Put it in `GOOGLE_TTS_API_KEY`.
4. **Gemini-TTS service account** (needed for `TTS_MODEL=gemini-2.5-flash-tts`):
   1. In the GCP project, enable the **Agent Platform API** (the Vertex AI /
      `aiplatform.googleapis.com` API) as well as the Cloud Text-to-Speech API.
   2. Create a **service account** and grant it `roles/aiplatform.user`.
   3. Create a **JSON key** for that service account
      (`gcloud iam service-accounts keys create sa.key --iam-account=<sa-email>`).
      **API keys are rejected for Gemini models**, even ones bound to the
      service account: the Text-to-Speech endpoint answers `UNAUTHENTICATED API
      keys are not supported by this API` (checked 2026-11-04).
   4. Set `GOOGLE_APPLICATION_CREDENTIALS` to the path of that file. It takes
      precedence over `GOOGLE_TTS_API_KEY`. In Kubernetes, mount it from a
      Secret. The same service account also translates Thai event names into
      search keywords (see [events-operations.md](events-operations.md)).
5. Set `TTS_ENABLED=true` (plus the Google credentials) in the environment and
   restart the bot.

Without any Google credential (`GOOGLE_APPLICATION_CREDENTIALS` and
`GOOGLE_TTS_API_KEY` both empty) the bot logs a warning at startup
(`tts: neither GOOGLE_APPLICATION_CREDENTIALS nor GOOGLE_TTS_API_KEY is set;
/tts join will connect to voice, but speech synthesis is disabled`), `/tts
join` still joins voice, nothing is spoken, and no usage is metered. `/ping`
and the rest of the bot keep working.

Environment for the Gemini setup used in production:

```
TTS_ENABLED=true
TTS_MODEL=gemini-2.5-flash-tts
TTS_VOICE=Charon
TTS_PROMPT=พูดแบบเป็นกันเอง เหมือนเพื่อนคุยกันในวงเกม
TTS_MONTHLY_BUDGET_THB=300
```

`TTS_MONTHLY_CHAR_LIMIT` is ignored while `TTS_MODEL` is a Gemini model. The
full variable list is in [operations.md](operations.md#environment-variables).

## Session behaviour

### How a session looks in Discord

1. The **first** `/tts join` in a server makes the bot join your voice channel
   and post a **public session card** (a compact Thai embed) in the text
   channel you ran it in. You get a one-line private (ephemeral) confirmation
   naming the voice assigned to you (e.g. `✅ เข้าร่วมแล้ว — … · เสียง Puck`).

   ```
   ### 🔈 #ห้องเสียง · 3 นาทีที่แล้ว   (heading: larger, with space below)
   👥 @a 🎙️Puck, @b 🎙️Orus · #general
   @c 🎙️Charon · #games
   /tts join เพื่อให้อ่าน · /tts leave เพื่อออก · ลบข้อความหลังอ่าน
   ```

   The participants are mentions (they do not notify anyone) with their
   assigned voice, grouped by the channel they are read in; the footer (small
   text) says how to join and leave and what happens to messages. It reads
   `ลบข้อความเมื่อจบ` when `TTS_DELETE_AFTER=0` and `⚠️ ไม่มีสิทธิ์ลบข้อความ`
   (with an orange embed) when the bot lacks Manage Messages in **that**
   channel. The session state that lets a restarted bot finish the session is
   hidden in the embed's URL (an embed without a title never shows it) and is
   written to the log as `tts: session state …` on every change.
2. **Voices per participant.** Each participant speaks with their own male
   voice from the pool (`TTS_VOICE_POOL`, defaulting to the 16 Gemini-TTS male
   voices: `Achird`, `Algenib`, `Algieba`, `Alnilam`, `Charon`, `Enceladus`,
   `Fenrir`, `Iapetus`, `Orus`, `Puck`, `Rasalgethi`, `Sadachbia`,
   `Sadaltager`, `Schedar`, `Umbriel`, `Zubenelgenubi`). A new participant gets
   a random voice not yet handed out in the current round; once all pool
   voices have been handed out in the session, a new round starts with the
   full pool available again. You keep your voice for the whole session —
   including after moving to another text channel, `/tts leave` + rejoin, or
   disconnecting from voice and rejoining; a new session starts fresh. `/tts
   join voice:<choice>` picks a specific voice (or changes your current one); a
   picked voice counts as used in the current round. `voice:` is an
   autocomplete list of the pool voices and the 14 Gemini-TTS female voices
   (`Achernar`, `Aoede`, `Autonoe`, `Callirrhoe`, `Despina`, `Erinome`,
   `Gacrux`, `Kore`, `Laomedeia`, `Leda`, `Pulcherrima`, `Sulafat`,
   `Vindemiatrix`, `Zephyr`); the autocomplete labels each `(ชาย)` or `(หญิง)`
   (the chosen value, the session card and the dashboard show the bare name,
   e.g. `Puck`). Type part of a name, or `หญิง`/`ชาย`, to filter (Discord shows
   25 at a time). Female voices are only picked, never handed out at random.
   Each assignment logs `tts: session voice guild=… user=… voice=… round=…`.
   With an empty `TTS_MODEL` the bare names map to `<lang>-Chirp3-HD-<Name>`.
3. Anyone else in **the same voice channel** runs `/tts join` **in the text
   channel they want read**. They are added. If that channel has no card yet,
   the bot posts one there; the cards in all channels are **edited** to show
   the new person. There is **one card per text channel** that has (or had)
   participants in the session, and every card lists everyone.
4. Running `/tts join` again **in another text channel moves you**: from then
   on your messages are read there, no longer in the old channel, and the
   reply says so. Running it again in the channel you are already read in
   (without changing `voice:`) changes nothing.
5. `/tts leave` removes you and edits all the cards. Leaving the voice channel
   (disconnecting or moving to another voice channel) does the same without
   the command. When the last participant leaves, the session ends.
6. While the session runs, each participant message is **deleted a few
   seconds after it has been read aloud** (below), in every channel.
7. When a session ends (last participant left, nobody but bots left in the
   voice channel, the bot was disconnected or moved, or the bot shuts down),
   the bot **deletes the messages the participants wrote** in the channels
   they were read in during the session that are still there and turns every
   card into the same short summary that stays in its channel as a log:

   ```
   📝 จบการอ่าน · @a @b · 25 นาที · ลบ 42 ข้อความ
   ```

   The count (over all channels) includes the messages deleted along the way.
   Failures show as `(ลบไม่ได้ N)`; if the bot could not delete in any channel
   it says `ไม่ได้ลบข้อความ (ไม่มีสิทธิ์)`.

Refusals are private and say what to do instead:

- `/tts join` from a **different voice channel** while the session's voice
  channel still has people in it: refused, naming the busy channel. If nobody
  is left there, the old session is ended (with clean-up) and the new one
  starts.
- `/tts leave` when you are not in the session, or no session runs.

A message is read only if its author is a participant **and** it was sent in
the channel that participant joined from. A participant's messages in other
channels are neither read nor deleted while the session runs (but see the
clean-up rules below for channels they were bound to earlier).

### What is spoken

- Bot, webhook and SONNIE's own messages are ignored, and so are system
  messages (joins, pins, thread starters, command responses): only normal
  messages and replies from people count.
- Normalisation (`internal/readaloud/text.go`): mentions become names (from the
  gateway cache; anything not cached is dropped, never fetched over REST);
  URLs become "ลิงก์" (or "link" when `TTS_LANG` is not Thai); custom emoji,
  timestamps, spoilers and code blocks are dropped; markdown markers are
  stripped; whitespace is collapsed.
- **Long text is cut, not skipped:** a message longer than `TTS_MAX_CHARS`
  (default 200 characters, never splitting a Thai vowel or tone mark from its
  base character) is read up to that point.
- `TTS_SPEAK_AUTHOR=true` (default) prefixes each message with the author's
  server nickname or display name.
- A message is **not spoken** when nothing is left after filtering (empty
  text, attachment only, custom emoji only, a spoiler or code block only), when
  its server's queue was full and it was the oldest waiting message (the
  **oldest waiting message is dropped**, because in a live chat the newest
  messages matter most; `TTS_QUEUE_SIZE`, default 20 per server), when
  synthesis failed or timed out (30 s), or when the monthly budget or
  character limit was reached (the first refusal each month is logged once:
  `tts: … messages are skipped until next month`). A message made only of a
  link is not dropped: it is read as "ลิงก์".
- The bot leaves automatically (ending the session, with clean-up) when
  nobody (other than bots) is left in the voice channel, or if it is
  disconnected from voice or moved to another channel.

### Deleting each message after it is read (`TTS_DELETE_AFTER`)

When the player has **finished playing** a participant's message (all audio
sent, not merely queued), the bot deletes that message after
`TTS_DELETE_AFTER` (default `8s`; `0` turns this off and leaves only the
clean-up at the end). Participant messages in the channel they are read in
that are **not spoken** are deleted after the same delay, counted from the
moment the bot gives up on them, so the channel stays clean: messages with
nothing left to say after filtering, and ones skipped because the queue was
full, synthesis failed or the monthly budget was reached. Messages of people
who have left the session, of people who never joined, and of participants in
a channel they are not read in are not touched this way.

- Only in channels where the bot had **Manage Messages** when that channel's
  card was posted (checked per channel).
- One single delete per message, in the background; speech never waits for it.
- A failed delete is logged once (`deleting message … after it was read`) and
  the message stays on the list for the clean-up at the end, which retries it.
  Deleted messages leave the list and are counted in the summary.
- When the session ends or the bot shuts down, pending timers are cancelled
  and the end-of-session clean-up (which scans the channel history) takes
  whatever is left, so nothing is deleted twice or missed.

### End-of-session clean-up: what is deleted, exactly

The session remembers a record of **(user, text channel, first join time)** for
everyone who took part at any time (including people who joined and later
left). In each channel it deletes the messages of the users bound to that
channel, created **after that person first ran `/tts join` there and before
the session ended**. A user who moved from `#a` to `#b` has both channels
cleaned: `#a` from their first join there, `#b` from their first join there,
including what they wrote in `#a` after moving away. Messages written after
`/tts leave` in a bound channel are also deleted at the end.

Never touched: messages by people who did not join, what a participant wrote
in a channel before they joined it, messages in channels they were never bound
to, anything from before the channel's card, and the bot's own cards. Because
anything a participant writes in a bound channel during the session is
deleted, `/tts join` tells people so.

- **Bulk delete and the 14-day rule.** Messages younger than 14 days go through
  Discord's bulk delete (chunks of at most 100; the cut-off is 14 days minus
  one hour of margin so a message cannot cross Discord's limit while the call
  is in flight); older ones are deleted one by one. If a bulk delete fails the
  messages are deleted one by one, and the failure is logged.
- **How messages are found.** The bot remembers the participants' messages as
  they arrive, and scans each channel's history after its card when the
  session ends (up to 10,000 messages per channel, 100 pages of 100), so a
  missed gateway event or a restart does not leave messages behind. In a
  channel with more than 10,000 messages after the card, the clean-up falls
  back to the messages it saw live.
- **Manage Messages is checked per channel** when that channel's card is
  posted. If it is missing there, that card turns orange and says nothing will
  be deleted in that channel, and the channel is skipped at the end; reading
  works as usual. Participants are told when they join whether their messages
  will be deleted, so the bot does not delete anything in a channel where it
  did not have the permission then.

### Restarts: recovery of unfinished sessions

If the bot **restarts** during a session, the session is lost (it leaves
voice). Every card carries a hidden marker in its embed URL
(`https://sonnie.invalid/tts#<base64url>`; cards from older versions had it in
the footer and are still read) with the whole session: the text channels, who
is read where, who left, and the (user, channel, join time) records, so each
card alone is enough to recover everything it names. The marker is **v2**
(`sonnie-tts-session v2 state=open|ended started=… voice=… cards=…
participants=… left=… binds=…`); **v1** markers from older versions (one text
channel, no join times) are still understood and cleaned from the card on.

When a guild becomes available after a start, the bot looks through the last
**300 messages of each channel it can read** (View Channel and Read Message
History; 3 pages of 100) for its own unfinished cards, **groups** the cards of
one session (v2 cards that name the same voice channel; a v1 card is a session
of its own), merges their markers, finishes the clean-up in every channel
where it found a card and turns all of them into the summary. Limits:

- A card that has been pushed out of that 300-message window by a busy channel
  is left as it is, and its channel is not cleaned.
- A channel the bot cannot read is skipped.
- A session started since the restart owns its own channels from its card on,
  so recovery stops there.
- If the shutdown clean-up does not finish within its grace period the bot logs
  `tts: clean-up did not finish in …; unfinished sessions are completed on the
  next start`, and recovery completes them on the next start.

Each session start, join, move, leave and end is logged as one line (user IDs,
channel IDs and counts) for `kubectl logs`, e.g. `tts: session end guild=…
channels=… reason="last participant left" users=… duration=25m0s deleted=42
failed=0`.

## Speech synthesis

### Engines and voices

`tts.Google` calls the Google Cloud Text-to-Speech v1 REST API (`OGG_OPUS` at
48 kHz). Without `TTS_MODEL` it uses Chirp 3: HD / Neural2 voices (defaults
`th-TH-Neural2-C` for Thai and `en-US-Neural2-F` for English, overridable with
`TTS_VOICE`; `TTS_VOICE` is also the fallback when no per-participant voice is
attached). With a Gemini `TTS_MODEL` it uses Gemini-TTS (below).
`voice.OggOpusEncoder` demuxes the Ogg pages into 20 ms Opus frames in pure Go
(no cgo, no `ffmpeg`). Voice transport (disgo voice gateway v8, AEAD
transport encryption, DAVE end-to-end encryption from dave-go; frames are
paced at 20 ms and held until the DAVE handshake is done) is described in
[voice-transport.md](voice-transport.md).

### Gemini-TTS

Setting `TTS_MODEL` to a Gemini model (`gemini-2.5-flash-tts`) sends the same
Cloud Text-to-Speech `v1/text:synthesize` request with Gemini fields:

```json
{"input":{"text":"...","prompt":"<TTS_PROMPT>"},
 "voice":{"languageCode":"th-TH","name":"Charon","modelName":"gemini-2.5-flash-tts"},
 "audioConfig":{"audioEncoding":"OGG_OPUS","sampleRateHertz":48000}}
```

- `TTS_VOICE` is the bare speaker name (default `Charon`), not a `th-TH-...`
  voice name; per-participant voices are bare names too.
- `TTS_LANG` still picks `languageCode` (`th` → `th-TH`, `en` → `en-US`).
- `TTS_PROMPT` is a natural-language style instruction (e.g. `พูดแบบเป็นกันเอง
  เหมือนเพื่อนคุยกันในวงเกม`). It is sent only to Gemini models, never to
  Chirp/Neural2, which reject it. Its length is billed as input on every
  message, so keep it short.
- Authentication is a service account (`GOOGLE_APPLICATION_CREDENTIALS`, see
  Setup); API keys are rejected for Gemini models.
- The audio is the same 48 kHz Ogg/Opus (single 20 ms frames), so the voice
  pipeline needs no change.
- **Latency:** with the unary REST call (`TTS_STREAMING=false`) expect roughly
  **3–4 s more** per message than Chirp 3 HD before speech starts (about 4 s
  for one sentence vs about 1 s), because the whole clip is generated before
  it is returned. Streaming, the default, is meant to cut that (about 400 ms).
  Messages queue as before, so a busy chat falls further behind.

### Streaming (Gemini)

With a Gemini `TTS_MODEL` and `GOOGLE_APPLICATION_CREDENTIALS`, the bot uses
the gRPC `StreamingSynthesize` call of Cloud Text-to-Speech
(`texttospeech.googleapis.com:443`) instead of `v1/text:synthesize`. It is on
by default; `TTS_STREAMING=false` goes back to the unary REST call. Chirp and
Neural2 voices always use REST. Streaming needs the service account: with only
`GOOGLE_TTS_API_KEY` the bot logs that and uses REST. If the gRPC connection
cannot be set up at startup it logs the error and uses REST.

- **Request shape:** one call per message: a `StreamingSynthesizeConfig` (same
  voice, language and `model_name` as the REST request, `OGG_OPUS` at 48 kHz),
  then one `StreamingSynthesisInput` with the text and `TTS_PROMPT`. Same
  service account, same token source as the REST client.
- Each `audio_content` chunk goes straight into an incremental Ogg demuxer;
  every Ogg page's Opus packets are queued for sending as soon as the page is
  complete, and playback starts with the first one while the rest is still
  arriving. Pacing (20 ms), the queue and "one message at a time" are
  unchanged.
- `/tts leave`, the bot leaving on its own, or shutdown cancel the call in
  flight. **A stream that sends no audio for 30 s is abandoned** (and so is a
  non-streamed synthesis that takes longer than 30 s).
- **Fallback:** if the stream fails before any audio arrives (an error, or the
  call ends without audio), the bot logs
  `tts: google: stream failed before any audio, retrying once with REST: ...`
  and synthesizes that message once with the REST call. A failure after audio
  has started is reported and not retried.
- **Billing:** a streamed message is charged when its stream ends, for the
  audio actually received (see
  [tts-cost-and-persistence.md](tts-cost-and-persistence.md#money-gemini)),
  including a stream cut off by an error or by the bot leaving.

On startup the bot logs `tts: streaming gemini-2.5-flash-tts over gRPC
(TTS_STREAMING); the REST API is the fallback` when streaming is active.

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
`kubectl logs <pod> | grep 'tts: timing'`. The dashboard's "งบ Google TTS"
panel shows the recent timings too.

### Host requirements

None beyond network access to Discord and `texttospeech.googleapis.com`
(HTTPS for REST; gRPC over HTTP/2 on the same host and port 443 for
streaming, so an egress proxy or firewall must allow HTTP/2 there). Google
Cloud TTS returns 48 kHz Ogg/Opus directly and SONNIE demuxes it in pure Go,
so neither cgo nor `ffmpeg` is needed at runtime and the container image
remains `distroless/static`.
