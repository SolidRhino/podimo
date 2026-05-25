# Agent Context: Podimo to RSS

> This file provides context for AI assistants working on the codebase.
> Last updated: 2025-05-24

## Language Policy

**English only.** All source code, comments, documentation, test names, commit messages, and user-facing strings must be written in English. This applies to:
- Source code comments and docstrings
- Variable and function names
- Git commit messages
- Markdown documentation and README files
- Test names and assertions
- Issue descriptions and PR descriptions
- Log messages (except external API responses)

Dutch locale identifiers like `nl`, `nl-NL`, `Nederland` are permitted only where they represent actual Podimo API region/locale values. Do not add Dutch-language comments, commit messages, or documentation to this project.

## Project Overview

**Podimo to RSS** is a self-hosted Go web service that reverse-engineers the Podimo mobile GraphQL API to expose exclusive/paywalled podcasts as standard RSS feeds. Users authenticate with their Podimo credentials, and the tool generates RSS XML that any podcast app (Apple Podcasts, Overcast, Pocket Casts, etc.) can subscribe to.

- **Language:** Go 1.26+
- **Framework:** Go standard library `net/http` + `chi` router
- **API:** Podimo GraphQL (`https://podimo.com/graphql`)
- **Auth:** HTTP Basic Auth (credentials embedded in URL) or local credentials mode
- **Tests:** Go testing (`go test`)
- **CI:** GitHub Actions — test matrix, Docker image publishing to GHCR
- **License:** EUPL 1.2

## Quick Architecture

```
main.go          → HTTP server, routes, handlers, middleware, RSS feed serving
config.go        → Environment variables, constants, block list, Config struct
podimo/
  client.go      → GraphQL API client (login, episode fetching, search, subscriptions)
  graphql.go     → GraphQL HTTP client wrapper
  rss.go         → RSS feed generation via `eduncan911/podcast`
  cache.go       → JSON-file-backed TTL cache (token, podcast, HEAD caches)
templates/       → HTML templates (index.html, feed_location.html), embedded via `embed.FS`
tests/
  main_test.go         → Handler tests (health, index, feed, search, subscriptions, rate limiting)
  podimo/client_test.go → PodimoClient constructor, login, token cache
  podimo/graphql_test.go → GraphQL client response handling
  podimo/rss_test.go    → RSS generation, audio URL extraction, HEAD caching
  podimo/cache_test.go  → FileCache get/set/expiration
```

## Key Files & Responsibilities

| File | What it does |
|------|-------------|
| `main.go` | Entry point. Defines routes (`/`, `/health`, `/search`, `/subscriptions`, `/feed/{id}.xml`, `/feed/{username}/{password}/{id}.xml`). Generates RSS XML via `podimo.PodcastsToRss`. |
| `config.go` | Loads `.env` + env vars with `godotenv`. Defines `Config` struct, regions, locales, cacheTTLs, feature flags, block list. |
| `podimo/client.go` | `PodimoClient` struct. Handles pre-register token → onboarding ID → login token flow. Fetches paginated episodes. Wraps search and subscription endpoints. |
| `podimo/graphql.go` | `GraphQLClient` — wraps `http.Post` with JSON encoding/decoding, GraphQL error extraction. |
| `podimo/rss.go` | `PodcastsToRss` — builds RSS XML from episode data, parallelizes HEAD requests per chunk, handles audio URL extraction and content-type detection. |
| `podimo/cache.go` | `FileCache` — per-key JSON files with `expires_at` timestamp. Three instances per app: tokens, podcast, head caches. |
| `templates/index.html` | Form: email, password, podcast ID, region, locale. Extracts UUID from full Podimo URLs via JS regex. Shows warning when credentials are embedded in URL. |
| `templates/feed_location.html` | Shows generated feed URL with copy button and QR code. |

## Authentication Flow (Podimo GraphQL)

The client makes **3 sequential GraphQL requests** to authenticate:

