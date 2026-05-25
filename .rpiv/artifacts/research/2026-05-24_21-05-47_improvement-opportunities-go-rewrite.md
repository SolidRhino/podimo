---
date: 2026-05-24T21:05:47+0200
author: SolidRhino
commit: 8c05dd7
branch: go-rewrite
repository: podimo
topic: "Improvement opportunities across the Go rewrite"
tags: [research, codebase, go, security, performance, correctness, tests]
status: complete
last_updated: 2026-05-24T21:05:47+0200
last_updated_by: SolidRhino
---

# Research: Improvement Opportunities Across the Go Rewrite

## Research Question
Identify improvement opportunities across the Podimo-to-RSS Go codebase on the go-rewrite branch. Focus on performance, security, correctness, test coverage, maintainability, and Go idioms.

## Summary
Eight distinct improvement areas were identified across the Go rewrite. The most critical are:
1. **Server timeouts missing** — `http.Server` started with bare `ListenAndServe` (`main.go:149`), no `ReadTimeout`, `WriteTimeout`, or `IdleTimeout`, exposing the service to slowloris attacks.
2. **Credential leakage in DEBUG logs** — `loggingMiddleware` logs `r.URL.String()` verbatim (`main.go:177/179`), including path-embedded passwords from `/feed/<username>/<password>/...`.
3. **Unbounded in-memory maps** — `App.clients` (`main.go:41`) and `RateLimiter.ips` (`main.go:47`) never evict entries, causing memory leaks. `FileCache.keyLocks` (`podimo/cache.go:14`) grows one mutex per unique cache key forever.
4. **Auth errors returned as 500** — `handleFeed` only checks for `*PodcastNotFoundError` (`main.go:368`); `*AuthenticationError` falls through to HTTP 500 instead of 401.
5. **Brittle error classification** — `GetPodcasts` uses `strings.Contains(err.Error(), "Unauthorized")` (`podimo/client.go:305`) to detect auth failures; a single wording change from Podimo would turn auth failures into 404s.
6. **Silent config fallbacks** — `parseBool` and `parseDuration` silently ignore invalid env-var input (`config.go:127/136`), producing surprising defaults. Developer decision: fail hard on invalid input.
7. **GraphQL response unbounded read** — `GraphQLClient.Query` uses `io.ReadAll(res.Body)` (`podimo/graphql.go:67`) with no size cap, risking memory exhaustion on large responses.
8. **Test coverage gaps** — Pagination logic, retry cancellation, proxy branches, scraper URL rewriting, and total-chunk failure in RSS generation are entirely untested.

## Detailed Findings

### 1. Server Timeouts and Connection Hardening
- `main.go:149-154` — `&http.Server{Addr: cfg.BindHost, Handler: router}` has zero timeouts. Missing: `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`, `MaxHeaderBytes`.
- Impact: Slowloris attacks, resource exhaustion from clients that never close connections.

### 2. Credential Leakage via DEBUG Logging
- `main.go:174-180` — `loggingMiddleware` logs `r.URL.String()` at both request start and completion.
- When `cfg.Debug` is true, the full path including `/feed/alice/secret123/...` is written to stdout.
- Developer decision: redact the password segment from logged URLs (e.g., `/feed/alice/REDACTED/...`).

### 3. Unbounded In-Memory Maps (Memory Leaks)
- `main.go:41-42` — `App.clients map[string]*http.Client` grows one entry per unique credential hash. No eviction.
- `main.go:47` — `RateLimiter.ips map[string][]time.Time` grows one entry per unique IP. Old IPs are never deleted.
- `podimo/cache.go:14` — `FileCache.keyLocks map[string]*sync.Mutex` grows one entry per unique cache key. File expiry removes the JSON file but not the mutex.
- Impact: Long-running servers accumulate memory proportional to distinct users, IPs, and podcast/episode IDs.

### 4. Auth Error Handling Returns 500 Instead of 401
- `main.go:368-374` — `handleFeed` checks `*podimo.PodcastNotFoundError` → 404. All other errors, including `*podimo.AuthenticationError`, return 500.
- `main.go:203-208` — `handleSearch` and `handleSubscriptions` also return generic 401 via `authenticate(w)` on any `checkAuth` error, losing the distinction between invalid credentials and other failures.
- Impact: Users with expired tokens see "Something went wrong" instead of "Unauthorized".

