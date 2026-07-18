<div align="center">

# Podimo to RSS

**Stream Podimo podcasts in any podcast app — no Podimo app required.**

Podimo is a proprietary podcast platform with exclusive shows behind a paywall. This tool bridges that gap by exposing your Podimo library as standard RSS feeds, compatible with any podcast client.

[![License: EUPL 1.2](https://img.shields.io/badge/License-EUPL_1.2-blue.svg)](https://joinup.ec.europa.eu/software/page/eupl)

</div>

---

## Table of Contents

- [What's New](#whats-new)
- [Installation](#installation)
- [Docker](#docker)
- [Finding your Podcast ID](#finding-your-podcast-id)
- [Configuration](#configuration)
- [Bot Detection](#bot-detection)
- [Privacy](#privacy)
- [Development](#development)
- [License](#license)

---

## What's New

🔍 **Search by name** — No need to hunt for UUIDs on Podimo's website. Type a podcast name and pick from results.

📻 **Your subscriptions** — After logging in, view all podcasts you follow and generate feeds with one click.

🩺 **Health & readiness endpoints** — A lightweight `/health` liveness probe plus a built-in `healthcheck` subcommand for Docker `HEALTHCHECK`, and a separate `/ready` endpoint that verifies outbound reachability to the Podimo API (for Kubernetes readiness probes).

🚀 **Single static binary** — Rewritten in Go, compiles to one executable with no runtime dependencies, packaged in a zero-attack-surface `scratch` Docker image.

🧪 **CI/CD** — GitHub Actions run tests and publish Docker images automatically.

---

## Installation

> Requires Go 1.26+ (or use Docker below)

```sh
git clone https://github.com/SolidRhino/podimo
cd podimo
just build
just install
just start
```

Visit [http://localhost:12104](http://localhost:12104) — you should see the interface.

To make it accessible from other machines or adjust settings:

```sh
just config
```

A full list of options is in [.env.example](.env.example).

---

## Docker

The fastest way to run this tool. No build tools needed.

### Quick start (Docker CLI)

```sh
# 1. Copy the Docker-specific environment file
cp .env.docker .env

# 2. Edit .env with your Podimo credentials
nano .env

# 3. Run the container
docker run -d \
    --name podimo-rss \
    --restart unless-stopped \
    --env-file .env \
    -p 12104:12104 \
    -v $(pwd)/cache:/tmp/podimo-rss-cache \
    ghcr.io/solidrhino/podimo:latest
```

Visit [http://localhost:12104](http://localhost:12104) once running.

### Docker Compose (recommended for persistence)

```sh
# 1. Copy the Docker-specific environment file
cp .env.docker .env

# 2. Edit .env with your Podimo credentials
nano .env

# 3. Start the stack
docker compose up -d
```

The `docker-compose.yml` includes:
- Persistent cache volume (`podimo-cache`)
- Auto-restart on failure
- Built-in health check (`/podimo-rss healthcheck`)

### Updating the container

```sh
docker pull ghcr.io/solidrhino/podimo:latest
docker stop podimo-rss && docker rm podimo-rss
# Re-run the docker run command above
```

Or with Docker Compose:

```sh
docker compose build --pull
docker compose up -d
```

### Environment files

| File | Use when | Why |
|------|----------|-----|
| `.env.docker` | **Running in Docker** | Pre-configured for containers: `0.0.0.0` bind host, `/tmp/podimo-rss-cache` volume path, `PODIMO_LOCAL_CREDENTIALS=true` |
| `.env.example` | Local Go install | Generic template — uncomment the lines you need |

See [.env.docker](.env.docker) for Docker-specific defaults, or [.env.example](.env.example) for the full option reference.

---

## Finding your Podcast ID

There are three ways to get a podcast's ID:

### 1. Search by name (Easiest)

Use the **Search** field on the homepage. Type the podcast name, hit **Search**, and click a result. The ID is auto-filled for you.

### 2. From your subscriptions

Log in with your Podimo credentials and visit the **Subscriptions** view to see all podcasts you follow.

### 3. Manual extraction from URL

1. Go to [open.podimo.com](https://open.podimo.com)
2. Search for and open the podcast page
3. Copy the UUID from the URL:

```text
https://open.podimo.com/podcast/09c55c96-9b1b-456e-bdf2-3abed3b61db5
                                 ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                                 your Podcast ID
```

Paste that ID into the **Podcast ID or URL** field on the homepage.

---

## API Endpoints

| Endpoint | Description | Auth |
|----------|-------------|------|
| `GET /` | Web interface (form + search) | — |
| `GET /health` | JSON liveness probe for Docker `HEALTHCHECK` | — |
| `GET /ready` | JSON readiness probe; checks Podimo API reachability (use for K8s readiness) | — |
| `GET /search?q=...` | Search podcasts by name | Basic Auth or `PODIMO_LOCAL_CREDENTIALS` |
| `GET /subscriptions` | List followed podcasts | Basic Auth or `PODIMO_LOCAL_CREDENTIALS` |
| `GET /feed/<id>.xml` | RSS feed (credentials in URL) | Basic Auth |
| `GET /feed/<user>/<pass>/<id>.xml` | RSS feed (credentials in path) | — |

---

## Configuration

All configuration is done via `config.yaml` (preferred), environment variables (`PODIMO_` prefix), or `.env` file. Run `just config` to edit `config.yaml` interactively.

Key settings:

| Variable | Default | Purpose |
|----------|---------|---------|
| `PODIMO_BIND_HOST` | `127.0.0.1:12104` | Where the server listens |
| `PODIMO_LOCAL_CREDENTIALS` | `false` | Store credentials server-side (recommended for personal use) |
| `PODIMO_EMAIL` / `PODIMO_PASSWORD` | — | Server-side credentials when `PODIMO_LOCAL_CREDENTIALS=true` |
| `PODIMO_ZENROWS_API` / `PODIMO_SCRAPER_API` | — | Anti-bot proxy keys |
| `PODIMO_PUBLIC_FEEDS` | `false` | Remove `<itunes:block>` from RSS for discoverability |

Full reference: [config.example.yaml](config.example.yaml) or [.env.example](.env.example)

---

## Bot Detection

Depending on your usage patterns, Podimo may trigger anti-bot protections. You can route requests through a proxy service to work around this.

### Zenrows

1. Create a free account at [app.zenrows.com/register](https://app.zenrows.com/register)
2. Set the `PODIMO_ZENROWS_API` environment variable to your key (or `zenrows_api` in `config.yaml`)

### ScraperAPI

1. Create a free account at [dashboard.scraperapi.com/signup](https://dashboard.scraperapi.com/signup)
2. Set the `PODIMO_SCRAPER_API` environment variable to your key (or `scraper_api` in `config.yaml`)

---

## Privacy

The tool handles credentials as follows:

- **Username and password** are used only to obtain an access token and are never written to disk
- **A cryptographic hash** of your credentials is kept in memory as a cache key
- **The Podimo access token** is cached in memory (or on disk if `PODIMO_STORE_TOKENS_ON_DISK=true`)

Credentials and tokens are never logged. Request URLs are redacted to scrub embedded passwords before logging.

---

## Development

### Running tests

```sh
just test      # Run Go tests with race detection
just lint      # Run go vet and gofmt checks
```

### Local CI with `act`

You can test GitHub Actions workflows locally using [`nektos/act`](https://nektosact.com/):

```sh
brew install act
act --dryrun
act -j test -W .github/workflows/test.yml
```

> **Note:** On macOS with Colima, `act` may fail to mount the Docker socket. If so, run CI directly on GitHub by opening a PR.

### Project structure

```
main.go          → HTTP server, routes, handlers, RSS feed serving
config.go        → Environment/YAML config loading (koanf), validation
podimo/
  client.go      → GraphQL API client (auth, episodes, search, subscriptions)
  graphql.go     → Thin GraphQL HTTP wrapper
  rss.go         → iTunes RSS generation with parallel HEAD requests
  cache.go       → File-based JSON cache with TTL
  boundedmap.go  → Generic in-memory LRU cache with TTL eviction
  *_test.go      → Go test suite
templates/       → HTML templates (embedded via //go:embed)
static/          → Stylesheet (embedded via //go:embed)
```

---

## License

```text
Copyright 2022-2023 Thijs Raymakers

Licensed under the EUPL, Version 1.2 or – as soon they
will be approved by the European Commission - subsequent
versions of the EUPL (the "Licence");
You may use this work except in compliance with the
Licence.

https://joinup.ec.europa.eu/software/page/eupl
```