1. **`AuthorizationPreregisterUser`** → get `preauth_token`
2. **`OnboardingQuery`** → get `prereg_id` (onboarding flow ID)
3. **`AuthorizationAuthorize`** → get `token` (final auth token, valid ~5 days)

All subsequent requests (episode fetching, search, subscriptions) use the final token in the `authorization` header.

## Caching Strategy

| Cache | Key | TTL | Purpose |
|-------|-----|-----|---------|
| `token_cache` | `SHA256(username~password)` | 5 days | Avoid re-logging in for every feed refresh |
| `podcast_cache` | `podcast_id` | 6 hours | Avoid re-fetching episode lists on every podcast app poll |
| `head_cache` | `episode_id` | 7 days | Avoid HEAD requests to audio URLs (content-length, content-type) |
| `clients` | `user_key` | process lifetime | Maintain `http.Client` with `cookiejar` per user |

## Important Code Patterns

### HTTP Client per User
Each authenticated user gets a dedicated `http.Client` stored in `App.clients` with its own `cookiejar`. If `ZenRowsAPI` or `HTTP_PROXY` is configured, the transport's `Proxy` is set accordingly. ScraperAPI is handled at the GraphQL endpoint URL level.

```go
func (a *App) getHTTPClient(key string) *http.Client
```

### URL-Based Credential Embedding
In the default mode, credentials are embedded in the feed URL for podcast apps to use:
```
https://email%40domain.com:password@host/feed/<podcast_id>.xml?region=nl&locale=nl-NL
```
Region and locale are comma-appended to the username in the Basic Auth string.

### Chunked Episode Processing
Episodes are added to the RSS feed in chunks of 5 with `sync.WaitGroup` + goroutines to parallelize HEAD requests:

```go
for _, chunk := range chunks(episodes, 5) {
    var wg sync.WaitGroup
    for i, ep := range chunk {
        wg.Add(1)
        go func(idx int, raw interface{}) {
            defer wg.Done()
 // ... build item
        }(i, ep)
    }
    wg.Wait()
    // add valid items to feed
}
```

### Rate Limiting
Feed endpoints (`/feed/...`, `/search`, `/subscriptions`) are protected by a per-IP rate limiter (8 requests per 10-second window):

```go
r.With(a.rateLimitMiddleware).Get("/feed/{podcast_id}.xml", a.handleFeed)
```

### Custom Exceptions (Go Error Types)
The client uses structured error types that satisfy `error`:

- `PodimoError` — base error type (`Error()` string method)
- `PodcastNotFoundError` — podcast ID doesn't exist
- `AuthenticationError` — invalid credentials

All have `Error()` methods and can be type-asserted for specific handling:

```go
if _, ok := err.(*podimo.PodcastNotFoundError); ok {
    http.Error(w, "Podcast not found", http.StatusNotFound)
}
```

### Request Logging
Requests are logged at both start and completion with timing via a `chi` middleware:

```go
func (a *App) loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        a.logger.Debug("Request started", ...)
        next.ServeHTTP(w, r)
        a.logger.Debug("Request completed", "duration", time.Since(start).Seconds())
    })
}
```

### Health Check Endpoint
A lightweight `/health` endpoint returns `{"status":"ok","service":"podimo-rss"}`. This is used by Docker `HEALTHCHECK` and orchestration tools (Kubernetes, Docker Compose, etc.). The endpoint has no external dependencies and should always return 200.

```go
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    w.Write([]byte(`{"status":"ok","service":"podimo-rss"}`))
}
```

## Common Tasks

### Adding a new region/locale
Edit `config.go`:
- Add to `Locales` slice (e.g., `"fr-FR"`)
- Add to `Regions` slice (e.g., `Region{Code: "fr", Name: "France"}`)

### Changing cache TTLs
Set environment variables in `.env` or shell:
- `TOKEN_CACHE_TIME` (default: 432000s = 5 days)
- `POCAST_CACHE_TIME` (default: 21600s = 6 hours)
- `HEAD_CACHE_TIME` (default: 604800s = 7 days)

