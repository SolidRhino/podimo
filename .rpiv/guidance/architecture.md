# Podimo to RSS

A self-hosted Go 1.23+ web service that reverse-engineers the Podimo GraphQL API to turn paywalled podcasts into standard RSS feeds. Uses `net/http` + `chi` for routing and `github.com/eduncan911/podcast` for RSS assembly.

## Architecture

Flat monolith: `main.go` orchestrates routing, middleware, and HTML serving; `podimo/` is the pure library boundary for GraphQL, auth, caching, and RSS generation.

```text
main.go          → chi routes, handlers, middleware, RSS serving
config.go        → Env loading via godotenv, Config struct, block list
podimo/          → GraphQL client, auth, caching, RSS assembly
templates/       → HTML templates (index.html, feed_location.html)
tests/           → go test (handler + per-package tests)
```

**Flow:** Incoming request → handler → `checkAuth()` → `PodimoClient` → Podimo GraphQL → `PodcastsToRss()` → XML response.

## Commands

| Command | Description |
|---------|-------------|
| `make test` | Run `go test ./...` |
| `make lint` | Run `go vet ./...` |
| `make docker-build` | Build multi-stage Docker image |
| `make docker-run` | Run container on `:12104` |

CI: GitHub Actions — test matrix, Docker image publishing to GHCR.

## Business Context

Users authenticate with Podimo credentials and receive an RSS URL that any podcast app can subscribe to. Supports HTTP Basic Auth (credentials embedded in URL) or `LOCAL_CREDENTIALS` mode.

<important if="you are adding or modifying a chi route">
1. Add `r.Get("/path", a.handleXxx)` or `r.With(a.rateLimitMiddleware).Get(...)` in `setupRoutes()`.
2. Implement `func (a *App) handleXxx(w http.ResponseWriter, r *http.Request)`.
3. Follow the standard auth resolution: branch on `a.cfg.LocalCredentials`, validate region/locale, call `a.checkAuth()`.
4. Map `*podimo.PodcastNotFoundError` to 404, `*podimo.AuthenticationError` to 401.
</important>

<important if="you are adding or modifying environment configuration">
1. Add the field to the `Config` struct in `config.go`.
2. Provide a default in `LoadConfig()` using `getEnv(key, fallback)` or `parseBool` / `parseDuration`.
3. Expose validation helpers like `isValidRegion` on `*Config`.
</important>

<important if="you are changing Docker or CI configuration">
- Dockerfile uses multi-stage `golang:1.23-alpine` (builder → runtime, root → non-root).
- `.github/workflows/test.yml` gates on `go vet` + `go test`.
- `.github/workflows/docker-publish.yml` publishes on tags.
</important>

<important if="you are adding a new GraphQL endpoint">
See `.rpiv/guidance/podimo/architecture.md` for the PodimoClient query checklist.
</important>

<important if="you are adding or modifying a template">
See `.rpiv/guidance/templates/architecture.md` for the template checklist.
</important>
