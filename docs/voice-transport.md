# Voice transport: AEAD + DAVE for SONNIE

Status: research done 2026-11-02; recommendation implemented on branch
`agent/voice-transport-dave` (see "What was built").

## Problem

`/tts` needs to send Opus audio into a Discord voice channel. SONNIE uses
`github.com/bwmarrin/discordgo` v0.29.0 (May 2025, still the newest tag), and
its voice client is no longer accepted by Discord:

- It only negotiates `xsalsa20_poly1305` transport encryption. Discord
  deprecated that mode and stopped accepting it on 2024-11-18.
- It does not implement DAVE, Discord's end-to-end encryption for audio and
  video. DAVE has been required in voice channels since March 2026.

## What Discord requires today

From Discord's own documentation and announcements:

| Requirement | Detail | Source |
|---|---|---|
| Voice gateway version | Use v8. Versions below 4 and unversioned connections have been rejected since 2024-11-18. | [Voice connections][dc-voice] |
| Transport encryption | You **must** support `aead_xchacha20_poly1305_rtpsize`. `aead_aes256_gcm_rtpsize` is "Available (Preferred)" and should be picked when the server offers it, which depends on hardware. Every `xsalsa20_*` mode and plain `aead_aes256_gcm` were discontinued on 2024-11-18. | [Voice connections][dc-voice] |
| DAVE | "Starting March 1st 2026, clients and apps without DAVE support will no longer be able to participate in Discord calls." This covers DMs, group DMs, server voice channels and Go Live. | [Discord blog, 2025-09-02][dc-blog], [Voice connections][dc-voice] |
| Bots | The Help Center FAQ says third-party apps and bots that connect to voice must implement DAVE to keep working after 2026-03-02. The developer docs say 2026-03-01. Either way, the deadline has passed. | [Help Center: minimum client versions][dc-help] |
| How a non-DAVE client fails | The voice gateway closes with **4017 "E2EE/DAVE protocol required"**: "This channel requires a client supporting E2EE via the DAVE Protocol". | [Opcodes and status codes][dc-codes] |
| Opt-out | None for voice channels. The only exception is **Stage channels**, which keep transport-only encryption. In May 2026 Discord said it was removing the remaining code for unencrypted fallback. | [Discord blog][dc-blog], [MediaNama, 2026-05-20][medianama] |
| Handshake | Identify (op 0) must carry `max_dave_protocol_version`. Sending 0 or omitting it means "no DAVE". MLS key exchange then runs over voice gateway opcodes 21–31, partly as binary frames. | [Voice connections][dc-voice], [DAVE protocol][dave-proto] |

So a bot has to speak voice gateway v8, use an AEAD `_rtpsize` mode **and**
run the DAVE MLS handshake, then encrypt every Opus frame with DAVE before
transport encryption. There is no setting we can flip to avoid DAVE.

## Go options

Versions and dates come from the Go module proxy (`go list -m -versions`)
and the GitHub API, checked on 2026-11-02. A cgo check means grepping the
module source for `import "C"`.