### Enabling/disabling features
- `LOCAL_CREDENTIALS=true` — single-user mode, credentials stored server-side
- `PUBLIC_FEEDS=true` — removes `<itunes:block>` from RSS
- `ENABLE_VIDEO=true` — adds HLS video URLs to episode descriptions
- `DEBUG=true` — verbose `slog` logging at `LevelDebug`

### Running locally
```bash
go mod download
go run .
# Visit http://localhost:12104
```

### Running tests
```bash
go test ./... -v
go vet ./...
```

### Running in Docker
```bash
docker build -t podimo-rss .
docker run -p 12104:12104 -e PODIMO_BIND_HOST=0.0.0.0:12104 podimo-rss
```

## Known Gotchas & Pitfalls

✅ **FIXED** — `return ValueError(...)` instead of `raise` in `client.py` (Python era)  
✅ **FIXED** — Fragile `getPodcastName` via dict insertion order  
✅ **FIXED** — Backwards content-type logic overwriting correct MIME types  
✅ **FIXED** — Empty episode list producing malformed RSS  
✅ **FIXED** — Cloudscraper created per request (now native `http.Client` reused per user)  
✅ **FIXED** — Block list using substring matching (now exact match via `map[string]struct{}`)  
✅ **FIXED** — No rate limiting (added per-IP limit: 8 req/10s)  
✅ **FIXED** — CORS wildcard on all responses (removed)  
✅ **FIXED** — Docker running as root with build deps (now multi-stage + non-root)  
✅ **FIXED** — `DEBUG=true` in `.env.example` (now commented out with security warning)  
✅ **FIXED** — String exception matching in `serve_feed` fallback (all structured via `PodimoError` types)  
✅ **FIXED** — Logging only at request start (now logs both start and end with duration + status code)  
✅ **FIXED** — No `/health` endpoint for Docker orchestration (added lightweight JSON health probe)  
✅ **FIXED** — Python/discscraper/async complexity (rewritten to Go with native `net/http`)

**Remaining:**
- **`split_username_region_locale` silent fallback** — If the username doesn't contain exactly 2 commas, it silently defaults to Dutch (`nl`, `nl-NL`). This is intentional for podcast app compatibility but can surprise non-Dutch users. **Do not change without a migration plan** — existing feed URLs would break.

## Podcast ID Discovery

Users no longer need to manually extract podcast IDs from Podimo URLs. The web UI provides two discovery mechanisms:

1. **Search by name** — The index page includes a search form that calls `GET /search?q=...` via the Podimo GraphQL `podcastsAutocomplete` endpoint. Results display cover image, title, and author. Clicking a result auto-fills the podcast ID field.

2. **Your subscriptions** — Authenticated users can view their followed podcasts via `GET /subscriptions` (Podimo GraphQL `podcastsFollowed` query).

The web form still supports pasting a full Podimo URL (e.g. `https://open.podimo.com/podcast/09c55c96-...`) — the UUID is extracted via client-side JavaScript regex.

## Testing

There is now a **Go test suite** with 5 test files:

| File | Coverage |
|------|----------|
| `main_test.go` | Handler tests: `/` 200, `/health` 200, `/search` 200, `/subscriptions` 200, 404, 400 for invalid UUID, rate limiter behavior |
| `podimo/client_test.go` | `NewPodimoClient` validation, cached token loading, `Login` 3-step flow, auth error handling |
| `podimo/graphql_test.go` | `GraphQLClient.Query` status-code handling, error extraction |
| `podimo/rss_test.go` | `PodcastsToRss` XML output, `ExtractAudioURL`, `URLHeadInfo`, content-type logic, `chunks` |
| `podimo/cache_test.go` | `FileCache` get/set, TTL expiration, concurrent access |