### 5. Brittle Error Classification in GraphQL Consumers
- `podimo/client.go:305` — `if strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "unauthenticated")`
- Podimo GraphQL errors are extracted as plain strings in `podimo/graphql.go:37-40`. No structured code field is checked.
- If Podimo rewords the error (e.g., "Not Authorized"), `GetPodcasts` wraps it as `PodcastNotFoundError` instead of `AuthenticationError`, and `main.go:368` serves 404 for an auth failure.
- Impact: Silent misclassification of auth failures as missing podcasts.

### 6. Silent Configuration Fallbacks
- `config.go:127` — `parseBool`: any non-matching string (e.g., `"TRUE "`, `"enabled"`) → `false` silently.
- `config.go:136` — `parseDuration`: any non-integer string (e.g., `"1h"`, `"3600s"`) → fallback seconds silently.
- `config.go:100-110` — Blocklist file entries are not validated as UUIDs; malformed lines are stored but will never match the UUID regex used in handlers.
- `main.go:614` — `splitUsernameRegionLocale` silently returns `nl`/`nl-NL` defaults for any malformed username. Intentional per AGENTS.md for backward compatibility.
- Developer decision: invalid env values should cause startup failure with a clear error message.

### 7. GraphQL Transport Risks
- `podimo/graphql.go:67` — `io.ReadAll(res.Body)` reads the entire response into memory with no size limit.
- `podimo/graphql.go:75` and `82` — `json.Unmarshal` does not use `DisallowUnknownFields`, silently ignoring unexpected fields.
- `podimo/graphql.go:69-74` — Non-200 responses truncate the body to 500 bytes but return a plain `fmt.Errorf`.
- Impact: Large GraphQL responses can OOM the process; schema changes are invisible; error context is minimal.

### 8. RSS Concurrency and Retry Edge Cases
- `podimo/rss.go:72-89` — Chunk model spawns goroutines per episode with no global limit. Large podcasts + concurrent feed requests create unbounded goroutines.
- `podimo/rss.go:149` — `buildFeedItem` creates a 10-second `context.WithTimeout` but the parent `ctx` has no enforced timeout in `PodcastsToRss`.
- `podimo/rss.go:232-245` — `URLHeadInfo` retries with hard-coded `time.Sleep(1 * time.Second)` and no cancellation check during sleep.
- `podimo/rss.go:152-155` — Total HEAD failure silently falls back to `audio/mpeg` with length 0, producing incorrect enclosure metadata.

### 9. Proxy and API Key Exposure
- `main.go:457-461` — `graphqlEndpoint()` embeds `ScraperAPI` key as plaintext query parameter.
- `main.go:473-477` — ZenRows branch embeds proxy API key into the proxy URL userinfo.
- `main.go:174-180` — `loggingMiddleware` may log these rewritten URLs if they ever appear in inbound request metadata.
- Impact: API keys are in plaintext in URL strings; accidental logging or error wrapping could expose them.

### 10. Test Coverage Gaps
- `podimo/client_test.go` — `GetPodcasts` pagination loop (`client.go:300-329`) never iterates past first page in tests.
- `podimo/client_test.go` — `SearchPodcasts` intermediate fallback (variant 0 fails, variant 1/2 succeeds) is untested.
- `podimo/client_test.go` — `GetFollowedPodcasts` GraphQL error return (`client.go:376-377`) is never hit.
- `main_test.go` — `checkAuth` token-miss → `Login` → cache-set path (`main.go:498-505`) is bypassed by pre-seeding the cache.
- `main_test.go` — `getHTTPClient` proxy branches (`main.go:473-480`) and `graphqlEndpoint` scraper rewriting (`main.go:462-465`) are never exercised.
- `podimo/rss_test.go` — No test for context cancellation during `URLHeadInfo` retry sleep.
- `podimo/rss_test.go` — No test for `PodcastsToRss` when every episode in a chunk fails `buildFeedItem`.

