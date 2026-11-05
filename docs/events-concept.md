# Concept: Discord Scheduled Events

Status: **implemented** (2026-11-03); see [events-operations.md](events-operations.md)
for how it works and [Deviations](#as-built-deviations) below.
Separate from TTS: the bot does not join voice or read chat for events.

## Goal

Let people create and manage the server's Discord Events through SONNIE,
with a cover image the bot finds on the internet by itself.

## Commands

| Command | What it does |
|---|---|
| `/event create` | A form (as built; see below), or options: name, date/time (Thai free text like "ศุกร์นี้ 3 ทุ่ม"), place (a voice channel, or free text/link), description, optional image attachment. Without an attachment the bot finds a cover (below). |
| `/event cover <event> [query]` | Change the cover: search again (by the event name, or by `query`) and offer a few candidates to pick from with buttons; or attach an image to use it directly. |
| `/event edit <event>` | Change name, time, place, description. |
| `/event cancel <event>` | Cancel the event. |
| `/event list` | Upcoming events as an embed, in the style of `/tts usage`. |

## Automation

- **Reminder** before the start (e.g. 15 min): mention or DM the users who
  pressed Interested.
- **Auto start / end**: set the event to active at its start time and to
  completed at its end time.

## Cover image from the internet (no AI generation)

1. Search by the event name (or the `query` given), take the best result,
   crop/resize to Discord's 800×320 cover, upload it with the event.
2. `/event cover` shows 3–5 candidates (thumbnails in an embed, buttons
   1–5, "ค้นใหม่") so the owner can swap it.
3. Credit the photographer/source in the event description when the source
   requires it.

Image source, to decide when building (all have free tiers; check terms then):

| Source | Notes |
|---|---|
| Unsplash API | High-quality photos, free licence for this use, attribution requested, rate-limited (demo ~50 req/h). |
| Pexels API | Free photos and licence, API key, generous limits, attribution appreciated. |
| Openverse API | Creative Commons images, no key needed, licence varies per image (must keep attribution). |
| Google Custom Search (image) | Widest results, but images are often copyrighted; 100 queries/day free. Not recommended. |

Recommendation: Pexels or Unsplash (clear licence for reuse). Thai event
names may need a translation/keyword step first (e.g. "เตะบอลวันศุกร์" →
"football"), since these APIs search best in English.

## Technical notes

- Discord API: Guild Scheduled Events (create/modify/delete, list, users
  interested, gateway events for interest added/removed and status). The
  cover is the `image` field as a data URI. Recurring events exist in the
  API but may need raw requests if discordgo lacks them.
- Permission: Create Events / Manage Events (the bot is Administrator today).
- Image resize/crop in pure Go (`golang.org/x/image/draw`) to keep the
  static, CGO-free image.
- New secret if the chosen image API needs a key (goes in the k8s Secret,
  like the TTS credentials).

## As built: deviations

What differs from the concept above, and why:

- **Image source:** Openverse is the default (no key, so it works tonight);
  Pexels and Unsplash are implemented behind the same interface and are
  tried first when `PEXELS_API_KEY` / `UNSPLASH_ACCESS_KEY` is set. Openverse
  results are limited to CC0, PDM, CC BY and CC BY-SA; every found cover is
  credited on the last line of the description. Google Custom Search is not
  used.
- **Thai names:** translated to English keywords with Gemini
  (`gemini-2.5-flash-lite`, Vertex AI, region `global`, same service
  account as Gemini-TTS), 3 s timeout, fallback to the raw name. Names
  without Thai letters are searched as they are, without a Gemini call.
- **`/event cover`:** 5 candidates (5 image embeds in one ephemeral
  message), buttons 1–5 and 🔎 ค้นใหม่ for the next page; also offered from
  a 🔄 เปลี่ยนรูป button on the `/event create` reply.
- **Reminder:** one message in a channel (`EVENT_CHANNEL_ID`, else
  `TTS_ALERT_CHANNEL_ID`) mentioning the interested users, not DMs.
- **Auto start/end:** only voice and external events (stage events need a
  stage instance); voice events are not ended by the bot because Discord
  ends them when the channel empties. Canceling an event that is already
  running ends it, because Discord does not allow ACTIVE → CANCELED.
- **Permissions:** create needs Create Events or Manage Events;
  edit/cover/cancel need Manage Events or being the creator. Who ran
  `/event create` is remembered in memory only, so after a restart only
  Manage Events can change the bot's own events.
- **State:** nothing is persisted. Reminders whose time passed during a
  restart are skipped (never sent twice); open cover pickers expire after
  15 minutes or on restart.
- **Form (2026-11-04):** the owner found the options hard to use live
  (Discord shows only the required ones and hides `place`/`location`
  behind "+6 more"), so `/event create` without options opens a modal
  instead: name, day (select: today + 24 days), time (text, `20:00`
  prefilled), voice channel (select, the caller's current one
  preselected) and description. All create options are optional now;
  with any of them the command works as before. Without a voice channel
  the form makes an external event at `Discord`. The modal uses Label
  components (type 18), which discordgo v0.29 cannot decode, so submits
  are read from the raw gateway event (`internal/modal`). Rejected
  inputs of any `/event` subcommand are logged with field and value.
- **Not done:** recurring events.
