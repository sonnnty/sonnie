# SONNIE: notes for agents

A Go Discord bot (discordgo). It builds into a static, CGO-free binary on
distroless and runs as a single replica. Read `README.md` and
`git log -15` first. The entry point is `cmd/bot`, wiring is in
`internal/bot`, and settings are in `internal/config` (environment
variables only).

## Rule: every feature registers a dashboard panel

The bot has a read-only web dashboard (`internal/dashboard`). **When you
add a feature, add its panel in the same change:**

1. Give the feature a cheap, read-only status method that returns an
   in-memory snapshot. It must never call Discord, because it runs every
   few seconds per open page.
2. Add a constructor in `internal/panels/` that returns a
   `dashboard.Panel{ID, Title, Snapshot}`. The ID is stable snake_case and
   the title is in Thai. Snapshot returns a JSON-ready struct with
   `[]`, not `null`, for lists. Take the status behind a small interface
   and test the JSON with a fake (see `internal/panels/panels_test.go`).
3. Append it in `dashboardPanels` in `internal/bot/dashboard.go`.
4. Optionally add a renderer under the same ID in
   `internal/dashboard/static/app.js`. Build DOM with `el()`/`textContent`
   only, never `innerHTML`: names and log lines are untrusted. Without a
   renderer, the panel shows as raw JSON.

The dashboard stays read-only unless the owner asks for actions.

## Conventions

- Run `gofmt`, `go vet ./...`, `go build ./...` and `go test -race ./...`
  before committing. Tests use fakes and `httptest`, never the real
  network.
- Keep it CGO-free (`CGO_ENABLED=0` in the `Dockerfile`). Use the standard
  library first; no web frameworks, CDNs or JS build step.
- Never log secrets (bot token, client secret, session key, webhook
  tokens, API keys) and never put them in error messages. The log ring
  also redacts them (`redactSecrets` in `internal/bot/dashboard.go`): add
  any new secret there.
- Discord-facing text is Thai; code, comments and logs are English.
- New settings go in `internal/config` with tests, in `.env.example` and in
  the README.
- Kubernetes manifests live in a separate repo (homelab-ops), not here.