## Code References
- `main.go:27` — UUID regex for podcast IDs
- `main.go:41-42` — `App.clients` unbounded map
- `main.go:47` — `RateLimiter.ips` unbounded map
- `main.go:59-76` — `RateLimiter.Allow()` with O(n) prune
- `main.go:149-154` — `http.Server` without timeouts
- `main.go:174-180` — `loggingMiddleware` with raw URL logging
- `main.go:183-195` — `rateLimitMiddleware` with `r.RemoteAddr` only
- `main.go:318-374` — `handleFeed` with error type assertions
- `main.go:391-395` — `handleFeedPath` credential extraction
- `main.go:456-461` — `graphqlEndpoint()` ScraperAPI key embedding
- `main.go:463-488` — `getHTTPClient()` unbounded client cache
- `main.go:488-506` — `checkAuth()` token lifecycle
- `main.go:614` — `splitUsernameRegionLocale()` silent fallback
- `config.go:47` — `LoadConfig()` env loading
- `config.go:100-110` — Blocklist loading without UUID validation
- `config.go:127` — `parseBool()` silent fallback
- `config.go:136` — `parseDuration()` silent fallback
- `config.go:146` — `isValidRegion()` linear scan
- `config.go:155` — `isValidLocale()` linear scan
- `podimo/client.go:52-73` — `NewPodimoClient()` token cache loading
- `podimo/client.go:117-123` — `TokenKey()` SHA256 of raw credentials
- `podimo/client.go:125-159` — `getPreregisterToken()` mutable field
- `podimo/client.go:161-187` — `getOnboardingID()` mutable field
- `podimo/client.go:192-229` — `Login()` three-step auth
- `podimo/client.go:247-316` — `GetPodcasts()` pagination and error classification
- `podimo/client.go:305` — Brittle `strings.Contains` error matching
- `podimo/client.go:336-358` — `SearchPodcasts()` variant fallback loop
- `podimo/client.go:364-387` — `GetFollowedPodcasts()` with lazy auth
- `podimo/graphql.go:34-40` — `gqlError` struct
- `podimo/graphql.go:43-82` — `GraphQLClient.Query()` full lifecycle
- `podimo/graphql.go:67` — Unbounded `io.ReadAll`
- `podimo/rss.go:17` — `PodcastsToRss()` entry point
- `podimo/rss.go:72-89` — Per-chunk goroutine spawning
- `podimo/rss.go:107-165` — `buildFeedItem()` with HEAD call
- `podimo/rss.go:149` — 10-second `context.WithTimeout`
- `podimo/rss.go:221-268` — `URLHeadInfo()` retry loop
- `podimo/rss.go:232-245` — Hard-coded 1-second retry sleep
- `podimo/cache.go:12-16` — `FileCache` struct with `keyLocks`
- `podimo/cache.go:23-28` — `NewFileCache()` constructor
- `podimo/cache.go:30-39` — `getKeyLock()` unbounded mutex allocation
- `podimo/cache.go:41-60` — `FileCache.Get()` with per-key file I/O
- `podimo/cache.go:62-75` — `FileCache.Set()` with 0644 permissions
- `podimo/cache_test.go` — Basic get/set/expiration tests
- `podimo/client_test.go` — Constructor and Login tests
- `podimo/graphql_test.go` — Status-code and error tests
- `podimo/rss_test.go` — Audio extraction and HEAD tests
- `main_test.go` — Handler routing and rate limiter tests

## Integration Points

### Inbound References
- `main.go:170` — `/feed/{username}/{password}/{podcast_id}.xml` route with credential-in-path
- `main.go:318` — `/feed/{podcast_id}.xml` route with Basic Auth
- `main.go:202` — `/search` route
- `main.go:260` — `/subscriptions` route
- `main.go:174` — `loggingMiddleware` wraps all routes

### Outbound Dependencies
- `podimo/graphql.go:58` — HTTP POST to Podimo GraphQL endpoint
- `podimo/rss.go:241` — HTTP HEAD to Podimo audio CDN URLs
- `main.go:457` — `graphqlEndpoint()` may rewrite to ScraperAPI proxy
- `main.go:475` — ZenRows proxy for GraphQL requests

### Infrastructure Wiring
- `main.go:114-126` — Three `FileCache` instances created at startup
- `main.go:137` — `RateLimiter` initialized with 8 req/10s window
- `main.go:149-154` — `http.Server` boot
- `config.go:48` — `.env` file loaded at startup

## Architecture Insights
1. **Go rewrite fixed Python-era issues but introduced Go-specific gaps**: The async/sync bridge, serial pagination, and RSS image misalignment from the Python version are resolved. New gaps are unbounded maps, missing server timeouts, and Go-specific concurrency edge cases.
2. **Mutable auth state is load-bearing**: `preauthToken` and `preregID` are intermediate mutable fields required for the final login query. The three-step flow cannot be simplified without changing Podimo's API contract.
3. **Error taxonomy is partially effective**: `PodcastNotFoundError` and `AuthenticationError` exist, but `GetPodcasts` classifies errors via string matching rather than structured codes, and `main.go` does not map `AuthenticationError` to 401 in feed handlers.
4. **FileCache is simple but leaky**: Per-key mutex prevents file corruption but the mutex map grows forever. JSON on disk with 0644 permissions is world-readable.
5. **Per-user HTTP clients improve isolation but leak memory**: Each credential pair gets a dedicated `http.Client` with cookie jar. No eviction means re-authenticated users accumulate clients indefinitely.
6. **GraphQL fragility persists**: The Go rewrite retained the variant-fallback pattern for `SearchPodcasts` (three query variants), validating the precedent that Podimo changes schemas without notice.

