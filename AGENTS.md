# Agent Context: Podimo to RSS

> This file provides context for AI assistants working on the codebase.
> Last updated: 2025

## Project Overview

**Podimo to RSS** is a self-hosted Python web service that reverse-engineers the Podimo mobile GraphQL API to expose exclusive/paywalled podcasts as standard RSS feeds. Users authenticate with their Podimo credentials, and the tool generates RSS XML that any podcast app (Apple Podcasts, Overcast, Pocket Casts, etc.) can subscribe to.

- **Language:** Python 3.10+ (fully typed, `mypy` passing)
- **Framework:** Quart (async Flask-like) + Hypercorn
- **API:** Podimo GraphQL (`https://podimo.com/graphql`)
- **Auth:** HTTP Basic Auth (credentials embedded in URL) or local credentials mode
- **Tests:** pytest + pytest-asyncio (3 test modules)
- **License:** EUPL 1.2

## Quick Architecture

```
main.py              → Quart web server, routes, RSS feed generation
podimo/
  client.py          → GraphQL API client (login, episode fetching)
  config.py          → Environment variables, constants, block list
  cache.py           → diskcache-backed token, podcast, and HEAD caches
  utils.py           → Header generation, async wrapping, helpers
tests/               → pytest suite (client, RSS, web routes)
mypy.ini             → mypy configuration (ignores missing third-party stubs)
```

## Key Files & Responsibilities

| File | What it does |
|------|------------|
| `main.py` | Entry point. Defines routes (`/`, `/feed/<id>.xml`, `/feed/<u>/<p>/<id>.xml`). Generates RSS XML via `feedgen`. |
| `podimo/client.py` | Podimo GraphQL client. Handles pre-register token → onboarding ID → login token flow. Fetches paginated episodes. |
| `podimo/config.py` | Loads `.env` + env vars. Defines regions, locales, cache TTLs, feature flags. |
| `podimo/cache.py` | Three diskcache instances: `TOKENS` (auth tokens), `podcast_cache` (episode lists), `head_cache` (audio file metadata). |
| `podimo/utils.py` | `generateHeaders()` (spoofs Android app), `async_wrap()` (sync→async bridge), `token_key()` (SHA256 cache key). |
| `templates/index.html` | Form: email, password, podcast ID, region, locale. Extracts UUID from full Podimo URLs. Shows warning when credentials are embedded in URL. |
| `templates/feed_location.html` | Shows generated feed URL with copy button and QR code. |
| `tests/conftest.py` | Shared pytest fixtures (mock podcast data with/without episodes). |
| `tests/test_client.py` | Tests for `PodimoClient` constructor, `getPodcastName`, `token_key`. |
| `tests/test_rss.py` | Tests for `podcastsToRss`, `extract_audio_url`, content-type logic. |
| `tests/test_web.py` | Tests for Quart routes, UUID regex validation, rate limiting. |

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

**Remaining:**
- **Debug enabled in template** — `.env.example` ships with `DEBUG=true` uncommented.
- **`split_username_region_locale` silent fallback** — If the username doesn't contain exactly 2 commas, it silently defaults to Dutch (`nl`, `nl-NL`). This is intentional for podcast app compatibility but can surprise non-Dutch users. **Do not change without a migration plan** — existing feed URLs would break.
- **String exception matching still partially present** — The `except Exception` fallback in `serve_feed` still string-matches as a last resort. The primary path now uses structured `PodimoError` subclasses.
- **Logging at `@app.before_request`** — Request logging happens before processing, so errors during the request are logged *after* the initial log line.

## Testing

There is now a **pytest suite** with 3 test modules:

| File | Coverage |
|------|----------|
| `tests/test_client.py` | `PodimoClient` constructor validation, `getPodcastName`, `token_key` |
| `tests/test_rss.py` | `podcastsToRss` with/without episodes, `extract_audio_url`, content-type logic |
| `tests/test_web.py` | Quart routes (`/` 200, 404, 400 for invalid UUID), UUID regex, rate limiter |

Run with:
```bash
pytest -v
pytest --cov=podimo --cov=main tests/  # with coverage
```

## Dependencies

See `requirements.txt`. Key runtime deps:
- `quart` + `hypercorn` — async web server
- `feedgen` — RSS/Atom generation
- `aiohttp` — async HTTP client (for HEAD requests)
- `cloudscraper` — bypasses Cloudflare bot detection (sync)
- `diskcache` — disk-backed key-value cache
- `zenrows` + `scraperapi` — optional proxy services

Dev/test deps:
- `pytest` + `pytest-asyncio`
- `mypy`

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
- Adding more granular rate limits per-user (currently IP-based only)
- Moving from `diskcache` to `redis` or similar for multi-instance deployments
- Adding health check endpoint (`/health`) for Docker orchestration
- Configuring `mypy --strict` (currently using basic config in `mypy.ini`)
