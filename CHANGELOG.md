# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [0.6.0] - 2026-07-21

### Added
- Subscriptions pagination: `/subscriptions` now renders on page load (LocalCredentials mode) with prev/next navigation and a per-page dropdown (10/20/50, default 10). Server-side slicing over the already-sorted results.
- Enhanced subscriptions toolbar: sort and per-page dropdowns wrapped in a labeled toolbar row (sort-arrow icon + "Sort", "Show") with custom chevron, accent focus ring, and dark-mode chevron swap. CSS ported from an Open Design exploration.
- Numbered pagination footer: `‹ 1 2 3 … N ›` with `aria-current="page"` on the active page, ellipsis gaps, and "Page X / Y (N podcasts)" summary. New `paginationPages` helper with edge windows (first/last 3 pages) and ellipsis for gaps > 1.
- User-selectable sort for subscriptions: Newest episode first (default), Most episodes, A–Z. Changing sort or per-page re-fetches via HTMX and resets to page 1.
- `/subscriptions.opml` endpoint: OPML 2.0 export of followed podcasts with `xmlUrl` entries pointing at per-podcast RSS feeds.
- `/ready` endpoint: probes outbound Podimo GraphQL reachability with a 10-second cached result. Returns 503 when unreachable, 200 when reachable. Use for Kubernetes readiness probes; use `/health` for liveness.
- ETag, Last-Modified, and Cache-Control headers on feed responses with `If-None-Match`/`If-Modified-Since` short-circuit to 304 Not Modified.
- Episode count and latest-episode date displayed in the subscriptions view. Date format configurable via `PODIMO_DATE_FORMAT` (Go `time.Format` layout, default `2006-01-02`).
- Configurable log level via `PODIMO_LOG_LEVEL` (debug/info/warn/warning/error), replacing the boolean `DEBUG` flag.
- GetPodcasts pagination guard: caps at 200 pages with episode-ID dedup to prevent pathological infinite loops.
- `withAuthRetry` helper: refreshes the token once on `*AuthenticationError` and retries, shared by search, subscriptions, and feed handlers.
- Response status code logged in request logging middleware (5xx → Error, 4xx → Warn, 2xx/3xx → Info).
- CSS minified with source maps; TTF fonts replaced with WOFF2 variable font.
- Direct browser visits to `/search` and `/subscriptions` (without HTMX header) redirect to `/`.
- Dependabot for GitHub Actions auto-updates.

### Changed
- Subscriptions load automatically on page open (LocalCredentials mode) instead of behind a "Show subscriptions" button.
- In NeedCredentials mode, subscriptions load via a `credentials-ready` trigger fired by JS once both email and password are filled, avoiding a premature 401 challenge on first paint.
- Per-page option labels compacted to "10 / page" (was "10 per page").
- Removed unused `.btn-sm` CSS class (replaced by `.page-btn`).

### Fixed
- `episodeCount` decoded as `float64` to match live Podimo GraphQL response types.
- Correct Podimo GraphQL field names used for subscription metadata.

## [0.5.0] - 2026-07-17

### Added
- Complete rewrite of the service from Python to Go, using `chi` for HTTP routing and `go:embed` for templates.
- UI refined with Open Design: form sections, field groups, skip links, ARIA labels, aria-live regions, autocomplete attributes, HTMX loading spinners.
- Responsive breakpoints (768px, 480px) with iOS zoom prevention and mobile-first button layouts.
- Dark mode contrast improvements: elevated surfaces, focus rings, error background readability.
- Reduced-motion support (`prefers-reduced-motion`).
- Self-hosted Newsreader variable font (WOFF2 + `@font-face` in `fonts.css`); CSS minified with source maps.

### Changed
- All CDN dependencies removed — HTMX, Alpine.js, QRCode.js, and Newsreader font are self-hosted under `/static/`.
- README rewritten for Go installation instructions (`make build`, `make test`, `make lint`) and module layout.
- Dockerfile switched to multi-stage `golang:1.26-alpine` build producing a static binary, with a `scratch` runtime image.
- Makefile tasks replaced with Go equivalents (`go test`, `go vet`, `gofmt`, `go build`).
- Minimum Go version raised to 1.26.
- Environment variable validation now produces clear, actionable error messages on startup.
- GraphQL client enforces a 10MB response size limit and surfaces structured error messages from Podimo API failures.
- Docker `:latest` tag updated only on stable releases, not on every main branch build.
- Footer: removed coffee link, corrected GitHub URL, opens in new tab.
- Search field and button now sit side-by-side (inline flex layout).
- Config loader migrated from Viper to koanf.

### Fixed
- QR code rendering: moved library to `<head>`, replaced fragile Alpine `x-init` with `DOMContentLoaded` script reading from anchor element.
- Copy-to-clipboard button reads URL from DOM anchor instead of embedded JS string.
- Removed unreachable dead code in feed URL construction.
- RSS generation now respects context cancellation and retries failed HEAD requests.
- Structured logging replaces `log.Printf` throughout the Podimo client.
- Redundant nil checks around type assertions removed (golangci-lint S1020).
- Capitalized error strings lowercased (golangci-lint ST1005).
- All unchecked error returns fixed (golangci-lint errcheck — 0 issues remaining).
- Dead CSS removed: unused `--space-2xl` variable, dead `hr` selector, redundant `@media 600px` breakpoint, simplified `.feed-result` dark mode.

### Security
- Credentials are automatically redacted from all log output.
- HTTP server timeouts hardened with sensible defaults.
- No external requests — all assets served from the embedded filesystem.

### Performance
- In-memory caches bounded with LRU eviction to prevent memory exhaustion under load.

### Breaking / Upgrade Notes
- Migration from Python to Go requires switching from the previous Python virtual-env setup to building the Go binary or using the updated Docker image.
- Config loader changed from Viper to koanf — `config.yaml` format is unchanged but some edge-case env var behaviors may differ.
