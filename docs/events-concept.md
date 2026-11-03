# Concept: Discord Scheduled Events

Status: idea only, not started (2026-11-04). Separate from TTS: the bot does
not join voice or read chat for events.

## Goal

Let people create and manage the server's Discord Events through SONNIE,
with a cover image the bot finds on the internet by itself.

## Commands

| Command | What it does |
|---|---|
| `/event create` | Name, date/time (Thai free text like "ศุกร์นี้ 3 ทุ่ม"), place (a voice channel, or free text/link), description, optional image attachment. Without an attachment the bot finds a cover (below). |
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
