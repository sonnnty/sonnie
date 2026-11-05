# Read-aloud: cost, usage cap, alerts and persistence

How SONNIE prices and caps Google Text-to-Speech, tells the owner about it,
and keeps the count across restarts without a database.

Related: [tts-operations.md](tts-operations.md) (setup, sessions, streaming),
[operations.md](operations.md) (environment variable reference).

## Cost

Google Cloud Text-to-Speech includes a monthly free tier of **1,000,000
characters per month** for Neural2 (the default) and Chirp 3: HD voices, and
**4,000,000 characters per month** for Standard and WaveNet voices. Beyond the
free tier, Neural2 voices cost **$16 per 1 million characters** ($0.000016 per
character; Chirp 3: HD is $30 per 1 million characters, and Standard is $4 per
1 million characters). See the
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
THB/USD. A 4 s message is roughly ฿0.035, so ฿300 is about 8,500 such
messages. The prices are named constants in `internal/usage/cost.go`; update
them there if Google changes them (nothing else hard-codes a price). The
project's bill is in THB and is covered by about ฿329/month of Google credits,
which is why the default budget is ฿300.

## Usage cap

One meter runs per process, chosen from config: with a Gemini `TTS_MODEL` it
meters **money** (below), otherwise **characters**. The meter exists whenever a
Google synthesizer is configured (`GOOGLE_APPLICATION_CREDENTIALS` or
`GOOGLE_TTS_API_KEY`); without one nothing is metered and `/tts usage` says
usage is not tracked.

### Characters (Chirp 3: HD, Neural2; no `TTS_MODEL`)

The bot counts every character (Unicode code points, spaces included) it sends
to Google, **before** the request (a failed request may still be billed), and
stops synthesizing once `used + message length > TTS_MONTHLY_CHAR_LIMIT`. The
default of 1,000,000 is the Chirp 3: HD free tier, so the cap keeps the bill
at $0. Skipped messages are simply not spoken; the first refusal each month
is logged once. The count resets at the start of each calendar month in
**US/Pacific** time, Google's billing time zone. Set
`TTS_MONTHLY_CHAR_LIMIT=0` to count without a cap. Because the bot counts what
it sends, the meter is only accurate if nothing else uses the same API key.

### Money (Gemini)

After every successful synthesis the bot prices the request:

```
cost = audio seconds × 25 × $10 / 1M  +  input tokens × $0.50 / 1M     (× TTS_USD_TO_THB)
```

- **Audio seconds** come from the Ogg granule position of the last page minus
  the pre-skip in `OpusHead`, at 48 kHz (not by counting packets; the two
  agree to within the pre-skip, and a test checks this on real fixtures). If
  the audio cannot be parsed, the bot bills an upper bound (size at 6 kbit/s)
  and logs it (`usage: could not read the audio duration; billing an upper
  bound …`).
