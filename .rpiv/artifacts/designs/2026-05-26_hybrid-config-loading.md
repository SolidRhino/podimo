---
date: 2026-05-26T15:46:08+02:00
author: SolidRhino
commit: deb0bbd
branch: go-rewrite
repository: podimo
topic: "Hybrid config loading with Viper (YAML + env vars + CLI flags)"
tags: [design, config, viper, yaml, env, go]
status: in-progress
parent: .rpiv/artifacts/research/2026-05-24_21-05-47_improvement-opportunities-go-rewrite.md
last_updated: 2026-05-26T15:46:08+02:00
last_updated_by: SolidRhino
---

# Design: Hybrid Config Loading with Viper

## Summary
Replace the hand-rolled `.env` + env-var loader in `config.go` with a Viper-backed hybrid loader that supports `config.yaml` as the primary configuration source while preserving full backward compatibility with environment variables and `.env` files. A new optional `--config` CLI flag lets users specify a custom config file path. The `Config` struct gains `mapstructure` tags for flat YAML unmarshaling. All strict validation (fail on invalid env values) is preserved.

## Requirements
- Support `config.yaml` as the primary configuration format
- Preserve full `.env` and environment-variable backward compatibility
- Maintain strict validation: invalid values cause startup failure with clear messages
- Keep the `Config` struct field names and types stable to avoid breaking `main.go` and tests
- Add an optional `--config` CLI flag for custom config file paths
- Precedence: CLI flags -> env vars (including `.env` pre-loaded via godotenv) -> config file -> hardcoded defaults
- Keep `Regions` and `Locales` hardcoded in Go (Podimo API contract values)
- Retain dead fields (`VideoEnabled`, `VideoCheckEnabled`, `VideoTitleSuffix`, `StoreTokensOnDisk`) for future wiring

## Current State Analysis
`config.go` contains a ~200-line hand-rolled `LoadConfig()` that:
1. Calls `godotenv.Load(".env")`
2. Reads every env var individually with `os.Getenv`
3. Uses custom `parseBool` and `parseDuration` helpers for strict typing
4. Constructs the `Config` literal field-by-field
5. Creates cache dir and loads block-list file
6. Returns `(*Config, error)` on any failure

This works but is verbose, doesn't support config files, and adding new fields requires touching multiple places. The dead fields were discovered by the integration scanner but are intentionally kept per developer decision.

### Key Discoveries
- `main.go` and `main_test.go` depend heavily on `Config` field names and `LoadConfig()` signature -- changing either causes compilation failures across 20+ call sites (`main_test.go:436-478` config tests, `main.go:90` startup, all handler fields) (see `b301fe08-5e8e-4d8` codebase-analyzer report)
- `godotenv` must stay as a pre-step: Viper does not natively read `.env` format; `godotenv.Load` writes into `os.Environ`, which Viper's `AutomaticEnv` picks up (precedent from `d9ec1a2` + `584a4b9` in Python era showed precedence bugs without explicit design)
- `StoreTokensOnDisk` is dead in `main.go` but loaded in `config.go`; it is never checked before creating `FileCache`. Correct.
- `parseBool` and `parseDuration` must stay strict: research found silent fallbacks were a critical issue (commit `77a2003` fixed this). Viper's unmarshaling returns errors on invalid values, preserving this behavior.
- Whitespace trimming was a follow-up fix in `77a2003` -- Viper handles this natively for env vars.

## Scope

### Building
- `config.go` -- add `mapstructure` tags to `Config`, rewrite `LoadConfig()` using Viper, add `LoadConfigWithPath()` helper
- `go.mod` / `go.sum` -- add `github.com/spf13/viper` and its transitive deps
- `main.go` -- add `flag.String("config", "", ...)` before `LoadConfig()` call
- `config.example.yaml` -- new reference YAML file with all options documented
- `.env.example` -- update header comments to note YAML is preferred, but env vars still supported
- `main_test.go` -- update `TestLoadConfig_*` tests to exercise Viper paths; add `TestLoadConfig_WithYAMLFile`

