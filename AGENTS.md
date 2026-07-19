# Agent Context: Podimo to RSS

> This file provides context for AI assistants working on the codebase.
> Last updated: 2026-07-19

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
- **CI:** GitHub Actions - test matrix, Docker image publishing to GHCR
- **License:** EUPL 1.2

## Quick Architecture

```
main.go          → HTTP server, routes, handlers, middleware (logging with status capture, rate limiting), RSS feed serving with ETag/Last-Modified/Cache-Control, /ready probe, /subscriptions.opml export, withAuthRetry helper, feedURLFor builder
config.go        → Environment variables, constants, block list, Config struct
podimo/
  client.go      → GraphQL API client (login, episode fetching, search, subscriptions)
  graphql.go     → GraphQL HTTP client wrapper
  rss.go         → RSS feed generation via `eduncan911/podcast`
  cache.go       → JSON-file-backed TTL cache (token, podcast, HEAD caches)
  boundedmap.go  → Generic in-memory LRU cache with TTL eviction
static/          → Embedded static files (CSS stylesheet)
templates/       → HTML templates (index.html, feed_location.html, partials/*.html), embedded via `embed.FS`
main_test.go     → Handler tests (health, index, feed, search, subscriptions, rate limiting)
  podimo/client_test.go → PodimoClient constructor, login, token cache
  podimo/graphql_test.go → GraphQL client response handling
  podimo/rss_test.go    → RSS generation, audio URL extraction, HEAD caching
  podimo/cache_test.go  → FileCache get/set/expiration
  podimo/boundedmap_test.go → BoundedMap LRU/TTL behavior
```

## Key Files & Responsibilities

| File | What it does |
|------|-------------|
| `main.go` | Entry point. Defines routes, middleware (logging, rate limiting), server timeouts, and credential redaction. Generates RSS XML via `podimo.PodcastsToRss`. |
| `config.go` | **Hybrid config loader:** uses `knadh/koanf` with a flat `config.yaml` as the primary source, with `PODIMO_`-prefixed env vars and `.env` (via godotenv) as overrides. Defines `Config` struct with `koanf` tags. Strict validation on all typed fields (invalid booleans/durations fail hard at startup).
| `podimo/client.go` | `PodimoClient` struct. Handles pre-register token → onboarding ID → login token flow. Fetches paginated episodes. Wraps search and subscription endpoints. Maps GraphQL auth failures to `AuthenticationError`. |
| `podimo/graphql.go` | `GraphQLClient` — wraps `http.Post` with JSON encoding/decoding, structured `GQLError` extraction, and a 10 MB response size limit. |
| `podimo/rss.go` | `PodcastsToRss` — builds RSS XML from episode data, parallelizes HEAD requests per chunk with context cancellation checks. Retries failed HEAD requests up to 3 times. Handles audio URL extraction and content-type detection. |
| `podimo/cache.go` | `FileCache` — per-key JSON files with `expires_at` timestamp. Per-key mutexes stored in a `BoundedMap`. Three instances per app: tokens, podcast, head caches. |
| `templates/index.html` | Form: email, password, podcast ID, region, locale. Uses HTMX and Alpine.js for search, subscriptions, and copy-to-clipboard. Extracts UUID from full Podimo URLs via JS regex. |
| `templates/feed_location.html` | Shows generated feed URL with copy button and QR code. |
| `templates/partials/*.html` | HTMX partials for search results, subscriptions, and feed result. |
| `static/style.css` | External stylesheet with dark mode support. |
| `config.example.yaml` | Reference configuration file. Documented flat-YAML schema with all options and defaults. Copy to `config.yaml` to customize. |
| `podimo/boundedmap.go` | `BoundedMap` - generic in-memory LRU cache with optional TTL and background cleanup. Used for per-user `http.Client` pools and rate-limiter IP tracking. |
| `Dockerfile` | Multi-stage build: `golang:1.26-alpine` builder → `scratch` runtime (zero attack surface: no shell, no package manager, no libs). Bundles CA certs for outbound HTTPS, runs as nonroot UID 65532, and declares `HEALTHCHECK CMD ["/podimo-rss", "healthcheck"]` using the built-in subcommand (no curl/shell needed). |
| `docker-compose.yml` | Compose stack with `podimo-cache` named volume, `PODIMO_BIND_HOST=0.0.0.0:12104`, and a `healthcheck` block mirroring the Dockerfile `HEALTHCHECK`. |

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
| `clients` | `user_key` | 5 days (token cache TTL) | Maintain `http.Client` with `cookiejar` per user; bounded to 100 entries with LRU eviction |
| `rate_limiter_ips` | `IP address` | 10 seconds | Track requests per IP for rate limiting; bounded to 10 000 entries with LRU eviction |

