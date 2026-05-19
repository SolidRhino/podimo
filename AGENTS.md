# Agent Context: Podimo to RSS

> This file provides context for AI assistants working on the codebase.
> Last updated: 2025

## Project Overview

**Podimo to RSS** is a self-hosted Python web service that reverse-engineers the Podimo mobile GraphQL API to expose exclusive/paywalled podcasts as standard RSS feeds. Users authenticate with their Podimo credentials, and the tool generates RSS XML that any podcast app (Apple Podcasts, Overcast, Pocket Casts, etc.) can subscribe to.

- **Language:** Python 3.10+
- **Framework:** Quart (async Flask-like) + Hypercorn
- **API:** Podimo GraphQL (`https://podimo.com/graphql`)
- **Auth:** HTTP Basic Auth (credentials embedded in URL) or local credentials mode
- **License:** EUPL 1.2

## Quick Architecture

```
main.py              → Quart web server, routes, RSS feed generation
podimo/
  client.py          → GraphQL API client (login, episode fetching)
  config.py          → Environment variables, constants, block list
  cache.py           → diskcache-backed token, podcast, and HEAD caches
  utils.py           → Header generation, async wrapping, helpers
templates/           → Jinja2 HTML (index form, feed result with QR code)
```

## Key Files & Responsibilities

| File | What it does |
|------|------------|
| `main.py` | Entry point. Defines routes (`/`, `/feed/<id>.xml`, `/feed/<u>/<p>/<id>.xml`). Generates RSS XML via `feedgen`. |
| `podimo/client.py` | PodimoGraphQL client. Handles pre-register token → onboarding ID → login token flow. Fetches paginated episodes. |
| `podimo/config.py` | Loads `.env` + env vars. Defines regions, locales, cache TTLs, feature flags. |
| `podimo/cache.py` | Three diskcache instances: `TOKENS` (auth tokens), `podcast_cache` (episode lists), `head_cache` (audio file metadata). |
| `podimo/utils.py` | `generateHeaders()` (spoofs Android app), `async_wrap()` (sync→async bridge), `token_key()` (SHA256 cache key). |
| `templates/index.html` | Form: email, password, podcast ID, region, locale. Extracts UUID from full Podimo URLs. |
| `templates/feed_location.html` | Shows generated feed URL with copy button and QR code. |

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
`cloudscraper` and `ZenRowsClient` are synchronous. They are wrapped via `async_wrap()` (uses `loop.run_in_executor`). This is wasteful but functional:

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

### Running in Docker
```bash
docker build -t podimo-rss .
docker run -p 12104:12104 -e PODIMO_BIND_HOST=0.0.0.0:12104 podimo-rss
```

## Known Gotchas & Pitfalls

1. **`return ValueError(...)` instead of `raise`** — `podimo/client.py:39` has a real bug where it returns an exception object instead of raising it.
2. **Fragile `getPodcastName`** — `podimo/client.py` uses `list(podcast.values())[1]["title"]` which assumes dict insertion order. Breaks if Podimo adds fields.
3. **Backwards content-type logic** — `main.py` overwrites `guess_type()` results with `audio/mpeg`.
4. **Empty episode list = malformed RSS** — if a podcast has 0 episodes, `feedgen` emits an empty/broken feed.
5. **ZenRowsClient created per request** — wasteful, should be a singleton.
6. **Block list uses substring matching** — a blocked UUID fragment could accidentally match other podcast IDs.
7. **No rate limiting** — the `/feed/...` endpoint can be hammered, and each hit triggers Podimo GraphQL calls.
8. **CORS wildcard** — `Access-Control-Allow-Origin: *` on all responses including RSS feeds.
9. **Debug enabled in template** — `.env.example` ships with `DEBUG=true` uncommented.
10. **Docker runs as root** and leaves build dependencies (`gcc`, `libc-dev`, etc.) in the final image.

## Testing

There are **no automated tests** in this project. Manual verification steps:
1. Start the server (`make start` or `python main.py`)
2. Open `http://localhost:12104`
3. Enter valid Podimo credentials and a podcast UUID from `open.podimo.com`
4. Copy the generated feed URL into a podcast app or `curl`
5. Verify the RSS XML contains `<enclosure>` tags with audio URLs
6. Check logs for errors (`make logs` if running as systemd service)

## Dependencies

See `requirements.txt`. Key ones:
- `quart` + `hypercorn` — async web server
- `feedgen` — RSS/Atom generation
- `aiohttp` — async HTTP client (for HEAD requests)
- `cloudscraper` — bypasses Cloudflare bot detection (sync)
- `diskcache` — disk-backed key-value cache
- `zenrows` + `scraperapi` — optional proxy services

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
- **Credentials in URLs** are unavoidable for podcast app compatibility, but warn users. The UI should display a prominent notice.
- **Use `LOCAL_CREDENTIALS=true`** for personal instances to avoid embedding passwords in URLs.
- **Always run behind HTTPS** (reverse proxy) in production — Basic Auth is cleartext otherwise.
- **Auth tokens are sensitive** — they grant full Podimo account access. `STORE_TOKENS_ON_DISK` should be `false` on shared/multi-user instances.

## Refactoring Opportunities

If modifying this codebase, consider:
- Replacing `cloudscraper` + `async_wrap` with an async-native anti-bot client (or reusing a single session).
- Adding `pytest` + `pytest-asyncio` tests for the GraphQL client.
- Adding rate limiting (e.g., `slowapi` or `quart-rate-limiter`).
- Cleaning the Dockerfile to use multi-stage builds and drop build deps.
- Replacing substring-based block list with exact UUID matching.
