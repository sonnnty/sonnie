# SONNIE Discord Bot v0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a working Go Discord bot skeleton that connects to Discord and responds to a `/ping` slash command with `pong`, structured so new commands can be added later without touching existing files.

**Architecture:** A `cmd/bot` entrypoint loads config and starts a `bot.Bot` (wrapping a `discordgo.Session`). Commands self-register into a package-level registry (`internal/commands`) via `init()`, so `internal/bot` never needs to know about individual commands — it only dispatches by name through `commands.Find`.

**Tech Stack:** Go 1.26 (module `github.com/Sonty15/SONNIE`), `github.com/bwmarrin/discordgo`, `github.com/joho/godotenv`.

**Spec:** `docs/superpowers/specs/2026-08-26-discord-bot-design.md`

## Global Constraints

- Language: Go, module path `github.com/Sonty15/SONNIE`.
- Discord library: `github.com/bwmarrin/discordgo` (chosen over `disgo` for community/docs size).
- Hosting: self-hosted VPS/personal machine — no CI/CD or containerization work in this plan (Dockerfile is a placeholder only).
- No mocking of the Discord gateway for v0 — code that touches the gateway (`internal/bot`, `cmd/bot`) is verified with `go build ./...`, not unit tests. Only pure logic (`internal/config`, `internal/commands` registry) gets unit tests.
- Secrets: bot token via `.env` (gitignored), a real-value-free template committed as `.env.example`.
- `docs/` is excluded from git via `.gitignore` — do not `git add` anything under `docs/` in any task below.

---

### Task 1: Config loading

**Files:**
- Create: `go.mod` (via `go mod init`)
- Create: `.env.example`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config{DiscordToken string}`, `func Load(getenv func(string) string) (Config, error)`

- [ ] **Step 1: Initialize the Go module**

Run: `go mod init github.com/Sonty15/SONNIE`
Expected: creates `go.mod` with module path `github.com/Sonty15/SONNIE` and a `go` version line.

- [ ] **Step 2: Create the env template**

Create `.env.example`:

```
DISCORD_BOT_TOKEN=
```

- [ ] **Step 3: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import "testing"

func TestLoad_ReturnsTokenFromEnv(t *testing.T) {
	getenv := func(key string) string {
		if key == "DISCORD_BOT_TOKEN" {
			return "test-token"
		}
		return ""
	}

	cfg, err := Load(getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DiscordToken != "test-token" {
		t.Fatalf("expected token %q, got %q", "test-token", cfg.DiscordToken)
	}
}

func TestLoad_ErrorsWhenTokenMissing(t *testing.T) {
	getenv := func(key string) string { return "" }

	_, err := Load(getenv)
	if err == nil {
		t.Fatal("expected error when DISCORD_BOT_TOKEN is missing, got nil")
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/config/...`
Expected: FAIL — `undefined: Load` (package `config` has no `Load` yet).

- [ ] **Step 5: Implement the minimal code to make the test pass**

Create `internal/config/config.go`:

```go
package config

import "errors"

type Config struct {
	DiscordToken string
}

func Load(getenv func(string) string) (Config, error) {
	token := getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		return Config{}, errors.New("DISCORD_BOT_TOKEN is not set")
	}
	return Config{DiscordToken: token}, nil
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/config/...`
Expected: PASS (2 tests)

- [ ] **Step 7: Commit**

```bash
git add go.mod .env.example internal/config/config.go internal/config/config_test.go
git commit -m "Add config loading with env token validation"
```

---

### Task 2: Command registry and /ping command

**Files:**
- Create: `internal/commands/registry.go`
- Create: `internal/commands/ping.go`
- Test: `internal/commands/registry_test.go`

**Interfaces:**
- Produces: `commands.Command{Definition *discordgo.ApplicationCommand, Handler func(s *discordgo.Session, i *discordgo.InteractionCreate)}`, `var commands.Registry []Command`, `func commands.Find(name string) (Command, bool)`

- [ ] **Step 1: Add the discordgo dependency**

Run: `go get github.com/bwmarrin/discordgo@latest`
Expected: `go.mod`/`go.sum` updated with the `discordgo` requirement.

- [ ] **Step 2: Write the failing test**

Create `internal/commands/registry_test.go`:

```go
package commands

import "testing"

func TestFind_ReturnsRegisteredCommand(t *testing.T) {
	if _, ok := Find("ping"); !ok {
		t.Fatal("expected \"ping\" command to be registered")
	}
}

func TestFind_ReturnsFalseForUnknownCommand(t *testing.T) {
	if _, ok := Find("does-not-exist"); ok {
		t.Fatal("expected unknown command to not be found")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/commands/...`
Expected: FAIL — `undefined: Find` (neither `Find` nor the registry exist yet).

- [ ] **Step 4: Implement the registry**

Create `internal/commands/registry.go`:

```go
package commands

import "github.com/bwmarrin/discordgo"

type Command struct {
	Definition *discordgo.ApplicationCommand
	Handler    func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

var Registry []Command

func register(c Command) {
	Registry = append(Registry, c)
}

func Find(name string) (Command, bool) {
	for _, c := range Registry {
		if c.Definition.Name == name {
			return c, true
		}
	}
	return Command{}, false
}
```

- [ ] **Step 5: Implement the /ping command**

