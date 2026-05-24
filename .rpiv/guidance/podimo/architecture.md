# podimo/ — Podimo API Adapter Layer

## Responsibility
Encapsulates all communication with Podimo's GraphQL backend: 3-step anonymous auth token flow, paginated episode fetching, search, and subscription listing. Also provides import-time configuration, disk-backed TTL caching, and sync→async bridging utilities.

## Dependencies
- **diskcache**: SQLite-backed key-value stores
- **python-dotenv**: `.env` + environment variable loading
- **(lazy)** zenrows: optional anti-bot proxy client
- **(lazy)** aiohttp: async HEAD requests for audio file metadata

## Consumers
- `main.py` is the sole consumer. Dependency arrow is strictly unidirectional.

## Module Structure
```
podimo/
├── client.py    # PodimoClient — GraphQL queries, auth, proxy switching
├── config.py    # Import-time env loading, constants, feature flags
├── cache.py     # diskcache instances + TTL helpers
└── utils.py     # async_wrap, header generation, validation helpers
```

## GraphQL POST with Proxy Switching
Three backends prioritized: ScraperAPI > ZenRows > direct cloudscraper. The `scraper` parameter is duck-typed (`: Any`) — `main.py` injects the concrete instance.

```python
async def post(self, headers, query, variables, scraper):
    if SCRAPER_API:
        url = f"https://api.scraperapi.com?api_key={SCRAPER_API}&url={GRAPHQL_URL}&keep_headers=true"
    elif ZENROWS_API:
        url = GRAPHQL_URL
        scraper = _get_zenrows_client()
    else:
        url = GRAPHQL_URL

    response = await async_wrap(scraper.post)(
        url, headers=headers, json={"query": query, "variables": variables},
        timeout=(6.05, 30)
    )
    # Validate status, extract data["data"], raise RuntimeError on failure
    return result
```

## Three-Step Auth Token Flow
Anonymous preregister → onboarding ID → credentials exchange.

```python
async def login(self, scraper):
    await self.get_preregister_token(scraper)
    await self.get_onboarding_id(scraper)
    result = await self.post(..., query=AUTHORIZE_QUERY, ...)
    self.token = result["tokenWithCredentials"]["token"]
```

## Cache Entry Pattern (Native TTL)
Use diskcache's native `expire` parameter instead of manual tuples.

```python
def get_entry(key, cache):
    return cache.get(key)

# Use expire=N (seconds); legacy manual (timestamp, value) tuples exist
# in older code but should not be copied for new caches
def put_entry(key, value, ttl, cache):
    cache.set(key, value, expire=ttl)
```

## Async Wrap Bridge (Sync → Async)
Wraps any sync callable into the default ThreadPoolExecutor so the Quart event loop isn't blocked.

```python
def async_wrap(func):
    async def run(*args, **kwargs):
        loop = asyncio.get_event_loop()
        return await loop.run_in_executor(None, partial(func, *args, **kwargs))
    return run
```

## Exception Hierarchy
All client errors extend `PodimoError` so the web layer can catch them generically.

```python
class PodimoError(Exception):
    pass

class PodcastNotFoundError(PodimoError):
    pass

class AuthenticationError(PodimoError):
    pass
```

## Naming
New code uses `snake_case`. Legacy `camelCase` remains in older `PodimoClient` methods (e.g. `getPodcastName`).

## Architectural Boundaries
- **NO formal HTTP client interface** — `scraper: Any` remains duck-typed.
- **Unidirectional dependency** — `podimo/` never imports `main.py`.
- **Import-time side effects** — `config.py` loads `.env` and reconfigures global logging at import time.

<important if="you are adding a new GraphQL query">
1. Ensure authenticated if endpoint requires bearer (`await self.podimo_login(scraper)`)
2. Generate headers: `self.generate_headers(self.token)` or `self.generate_headers(None)`
3. Write triple-quoted query string; inline fragments in same string
4. Assemble variables dict; every declared variable must be consumed in query body
5. For unstable endpoints, implement fallback variants with different result keys
</important>

<important if="you are adding a new cache type">
1. Create `Cache(join(CACHE_DIR, 'my_domain_cache'))` as module-level instance
2. Add `get_my_entry(key)` → `cache.get(key)`
3. Add `put_my_entry(key, value)` → `cache.set(key, value, expire=TTL)`
4. Avoid the legacy `(timestamp, value)` tuple pattern used in older caches
</important>
