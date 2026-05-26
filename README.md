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

🩺 **Health endpoint** — A lightweight `/health` probe for Docker and Kubernetes orchestration.

🚀 **Single static binary** — Rewritten in Go, compiles to one executable with no runtime dependencies.

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

### Updating the container

```sh
docker pull ghcr.io/solidrhino/podimo:latest
docker stop podimo-rss && docker rm podimo-rss
# Re-run the docker run command above
```

Or with Docker Compose:

```sh
docker compose pull
docker compose up -d
```

### Environment files

| File | Use when | Why |
|------|----------|-----|
| `.env.docker` | **Running in Docker** | Pre-configured for containers: `0.0.0.0` bind host, `/tmp/podimo-rss-cache` volume path, `LOCAL_CREDENTIALS=true` |
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
| `GET /health` | JSON health probe for Docker/K8s | — |
| `GET /search?q=...` | Search podcasts by name | Basic Auth or `LOCAL_CREDENTIALS` |
| `GET /subscriptions` | List followed podcasts | Basic Auth or `LOCAL_CREDENTIALS` |
| `GET /feed/<id>.xml` | RSS feed (credentials in URL) | Basic Auth |
| `GET /feed/<user>/<pass>/<id>.xml` | RSS feed (credentials in path) | — |

---

## Configuration

All configuration is done via environment variables or the `.env` file. Run `just config` to edit it interactively.

Key settings:

| Variable | Default | Purpose |
|----------|---------|---------|
| `PODIMO_BIND_HOST` | `127.0.0.1:12104` | Where the server listens |
| `LOCAL_CREDENTIALS` | `false` | Store credentials server-side (recommended for personal use) |
| `PODIMO_EMAIL` / `PODIMO_PASSWORD` | — | Server-side credentials when `LOCAL_CREDENTIALS=true` |
| `ZENROWS_API` / `SCRAPER_API` | — | Anti-bot proxy keys |
| `PUBLIC_FEEDS` | `false` | Remove `<itunes:block>` from RSS for discoverability |

Full reference: [.env.example](.env.example)

---

## Bot Detection

Depending on your usage patterns, Podimo may trigger anti-bot protections. You can route requests through a proxy service to work around this.

### Zenrows

1. Create a free account at [app.zenrows.com/register](https://app.zenrows.com/register)
2. Set the `ZENROWS_API` environment variable to your API key

### ScraperAPI

1. Create a free account at [dashboard.scraperapi.com/signup](https://dashboard.scraperapi.com/signup)
2. Set the `SCRAPER_API` environment variable to your API key

---

## Privacy

The tool handles credentials as follows:

- **Username and password** are used only to obtain an access token and are never written to disk
- **A cryptographic hash** of your credentials is kept in memory as a cache key
- **The Podimo access token** is cached in memory (or on disk if `STORE_TOKENS_ON_DISK=true`)

Nothing is ever logged.

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
podimo/
  cache.go      → File-based JSON cache with TTL
  client.go     → GraphQL client (auth, search, episodes)
  graphql.go    → Thin GraphQL HTTP wrapper
  rss.go        → iTunes RSS generation
  *_test.go     → Go test suite
templates/
  index.html    → Web form (embedded via //go:embed)
  feed_location.html → Result page with QR code
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