## Precedents & Lessons

### Precedent: Go rewrite from Python (entire service)
**Commit(s)**: `4539b58` — "feat: rewrite entire service from Python to Go" (2026-05-24)
**Blast radius**: 30 files across all layers
**Follow-up fixes**:
- `a46d14d` (2026-05-24) — Dockerfile and Makefile migration from Python to Go targets
- `01647d5` (2026-05-24) — Validation pass found dead code and missing structured logging
- `0da7fd6` (2026-05-24) — Test expansion to cover gaps discovered after initial rewrite
- `cdffd8c` (2026-05-24) — Distroless Docker migration
- `8c05dd7` (2026-05-24) — Docs update for new cache paths
**Lessons**: Full rewrites generate immediate follow-ups in build tooling, Docker, tests, and dead-code cleanup. Validation sweeps within hours are essential.

### Precedent: Critical runtime bugs (Python era)
**Commit(s)**: `a476bd0` — "fix: correct four critical runtime bugs" (2026-05-19)
**Blast radius**: 2 files
**Lessons**: Backwards boolean logic and silent `return Exception(...)` instead of `raise` are easy to miss. The Go rewrite's type system prevents the `return`/`raise` class of bug, but not silent fallbacks or brittle string matching.

### Precedent: Adding /search and /subscriptions endpoints (Python era)
**Commit(s)**: `85ff27d` (2026-05-20), `ce5d33f` (2026-05-20), `6764449` (2026-05-20)
**Blast radius**: 5 files
**Lessons**: Podimo's GraphQL schema changes without notice. Hardcoded queries break. Fallback variants and variable-use validation are mandatory. The Go rewrite preserved this pattern.

### Precedent: Bypassing feedgen image validation
**Commit(s)**: `c4e796f` (2026-05-20), `d6034c9` (2026-05-21)
**Blast radius**: 2 files
**Lessons**: When bypassing a library's validation, verify the alternative APIs actually exist. The Go rewrite uses `eduncan911/podcast` which avoids the Python lxml workaround entirely.

### Composite Lessons
- **GraphQL schema changes silently** — hardcoded queries break. Implement fallback variants and validate every declared variable is consumed.
- **Verify library APIs before relying on them** — `fe.lxml()` / `fg.lxml()` were assumed but non-existent.
- **Validation passes after large changes find dead code and logging gaps** — schedule a validation sweep within hours of any change touching `main.go`, `podimo/client.go`, or `podimo/rss.go`.
- **Adding tests to an untested codebase creates cascading fixes** — expect 3-5 follow-up commits within 24 hours.
- **Base image changes break shell/tool assumptions** — audit every `RUN`, `HEALTHCHECK`, and binary dependency.

## Historical Context
- `.rpiv/artifacts/research/2026-05-22_15-54-18_improvement-opportunities.md` — Prior research on the Python codebase; motivated the Go rewrite.
- `.rpiv/artifacts/research/2026-05-24_19-54-46_docker-distroless-migration.md` — Docker migration research.

## Developer Context
**Q (`config.go:127` and `config.go:136`): Should invalid env values cause startup failure to surface misconfiguration, or log a warning and continue with defaults?**
A: Startup failure (Recommended) — hard fail on invalid env values with a clear error message.

**Q (`main.go:174-180`): Should DEBUG logs redact the password portion from `/feed/<username>/<password>/...` URLs, or exclude feed endpoints from DEBUG URL logging entirely?**
A: Redact password in path — keep logging feed URLs but strip the password segment.

## Related Research
- `.rpiv/artifacts/research/2026-05-22_15-54-18_improvement-opportunities.md` — Python baseline issues that motivated the Go rewrite.
- `.rpiv/artifacts/research/2026-05-24_19-54-46_docker-distroless-migration.md` — Docker hardening research.

## Open Questions
1. Should `AuthenticationError` in `handleFeed` return HTTP 401 instead of falling through to 500?
2. Should `FileCache` switch from per-key `sync.Mutex` to a bounded-size LRU mutex pool to prevent unbounded growth?
3. Should `RateLimiter` consult `X-Forwarded-For` for IP extraction when deployed behind a reverse proxy?
4. Should `GraphQLClient.Query` enforce a response body size limit (e.g., via `io.LimitReader`)?
5. Should `PodcastsToRss` enforce a global goroutine semaphore instead of chunk-local WaitGroups?
6. Should `PodimoClient.Login` use a dedicated struct for the three-step auth state instead of mutable fields?
