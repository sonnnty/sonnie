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
configuration from environment variables only. Its one inbound port is the
HTTP server on 8080 (dashboard, webhooks, `/healthz`; see
[Dashboard & webhooks](#dashboard--webhooks)).

```bash
docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t sonnie .
docker run --rm --env-file .env -p 8080:8080 sonnie
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
A command can also answer autocomplete (`Command.Autocomplete`) and its own
buttons (`Command.Component`, for custom IDs starting with
`ComponentPrefix`); see `event.go`.

## Adding a feature: register a dashboard panel

Every feature shows its state on the dashboard as one panel. When you add
a feature:

1. Give it a cheap, read-only status method (an in-memory snapshot; never a
   Discord call), like `readaloud.Manager.Status` or `events.Scheduler.Status`.
2. Write a panel constructor in `internal/panels/` that takes that status
   behind a small interface and returns a `dashboard.Panel{ID, Title,
   Snapshot}` whose snapshot is a JSON-ready struct. Test it with a fake.
3. Append it in `dashboardPanels` in `internal/bot/dashboard.go`.
4. Optionally add a renderer under the same ID in
   `internal/dashboard/static/app.js`. Without one the panel is shown as
   plain JSON, so it works before it looks nice. Build DOM with
   `textContent` only (the `el()` helper), never `innerHTML`.

## Server events (`/event`)

`/event` creates and manages the server's Discord **Scheduled Events**, with a
cover picture the bot finds on image libraries (optionally the web, see Covers) by itself. It is separate
from `/tts`: it never joins voice or reads chat, needs no privileged intent,
and is always on.

| Command | What it does |
|---|---|
| `/event create` | Opens a form (below). |
| `/event create name when [place] [location] [description] [end] [image] [keywords]` | For power users: with any option there is no form, and `name`, `when` and one of `place` (voice channel) or `location` (text or a link) are needed. Without `image` the bot finds a cover. Replies in the channel with the event (time, place, cover) and a **🔄 เปลี่ยนรูป** button. |
| `/event cover event [keywords] [image]` | With `image`, uses that picture. Otherwise shows you (only you) 5 pictures with buttons **1**–**5** and **🔎 ค้นใหม่** (next page); a click puts the picture on the event. |
| `/event edit event [name] [when] [end] [place] [location] [description]` | Changes what you give. Moving the start keeps the event's length. |
| `/event cancel event` | Asks to confirm, then cancels (an event that is already running is ended instead). |
| `/event list` | Upcoming events, soonest first, with the number of people interested. |

The `event` option autocompletes from the upcoming events (`ศ. 6 พ.ย. 21:00 · เตะบอล`).
Replies are compact Thai embeds; everything except the `/event create` reply is
visible only to you.

### The form

`/event create` without options opens a form **สร้าง Event**:

| Field | What |
|---|---|
| ชื่อ event | Required, up to 100 characters. |
| วัน | A list: today and the next 24 days in Asia/Bangkok (`วันนี้ (พุธ 4 พ.ย.)`, `พรุ่งนี้ (พฤหัส 5 พ.ย.)`, `ศุกร์ 6 พ.ย.`, …); today is preselected. |
| เวลา | Prefilled `20:00`; any time of day from [Times](#times) (`3 ทุ่ม`, `บ่าย 2`, `19.30`), on the chosen day. A date in this field is refused (pick it in วัน); a time that has already passed today is refused with what you typed. |
| ห้องเสียง | A voice channel, optional; preselected with the one you are in. Left empty, the event is held outside voice with location `Discord` (the reply says so; change it with `/event edit location:`). |
| รายละเอียด | Optional, up to 1000 characters (shortened at the end if the cover credit needs the room). |

The event lasts 2 hours (change it with `/event edit end:`), gets a cover
found by the bot and the same public reply as with options. Mistakes are
answered privately; open the form again with `/event create`. A form left
open across a bot update that changed the form is refused as outdated.

The Discord library the bot uses (discordgo v0.29) cannot read forms built
from Label components yet, so the bot reads the submitted form from the
raw gateway event. discordgo still logs one harmless line per submitted
form: `error unmarshalling INTERACTION_CREATE event, ... unknown component
type: 18`.

**Who may use it:** `create` needs **Create Events** or **Manage Events**;
`edit`, `cover` and `cancel` need **Manage Events** or being the event's
creator (the person who ran `/event create`, or who made the event in
Discord). Others get a private refusal. The bot itself needs **Manage
Events** and **Create Events** (Administrator covers both), plus **View
Channel** and **Send Messages** in the reminder channel.

### Times

`when` and `end` are read in Asia/Bangkok, in Thai or simple English:

| You type | Means |
|---|---|
| `ศุกร์นี้ 3 ทุ่ม` | the coming Friday, 21:00 (`ศุกร์หน้า` = Friday of next week) |
| `พรุ่งนี้ 20:00`, `มะรืน 10.30`, `คืนนี้ ตีสอง` | tomorrow 20:00, the day after 10:30, tonight 02:00 |
| `วันเสาร์ 14.30`, `12/11 19:00`, `20 พ.ย. 69 19:00`, `2026-11-20 19:00` | dates (day/month; years in CE or BE) |
| `บ่ายสอง`, `สองทุ่มครึ่ง`, `เจ็ดโมงเช้า`, `ห้าโมงเย็น`, `เที่ยง`, `เที่ยงคืน`, `ตี 5` | 14:00, 20:30, 07:00, 17:00, 12:00, 00:00, 05:00 |
| `tomorrow 8pm`, `next friday 21:00`, `nov 20 7:30pm` | English |

A time without a date is the next time it comes (today or tomorrow). Times
in the past are refused. `end` also takes a length (`3 ชม.`, `90 นาที`,
`1h30m`); without `end` an event lasts 2 hours.

### Covers

1. **Search words.** `keywords` if you give them; otherwise the event name.
   Thai names are turned into 2–4 English keywords by Gemini
   (`gemini-2.5-flash-lite` on Vertex AI, with the same service account as
   Gemini-TTS in `GOOGLE_APPLICATION_CREDENTIALS`; needs
   `roles/aiplatform.user`). The call gets 3 s and falls back to the name.
   Each call logs one line; a permission error is logged once and the
   translation is then skipped until restart. It is not counted by the TTS
   money meter; one short call per event costs next to nothing.
2. **Search.** The libraries with a key are asked first, in this order:
   Brave web images (`BRAVE_SEARCH_API_KEY`, see below), Unsplash
   (`UNSPLASH_ACCESS_KEY`), Pexels (`PEXELS_API_KEY`); **Openverse** (no
   key) is always last. The 5 slots of `/event cover` are filled in that order,
   asking the next library only when slots are still free and skipping a
   picture whose image URL is already in the list. Within a library,
   landscape pictures at least 800 px wide come first.
3. **Cover.** The picture (JPEG, PNG or WebP, at most 10 MB) is cropped to
   5:2 around its centre and scaled to 800×320, in pure Go.

**Licensing.** Openverse results are limited to licences that allow reuse
and changes: CC0, Public Domain Mark, CC BY and CC BY-SA (no NC, no ND).
Pexels and Unsplash pictures are under their own free licences. Every found
cover is credited on the last line of the event description, e.g.
`📷 “Football at night” — Jane Doe · CC BY-SA 2.0 · https://www.flickr.com/…`;
the line is replaced when the cover changes and kept when you edit the
description. For Unsplash the bot also reports the download, as Unsplash's
API terms require. A picture you attach yourself gets no credit line — make
sure you may use it.

**Brave web images — no licence.** With `BRAVE_SEARCH_API_KEY` set, the bot
also searches the web for pictures with the Brave Search API
(`safesearch=strict`, 20 results). These are **ordinary web images with no
licence** — nobody has allowed their reuse. They are used because the owner
asked for it, on a private, non-commercial friends' server; do not enable
this on a public or commercial bot. Their credit line claims no licence,
only where the picture is from: `📷 example.com · https://example.com/page`.
Results are filtered: stock-photo/watermark sites (Getty, iStock,
Shutterstock, Alamy, Dreamstime, 123RF, Depositphotos, Adobe Stock, Vecteezy,
Freepik, Magnific, Pond5, CanStockPhoto, Bigstock, Age Fotostock; the list is
`stockDomains` in `internal/cover/brave.go`), pictures under 800 px wide or
not landscape (width/height < 1.2) when their size is known, and anything
that is not an https/http JPEG, PNG or WebP URL (https first). If the full
picture cannot be downloaded the next result is tried, never the thumbnail.
**Brave first, Unsplash when the credit is gone.** Brave is tried first
because web search finds game titles and specific things the photo
libraries do not have. The owner's Brave plan has a monthly credit; when
Brave answers HTTP 402, or 429 with no requests left in its *monthly*
rate-limit window (`X-RateLimit-Remaining`, whose values come per window,
e.g. `0, 0`), the bot **pauses Brave until the quota resets** — read from
`X-RateLimit-Reset` (seconds), else the 1st of next month 00:00 UTC — and
the other libraries (Unsplash first) fill the covers without calling Brave.
A plain per-second 429 is not exhaustion: that one request just falls
through to the next library. When a pause starts the bot posts one message
in `EVENT_CHANNEL_ID` (else `TTS_ALERT_CHANNEL_ID`), e.g. `⚠️ เครดิต Brave
Search เดือนนี้หมดแล้ว — ใช้รูปจาก Unsplash แทนจนถึง <date>`, once per pause,
and logs a line. **The pause is kept in memory only**: after a restart Brave
is tried again and pauses again on its first 402 (one wasted call, and one
more alert).

**To turn Brave off, unset `BRAVE_SEARCH_API_KEY`** (and restart): the bot
then uses only the licensed libraries.

### Reminders and automatic start/end

- **Reminder:** `EVENT_REMIND_BEFORE` (default 15 min) before the start the
  bot posts one message in `EVENT_CHANNEL_ID` (or `TTS_ALERT_CHANNEL_ID`)
  mentioning everyone who pressed *Interested*, with the event link (Discord
  shows it as a card with the cover). No channel, or `0`, turns reminders
  off.
- **Start/end:** at the start time the bot sets the event to *active*; for
  events outside voice it sets them to *completed* at the end time (Discord
  ends voice events itself when the channel empties). Events created with
  `/event` are always handled; events made by people in Discord only with
  `EVENT_AUTO_START=true` (the default). Stage events are left alone.
- **Restarts:** the bot reads every server's events when it connects and
  keeps them current from gateway events. A reminder whose time passed while
  the bot was down is skipped (never sent late or twice); an event that
  should be running is started, as long as its end has not passed.

**Logs.** Every rejected input logs one line with the user, the
subcommand, the field, what was typed (first 80 characters) and the error,
e.g. `events: rejected /event create input from user 42: field when, value
"<@123> พรุ่งนี้": …`. When `when` or `end` contains a mention the reply
adds `ช่อง when ใส่เวลาเท่านั้น`.

Kept in memory only, so lost on restart: open cover pickers (their buttons
then say to run `/event cover` again) and who ran `/event create` for an
event (after a restart only Manage Events can change the bot's own events).
An event created less than `EVENT_REMIND_BEFORE` before its start gets no
reminder.

### Settings

| Variable | Default | Meaning |
|---|---|---|
| `EVENT_CHANNEL_ID` | `TTS_ALERT_CHANNEL_ID` | Channel for reminders. |
| `EVENT_REMIND_BEFORE` | `15m` | How long before the start to remind; `0` = off. |
| `EVENT_AUTO_START` | `true` | Also start/end events the bot did not create. |
| `PEXELS_API_KEY` | – | Optional; adds Pexels (after Unsplash) before Openverse. |
| `UNSPLASH_ACCESS_KEY` | – | Optional; adds Unsplash before Openverse; it takes over while Brave is paused for the month. |
| `BRAVE_SEARCH_API_KEY` | – | Optional secret; adds Brave web image search (unlicensed web images, private server only), tried first; paused until the monthly reset when its credit runs out. Unset = off. |
| `GOOGLE_APPLICATION_CREDENTIALS` | – | Service account for the Thai → English keywords (shared with TTS). |

Network: HTTPS to `api.openverse.org`, `api.pexels.com`, `api.unsplash.com`,
`api.search.brave.com`, `aiplatform.googleapis.com`, and to the hosts the pictures live on (Flickr,
Wikimedia and others for Openverse).

## Read chat aloud (`/tts`)

Reading aloud is **opt-in per person** and lives in a **session**. `/tts join`
(run in any text channel, while you are in a voice channel) joins the session:
from then on the bot reads **your** messages **in the channel where you ran
it** aloud in the voice channel, and nobody else's. Every participant picks
their own channel, so a session can read several text channels at once, but the
voice channel is one per server (Discord allows a single voice connection per
bot per server). `/tts leave` takes you out again. `/tts usage` shows this
month's Google TTS usage, in characters or baht (see
[Usage cap and alerts](#usage-cap-and-alerts)). Each server has at most one
session.

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
   assigned voice, grouped by the
   channel they are read in; the footer (small text) says how to join and
   leave and what happens to messages. It reads `ลบข้อความเมื่อจบ` when
   `TTS_DELETE_AFTER=0` and `⚠️ ไม่มีสิทธิ์ลบข้อความ` (with an orange embed)
   when the bot lacks Manage Messages in **that** channel. The session state that
   lets a restarted bot finish the session is hidden in the embed's URL (an
   embed without a title never shows it) and written to the log as
   `tts: session state …` on every change.
2. **Voices per participant.** Each participant in a session speaks with their
   own male voice from the pool (`TTS_VOICE_POOL`, defaulting to the 16
   Gemini-TTS male voices: `Achird`, `Algenib`, `Algieba`, `Alnilam`, `Charon`,
   `Enceladus`, `Fenrir`, `Iapetus`, `Orus`, `Puck`, `Rasalgethi`, `Sadachbia`,
   `Sadaltager`, `Schedar`, `Umbriel`, `Zubenelgenubi`). A new participant gets
   a random voice not yet handed out in the current round; once all pool voices
   have been handed out in the session, a new round starts with the full pool
   available again. You keep your voice for the whole session — including after
   moving to another text channel, `/tts leave` + rejoin, or disconnecting from
   voice and rejoining; a new session starts fresh. Run `/tts join voice:<choice>`
   to pick a specific voice (or change your current one); a picked voice counts
   as used in the current round. `voice:` is an autocomplete list of the pool
   voices and the 14 Gemini-TTS female voices (`Achernar`, `Aoede`, `Autonoe`,
   `Callirrhoe`, `Despina`, `Erinome`, `Gacrux`, `Kore`, `Laomedeia`, `Leda`,
   `Pulcherrima`, `Sulafat`, `Vindemiatrix`, `Zephyr`), each labelled `(ชาย)` or
   `(หญิง)`; type part of a name, or `หญิง`/`ชาย`, to filter (Discord shows 25
   at a time). Female voices are only picked, never handed out at random. Each assignment logs
   `tts: session voice guild=… user=… voice=… round=…`.
3. Anyone else in **the same voice channel** runs `/tts join` **in the text
   channel they want read**. They are added. If that channel has no card yet,
   the bot posts one there; the cards in all channels are **edited** to show
   the new person. There is **one card per text channel** that has (or had)
   participants in the session, and every card lists everyone.
4. Running `/tts join` again **in another text channel moves you**: from then
   on your messages are read there, no longer in the old channel, and the reply
   says so. Running it again in the channel you are already read in (without
   changing `voice:`) changes nothing.
5. `/tts leave` removes you and edits all the cards. Leaving the voice
   channel (disconnecting or moving to another voice channel) does the same
   without the command. When the last participant leaves, the session ends.
6. While the session runs, each participant message is **deleted a few
   seconds after it has been read aloud** (see below), in every channel.
7. When a session ends (last participant left, nobody but bots left in the
   voice channel, the bot was disconnected or moved, or the bot shuts down),
   the bot **deletes the messages the participants wrote** in the channels they
   were read in during the session that are still there and turns every card
   into the same short summary that stays in its channel as a log:

   ```
   📝 จบการอ่าน · @a @b · 25 นาที · ลบ 42 ข้อความ
   ```

   The count (over all channels) includes the messages deleted along the way.
   Failures show as `(ลบไม่ได้ N)`; if the bot could not delete in any channel
   it says `ไม่ได้ลบข้อความ (ไม่มีสิทธิ์)`.

Refusals are private and say what to do instead:

- `/tts join` from a **different voice channel** while the session's voice
  channel still has people in it: refused, naming the busy channel. If nobody
  is left there, the old session is ended (with clean-up) and the new one starts.
- `/tts leave` when you are not in the session, or no session runs.

A message is read only if its author is a participant **and** it was sent in
the channel that participant joined from. A participant's messages in other
channels are neither read nor deleted while the session runs (but see the
clean-up rules below for channels they were bound to earlier).

#### Deleting each message after it is read (`TTS_DELETE_AFTER`)

When the player has **finished playing** a participant's message (all audio
sent, not merely queued), the bot deletes that message after
`TTS_DELETE_AFTER` (default `8s`, i.e. the 5–10 s window; `0` turns this off
and leaves only the clean-up at the end). Participant messages in the channel
they are read in that are **not spoken** are deleted after the same delay,
counted from the moment the bot gives up on them, so the channel stays clean:
messages with nothing left to say after filtering (only a link, emoji or
attachment), and ones skipped because the queue was full, synthesis failed or
the monthly budget was reached. Messages of people who have left the session,
of people who never joined, and of participants in a channel they are not read
in are not touched this way.

- Only in channels where the bot had **Manage Messages** when that channel's
  card was posted (checked per channel).
- One single delete per message, in the background; speech never waits for it.
- A failed delete is logged once (`deleting message … after it was read`) and
  the message stays on the list for the clean-up at the end, which retries it.
  Deleted messages leave the list and are counted in the summary.
- When the session ends or the bot shuts down, pending timers are cancelled
  and the end-of-session clean-up (which scans the channel history) takes
  whatever is left, so nothing is deleted twice or missed.

What gets deleted at the end, exactly: the session remembers a record of
**(user, text channel, first join time)** for everyone who took part at any
time (including people who joined and later left). In each channel it deletes
the messages of the users bound to that channel, created **after that person
first ran `/tts join` there and before the session ended**. A user who moved
from `#a` to `#b` has both channels cleaned: `#a` from their first join there,
`#b` from their first join there, including what they wrote in `#a` after
moving away. Messages written after `/tts leave` in a bound channel are also
deleted at the end. Never touched: messages by people who did not join, what a
participant wrote in a channel before they joined it, messages in channels they
were never bound to, anything from before the channel's card, and the bot's
own cards. Messages younger than 14 days go through Discord's bulk delete
(chunks of at most 100); older ones are deleted one by one. The bot also
remembers the participants' messages as they arrive, and scans each channel's
history after its card when the session ends, so a missed event or a restart
does not leave messages behind. Because anything a participant writes in a
bound channel during the session is deleted, `/tts join` tells people so.

**Manage Messages** is needed for the deletion. The bot checks it in each text
channel when that channel's card is posted. If it is missing there, that card
turns orange and says nothing will be deleted in that channel, and the channel
is skipped at the end; reading works as usual. Participants are told when they
join whether their messages will be deleted, so the bot does not delete
anything in a channel where it did not have the permission then.

If the bot **restarts** during a session, the session is lost (it leaves
voice). Every card carries a hidden marker in its embed URL
(`https://sonnie.invalid/tts#<base64url>`; cards from older versions had it in
the footer and are still read) with the whole session: the text channels, who is read where,
who left, and the (user, channel, join time) records, so each card alone is
enough to recover everything it names. The marker is **v2**
(`sonnie-tts-session v2 … cards=… participants=… left=… binds=…`); v1 markers
from older versions (one text channel, no join times) are still understood and
cleaned from the card on. When the bot comes back it looks through the last 300
messages of each channel it can read for its own unfinished cards, groups the
cards of one session (v2 cards that name the same voice channel; a v1 card is
a session of its own), merges their markers, and finishes the clean-up in every
channel where it found a card and turns all of them into the summary. A card
that has been pushed out of that window by a busy channel is left as it is, and
its channel is not cleaned. A session started since the restart owns its own
channels from its card on, so recovery stops there.

Each session start, join, move, leave and end is logged as one line (user IDs,
channel IDs and counts) for `kubectl logs`, e.g. `tts: session end guild=…
channels=… reason="last participant left" users=… duration=25m0s deleted=42
failed=0`.

### Status

What works now:

- `/tts join` and `/tts leave`: opt-in sessions, one per server and one voice
  channel, with one session card per text channel and clean-up when it ends
  (see above). Only participants' messages, in the channel they joined from,
  are spoken.
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
   participants' messages, after they are read and when the session ends;
   without it reading works but nothing is deleted). When inviting the bot, include the `bot` and
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
| `TTS_VOICE`          | *(empty)* | Fallback Google voice name when no per-participant session voice is attached (e.g. `th-TH-Chirp3-HD-Achird`, or bare speaker name `Charon` with a Gemini model) |
| `TTS_VOICE_POOL`     | 16 Gemini male voices | Comma-separated pool of speaker voices handed out per participant in a session (`Achird,Algenib,Algieba,Alnilam,Charon,Enceladus,Fenrir,Iapetus,Orus,Puck,Rasalgethi,Sadachbia,Sadaltager,Schedar,Umbriel,Zubenelgenubi`). When `TTS_MODEL` is empty, bare names map to `<lang>-Chirp3-HD-<Name>` |
| `TTS_MODEL`          | *(empty)* | Gemini-TTS model, e.g. `gemini-2.5-flash-tts`. Empty = Chirp/Neural2 as before (see [Gemini-TTS](#gemini-tts)) |
| `TTS_PROMPT`         | *(empty)* | Style prompt sent as `input.prompt`; Gemini models only, ignored otherwise |
| `TTS_STREAMING`      | `true` with a Gemini `TTS_MODEL`, else `false` | Gemini only: synthesize over the gRPC streaming API and start speaking at the first audio (see [Streaming](#streaming-gemini)). Needs `GOOGLE_APPLICATION_CREDENTIALS`. Ignored for Chirp/Neural2 |
| `TTS_MONTHLY_BUDGET_THB` | `300` | Gemini only: money cap in baht per US/Pacific month; reading pauses once spend reaches it. `0` = no cap |
| `TTS_USD_TO_THB`     | `33`    | Gemini only: exchange rate used to turn the USD price into baht |
| `TTS_MAX_CHARS`      | `200`   | Max characters spoken per message                             |
| `TTS_QUEUE_SIZE`     | `20`    | Max messages waiting per server; oldest is dropped            |
| `TTS_DELETE_AFTER`   | `8s`    | Go duration: delete each participant's message this long after it has been read aloud (or skipped). `0` = only clean up when the session ends |
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
with no visible marker. The machine-readable marker is hidden in the embed URL
(`https://sonnie.invalid/usage#<base64url>`; the embed has no title, so Discord
does not show the URL; older messages had the marker in the footer and are
still read):

```
sonnie-tts-usage v2 month=2026-11 unit=chars value=12345 audio_ms=0 alerted=50,80
sonnie-tts-usage v2 month=2026-11 unit=thb_micro value=240000000 audio_ms=29000000 alerted=50,80
```

`unit=chars` counts characters; `unit=thb_micro` counts millionths of a baht
(`240000000` = ฿240.00). `audio_ms` is the audio generated this month.

On startup the bot scans the channel's last 100 messages for the newest such
message written by itself and restores the count from it (supporting the URL
marker, the older footer marker and legacy plain-content markers, migrating old-format messages
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

## Dashboard & webhooks

The bot runs a small HTTP server (`DASHBOARD_ADDR`, default `:8080`) with:

- **`GET /healthz`**: `ok` (200) while the Discord gateway is connected,
  503 otherwise. No login. For Kubernetes liveness/readiness probes.
- **The dashboard** at `/`: a read-only monitoring page behind Discord
  login, in Thai, light/dark aware and usable on a phone. It refreshes
  every 5 seconds and has one panel per feature: the bot (online, gateway
  ping, uptime, version, guilds, goroutines and memory), read-aloud
  sessions (voice channel, text channels, participants with their channel
  and voice, start, queue, messages read and deleted), the Google TTS
  budget (as in the Discord usage embed, plus recent time-to-first-audio),
  events (upcoming events, pending reminders, the auto start/end
  scheduler, cover providers and Brave's pause), webhooks (hooks and their
  recent calls) and the last 300 log lines with a filter box.
- **`POST /hooks/{name}`**: a generic incoming webhook that posts to a
  Discord channel.

It works over plain HTTP on a LAN address (for example a Kubernetes
NodePort). Set `DASHBOARD_ADDR=` (empty) or `off` to turn the server off.

### Who can see the dashboard

A Discord user can see it when they are listed in `DASHBOARD_ALLOWED_USERS`,
**or** when they own, or have **Administrator** or **Manage Server** in, a
server the bot is in. The bot checks with its own token (guild roles from
the gateway, the user's roles over REST), never the user's token. Anyone
else gets a polite 403 page in Thai. The check is redone at most every 10
minutes per user, so losing the role takes effect within 10 minutes.

### Setting up login

1. In the [Developer Portal](https://discord.com/developers/applications),
   open the bot's application → **OAuth2**.
2. Copy the **Client Secret** (Reset Secret if you never saved it) into
   `DISCORD_CLIENT_SECRET`. The **Client ID** on the same page is the
   default `DISCORD_CLIENT_ID`; you only need to set it if it differs from
   the bot's application.
3. Under **Redirects**, add `<DASHBOARD_PUBLIC_URL>/auth/callback`, e.g.
   `http://10.10.5.35:31120/auth/callback`, and save. It must match exactly
   (scheme, address, port). The bot logs the URI it uses at startup.
4. Set `DASHBOARD_PUBLIC_URL` to the address browsers use to reach the
   server, and `DASHBOARD_SESSION_KEY` to a random secret of at least 32
   bytes:

   ```bash
   openssl rand -base64 48
   ```

The login asks only for the `identify` scope. The session is an encrypted
and signed cookie (AES-256-GCM): `HttpOnly`, `SameSite=Lax`, `Secure` when
the public URL is https, and valid for 7 days. Changing the session key
logs everyone out. `/auth/logout` logs you out.

Without the three login settings the dashboard page says what is missing.
`/healthz` and the webhooks still work. Setting only some of them stops the
bot at startup with an error.

### Webhooks

`WEBHOOKS` (a secret) lists the hooks, comma-separated, as
`name=token@channelID`:

```
WEBHOOKS=grafana=7f3c…long-random…@123456789012345678,ci=9a1e…@123456789012345679
```

Names are lowercase letters, digits, `-` and `_`. Tokens are at least 16
characters (`openssl rand -hex 24`). The bot needs **View Channel** and
**Send Messages** (and **Embed Links** for embeds) in each channel.

Call a hook with its token in `Authorization: Bearer <token>` or
`X-Sonnie-Token: <token>`:

```bash
# simple form: becomes one embed
curl -X POST http://10.10.5.35:31120/hooks/grafana \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"title":"CPU สูง","text":"node1 95%","color":"#ff8800","url":"https://grafana.example/d/abc"}'

# Discord-webhook-like
curl -X POST http://10.10.5.35:31120/hooks/ci \
  -H "X-Sonnie-Token: $TOKEN" -H "Content-Type: application/json" \
  -d '{"content":"build ok","embeds":[{"title":"main @ 1a2b3c","color":5763719}]}'

# plain text
curl -X POST http://10.10.5.35:31120/hooks/ci -H "X-Sonnie-Token: $TOKEN" \
  -H "Content-Type: text/plain" --data 'deploy finished'
```

- Success is `200 {"ok":true,"message_id":"…","channel_id":"…"}`.
- An unknown hook and a wrong token both answer `401`, so the answer never
  says which hooks exist.
- Body at most 64 KB (`413`). At most 30 calls per minute per hook (`429`
  with `Retry-After`). Bad JSON or nothing to post gives `400`, another
  content type `415`, and Discord refusing the message `502`.
- Embeds are rebuilt from the known fields and cut to Discord's limits:
  10 embeds, 6000 characters in total, http(s) URLs only.
- **Nothing pings anyone**: messages are sent with
  `allowed_mentions: {parse: []}`, so `@everyone`, roles and users stay
  plain text.
- Each call (time, hook, status, source IP, size; never the token or body)
  is listed on the dashboard's Webhooks panel.

### Settings

| Variable | Default | Meaning |
|---|---|---|
| `DASHBOARD_ADDR` | `:8080` | Listen address; empty or `off` turns the HTTP server off |
| `DASHBOARD_PUBLIC_URL` | – | Browser-facing URL, e.g. `http://10.10.5.35:31120`; the redirect URI is this + `/auth/callback` |
| `DISCORD_CLIENT_ID` | bot's application ID | OAuth2 client ID |
| `DISCORD_CLIENT_SECRET` | – | OAuth2 client secret (**secret**) |
| `DASHBOARD_SESSION_KEY` | – | ≥ 32 random bytes that seal the session cookie (**secret**) |
| `DASHBOARD_ALLOWED_USERS` | – | Comma-separated user IDs always allowed in |
| `WEBHOOKS` | – | `name=token@channelID,…` (**secret**) |

The log panel shows the bot's own log, the same lines as `kubectl logs`.
The bot never logs its token or keys. As a second line of defence, every
configured secret is replaced with `[redacted]` in the dashboard's copy.