## Important Code Patterns

### HTTP Client per User
Each authenticated user gets a dedicated `http.Client` stored in `App.clients` with its own `cookiejar`. If `ZenRowsAPI` or `HTTP_PROXY` is configured, the transport's `Proxy` is set accordingly. ScraperAPI is handled at the GraphQL endpoint URL level.

```go
func (a *App) getHTTPClient(key string) *http.Client
```

### Credential Redaction in Logs
URLs containing embedded credentials are redacted before logging to avoid leaking passwords in log output:

```go
func redactURLString(raw string) string
```

### URL-Based Credential Embedding
In the default mode, credentials are embedded in the feed URL for podcast apps to use:
```
https://email%40domain.com:password@host/feed/<podcast_id>.xml?region=nl&locale=nl-NL
```
Region and locale are comma-appended to the username in the Basic Auth string.

### Chunked Episode Processing with Context Cancellation
Episodes are added to the RSS feed in chunks of 5 with `sync.WaitGroup` + goroutines to parallelize HEAD requests. The loop checks `ctx.Err()` between chunks and inside each goroutine to allow fast cancellation:

```go
for _, chunk := range chunks(episodes, 5) {
    if ctx.Err() != nil {
        return nil, ctx.Err()
    }
    var wg sync.WaitGroup
    for i, ep := range chunk {
        wg.Add(1)
        go func(idx int, raw interface{}) {
            defer wg.Done()
            if ctx.Err() != nil {
                return
            }
            // ... build item
        }(i, ep)
    }
    wg.Wait()
    // add valid items to feed
}
```

### HEAD Request Retry
Each episode's HEAD request (for content-length and content-type) retries up to 3 times with a 1-second backoff before falling back to safe defaults (`audio/mpeg`, length 0):

