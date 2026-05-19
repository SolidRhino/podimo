# Plan: Fix Critical Bugs in Podimo-to-RSS

## Context

Code review identified 4 critical bugs that cause runtime failures, brittle API parsing, or broken RSS output. This plan fixes them in-place without changing architecture or dependencies.

---

## Critical Bugs

| # | Bug | File | Line | Impact |
|---|-----|------|------|--------|
| 1 | `return ValueError(...)` instead of `raise` | `podimo/client.py` | ~39 | `PodimoClient` constructor returns a `ValueError` object instead of raising; caller gets the exception instance and crashes later with `AttributeError` |
| 2 | Fragile `getPodcastName` via dict insertion order | `podimo/client.py` | ~159 | `list(podcast.values())[1]["title"]` breaks if GraphQL response adds fields; podcast name becomes wrong or `KeyError` |
| 3 | Backwards content-type logic overwrites correct MIME type | `main.py` | ~206-211 | Valid `.m4a`/`.ogg` guesses are overwritten with `audio/mpeg` |
| 4 | Empty episode list produces malformed RSS | `main.py` | ~259-281 | When `len(episodes) == 0`, `feedgen` emits no title/description/image/language, resulting in invalid RSS |

---

## Approach

Fix each bug with the smallest possible code change — no refactors, no new dependencies, no behavior changes beyond correctness.

---

## Files to Modify

- `podimo/client.py` — bugs 1 & 2
- `main.py` — bugs 3 & 4

---

## Steps

- [ ] **Step 1 — Fix `return ValueError` in `client.py`**
  Change `return ValueError(...)` to `raise ValueError(...)` in `__init__`.

- [ ] **Step 2 — Harden `getPodcastName` in `client.py`**
  Replace `list(podcast.values())[1]["title"]` with:
  ```python
  return podcast.get("podcast", {}).get("title", "Unknown")
  ```

- [ ] **Step 3 — Fix content-type logic in `main.py`**
  Replace the backwards `if/else` with:
  ```python
  content_type, _ = guess_type(url)
  if content_type is None:
      content_type = response.headers.get('content-type', 'audio/mpeg')
  ```

- [ ] **Step 4 — Guard empty episode list in `main.py`**
  Move the feed metadata setup (title, description, image, language, author) **outside** the `if len(episodes) > 0:` block so it always runs. Use fallback values from the `podcast` dict when available.

- [ ] **Step 5 — Verify**
  - Server starts without errors
  - Feed-generation path still works for known podcast IDs
  - `mypy`/`python -m py_compile` passes on changed files
  - (Optional) add a one-off manual test: request a feed with 0 episodes and inspect the generated XML for well-formedness

---

## Verification

```bash
# 1. Syntax / smoke test
python -m py_compile podimo/client.py main.py

# 2. Run locally
python main.py
# Then hit http://localhost:12104/feed/<valid_id>.xml?region=nl&locale=nl-NL
# with valid credentials and check the RSS XML.

# 3. Check logs for no regressions
```
