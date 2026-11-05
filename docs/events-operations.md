# Server events (`/event`): operations reference

`/event` creates and manages the server's Discord **Scheduled Events**, with a
cover picture the bot finds on image libraries (optionally the web, see
[Covers](#covers)) by itself. It is separate from `/tts`: it never joins voice
or reads chat, needs no privileged intent, and is always on.

Users read the Thai manual (`docs/manual/20-events.md`, served at
`/docs/20-events`). The original design notes are in
[events-concept.md](events-concept.md). Environment variables are listed in
[operations.md](operations.md#environment-variables).

| Command | What it does |
|---|---|
| `/event create` | Opens a form (below). |
| `/event create name when [place] [location] [description] [end] [image] [keywords]` | For power users: with any option there is no form, and `name`, `when` and one of `place` (voice channel) or `location` (text or a link) are needed. Without `image` the bot finds a cover. Replies in the channel with the event (time, place, cover) and a **🔄 เปลี่ยนรูป** button. |
| `/event cover event [keywords] [image]` | With `image`, uses that picture. Otherwise shows you (only you) 5 pictures with buttons **1**–**5** and **🔎 ค้นใหม่** (next page); a click puts the picture on the event. |
| `/event edit event [name] [when] [end] [place] [location] [description]` | Changes what you give. Moving the start keeps the event's length. |
| `/event cancel event` | Asks to confirm, then cancels (an event that is already running is ended instead). |
| `/event list` | Upcoming events, soonest first, with the number of people interested. |

The `event` option autocompletes from the upcoming events (`ศ. 6 พ.ย. 21:00 ·
เตะบอล`). Replies are compact Thai embeds; everything except the `/event
create` reply is visible only to you.

## The form

`/event create` without options opens a form **สร้าง Event**:

| Field | What |
|---|---|
| ชื่อ event | Required, up to 100 characters. |
| วัน | A list: today and the next 24 days in Asia/Bangkok (`วันนี้ (พุธ 4 พ.ย.)`, `พรุ่งนี้ (พฤหัส 5 พ.ย.)`, `ศุกร์ 6 พ.ย.`, …); today is preselected. |
| เวลา | Prefilled `20:00`; any time of day from [Times](#times) (`3 ทุ่ม`, `บ่าย 2`, `19.30`), on the chosen day. A date in this field is refused (pick it in วัน); a time that has already passed today is refused with what you typed. |
| ห้องเสียง | A voice channel, optional; preselected with the one you are in (from the gateway state; a stage channel is not offered). Left empty, the event is held outside voice with location `Discord` (the reply says so; change it with `/event edit location:`). |
| รายละเอียด | Optional, up to 1000 characters (shortened at the end if the cover credit needs the room). |

The event lasts 2 hours (change it with `/event edit end:`), gets a cover
found by the bot and the same public reply as with options. Mistakes are
answered privately; open the form again with `/event create`.

**Outdated forms.** The form's custom ID carries a version number
(`formID` in `internal/events/form.go`). A restart alone does not expire an
open form; only an update that changes the form's fields and so its ID does.
A form opened before such an update is refused with `ฟอร์มนี้เก่าแล้ว (บอทเพิ่งอัปเดต)
— เปิดใหม่ด้วย /event create`.

The Discord library the bot uses (discordgo v0.29) cannot read forms built
from Label components (component type 18) yet, so the bot reads the submitted
form from the raw gateway event (`internal/modal`). discordgo still logs one
harmless line per submitted form: `error unmarshalling INTERACTION_CREATE
event, ... unknown component type: 18`.

**Who may use it:** `create` needs **Create Events** or **Manage Events**;
`edit`, `cover` and `cancel` need **Manage Events** or being the event's
creator (the person who ran `/event create`, or who made the event in
Discord). Others get a private refusal. The bot itself needs **Manage Events**
and **Create Events** (Administrator covers both), plus **View Channel** and
**Send Messages** in the reminder channel.

## Times

`when` and `end` are read in Asia/Bangkok, in Thai or simple English
(`internal/eventtime`):

| You type | Means |
|---|---|
| `ศุกร์นี้ 3 ทุ่ม` | the coming Friday, 21:00 (`ศุกร์หน้า` = Friday of next week) |
| `พรุ่งนี้ 20:00`, `มะรืน 10.30`, `คืนนี้ ตีสอง` | tomorrow 20:00, the day after 10:30, tonight 02:00 |
| `วันเสาร์ 14.30`, `12/11 19:00`, `20 พ.ย. 69 19:00`, `2026-11-20 19:00` | dates (day/month; years in CE or BE) |
| `บ่ายสอง`, `สองทุ่มครึ่ง`, `เจ็ดโมงเช้า`, `ห้าโมงเย็น`, `เที่ยง`, `เที่ยงคืน`, `ตี 5` | 14:00, 20:30, 07:00, 17:00, 12:00, 00:00, 05:00 |
| `tomorrow 8pm`, `next friday 21:00`, `nov 20 7:30pm` | English |

A time without a date is the next time it comes (today or tomorrow). Times in
the past are refused. `end` also takes a length (`3 ชม.`, `90 นาที`, `1h30m`);
without `end` an event lasts 2 hours.

## Covers

1. **Search words.** `keywords` if you give them; otherwise the event name.
   Thai names are turned into 2–4 English keywords by Gemini
   (`gemini-2.5-flash-lite` on Vertex AI, with the same service account as
   Gemini-TTS in `GOOGLE_APPLICATION_CREDENTIALS`; needs
   `roles/aiplatform.user`, see [tts-operations.md](tts-operations.md#setup)).
   The call gets 3 s and falls back to the name. Each call logs one line
   (`cover: gemini keywords for "…": "…" (N ms)`); a permission error is logged
   once and the translation is then skipped until restart. It is not counted by
   the TTS money meter; one short call per event costs next to nothing.
2. **Search.** The libraries with a key are asked first, in this order: Brave
   web images (`BRAVE_SEARCH_API_KEY`, see below), Unsplash
   (`UNSPLASH_ACCESS_KEY`), Pexels (`PEXELS_API_KEY`); **Openverse** (no key)
   is always last. The 5 slots of `/event cover` are filled in that order,
   asking the next library only when slots are still free and skipping a
   picture whose image URL is already in the list. Within a library, landscape
   pictures at least 800 px wide come first. The dashboard's events panel lists
   the providers in the order they are tried.
3. **Cover.** The picture (JPEG, PNG or WebP, at most 10 MB) is cropped to 5:2
   around its centre and scaled to 800×320 JPEG, in pure Go
   (`golang.org/x/image`).

### Licensing

Openverse results are limited to licences that allow reuse and changes: CC0,
Public Domain Mark, CC BY and CC BY-SA (no NC, no ND). Pexels and Unsplash
pictures are under their own free licences. Every found cover is credited on
the last line of the event description, e.g. `📷 “Football at night” — Jane
Doe · CC BY-SA 2.0 · https://www.flickr.com/…`; the line is replaced when the
cover changes and kept when you edit the description. For Unsplash the bot
also reports the download, as Unsplash's API terms require. A picture you
attach yourself gets no credit line — make sure you may use it.

### Brave web images — no licence

> **⚠️ Warning:** Brave results are **ordinary web images with no licence** —
> nobody has allowed their reuse. They are used because the owner asked for it,
> on a private, non-commercial friends' server. **Do not enable this on a
> public or commercial bot.**

With `BRAVE_SEARCH_API_KEY` set, the bot also searches the web for pictures
with the Brave Search API (`safesearch=strict`, 20 results). Their credit line
claims no licence, only where the picture is from: `📷 example.com ·
https://example.com/page`.

Results are filtered:

- stock-photo/watermark sites are dropped when the page, the source or the
  image is on one of them or a subdomain (Getty, iStock, Shutterstock, Alamy,
  Dreamstime, 123RF, Depositphotos, Adobe Stock, Vecteezy, Freepik, Magnific,
  Pond5, CanStockPhoto, Bigstock, Age Fotostock; the list is `stockDomains` in
  `internal/cover/brave.go`, the one place to edit);
- pictures under 800 px wide, or not landscape (width/height < 1.2), when their
  size is known;
- anything that is not an https/http JPEG, PNG or WebP URL (https first).

If the full picture cannot be downloaded the next result is tried, never the
thumbnail.

**Brave first, Unsplash when the credit is gone.** Brave is tried first
because web search finds game titles and specific things the photo libraries
do not have. The owner's Brave plan has a monthly credit; when Brave answers
HTTP 402, or 429 with no requests left in its *monthly* rate-limit window
(`X-RateLimit-Remaining`, whose values come per window, e.g. `0, 0`; the
window is identified from `X-RateLimit-Policy`, else the last value), the bot
**pauses Brave until the quota resets** — read from `X-RateLimit-Reset`
(seconds; a value over 32 days is not believed), else the 1st of next month
00:00 UTC — and the other libraries (Unsplash first) fill the covers without
calling Brave. A plain per-second 429 is not exhaustion: that one request just
falls through to the next library. When a pause starts the bot posts one
message in `EVENT_CHANNEL_ID` (else `TTS_ALERT_CHANNEL_ID`; no channel, no
message), e.g. `⚠️ เครดิต Brave Search เดือนนี้หมดแล้ว — ใช้รูปจาก Unsplash
แทนจนถึง <date>`, once per pause, and logs a line (`cover: brave: monthly
credit used up; paused until …`). **The pause is kept in memory only**: after a
restart Brave is tried again and pauses again on its first 402 (one wasted
call, and one more alert). The dashboard's events panel shows the pause next to
the provider.

**To turn Brave off, unset `BRAVE_SEARCH_API_KEY`** (and restart): the bot then
uses only the licensed libraries.

## Reminders and automatic start/end

- **Reminder:** `EVENT_REMIND_BEFORE` (default 15 min) before the start the bot
  posts one message in `EVENT_CHANNEL_ID` (or `TTS_ALERT_CHANNEL_ID`)
  mentioning everyone who pressed *Interested*, with the event link (Discord
  shows it as a card with the cover). No channel, or `0`, turns reminders off.
  An event created less than `EVENT_REMIND_BEFORE` before its start gets no
  reminder.
- **Start/end:** at the start time the bot sets the event to *active*; for
  events outside voice it sets them to *completed* at the end time (Discord
  ends voice events itself when the channel empties). Events created with
  `/event` are always handled; events made by people in Discord only with
  `EVENT_AUTO_START=true` (the default). **Stage events are left alone.**
- **Restarts:** the bot reads every server's events when it connects and keeps
  them current from gateway events (it needs **Manage Events** to read them;
  otherwise it logs `… (Manage Events may be missing)`). A reminder whose time
  passed while the bot was down is skipped (never sent late or twice); an event
  that should be running is started, as long as its end has not passed.

**State lost on restart** (kept in memory only): open cover pickers (their
buttons then say to run `/event cover` again), who ran `/event create` for an
event (after a restart only Manage Events can change the bot's own events) and
Brave's pause.

## Logs

Every rejected input logs one line with the user, the subcommand, the field,
what was typed (first 80 characters) and the error, e.g. `events: rejected
/event create input from user 42: field when, value "<@123> พรุ่งนี้": …`. When
`when` or `end` contains a mention the reply adds `ช่อง when ใส่เวลาเท่านั้น`.
At startup the bot logs which cover providers are active, whether Thai
keywords are translated, the reminder channel and the auto-start setting.

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `EVENT_CHANNEL_ID` | `TTS_ALERT_CHANNEL_ID` | Channel for reminders and the Brave quota notice. |
| `EVENT_REMIND_BEFORE` | `15m` | How long before the start to remind; `0` = off. |
| `EVENT_AUTO_START` | `true` | Also start/end events the bot did not create. |
| `PEXELS_API_KEY` | – | Optional; adds Pexels (after Unsplash) before Openverse. |
| `UNSPLASH_ACCESS_KEY` | – | Optional; adds Unsplash before Openverse; it takes over while Brave is paused for the month. |
| `BRAVE_SEARCH_API_KEY` | – | Optional secret; adds Brave web image search (unlicensed web images, private server only), tried first; paused until the monthly reset when its credit runs out. Unset = off. |
| `GOOGLE_APPLICATION_CREDENTIALS` | – | Service account for the Thai → English keywords (shared with TTS). |

Network: HTTPS to `api.openverse.org`, `api.pexels.com`, `api.unsplash.com`,
`api.search.brave.com`, `aiplatform.googleapis.com`, and to the hosts the
pictures live on (Flickr, Wikimedia and others for Openverse).