- **Input tokens** are estimated as the runes of the message (including the
  author's name when it is spoken) plus the runes of `TTS_PROMPT`. Gemini's
  tokenizer is not available offline; one token per character over-counts
  Thai, so the estimate errs towards spending too much, and input is only
  about 1/20 of the audio cost anyway.
- Failed requests are not charged.
- **Streamed messages** ([tts-operations.md](tts-operations.md#streaming-gemini))
  are charged once, when the stream ends, from the complete Ogg pages
  received: granule of the last one minus the pre-skip. A stream that breaks
  or is cancelled halfway pays for the part that arrived; one that delivers no
  audio pays nothing. Audio Google generated but had not yet delivered when
  the bot cancelled is not visible to the bot and not counted.
- **Budget check and the one-message overshoot:** the cost is unknown until
  the audio comes back, so the bot can only check before a request that the
  spend so far has not yet reached `TTS_MONTHLY_BUDGET_THB`. The message that
  crosses the budget is spoken, so the month can overshoot by one message (a
  few satang), or one per request already in flight in other servers. After
  that, messages are skipped until next month.
  `TTS_MONTHLY_BUDGET_THB=0` counts without a cap.
- The month is still the US/Pacific calendar month, so it does not line up
  exactly with a THB invoice or credit period.
- The meter only sees this bot's requests; anything else using the same key is
  not counted.

### `/tts usage`

Replies (only to you) with a compact Thai Discord embed, e.g.:

```
🔊 Google TTS · พฤศจิกายน 2026
▰▰▱▱▱▱▱▱▱▱  12%
฿36.20 / ฿300.00
เสียงที่สร้าง: 6.7 วินาที   สถานะ: 🟢 กำลังอ่าน   รีเซ็ต: <t:unix:R>
```

The status reads `⏸️ หยุดอ่าน (ครบงบ)` (or `ครบโควตา` for the character meter)
once the cap is reached. The embed colour reflects the percentage (green
< 50%, yellow 50–79%, orange 80–99%, red ≥ 100%). The dashboard panel
"งบ Google TTS" shows the same figures.

## Alerts

Set `TTS_ALERT_CHANNEL_ID` to a text channel's ID (enable Developer Mode in
Discord, right-click the channel, *Copy Channel ID*). The bot posts a
notification preview line and a small embed card the first time usage crosses
50%, 80% and 100% in a month (colours match the thresholds):

```
⚠️ Google TTS ใช้ไป 80% ของงบเดือนนี้
[Embed: 🔊 Google TTS · พฤศจิกายน 2026 | ▰▰▰▰▰▰▰▰▱▱  80% | ฿240.00 / ฿300.00]
```

The 100% message announces that reading is paused until next month. It also
fires when a message is refused for not fitting in what is left, since usage
may never land exactly on 100%. Each threshold fires once per month, also
across restarts (the fired thresholds are part of the persisted marker).
Without `TTS_ALERT_CHANNEL_ID` no alerts are posted (see the in-memory
fallback below).

`EVENT_CHANNEL_ID` is a separate setting: it only receives event reminders and
Brave quota notices, and falls back to `TTS_ALERT_CHANNEL_ID` when unset. The
usage status message and the threshold alerts always go to
`TTS_ALERT_CHANNEL_ID`.

## Persistence

The deployment has no persistent volume, so the counter lives in Discord: the
bot keeps one status message in the alert channel as an embed with no visible
marker. The machine-readable marker is hidden in the embed URL
(`https://sonnie.invalid/usage#<base64url>`; the embed has no title, so
Discord does not show the URL). Older messages had the marker in the embed
footer or as plain message content and are still read; a plain-content
message is rewritten as an embed with the URL marker right at startup, a
footer one the next time the count is saved.

The decoded marker:

```
sonnie-tts-usage v2 month=2026-11 unit=chars value=12345 audio_ms=0 alerted=50,80
sonnie-tts-usage v2 month=2026-11 unit=thb_micro value=240000000 audio_ms=29000000 alerted=50,80
```

`unit=chars` counts characters; `unit=thb_micro` counts millionths of a baht
(`240000000` = ฿240.00). `audio_ms` is the audio generated this month in
milliseconds. `alerted` lists the thresholds already announced. A v1 marker
(`sonnie-tts-usage v1 month=… chars=…`, characters only) is still read by the
character meter. Unknown keys are ignored so a later writer can add fields.

The session cards use a similar hidden marker (`sonnie-tts-session v2 …`, in
the card's embed URL `https://sonnie.invalid/tts#<base64url>`, older cards in
the footer, v1 still read); see
[tts-operations.md](tts-operations.md#restarts-recovery-of-unfinished-sessions).

**Restoring on startup.** The bot scans the alert channel's last 100 messages
for the newest such message written by itself and restores the count from it.

- A marker from a **different month** means start at 0 (the reason is logged).
- A marker in a **different unit** means start at 0 (logged): the money meter
  starts at ฿0 when it finds an old `v1` (characters) marker or a
  `unit=chars` one, e.g. right after switching to Gemini, and the character
  meter does the same for a `unit=thb_micro` one. The counts are not
  converted.
- A message without a valid marker, or no message at all, means start at 0
  (logged).

**Writing.** The bot edits that message at most once a minute, only when the
count changed, and once more on graceful shutdown. A crash can lose up to a
minute of counts.

**Operating rules for the alert channel:**

- **Do not delete the status message, and do not let 100+ messages pile up
  after it** in that channel; if it cannot be found the count restarts at 0
  (and a new status message is created). Use a dedicated channel.
- The bot needs **View Channel**, **Send Messages** and **Read Message
  History** in the alert channel, plus **Embed Links** because the status
  message and alerts are embeds. No pinning or other permissions.
- **No alert channel:** if `TTS_ALERT_CHANNEL_ID` is not set, the count is kept
  in memory only, resets when the bot restarts, no alerts are posted, and a
  warning is logged at startup (`usage: TTS_ALERT_CHANNEL_ID is not set; the
  monthly TTS usage count is kept in memory only and resets when the bot
  restarts`).
