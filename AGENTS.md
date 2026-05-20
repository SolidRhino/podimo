# Agent Context: Podimo to RSS

> This file provides context for AI assistants working on the codebase.
> Last updated: 2026-05-20

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

**Podimo to RSS** is a self-hosted Python web service that reverse-engineers the Podimo mobile GraphQL API to expose exclusive/paywalled podcasts as standard RSS feeds. Users authenticate with their Podimo credentials, and the tool generates RSS XML that any podcast app (Apple Podcasts, Overcast, Pocket Casts, etc.) can subscribe to.

- **Language:** Python 3.12+ (fully typed, `mypy` passing)
- **Framework:** Quart (async Flask-like) + Hypercorn
- **API:** Podimo GraphQL (`https://podimo.com/graphql`)
- **Auth:** HTTP Basic Auth (credentials embedded in URL) or local credentials mode
- **Tests:** pytest + pytest-asyncio (70 tests across 4 modules)
- **CI:** GitHub Actions — test matrix on Python 3.10/3.11/3.12, Docker image publishing to GHCR
- **License:** EUPL 1.2

## Quick Architecture

```
main.py              → Quart web server, routes, RSS feed generation
podimo/
  client.py          → GraphQL API client (login, episode fetching)
  config.py          → Environment variables, constants, block list
  cache.py           → diskcache-backed token, podcast, and HEAD caches
  utils.py           → Header generation, async wrapping, helpers
tests/               → pytest suite (client, RSS, web routes, utils)
mypy.ini             → mypy configuration (ignores missing third-party stubs)
```

## Key Files & Responsibilities

| File | What it does |
|------|-------------|
| `main.py` | Entry point. Defines routes (`/`, `/health`, `/feed/<id>.xml`, `/feed/<u>/<p>/<id>.xml`). Generates RSS XML via `feedgen`. |
| `podimo/client.py` | Podimo GraphQL client. Handles pre-register token → onboarding ID → login token flow. Fetches paginated episodes. |
| `podimo/config.py` | Loads `.env` + env vars. Defines regions, locales, cache TTLs, feature flags. |
| `podimo/cache.py` | Three diskcache instances: `TOKENS` (auth tokens), `podcast_cache` (episode lists), `head_cache` (audio file metadata). |
| `podimo/utils.py` | `generateHeaders()` (spoofs Android app), `async_wrap()` (sync→async bridge), `token_key()` (SHA256 cache key), `is_correct_email_address()`. |
| `templates/index.html` | Form: email, password, podcast ID, region, locale. Extracts UUID from full Podimo URLs via JS regex. Shows warning when credentials are embedded in URL. |
| `templates/feed_location.html` | Shows generated feed URL with copy button and QR code. |
| `tests/conftest.py` | Shared pytest fixtures (mock podcast data with/without episodes, reset rate limiter). |
| `tests/test_client.py` | Tests for `PodimoClient` constructor, `getPodcastName`, `token_key`. |
| `tests/test_rss.py` | Tests for `podcastsToRss`, `extract_audio_url`, content-type logic, `urlHeadInfo`. |
| `tests/test_web.py` | Tests for Quart routes (`/`, `/health`, 404, 400 for invalid UUID), UUID regex, rate limiting. |
| `tests/test_utils.py` | Tests for `is_correct_email_address`, `generateHeaders`, `chunks`, `async_wrap`. |

## Authentication Flow (Podimo GraphQL)

The client must make **3 sequential GraphQL requests** to authenticate:

1. **`AuthorizationPreregisterUser`** → get `preauth_token`
2. **`OnboardingQuery`** → get `prereg_id` (onboarding flow ID)
3. **`AuthorizationAuthorize`** → get `token` (final auth token, valid ~5 days)

All subsequent requests (episode fetching) use the final token in the `authorization` header.

## Caching Strategy

| Cache | Key | TTL | Purpose |
|-------|-----|-----|---------|
| `TOKENS` | `SHA256(username~password)` | 5 days | Avoid re-logging in for every feed refresh |
| `podcast_cache` | `podcast_id` | 6 hours | Avoid re-fetching episode lists on every podcast app poll |
| `head_cache` | `episode_id` | 7 days | Avoid HEAD requests to audio URLs (content-length, content-type) |
| `cookie_jars` | `user_key` | in-memory | Maintain session cookies between requests |

## Important Code Patterns

### Sync→Async Bridge
`cloudscraper` and `ZenRowsClient` are synchronous. They are wrapped via `async_wrap()` (uses `loop.run_in_executor`). `ZenRowsClient` is a **lazy singleton** — created once at first use, not per request.

