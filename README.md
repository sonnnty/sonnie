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

## Adding a command

Add a new file under `internal/commands/`, following the pattern in
`ping.go`: define an `init()` that calls `register()` with a
`discordgo.ApplicationCommand` definition and a handler. No other file
needs to change.