```go
retries := 3
for attempt := 0; attempt < retries; attempt++ {
    resp, err := client.Do(req)
    if err != nil {
        if attempt < retries-1 {
            select {
            case <-ctx.Done():
                return "0", "audio/mpeg", ctx.Err()
            case <-time.After(1 * time.Second):
            }
            continue
        }
        return "0", "audio/mpeg", err
    }
    // ... cache and return headers
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

### Server Timeouts
The HTTP server is configured with explicit timeouts to harden against slowloris and resource exhaustion:

```go
server := &http.Server{
    Addr:              cfg.BindHost,
    Handler:           router,
    ReadTimeout:       30 * time.Second,
    WriteTimeout:      60 * time.Second,
    IdleTimeout:       120 * time.Second,
    ReadHeaderTimeout: 10 * time.Second,
    MaxHeaderBytes:    1 << 20, // 1MB
}
```

### Request Logging
Requests are logged at both start and completion with timing via a `chi` middleware. URLs are redacted to scrub embedded credentials:

```go
func (a *App) loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        a.logger.Debug("Request started", "method", r.Method, "url", redactURLString(r.URL.String()), "ip", r.RemoteAddr, "ua", r.UserAgent())
        rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
        next.ServeHTTP(rec, r)
        a.logger.Debug("Request completed", "method", r.Method, "url", redactURLString(r.URL.String()), "status", rec.status, "duration", time.Since(start).Seconds())
    })
}
```

### Health Check Endpoint
A lightweight `/health` endpoint returns `{"status":"ok","service":"podimo-rss"}`. This is used by Docker `HEALTHCHECK` and orchestration tools (Kubernetes, Docker Compose, etc.). The endpoint has no external dependencies and should always return 200.

The `scratch` runtime image has no shell or `curl`, so the Dockerfile `HEALTHCHECK` invokes a built-in `healthcheck` subcommand (`/podimo-rss healthcheck`) that probes the same `/health` endpoint via loopback. It reads `PODIMO_BIND_HOST` from the environment (defaulting to `127.0.0.1:12104`, normalizing wildcard bind hosts `0.0.0.0`, `::`, and empty host to `127.0.0.1`) and exits 0 on HTTP 200, 1 otherwise. It is side-effect-free (no config load, no cache I/O).

```go
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    w.Write([]byte(`{"status":"ok","service":"podimo-rss"}`))
}
```

### Ready Endpoint
Distinct from `/health` (a cheap liveness probe), `/ready` verifies outbound reachability to the Podimo GraphQL endpoint. A `readyProbe` caches the result for 10 seconds so orchestration probes do not generate a request per poll. Any HTTP response (even 405/404) proves reachability; only connection failures or timeouts mark it unready. Returns 503 when unreachable, 200 when reachable. Not rate-limited, matching `/health`. Use for Kubernetes readiness probes; use `/health` for liveness.

```go
type readyProbe struct {
    endpoint string
    mu       sync.Mutex
    ok       bool
    checked  time.Time
}
```

### Auth Retry Helper
The 3 sites that call the Podimo client (`handleSearch`, `handleSubscriptions`, `serveFeed`) share a generic `withAuthRetry[T]` that runs the call, refreshes the token once on `*AuthenticationError`, and retries. Returns the zero value on error. Callers still type-assert the returned error for status-specific handling (`PodcastNotFoundError`, `AuthenticationError`).

```go
func withAuthRetry[T any](ctx context.Context, client *podimo.PodimoClient, fn func(context.Context) (T, error)) (T, error)
```

### Pagination Guard
`GetPodcasts` paginates episodes in pages of 100. Two guards prevent pathological infinite loops: a `maxPages` cap (200) and a seen-episode-ID dedup map that breaks early if the API repeats an ID across pages. Both log a warning and stop pagination.

### Feed HTTP Caching
`serveFeed` emits conditional-GET headers so podcast apps and shared proxies can cache: a strong `ETag` (sha256 of content-determining fields, 16 hex chars), a `Last-Modified` derived from the newest episode publish time (truncated to second granularity), and `Cache-Control: public, max-age=3600, stale-while-revalidate=86400`. `If-None-Match` and `If-Modified-Since` short-circuit to `304 Not Modified`. The ETag covers podcast ID, title, author, episode count, newest publish date, and the `public_feeds` flag.

### OPML Export
`GET /subscriptions.opml` returns the user's followed podcasts as an OPML 2.0 document with `xmlUrl` entries pointing at the per-podcast RSS feeds, downloadable as an attachment. Auth mirrors `/subscriptions`. Uses `feedURLFor` (the single feed-URL builder shared with `handleIndex`) which embeds credentials in non-local mode or delegates to `buildFeedURL` in `LocalCredentials` mode.

## Common Tasks

### Adding a new region/locale
Edit `config.go`:
- Add to `Locales` slice (e.g., `"fr-FR"`)
- Add to `Regions` slice (e.g., `Region{Code: "fr", Name: "France"}`)

### Changing cache TTLs
Set in `config.yaml` (preferred) or via env var:
```yaml
# config.yaml
token_cache_time: "432000s"   # 5 days (default)
podcast_cache_time: "21600s"  # 6 hours (default)
head_cache_time: "604800s"    # 7 days (default)
```
Or via environment variable:
```bash
PODIMO_TOKEN_CACHE_TIME=432000
PODIMO_PODCAST_CACHE_TIME=21600
PODIMO_HEAD_CACHE_TIME=604800
```

### Enabling/disabling features
In `config.yaml`:
```yaml
local_credentials: true   # single-user mode
debug: true                 # verbose logging
```
Or via environment variables (prefixed with `PODIMO_`):
- `PODIMO_LOCAL_CREDENTIALS=true` — single-user mode, credentials stored server-side
- `PODIMO_PUBLIC_FEEDS=true` — removes `<itunes:block>` from RSS
- `PODIMO_DEBUG=true` — verbose `slog` logging at `LevelDebug`

### Running locally
```bash
go mod download
go run .
# Visit http://localhost:12104
```

### Running tests
```bash
just test
just lint
```

### Running in Docker
```bash
just docker-build
just docker-run
# Or manually:
docker build -t podimo-rss .
docker run -p 12104:12104 -e PODIMO_BIND_HOST=0.0.0.0:12104 podimo-rss
```

## Known Gotchas & Pitfalls

✅ **FIXED** - `return ValueError(...)` instead of `raise` in `client.py` (Python era)
✅ **FIXED** - Fragile `getPodcastName` via dict insertion order
✅ **FIXED** - Backwards content-type logic overwriting correct MIME types
✅ **FIXED** - Empty episode list producing malformed RSS
✅ **FIXED** - Cloudscraper created per request (now native `http.Client` reused per user)
✅ **FIXED** - Block list using substring matching (now exact match via `map[string]struct{}`)
✅ **FIXED** - No rate limiting (added per-IP limit: 8 req/10s)
✅ **FIXED** - CORS wildcard on all responses (removed)
✅ **FIXED** — Docker running as root with build deps (now multi-stage `golang:1.26-alpine` builder → `scratch` runtime with CA certs, nonroot UID 65532, and built-in `healthcheck` subcommand for `HEALTHCHECK`)
✅ **FIXED** — `DEBUG=true` in `.env.example` (now commented out with security warning)
✅ **FIXED** — String exception matching in `serve_feed` fallback (all structured via `PodimoError` types)
✅ **FIXED** — Logging only at request start (now logs both start and end with duration + status code)
✅ **FIXED** — No `/health` endpoint for Docker orchestration (added lightweight JSON health probe)
✅ **FIXED** — Python/discscraper/async complexity (rewritten to Go with native `net/http`)
✅ **FIXED** — No server timeouts (added `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`, `MaxHeaderBytes`)
✅ **FIXED** — Credentials leaked in logs (added `redactURLString` before logging request URLs)
✅ **FIXED** — Unbounded in-memory caches (replaced with `BoundedMap` LRU+TTL for per-user clients and rate-limiter IPs)
✅ **FIXED** — Unstructured GraphQL errors and unbounded response sizes (added `GQLError` type + 10 MB response limit)
✅ **FIXED** — RSS generation ignoring context cancellation (now checks `ctx.Err()` between chunks and per-goroutine)
✅ **FIXED** — No HEAD retry on transient audio URL failures (now retries up to 3× with 1 s backoff)

**Remaining:**
- **`split_username_region_locale` silent fallback** - If the username doesn't contain exactly 2 commas, it silently defaults to Dutch (`nl`, `nl-NL`). This is intentional for podcast app compatibility but can surprise non-Dutch users. **Do not change without a migration plan** - existing feed URLs would break.

## Podcast ID Discovery

Users no longer need to manually extract podcast IDs from Podimo URLs. The web UI provides two discovery mechanisms:

1. **Search by name** - The index page includes a search form that calls `GET /search?q=...` via the Podimo GraphQL `podcastsAutocomplete` endpoint. Results display cover image, title, and author. Clicking a result auto-fills the podcast ID field.

2. **Your subscriptions** - Authenticated users can view their followed podcasts via `GET /subscriptions` (Podimo GraphQL `podcastsFollowed` query). Each entry shows the episode count and latest-episode date (fetched via the `episodeCount` and `latestEpisode { publishDatetime }` fields, with a minimal-field fallback if the schema rejects them). The date format is configurable via `PODIMO_DATE_FORMAT` (Go `time.Format` layout, default `2006-01-02`).

The web form still supports pasting a full Podimo URL (e.g. `https://open.podimo.com/podcast/09c55c96-...`) - the UUID is extracted via client-side JavaScript regex.