Create `internal/commands/ping.go`:

```go
package commands

import "github.com/bwmarrin/discordgo"

func init() {
	register(Command{
		Definition: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Replies with pong",
		},
		Handler: func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "pong",
				},
			})
		},
	})
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/commands/...`
Expected: PASS (2 tests)

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/commands/registry.go internal/commands/ping.go internal/commands/registry_test.go
git commit -m "Add command registry and /ping command"
```

---

### Task 3: Bot session wiring

**Files:**
- Create: `internal/bot/bot.go`

**Interfaces:**
- Consumes: `commands.Registry []Command`, `commands.Find(name string) (commands.Command, bool)`
- Produces: `bot.Bot{Session *discordgo.Session}`, `func New(token string) (*Bot, error)`, `func (b *Bot) Open() error`, `func (b *Bot) Close() error`

- [ ] **Step 1: Implement the bot wrapper**

Create `internal/bot/bot.go`:

```go
package bot

import (
	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/commands"
)

type Bot struct {
	Session *discordgo.Session
}

func New(token string) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	return &Bot{Session: session}, nil
}

func (b *Bot) Open() error {
	b.Session.AddHandler(handleInteraction)

	if err := b.Session.Open(); err != nil {
		return err
	}

	return b.registerCommands()
}

func handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c, ok := commands.Find(i.ApplicationCommandData().Name)
	if !ok {
		return
	}
	c.Handler(s, i)
}

func (b *Bot) registerCommands() error {
	for _, c := range commands.Registry {
		if _, err := b.Session.ApplicationCommandCreate(b.Session.State.User.ID, "", c.Definition); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bot) Close() error {
	return b.Session.Close()
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: no errors (this package is not unit-tested — it only wires up the Discord gateway, which is out of scope for mocking in v0).

- [ ] **Step 3: Commit**

```bash
git add internal/bot/bot.go
git commit -m "Add bot session wiring around discordgo"
```

---

### Task 4: Entrypoint, README, Dockerfile placeholder

**Files:**
- Create: `cmd/bot/main.go`
- Create: `README.md`
- Create: `Dockerfile`

**Interfaces:**
- Consumes: `config.Load(getenv func(string) string) (config.Config, error)`, `bot.New(token string) (*bot.Bot, error)`, `(*bot.Bot).Open() error`, `(*bot.Bot).Close() error`

- [ ] **Step 1: Add the godotenv dependency**

Run: `go get github.com/joho/godotenv@latest`
Expected: `go.mod`/`go.sum` updated with the `godotenv` requirement.

- [ ] **Step 2: Write the entrypoint**

Create `cmd/bot/main.go`:

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/Sonty15/SONNIE/internal/bot"
	"github.com/Sonty15/SONNIE/internal/config"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	b, err := bot.New(cfg.DiscordToken)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	if err := b.Open(); err != nil {
		log.Fatalf("open: %v", err)
	}
	defer b.Close()

	log.Println("bot is running, press Ctrl+C to exit")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down")
}
```

- [ ] **Step 3: Write the README**

Create `README.md`:

```markdown
# SONNIE

A general-purpose Discord bot written in Go, grown one command at a time.

## Setup

1. Copy `.env.example` to `.env` and fill in `DISCORD_BOT_TOKEN` with your
   bot's token from the [Discord Developer Portal](https://discord.com/developers/applications).
2. Run the bot:

   \`\`\`bash
   go run ./cmd/bot
   \`\`\`

3. In a server the bot has been invited to, run `/ping` — it should reply `pong`.

## Adding a command

Add a new file under `internal/commands/`, following the pattern in
`ping.go`: define an `init()` that calls `register()` with a
`discordgo.ApplicationCommand` definition and a handler. No other file
needs to change.
```

- [ ] **Step 4: Add the Dockerfile placeholder**

Create `Dockerfile`:

```dockerfile
# Placeholder — containerized deploy is not set up yet.
# v0 runs directly on the host VPS: see README.md.
```

- [ ] **Step 5: Verify it builds**

Run: `go build ./...`
Expected: no errors; produces a `bot` binary under the module's build output when run as `go build -o bin/bot ./cmd/bot`.

- [ ] **Step 6: Manual verification (requires a real bot token)**

1. Create a Discord application + bot user in the Discord Developer Portal, invite it to a personal/test server with the `applications.commands` and `bot` scopes.
2. `cp .env.example .env` and set `DISCORD_BOT_TOKEN` to the bot's token.
3. Run `go run ./cmd/bot` — expect log line `bot is running, press Ctrl+C to exit`.
4. In the test server, run `/ping` — expect the bot to reply `pong`.
5. Press Ctrl+C — expect log line `shutting down` and clean exit.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum cmd/bot/main.go README.md Dockerfile
git commit -m "Add bot entrypoint, README, and Dockerfile placeholder"
```

---

### Task 5: Push to GitHub

**Files:** none (git operation only)

- [ ] **Step 1: Push all commits to the GitHub remote**

Run: `git push -u origin main`
Expected: `main` branch created on `Sonty15/SONNIE` with all commits from Tasks 1-4 (plus the earlier design-doc and `.gitignore` commits).

- [ ] **Step 2: Verify**

Run: `git log --oneline origin/main`
Expected: shows the same commit history as local `git log --oneline main`.
