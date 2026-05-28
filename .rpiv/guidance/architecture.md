# Podimo to RSS

A self-hosted Go 1.26+ web service that reverse-engineers the Podimo GraphQL API to turn paywalled podcasts into standard RSS feeds. Uses `net/http` + `chi` for routing and `github.com/eduncan911/podcast` for RSS assembly.

## Architecture

Flat monolith: `main.go` orchestrates routing, middleware, and HTML serving; `podimo/` is the pure library boundary for GraphQL, auth, caching, and RSS generation; `templates/` and `static/` constitute the server-rendered presentation layer.

```
main.go          → HTTP server, routes, handlers, middleware, RSS serving
config.go        → Environment variables, constants, block list, Config struct
podimo/          → GraphQL client, auth, caching, RSS assembly
templates/       → HTML templates (index.html, feed_location.html, partials/*.html)
static/          → Shared stylesheet (style.css)
```

**Flow:** Incoming request → handler → `checkAuth()` → `PodimoClient` → Podimo GraphQL → `PodcastsToRss()` → XML response.

## Commands

| Command | Description |
|---------|-------------|
| `just test` | Run `go test ./...` |
| `just lint` | Run `go vet ./...` |
| `just docker-build` | Build multi-stage Docker image |
| `just docker-run` | Run container on `:12104` |

CI: GitHub Actions — test matrix, Docker image publishing to GHCR.

## Business Context

Users authenticate with Podimo credentials and receive an RSS URL that any podcast app can subscribe to. Supports HTTP Basic Auth (credentials embedded in URL) or `LOCAL_CREDENTIALS` mode.

## Layer Guidance

| Layer | File |
|-------|------|
| Domain / GraphQL / Cache / RSS | `.rpiv/guidance/podimo/architecture.md` |
| Presentation (templates + static) | `.rpiv/guidance/templates/architecture.md` |

<important if="you are adding or modifying a chi route">
1. Add `r.Get("/path", a.handleXxx)` or `r.With(a.rateLimitMiddleware).Get(...)` in `setupRoutes()`.
2. Implement `func (a *App) handleXxx(w http.ResponseWriter, r *http.Request)`.
3. Follow the standard auth resolution: branch on `a.cfg.LocalCredentials`, validate region/locale, call `a.checkAuth()`.
4. Map `*podimo.PodcastNotFoundError` to 404, `*podimo.AuthenticationError` to 401.
5. Add a test in `main_test.go` using `setupTestApp(t)` and `httptest.NewRecorder`.
</important>

<important if="you are adding or modifying environment configuration">
1. Add the field to the `Config` struct in `config.go` with a `mapstructure` tag.
2. Provide a default in `LoadConfig()` using `v.SetDefault()`.
3. Expose validation helpers like `isValidRegion` on `*Config`.
4. Update `config.example.yaml` and `AGENTS.md` with the new option.
5. Add a config test in `main_test.go` (invalid value, trimmed value, YAML loading).
</important>