## Testing

There is now a **Go test suite** with 6 test files:

| File | Coverage |
|------|----------|
| `main_test.go` | Handler tests: `/` 200, `/health` 200, `/ready` (reachable/unreachable/cached), `/search` 200, `/subscriptions` 200 + metadata rendering, `/subscriptions.opml`, feed ETag/304/Cache-Control/Last-Modified, `withAuthRetry`, `statusRecorder` logging, `feedURLFor`, rate limiter behavior, 404, 400 for invalid UUID |
| `podimo/client_test.go` | `NewPodimoClient` validation, cached token loading, `Login` 3-step flow, auth error handling, `GetPodcasts` pagination dedup + page cap, `GetFollowedPodcasts` extended + minimal fallback |
| `podimo/graphql_test.go` | `GraphQLClient.Query` status-code handling, structured `GQLError` extraction, 10 MB limit |
| `podimo/rss_test.go` | `PodcastsToRss` XML output, `ExtractAudioURL`, `URLHeadInfo`, content-type logic, `chunks` |
| `podimo/cache_test.go` | `FileCache` get/set, TTL expiration, concurrent access |
| `podimo/boundedmap_test.go` | `BoundedMap` get/set, LRU eviction, TTL expiration, concurrent access |

Run with:
```bash
just test
```

## Dependencies

