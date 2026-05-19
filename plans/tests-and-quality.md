# Plan: Tests, Type Hints, Error Handling & Logging Fix

## Context

The critical and moderate bugs are fixed and committed. Now improve code quality, test coverage, and robustness so future changes can be made safely.

**Note:** `split_username_region_locale` is intentionally excluded from this plan — it is an architectural design choice for podcast app compatibility, not a bug. Changing it risks breaking existing feed URLs.

---

## Goals

1. **Add tests** — pytest + pytest-asyncio for the GraphQL client, RSS generation, and web endpoints
2. **Add type hints** — Make `mypy` pass on all source files
3. **Improve error handling** — Stop string-matching on exception messages; use structured checks
4. **Fix logging** — Move request logging from `@app.after_request` to `@app.before_request`

---

## Files to Modify

- `podimo/client.py` — Type hints, error handling, logging
- `main.py` — Type hints, error handling, logging fix
- `podimo/utils.py` — Type hints
- `podimo/cache.py` — Type hints
- `podimo/config.py` — Type hints
- `tests/` — New directory with test files
- `requirements.txt` — Add `pytest`, `pytest-asyncio`, `mypy`

---

## Steps

- [ ] Step 1 — Add `pytest`, `pytest-asyncio`, `mypy`, `types-aiohttp` to `requirements.txt`
- [ ] Step 2 — Create `tests/conftest.py` with shared fixtures
- [ ] Step 3 — Write `tests/test_client.py` (constructor, `getPodcastName`, token key)
- [ ] Step 4 — Write `tests/test_rss.py` (`podcastsToRss`, `extract_audio_url`, content-type)
- [ ] Step 5 — Write `tests/test_web.py` (`/` route, 400 for invalid ID, 429 rate limit)
- [ ] Step 6 — Add type hints to `podimo/utils.py`, `podimo/cache.py`, `podimo/config.py`
- [ ] Step 7 — Add type hints to `podimo/client.py` and `main.py`
- [ ] Step 8 — Improve error handling: add custom exceptions in `podimo/client.py`, replace string matching in `main.py`
- [ ] Step 9 — Fix logging: move from `@app.after_request` to `@app.before_request`
- [ ] Step 10 — Run `mypy` and fix type errors
- [ ] Step 11 — Run `pytest` and fix test failures
- [ ] Step 12 — Commit

---

## Verification

```bash
pip install -r requirements.txt
mypy podimo/ main.py
pytest -v
```

## Scope Boundaries

- No external API changes (same routes, same behavior)
- No new runtime dependencies (only dev/test deps)
- No Docker changes
- No UI/template changes
- `split_username_region_locale` left unchanged (architectural design)