| Option | AEAD | DAVE | cgo | Licence | Maintenance | Fit for SONNIE |
|---|---|---|---|---|---|---|
| **A. disgo `voice` + dave-go** (recommended) | yes (AES-GCM preferred, XChaCha fallback) | yes, via [dave-go][dave-go] (pure Go, on [mls-go][mls-go]) | **no** | Apache-2.0 (disgo, godave, dave-go), MIT (mls-go) | [disgo][disgo]: 622★, voice fixes merged through 2026-10-28, last tag v0.19.6 (2026-06-07). dave-go: 10★, single author plus contributors, last tag v0.5.1 (2026-07-15), fixes on `main` until 2026-10-24. | Good. Its voice client can be fed events from *any* main gateway, so discordgo stays for everything else. |
| B. [`darui3018823/dgo`][dgo] hard fork | yes | yes (own MLS code on cloudflare/circl, plus mls-go) | no | BSD-3-Clause | 2★, one maintainer, v1.1.0 (2026-08-30), commits until 2026-10-23 | Works only by migrating the **whole bot** to a different module and package name (`dgo`). It also brings its own behaviour changes, such as no default intents. |
| C. [`yeongaori/discordgo-fork`][yeongaori] (upstream PR [#1704][dg-1704]) | yes | yes (own MLS code on circl) | no | BSD-3-Clause | 12★, one maintainer, commits until 2026-09-29. The upstream PR has been open with no review since 2026-03-21. | Would be a one-line `replace` (same module path), but pins the entire Discord client to an untagged personal fork with a hand-written MLS stack. |
| D. [`aleph-garden/discordgo`][aleph], [`cartridge-gg/discordgo`][cartridge] | yes | yes, via Discord's C++ [libdave][libdave] | **yes** | BSD-3-Clause + libdave MIT | `v0.29.1-dave.N` pre-releases (May–June 2026). The aleph-garden repo is no longer public. | Needs cgo plus a C++ toolchain to build libdave. Rejected. |
| E. disgo `voice` + [godave][godave]'s libdave binding (`golibdave`) | yes | yes | **yes** | Apache-2.0 + MIT | maintained by the disgo org | Same as A but with cgo. Only worth it if dave-go turns out to be wrong. |
| F. Upstream discordgo | AEAD on `master` ([#1677][dg-1677], Dec 2025), **not released** | no ([issue #1697][dg-1697] open) | no | BSD-3-Clause | last commit 2026-02-14 | Not viable. |
| G. arikawa | the [`merlinfuchs/arikawa`][arikawa-pr] fork merged gateway v8 + AEAD + DAVE (via dave-go) on 2026-10-30. Upstream arikawa has no DAVE. | fork only | no | ISC | the fork has 0★ | Would need moving the bot to arikawa *and* to a fork. Rejected. |

Notes on option A:

- disgo's `voice` package implements voice gateway v8 and the DAVE opcodes,
  and picks `aead_aes256_gcm_rtpsize` over `aead_xchacha20_poly1305_rtpsize`.
  It does not need the rest of disgo: `voice.NewManager` takes a callback
  that sends the main-gateway op 4, and the caller passes in the
  `VOICE_STATE_UPDATE` and `VOICE_SERVER_UPDATE` events. discordgo can do
  both (`ChannelVoiceJoinManual` plus event handlers).
- The tagged disgo v0.19.6 predates several voice fixes that matter to us.
  - [#561][disgo-561]: hold audio until DAVE is ready.
  - [#568][disgo-568]: close the DAVE session when the connection is
    dropped.
  - [#580][disgo-580]: wait for **both** gateway events before opening.
    discordgo delivers events on separate goroutines, so their order is not
    guaranteed.
  - [#587][disgo-587]: reconnect with a fresh session.
  - [#588][disgo-588]: fix a deadlock in the gateway's lock ordering.

  We therefore pin disgo `master` as a pseudo-version
  (`v0.19.7-0.20261101233420-18ac6df9b116`). dave-go is pinned the same way
  (`v0.5.2-0.20261024193506-083b59341154`), because it includes the
  `select_protocol_ack(0)` handling fixed in October. Move both to tags once
  they are cut.
- dave-go states it "has **not** had a formal third-party security audit".
  The author says it runs in production and has been checked end to end
  against the real Discord client (audio plays; the epoch authenticator
  matches the client's). For a TTS bot reading public chat aloud, that risk
  is acceptable. In the worst case listeners hear nothing, and no secrets
  are involved.
- Build cost: no cgo and no system libraries. New dependencies are disgo
  (`voice`, `gateway`, `discord` packages), `disgoorg/{godave,json,omit,snowflake}`,
  `klauspost/compress`, `sasha-s/go-csync`, dave-go and mls-go. gorilla/websocket
  moves from 1.4.2 to 1.5.3 and golang.org/x/crypto to a 2026 version, both
  shared with discordgo. The `go 1.26.7` directive in `go.mod` already meets
  dave-go's floor of Go 1.26.

## Sidecar (only if no Go option worked)

All of these exist and are maintained, so a sidecar is a fallback, not a
need:

| Sidecar | DAVE via | Runtime cost |
|---|---|---|
| Node: [`@discordjs/voice`][djs-voice] 0.19.2 (Apache-2.0) | [`@snazzah/davey`][davey] (MIT; Rust with prebuilt N-API binaries) | Node ≥ 22.12 on the host |
| Python: [discord.py][dpy] 2.7.1 `[voice]` extra (MIT) | `davey` Python wheels | Python + PyNaCl + davey |
| Rust: [songbird][songbird] 0.6.x (ISC) | built in since PR #291 (merged 2026-03-28) | separate Rust build |

How the Go bot would talk to it: the sidecar needs its own bot login, or a
second gateway connection with the same token, to send op 4 and receive the
voice events. The alternative is to forward SONNIE's `VOICE_*` events to it.
`@discordjs/voice` supports the forwarding mode through its "adapter"
interface. SONNIE would then stream 20 ms Opus frames over a local socket,
for example length-prefixed frames on a Unix socket, plus join/leave/speaking
control messages. `voice.Conn` and `readaloud.Joiner` would become IPC
clients.

Costs: a second runtime to install and update, a process to supervise, an
IPC protocol to version, and two places where voice state can drift. That
is clearly worse than option A while A works.

## Recommendation

Use **option A**: disgo's `voice` package with dave-go as the DAVE session,
driven by the existing discordgo session. It is the only choice that is
pure Go, comes from a maintained library with an active org behind it,
keeps discordgo for everything else, and fits behind the existing
`voice.Conn` and `readaloud.Joiner` interfaces without design changes.

Fallbacks, in order:

1. If dave-go misbehaves in practice, switch the DAVE session to godave's
   libdave binding (option E). That is a one-line change in the joiner but
   adds cgo.
2. If disgo's voice client misbehaves, try option C as a `replace`
   directive.
3. Build a Node sidecar only if both fail.

## What was built

- `internal/voice/dave.go`: `DAVEJoiner` implements `readaloud.Joiner`.
  It creates one disgo `voice.Manager`, using dave-go sessions, the first
  time it is used, because it needs the bot user ID from the open session.
  It sends op 4 through `discordgo.Session.ChannelVoiceJoinManual` and
  forwards discordgo's `VoiceStateUpdate` and `VoiceServerUpdate` events to
  the manager.
- `daveConn` implements `voice.Conn`.
  - `SendOpus` paces frames at 20 ms and waits, up to a timeout, until the
    DAVE session is ready.
  - `Speaking` sets or clears the microphone flag. Clearing it sends five
    silence frames first, as Discord asks.
  - `Disconnect` closes the voice connection, which also sends op 4 to
    leave.
  - It does not use disgo's background `AudioSender`, because that goroutine
    is never stopped when the connection is closed while idle.
- The old `DiscordJoiner` (discordgo's own voice client) was removed.

Not verified: a live join against Discord. This job had no bot token, and
real audio also needs the TTS engine and the Opus encoder, which are still
stubs. The first live test should check four things:

1. `/tts join` succeeds without a 4017 or 4016 close.
2. The logs show the selected `aead_*` mode.
3. Speech is heard by a normal desktop client.
4. The client shows the call as end-to-end encrypted.

## Sources

- Discord: [Voice connections][dc-voice], [Opcodes and status codes][dc-codes], [Bringing DAVE to all Discord platforms][dc-blog], [Help Center: minimum client versions for voice][dc-help], [DAVE protocol whitepaper][dave-proto], [libdave][libdave]
- News: [MediaNama, 2026-05-20][medianama]
- discordgo: [issue #1697][dg-1697], [PR #1704][dg-1704], [PR #1677][dg-1677]
- Go libraries: [disgo][disgo] ([`voice` docs][disgo-voice]), [godave][godave], [dave-go][dave-go], [mls-go][mls-go], [dgo][dgo], [yeongaori/discordgo-fork][yeongaori], [aleph-garden/discordgo][aleph], [cartridge-gg/discordgo][cartridge], [arikawa fork PR][arikawa-pr]
- Sidecars: [@discordjs/voice][djs-voice], [@snazzah/davey][davey], [discord.py][dpy], [songbird][songbird]

[dc-voice]: https://docs.discord.com/developers/topics/voice-connections
[dc-codes]: https://docs.discord.com/developers/topics/opcodes-and-status-codes
[dc-blog]: https://discord.com/blog/bringing-dave-to-all-discord-platforms
[dc-help]: https://support.discord.com/hc/en-us/articles/38025123604631-Minimum-Client-Version-Requirements-for-Voice-Chat
[dave-proto]: https://daveprotocol.com
[libdave]: https://github.com/discord/libdave
[medianama]: https://www.medianama.com/2026/05/223-discord-end-to-end-encryption-voice-video-calls/
[dg-1697]: https://github.com/bwmarrin/discordgo/issues/1697
[dg-1704]: https://github.com/bwmarrin/discordgo/pull/1704
[dg-1677]: https://github.com/bwmarrin/discordgo/pull/1677
[disgo]: https://github.com/disgoorg/disgo
[disgo-voice]: https://pkg.go.dev/github.com/disgoorg/disgo/voice
[disgo-561]: https://github.com/disgoorg/disgo/pull/561
[disgo-568]: https://github.com/disgoorg/disgo/pull/568
[disgo-580]: https://github.com/disgoorg/disgo/pull/580
[disgo-587]: https://github.com/disgoorg/disgo/pull/587
[disgo-588]: https://github.com/disgoorg/disgo/pull/588
[godave]: https://github.com/disgoorg/godave
[dave-go]: https://github.com/thomas-vilte/dave-go
[mls-go]: https://github.com/thomas-vilte/mls-go
[dgo]: https://github.com/darui3018823/dgo
[yeongaori]: https://github.com/yeongaori/discordgo-fork
[aleph]: https://pkg.go.dev/github.com/aleph-garden/discordgo
[cartridge]: https://github.com/cartridge-gg/discordgo
[arikawa-pr]: https://github.com/merlinfuchs/arikawa/pull/3
[djs-voice]: https://www.npmjs.com/package/@discordjs/voice
[davey]: https://github.com/Snazzah/davey
[dpy]: https://pypi.org/project/discord.py/
[songbird]: https://github.com/serenity-rs/songbird