### Not Building
- Hot-reload of config at runtime (Viper supports `WatchConfig` but out of scope)
- Nested/grouped YAML sections (developer chose flat structure)
- Removing or wiring up dead fields (`VideoEnabled`, etc.) -- deferred to future PR
- Changing `Regions`/`Locales` to be YAML-configurable
- Cobra CLI framework -- overkill for a single `--config` flag; standard `flag` is sufficient
- `justfile` recipes -- may be updated in follow-up if desired, but not in this design
- `Dockerfile` changes -- already sets `ENV CACHE_DIR=/tmp/podimo-rss-cache`; no changes needed

## Decisions

### Decision 1: Use flat YAML structure
**Explored:**
- Option A: Flat YAML (`bind_host: 127.0.0.1:12104`, `token_cache_time: 5d`) -- matches current env var naming, trivial `mapstructure` tags (`mapstructure:"bind_host"`), easy mental model for users.
- Option B: Nested YAML (`server: { bind_host: ... }`, `cache: { token_ttl: ... }`) -- more readable for power users, but requires nested `mapstructure` tags and more documentation. Since the `Config` struct is flat, nested would require either a nested struct redesign (breaking `main.go`) or awkward dotted tags (`mapstructure:"server.bind_host"`).
**Decision:** Flat YAML. Evidenced by `Config` struct at `config.go:15` being flat and `main.go` accessing `cfg.BindHost` directly at 60+ call sites. Changing struct shape would cascade.

### Decision 2: Keep godotenv as pre-load step
**Explored:**
- Option A: Drop `.env` support entirely, tell users to use `config.yaml` or real env vars.
- Option B: Call `godotenv.Load(".env")` before Viper initialization so Viper sees `.env` values through `os.Environ`.
- Option C: Add a custom Viper `.env` provider.
**Decision:** Option B. Viper does not natively read `.env`. Precedent `d9ec1a2` -> `584a4b9` from Python era showed that `.env` precedence is load-bearing for users. Dropping it is a breaking change for Docker Compose users who mount `.env` files. `godotenv.Load` modifies `os.Environ` in-process, which Viper's `AutomaticEnv` reads. Cost: one extra dependency we already have.

### Decision 3: Standard `flag` package for `--config`
**Explored:**
- Option A: `github.com/spf13/cobra` -- rich CLI framework, but adds ~10 transitive deps and boilerplate for a single flag.
- Option B: Standard `flag` package -- `flag.String("config", "", "path to config file")` in `main()`, then `flag.Parse()` before `LoadConfig()`.
**Decision:** Standard `flag`. Single flag, zero new deps, idiomatic Go. `LoadConfig()` gains an optional `configFile string` param (or we use a `LoadConfigWithPath` variant).

### Decision 4: Keep dead fields
**Explored:**
- Option A: Delete them now -- clean struct, less confusion.
- Option B: Keep them and wire them up later -- less blast radius, preserves existing `.env` files that may set these vars.
**Decision:** Keep. The developer explicitly chose "Keep but wire up later." Removing them breaks any `.env` that sets `ENABLE_VIDEO`, `STORE_TOKENS_ON_DISK`, etc.

### Decision 5: Keep Regions and Locales hardcoded
**Explored:**
- Option A: YAML-configurable
- Option B: Hardcoded in Go
**Decision:** Hardcoded. These are Podimo API contract values; user misconfiguration would cause broken API calls.

### Decision 6: Unify all env vars under `PODIMO_` prefix
**Explored:**
- Option A: Dual-bind both legacy bare names (`DEBUG`) and new prefixed names (`PODIMO_DEBUG`) via `viper.BindEnv`. Preserves 100% backward compat but adds ~15 explicit BindEnv calls and perpetuates inconsistent naming.
- Option B: Unify everything under `PODIMO_` prefix (`PODIMO_DEBUG`, `PODIMO_CACHE_DIR`, etc.). Breaking change for bare-name env vars but finally consistent. `PODIMO_HOSTNAME`, `PODIMO_BIND_HOST`, `PODIMO_EMAIL`, `PODIMO_PASSWORD` already use the prefix.
**Decision:** Option B. The developer chose "Unify to PODIMO_ prefix." This is a breaking change documented in the migration notes. Existing `.env` files and Docker `ENV` directives using bare names (`DEBUG`, `CACHE_DIR`, etc.) must be updated. The `.env.example` will be rewritten with all `PODIMO_` prefixed names.

