# Podimo-to-RSS

A self-hosted Python 3.12+ async web service that reverse-engineers Podimo's GraphQL API to generate standard RSS feeds for paywalled podcasts. Built with Quart + Hypercorn, deployed via Docker.

# Architecture

Flat-module monolith with a single entrypoint (`main.py`) orchestrating all HTTP concerns, and a single package (`podimo/`) handling all external API interaction.

```text
main.py          → Quart routes, auth orchestration, RSS generation, server boot
podimo/          → GraphQL client, caching, config, async utilities
templates/       → Jinja2 forms (index.html, feed_location.html)
tests/           → pytest + pytest-asyncio (mirror source structure)
```

**Flow:** Incoming request → `main.py` route handler → `check_auth()` → `PodimoClient` → Podimo GraphQL → `podcastsToRss()` → XML response.

# Commands

| Command | Description |
|---------|-------------|
| `make test` | Run pytest suite (requires venv) |
| `make lint` | Run mypy type checker |
| `make format` | Check formatting with black (not installed by default) |
| `make docker-build` | Build multi-stage Docker image |
| `make docker-run` | Run container on localhost:12104 |
| `make install` | Install as systemd service (Linux) |

CI: GitHub Actions matrix across Python 3.10/3.11/3.12; Docker publish on tags.

# Business Context

Users authenticate with Podimo credentials and receive an RSS URL that any podcast app can subscribe to. Supports HTTP Basic Auth (credentials embedded in URL) or `LOCAL_CREDENTIALS` mode for single-user instances.

<important if="you are adding or modifying a Quart route">
1. Add `@app.route("/path")` decorator
2. Add `@limit_request()` decorator **below** the route decorator
3. Return `Response`, `jsonify(...)`, or `render_template(...)`
4. Ensure rate limiter is applied before async handler runs
</important>

<important if="you are adding or modifying environment configuration">
- All configuration goes through `podimo/config.py` (import-time dotenv + os.environ merge)
- Feature flags use the `bool(str(value).lower() in [...])` pattern (see `DEBUG`, `LOCAL_CREDENTIALS`)
- Add new variable to `.env.example` for documentation
- Use `from podimo.config import *` for module-level constants
</important>

<important if="you are changing Docker or CI configuration">
- Dockerfile uses multi-stage `python:3.12-alpine` (builder → runtime, root → non-root)
- Runtime `USER podimo` is set after all `RUN` steps requiring root
- `.github/workflows/test.yml` gates CI on `mypy` + `pytest`, uploads coverage for Python 3.10 only
- `.github/workflows/docker-publish.yml` publishes on tags and successful Tests workflow completion
</important>

<important if="you are adding a new GraphQL endpoint">
See `.rpiv/guidance/podimo/architecture.md` for the PodimoClient query checklist.
</important>

<important if="you are adding or modifying a template">
See `.rpiv/guidance/templates/architecture.md` for the Jinja2 template checklist.
</important>
