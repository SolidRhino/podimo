# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Complete rewrite of the service from Python to Go, using `chi` for HTTP routing and `go:embed` for templates.

### Changed
- README rewritten for Go installation instructions (`make build`, `make test`, `make lint`) and module layout.
- Dockerfile switched to multi-stage `golang:1.26-alpine` build producing a static binary, with a `distroless/static-debian13:nonroot` runtime image.
- Makefile tasks replaced with Go equivalents (`go test`, `go vet`, `gofmt`, `go build`).
- Minimum Go version raised to 1.26.
- Environment variable validation now produces clear, actionable error messages on startup.
- GraphQL client enforces a 10MB response size limit and surfaces structured error messages from Podimo API failures.
- Docker `:latest` tag updated only on stable releases, not on every main branch build.

### Fixed
- Removed unreachable dead code in feed URL construction.
- RSS generation now respects context cancellation and retries failed HEAD requests.
- Structured logging replaces `log.Printf` throughout the Podimo client.

### Security
- Credentials are automatically redacted from all log output.
- HTTP server timeouts hardened with sensible defaults.

### Performance
- In-memory caches bounded with LRU eviction to prevent memory exhaustion under load.

### Breaking / Upgrade Notes
- Migration from Python to Go requires switching from the previous Python virtual-env setup to building the Go binary or using the updated Docker image.
