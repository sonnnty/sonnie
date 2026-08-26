# SONNIE Discord Bot — Design

## Purpose

A general-purpose Discord bot, starting from a minimal working skeleton and
growing incrementally as new commands/integrations are added. No single
fixed feature set — utility commands, integrations with external
systems/APIs, and AI-assisted commands are all expected over time.

## Constraints

- Language: Go (matches the owner's daily-driver language, and produces a
  single deployable binary — no runtime dependency management on the host).
- Hosting: self-hosted on a personal VPS/machine (not a managed platform).
  No CI/CD or container orchestration needed yet.
- Repo: `Sonty15/SONNIE` on GitHub, `main` branch.

## Approach

Use `discordgo` as the Discord API library — most mature Go library for
this, largest community, best documentation, full slash-command support.
Considered `disgo` (more idiomatic API) but rejected for now: smaller
community means harder troubleshooting while the bot's feature set is
still being figured out.

## Structure

```
SONNIE/
├── cmd/bot/main.go        # entrypoint: load config, open session, wait for signal
├── internal/
│   ├── config/             # load bot token from env
│   ├── commands/           # one file per slash command, self-registering
│   └── bot/                # session setup, event handler wiring
├── .env.example
├── .gitignore              # excludes .env, compiled binaries
├── go.mod
├── README.md
└── Dockerfile               # placeholder for future containerized deploy
```

Each new command is added as its own file under `internal/commands/` and
registered in one place — no other part of the codebase needs to change to
add a command.

## Initial scope (v0)

- Bot connects to Discord using a token from `.env` (`DISCORD_BOT_TOKEN`).
- One working slash command, `/ping`, replying `pong` — proves the
  connect → register → handle → reply pipeline works end to end.
- Graceful shutdown on SIGINT/SIGTERM.

Everything beyond `/ping` (utility commands, external integrations, AI
commands) is out of scope for v0 and will be added incrementally, each as
its own follow-up unit of work.

## Testing

- `go build ./...` must succeed.
- Manual verification: run the bot against a real (personal/test) Discord
  server, confirm `/ping` responds.
- No mocking of the Discord gateway for v0 — the surface is too small to
  be worth it yet; revisit if/when command logic grows complex enough to
  warrant unit tests independent of Discord.

## Secrets handling

- Bot token lives in `.env` (gitignored), loaded at startup.
- `.env.example` checked into the repo as a template with no real values.