Run with:
go test ./... -v
```

## Dependencies

See `go.mod`. Key runtime deps:
- `github.com/go-chi/chi/v5` (~=5.1.0) — HTTP router and middleware
- `github.com/eduncan911/podcast` (~=1.48.2) — RSS/Atom generation
- `github.com/joho/godotenv` (~=1.5.1) — `.env` file loading

Go standard library fills the rest: `net/http`, `html/template`, `embed`, `log/slog`, `sync`, `context`, etc.

## Environment Reference

| Variable | Default | Purpose |
|----------|---------|---------|
| `PODIMO_HOSTNAME` | `localhost:12104` | Hostname shown in generated URLs |
| `PODIMO_BIND_HOST` | `127.0.0.1:12104` | IP:port the server listens on |
| `PODIMO_PROTOCOL` | `http` | Protocol for generated URLs |
| `LOCAL_CREDENTIALS` | `false` | Store creds server-side vs embed in URL |
| `PODIMO_EMAIL` / `PODIMO_PASSWORD` | — | Server-side credentials |
| `HTTP_PROXY` | — | Generic proxy for outbound requests |
| `ZENROWS_API` / `SCRAPER_API` | — | Anti-bot proxy API keys |
| `STORE_TOKENS_ON_DISK` | `true` | Persist auth tokens to disk |
| `CACHE_DIR` | `./cache` | Where `FileCache` stores JSON files |
| `BLOCK_LIST_FILE` | `./.block-list` | File with blocked podcast IDs |

## Security Notes for Agents

- **Never commit real credentials** to `.env` or the repo. `.env` is in `.gitignore`.
- **Credentials in URLs** are unavoidable for podcast app compatibility, but warn users. The UI displays a prominent notice when `NeedCredentials=true`.
- **Use `LOCAL_CREDENTIALS=true`** for personal instances to avoid embedding passwords in URLs.
- **Always run behind HTTPS** (reverse proxy) in production — Basic Auth is cleartext otherwise.
- **Auth tokens are sensitive** — they grant full Podimo account access. `STORE_TOKENS_ON_DISK` should be `false` on shared/multi-user instances.
- **Rate limiting is active** — 8 requests per 10-second window per IP on feed/search/subscription endpoints.

## Refactoring Opportunities

If modifying this codebase, consider:
- Adding more granular rate limits per-user (currently IP-based only)
- Moving from `FileCache` to `redis` or similar for multi-instance deployments
- Configuring stricter `go vet` / `staticcheck` / `golangci-lint` rules
- Adding OpenAPI/Swagger docs for the JSON endpoints (`/search`, `/subscriptions`)

## Developer Workflow

### Pushing to GitHub with 1Password

If you use **1Password** for GitHub authentication, `gh` CLI will not work directly because the GitHub token is stored in 1Password rather than in your shell environment.

To run any `gh` command (e.g. `gh run list`, `gh repo sync`, `gh pr create`), prepend it with:

```bash
op plugin run -- gh <command>
# Examples:
op plugin run -- gh run list --repo SolidRhino/podimo
op plugin run -- gh repo sync
op plugin run -- gh pr create
```

This ensures the `GITHUB_TOKEN` is injected from your 1Password vault for the duration of the command.

If you **do not** use 1Password for GitHub auth, make sure `gh auth login` is run once to set up standard token-based authentication.

### Running GitHub Actions locally with `act`

You can test workflows locally using [`nektos/act`](https://nektosact.com/):

```bash
# Install with Homebrew
brew install act

# Dry-run to see what would execute
act --dryrun

# Run the Tests workflow (may need Docker socket path adjustments for Colima)
act -j test -W .github/workflows/test.yml
```

**Known limitation on macOS + Colima:** `act` may fail to mount the Docker socket because Colima stores it in `~/.colima/` rather than `/var/run/docker.sock`. If you encounter this, either use Docker Desktop instead of Colima, or run CI directly on GitHub via PR.