## Architecture

### config.go -- MODIFY
```go
// TODO: fill with Slice 1 + Slice 2 merged code
```

### go.mod -- MODIFY
```go
// TODO: fill with Slice 1 dependency additions
```

### main.go -- MODIFY
```go
// TODO: fill with Slice 2 flag parsing changes
```

### config.example.yaml -- NEW
```yaml
# TODO: fill with Slice 3 reference config
```

### .env.example -- MODIFY
```bash
# TODO: fill with Slice 3 updated comments
```

### main_test.go -- MODIFY
```go
// TODO: fill with Slice 4 updated tests
```

## Slices

### Slice 1: Foundation -- Config struct + Viper dependency
**Files**: `config.go`, `go.mod`, `go.sum`

#### Automated Verification:
- [ ] Type checking passes: `go test ./...` compiles
- [ ] Viper dependency resolves: `go mod tidy` completes without errors
- [ ] Config struct has `mapstructure` tags on every exported field

#### Manual Verification:
- [ ] `go vet ./...` reports no issues

### Slice 2: Core -- LoadConfig() rewrite + CLI flag
**Files**: `config.go`, `main.go`

#### Automated Verification:
- [ ] `TestLoadConfig_InvalidBool` still fails on bad boolean with clear error
- [ ] `TestLoadConfig_InvalidDuration` still fails on bad duration with clear error
- [ ] `TestLoadConfig_ValidDefaults` passes with zero env vars
- [ ] `TestLoadConfig_WithYAMLFile` creates a temp YAML, loads it, and asserts values
- [ ] `TestHandleFeed_*` suite still passes (no regression)
- [ ] `TestHandleSearch_*` suite still passes
- [ ] `TestHandleSubscriptions_*` suite still passes

#### Manual Verification:
- [ ] `./podimo-rss --config=/tmp/test.yaml` starts with values from YAML
- [ ] `PODIMO_DEBUG=true ./podimo-rss` still picks up env var
- [ ] `.env` with `DEBUG=true` still works when `godotenv.Load` runs before Viper
- [ ] Invalid config file path produces clear startup error

### Slice 3: Examples -- config.example.yaml + .env.example
**Files**: `config.example.yaml`, `.env.example`

#### Automated Verification:
- [ ] `config.example.yaml` is valid YAML (`yq` or `python -c 'import yaml; yaml.safe_load(...)'`)
- [ ] `.env.example` syntax is valid (`godotenv.Load` succeeds on it in a test)

#### Manual Verification:
- [ ] `config.example.yaml` comments are accurate and match `Config` struct defaults
- [ ] `.env.example` header tells users YAML is preferred

### Slice 4: Tests -- Update test suite
**Files**: `main_test.go`

#### Automated Verification:
- [ ] `go test ./... -v` passes (no failures in config, handler, or package tests)
- [ ] `go vet ./...` passes
- [ ] `go mod tidy` produces no changes

#### Manual Verification:
- [ ] `just test` passes
- [ ] `just lint` passes

## Desired End State

### Usage: config.yaml
```yaml
# config.yaml
hostname: "localhost:12104"
bind_host: "127.0.0.1:12104"
protocol: "http"
cache_dir: "./cache"
debug: false
local_credentials: false
public_feeds: false
token_cache_time: "5d"
podcast_cache_time: "6h"
head_cache_time: "7d"
```

### Usage: CLI flag
```bash
./podimo-rss --config=/etc/podimo-rss/config.yaml
```

### Usage: env var (still works)
```bash
PODIMO_DEBUG=true PODIMO_BIND_HOST=0.0.0.0:12104 ./podimo-rss
```

### Usage: .env file (still works)
```bash
# .env
DEBUG=true
BIND_HOST=0.0.0.0:12104
```

## File Map
- `config.go` -- MODIFY -- Config struct tags + Viper-based LoadConfig
- `go.mod` / `go.sum` -- MODIFY -- add Viper dependency
- `main.go` -- MODIFY -- parse `--config` flag before LoadConfig()
- `config.example.yaml` -- NEW -- reference YAML config
- `.env.example` -- MODIFY -- note YAML preference
- `main_test.go` -- MODIFY -- config parsing tests for Viper + YAML

