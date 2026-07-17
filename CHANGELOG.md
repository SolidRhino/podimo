# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.0] - 2026-07-17

### Added
- Complete rewrite of the service from Python to Go, using `chi` for HTTP routing and `go:embed` for templates.
- UI refined with Open Design: form sections, field groups, skip links, ARIA labels, aria-live regions, autocomplete attributes, HTMX loading spinners.
- Responsive breakpoints (768px, 480px) with iOS zoom prevention and mobile-first button layouts.
- Dark mode contrast improvements: elevated surfaces, focus rings, error background readability.
- Reduced-motion support (`prefers-reduced-motion`).
- Self-hosted Newsreader font (TTF + `@font-face` in `fonts.css`).

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