```python
response = await async_wrap(scraper.post)(url, headers=..., json=..., timeout=...)
```

### URL-Based Credential Embedding
In the default mode, credentials are embedded in the feed URL for podcast apps to use:
```
https://email%40domain.com:password@host/feed/<podcast_id>.xml?region=nl&locale=nl-NL
```
Region and locale are comma-appended to the username in the Basic Auth string.

### Chunked Episode Processing
Episodes are added to the RSS feed in chunks of 5 with `asyncio.gather` to parallelize HEAD requests:
```python
for chunk in chunks(episodes, 5):
    await asyncio.gather(*[addFeedEntry(fg, ep, session, locale) for ep in chunk])
```

### Rate Limiting
Feed endpoints (`/feed/...`) are protected by a per-IP rate limiter (8 requests per 10-second window):
```python
@app.route("/feed/<...")
@limit_request()
async def serve_feed(...):
```

### Custom Exceptions
The client raises structured exceptions instead of generic `RuntimeError`:
- `PodimoError` — base exception
- `PodcastNotFoundError` — podcast ID doesn't exist
- `AuthenticationError` — invalid credentials

### Request Logging
Requests are logged at both start and completion with timing:
- `@app.before_request` — logs incoming request (method, URL, IP, User-Agent), stores start time
- `@app.after_request` — logs response status code and request duration in seconds

```python
# Example log output (DEBUG mode):
# --> GET /feed/12345-...xml from 192.168.1.1 UA=Overcast/1.0
# <-- GET /feed/12345-...xml 200 (0.423s)
```

### Health Check Endpoint
A lightweight `/health` endpoint returns `{"status": "ok", "service": "podimo-rss"}`. This is used by Docker `HEALTHCHECK` and orchestration tools (Kubernetes, Docker Compose, etc.). The endpoint has no external dependencies and should always return 200.

```python
@app.route("/health")
async def health():
    return jsonify({"status": "ok", "service": "podimo-rss"})
```

## Common Tasks

### Adding a new region/locale
Edit `podimo/config.py`:
- Add to `LOCALES` list (e.g., `'fr-FR'`)
- Add to `REGIONS` tuple (e.g., `('fr', 'France')`)

### Changing cache TTLs
Set environment variables in `.env` or shell:
- `TOKEN_CACHE_TIME` (default: 432000 = 5 days)
- `PODCAST_CACHE_TIME` (default: 21600 = 6 hours)
- `HEAD_CACHE_TIME` (default: 604800 = 7 days)

### Enabling/disabling features
- `LOCAL_CREDENTIALS=true` — single-user mode, credentials stored server-side
- `PUBLIC_FEEDS=true` — removes `<itunes:block>` from RSS
- `ENABLE_VIDEO=true` — adds HLS video URLs to episode descriptions
- `DEBUG=true` — verbose logging

### Running locally
```bash
python -m venv venv
source venv/bin/activate
pip install -r requirements.txt
python main.py
# Visit http://localhost:12104
```

### Running tests
```bash
pip install -r requirements.txt
pytest -v
mypy podimo/ main.py
```

### Running in Docker
```bash
docker build -t podimo-rss .
docker run -p 12104:12104 -e PODIMO_BIND_HOST=0.0.0.0:12104 podimo-rss
```

## Known Gotchas & Pitfalls

✅ **FIXED** — `return ValueError(...)` instead of `raise` in `client.py`  
✅ **FIXED** — Fragile `getPodcastName` via dict insertion order  
✅ **FIXED** — Backwards content-type logic overwriting correct MIME types  
✅ **FIXED** — Empty episode list producing malformed RSS  
✅ **FIXED** — `ZenRowsClient` created per request (now a lazy singleton)  
✅ **FIXED** — Block list using substring matching (now exact match)  
✅ **FIXED** — No rate limiting (added per-IP limit: 8 req/10s)  
✅ **FIXED** — CORS wildcard on all responses (removed)  
✅ **FIXED** — Docker running as root with build deps (now multi-stage + non-root)  
✅ **FIXED** — `DEBUG=true` in `.env.example` (now commented out with security warning)  
✅ **FIXED** — String exception matching in `serve_feed` fallback (all structured via `PodimoError` subclasses)  
✅ **FIXED** — Logging only at `@app.before_request` (now logs both start and end with duration + status code)  
✅ **FIXED** — No `/health` endpoint for Docker orchestration (added lightweight JSON health probe)