See `go.mod`. Key runtime deps:
- `github.com/go-chi/chi/v5` (~=5.1.0) — HTTP router and middleware
- `github.com/eduncan911/podcast` (~=1.4.2) — RSS/Atom generation
- `github.com/joho/godotenv` (~=1.5.1) — `.env` file loading
- `github.com/knadh/koanf/v2` (~=2.3.5) — YAML/env configuration with precedence and defaults

Go standard library fills the rest: `net/http`, `html/template`, `embed`, `log/slog`, `sync`, `context`, etc.

## Configuration

### `config.yaml` (preferred)
Place a `config.yaml` in the working directory or at `/etc/podimo-rss/config.yaml`. All fields are optional; defaults are applied when absent. See `config.example.yaml` for a full reference.

Example:
```yaml
hostname: "myserver.example.com"
bind_host: "0.0.0.0:12104"
debug: true
```

### Env vars (override)
All variables must use the `PODIMO_` prefix (e.g. `PODIMO_DEBUG=true`). `.env` files are still supported via godotenv pre-load.

### CLI flag (custom file path)
```bash
./podimo-rss --config=/etc/podimo-rss/config.yaml
```

### Precedence (highest first)
1. `--config /path/to/config.yaml` (explicit CLI flag)
2. Environment variables (`PODIMO_DEBUG=true`)
3. `config.yaml` in working dir or `/etc/podimo-rss/`
4. `.env` file (pre-loaded into env vars)
5. Hardcoded defaults

| Variable / YAML key | Default | Purpose |
|---------------------|---------|---------|
| `PODIMO_HOSTNAME` / `hostname` | `localhost:12104` | Hostname shown in generated URLs |
| `PODIMO_BIND_HOST` / `bind_host` | `127.0.0.1:12104` | IP:port the server listens on |
| `PODIMO_PROTOCOL` / `protocol` | `http` | Protocol for generated URLs |
| `PODIMO_LOCAL_CREDENTIALS` / `local_credentials` | `false` | Store creds server-side vs embed in URL |
| `PODIMO_EMAIL` / `email` | — | Server-side credentials |
| `PODIMO_PASSWORD` / `password` | — | Server-side credentials |
| `PODIMO_HTTP_PROXY` / `http_proxy` | — | Generic proxy for outbound requests |
| `PODIMO_ZENROWS_API` / `zenrows_api` | — | Anti-bot proxy API key |
| `PODIMO_SCRAPER_API` / `scraper_api` | — | Anti-bot proxy API key |
| `PODIMO_STORE_TOKENS_ON_DISK` / `store_tokens_on_disk` | `true` | Persist auth tokens to disk |
| `PODIMO_CACHE_DIR` / `cache_dir` | `./cache` | Where `FileCache` stores JSON files |
| `PODIMO_BLOCK_LIST_FILE` / `block_list_file` | `./.block-list` | File with blocked podcast IDs |
| `PODIMO_TOKEN_CACHE_TIME` / `token_cache_time` | `432000s` | Auth token cache TTL |
| `PODIMO_PODCAST_CACHE_TIME` / `podcast_cache_time` | `21600s` | Episode list cache TTL |
| `PODIMO_HEAD_CACHE_TIME` / `head_cache_time` | `604800s` | HEAD response cache TTL |
| `PODIMO_PUBLIC_FEEDS` / `public_feeds` | `false` | Remove `<itunes:block>` from RSS |
| `PODIMO_DATE_FORMAT` / `date_format` | `2006-01-02` | Go `time.Format` layout for the latest-episode date on `/subscriptions` |
| `PODIMO_DEBUG` / `debug` | `false` | Verbose `slog` logging

## Security Notes for Agents

- **Never commit real credentials** to `.env` or the repo. `.env` is in `.gitignore`.
- **Credentials in URLs** are unavoidable for podcast app compatibility, but warn users. The UI displays a prominent notice when `NeedCredentials=true`.
- **Use `LOCAL_CREDENTIALS=true`** for personal instances to avoid embedding passwords in URLs.
- **Always run behind HTTPS** (reverse proxy) in production - Basic Auth is cleartext otherwise.
- **Auth tokens are sensitive** - they grant full Podimo account access. `STORE_TOKENS_ON_DISK` should be `false` on shared/multi-user instances.
- **Rate limiting is active** - 8 requests per 10-second window per IP on feed/search/subscription endpoints.

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