## Ordering Constraints
1. Slice 1 must complete before Slice 2 (types needed for implementation)
2. Slice 2 must complete before Slice 3 (need final LoadConfig behavior to document in example)
3. Slice 2 must complete before Slice 4 (tests exercise the new loader)
4. Slice 3 and Slice 4 can conceptually be parallel but are sequential for checkpoint simplicity

## Verification Notes
- `main_test.go:436-478` currently has four `TestLoadConfig_*` tests that exercise strict validation. These are load-bearing correctness checks from the `77a2003` validation fix. Must be preserved exactly.
- `main_test.go:24-45` (`setupTestApp`) constructs a `&Config{}` literal. This should NOT need changes if the struct shape is stable, but any new required field would break it. We are not adding required fields.
- **Breaking change**: Env var names unified under `PODIMO_` prefix. Bare names (`DEBUG`, `CACHE_DIR`, etc.) no longer work. `main_test.go:436-478` tests must rename `t.Setenv("DEBUG", ...)` to `t.Setenv("PODIMO_DEBUG", ...)` etc.
- **Docker**: `Dockerfile` sets `ENV CACHE_DIR=/tmp/podimo-rss-cache`. This must be updated to `ENV PODIMO_CACHE_DIR=/tmp/podimo-rss-cache` for Viper to pick it up.
- **IMPORTANT**: All env var names are now consistently prefixed with `PODIMO_`. The `PODIMO_HOSTNAME`, `PODIMO_BIND_HOST`, `PODIMO_EMAIL`, `PODIMO_PASSWORD` names already had it and are unchanged.

## Performance Considerations
- Viper unmarshaling adds ~1-2 ms at startup (negligible for a long-running HTTP server)
- No runtime config access changes -- `a.cfg.*` reads are still direct struct field access, not Viper lookups
- File I/O limited to startup (config file read once)

## Migration Notes
- **Breaking change — env var prefix unification**: All environment variables now require `PODIMO_` prefix. Bare names (`DEBUG`, `CACHE_DIR`, `LOCAL_CREDENTIALS`, etc.) no longer work. Update your `.env` file: change `DEBUG=true` to `PODIMO_DEBUG=true`, `CACHE_DIR=./cache` to `PODIMO_CACHE_DIR=./cache`, etc.
- **Existing `.env` files**: Still supported, but variables must use `PODIMO_` prefix. `godotenv.Load(".env")` runs before Viper initialization.
- **New `config.yaml`**: Users can migrate gradually by moving env vars into YAML. No other breaking changes.
- **Docker**: `Dockerfile` `ENV` directives must use prefixed names (`ENV PODIMO_CACHE_DIR=/tmp/podimo-rss-cache`). `.env.docker` must also be updated if it uses bare names.
- **Config precedence**: CLI flag (`--config`) → env vars (via `godotenv` then Viper) → config file → defaults.

## Pattern References
- `config.go:15-205` -- current hand-rolled config loading (what to replace)
- `main.go:90` -- `LoadConfig()` call site
- `main_test.go:24-45` -- test Config literal construction
- `main_test.go:436-478` -- strict validation tests (must preserve behavior)

## Developer Context
- **Q (Step 4): Env var naming — dual bind or unify?** A: Unify to `PODIMO_` prefix (breaking but clean)
- **Q (Step 4): YAML structure -- flat vs nested?** A: Flat (Recommended)
- **Q (Step 4): Dead fields -- remove or keep?** A: Keep but wire up later
- **Q (Step 4): Regions/Locales -- hardcoded or YAML-configurable?** A: Keep hardcoded

## Design History
- Slice 1: Foundation — approved as generated
- Slice 2: Core — pending
- Slice 3: Examples — pending
- Slice 4: Tests — pending

## References
- `.rpiv/artifacts/research/2026-05-24_21-05-47_improvement-opportunities-go-rewrite.md` -- research that identified config strict validation and silent fallback issues
- `.rpiv/artifacts/plans/2026-05-25_21-55-14_comprehensive-go-hardening.md` -- plan for comprehensive hardening that included strict env validation
- `github.com/spf13/viper` -- Go configuration library
- `github.com/joho/godotenv` -- already in use for `.env` loading