**Remaining:**
- **`split_username_region_locale` silent fallback** — If the username doesn't contain exactly 2 commas, it silently defaults to Dutch (`nl`, `nl-NL`). This is intentional for podcast app compatibility but can surprise non-Dutch users. **Do not change without a migration plan** — existing feed URLs would break.

## Podcast ID Discovery

Currently, users must manually find the podcast ID from Podimo's website or app. The web form (`templates/index.html`) will extract the UUID from a pasted Podimo URL (e.g. `https://open.podimo.com/podcast/09c55c96-...`) via client-side JavaScript regex.

**Future improvement:** Add a `/search?q=<query>` endpoint that queries Podimo's GraphQL `searchPodcasts` (if available in the API schema) so users can search by name without visiting Podimo's website. Alternatively, fetch the user's subscribed podcasts after login and present them as a selectable list.

## Testing

There is now a **pytest suite** with 4 test modules:

| File | Coverage |
|------|----------|
| `tests/test_client.py` | `PodimoClient` constructor validation, `getPodcastName`, `token_key` |
| `tests/test_rss.py` | `podcastsToRss` with/without episodes, `extract_audio_url`, `urlHeadInfo`, content-type logic |
| `tests/test_web.py` | Quart routes (`/` 200, `/health` 200, 404, 400 for invalid UUID), UUID regex, rate limiter |
| `tests/test_utils.py` | `is_correct_email_address`, `generateHeaders`, `chunks`, `async_wrap` |

Run with:
```bash
pytest -v
pytest --cov=podimo --cov=main tests/  # with coverage
```

## Dependencies

See `requirements.txt`. Key runtime deps:
- `quart` (~=0.20.0) + `hypercorn` (~=0.17.0) — async web server
- `feedgen` (~=0.9.0) — RSS/Atom generation
- `aiohttp` (~=3.13.5) — async HTTP client (for HEAD requests)
- `cloudscraper` (~=1.2.71) — bypasses Cloudflare bot detection (sync)
- `diskcache` (~=5.6.3) — disk-backed key-value cache
- `zenrows` (~=1.3.2) + `scraperapi` — optional proxy services

Dev/test deps:
- `pytest` (~=8.0.0) + `pytest-asyncio` (~=0.23.0) + `pytest-cov` (~=6.0.0)
- `mypy` (~=1.8.0)

**Security note:** `werkzeug` is pinned to `~=3.1.8` to include the GHSA-q34m-jh98-gwm2 fix (path traversal vulnerability in earlier 3.0.x releases). Do not downgrade.

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
| `CACHE_DIR` | `./cache` | Where diskcache stores data |
| `BLOCK_LIST_FILE` | `./.block-list` | File with blocked podcast IDs |

## Security Notes for Agents

- **Never commit real credentials** to `.env` or the repo. `.env` is in `.gitignore`.
- **Credentials in URLs** are unavoidable for podcast app compatibility, but warn users. The UI now displays a prominent notice when `need_credentials=true`.
- **Use `LOCAL_CREDENTIALS=true`** for personal instances to avoid embedding passwords in URLs.
- **Always run behind HTTPS** (reverse proxy) in production — Basic Auth is cleartext otherwise.
- **Auth tokens are sensitive** — they grant full Podimo account access. `STORE_TOKENS_ON_DISK` should be `false` on shared/multi-user instances.
- **Rate limiting is active** — 8 requests per 10-second window per IP on feed endpoints.

## Refactoring Opportunities

If modifying this codebase, consider:
- ~~Replacing `cloudscraper` + `async_wrap` with an async-native anti-bot client~~ (deferred — working well enough)
- ~~Adding `pytest` + `pytest-asyncio` tests~~ ✅ Done
- ~~Adding rate limiting~~ ✅ Done (lightweight in-memory)
- ~~Cleaning the Dockerfile~~ ✅ Done (multi-stage + non-root)
- ~~Replacing substring-based block list~~ ✅ Done (exact match)
- ~~Adding health check endpoint~~ ✅ Done (`/health` JSON endpoint)
- Adding more granular rate limits per-user (currently IP-based only)
- Moving from `diskcache` to `redis` or similar for multi-instance deployments
- ~~Adding `workflow_dispatch` to CI~~ ✅ Done (manual test runs)
- Configuring `mypy --strict` (currently using basic config in `mypy.ini`)
- Adding a `/search` endpoint via Podimo GraphQL search (if schema supports it)

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
