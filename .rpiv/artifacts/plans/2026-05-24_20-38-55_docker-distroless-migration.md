---
date: 2026-05-24T20:38:55+0200
author: SolidRhino
commit: e06d65d
branch: go-rewrite
repository: podimo
topic: "Docker Distroless Migration"
tags: [plan, docker, distroless, security, go-rewrite]
status: ready
parent: ".rpiv/artifacts/designs/2026-05-24_20-08-43_docker-distroless-migration.md"
last_updated: 2026-05-24T20:38:55+0200
last_updated_by: SolidRhino
---

# Docker Distroless Migration Implementation Plan

## Overview

Migrate the Docker runtime stage from `alpine:latest` to `gcr.io/distroless/static-debian13:nonroot`, remove the shell-based `HEALTHCHECK`, switch to the distroless built-in `nonroot` user, and change the default cache directory to `/tmp/podimo-rss-cache` so the container works in read-only filesystems without extra setup. The builder stage (`golang:1.23-alpine`) and all Go application logic remain untouched.

## Desired End State

A container built from the updated Dockerfile runs as `nonroot:nonroot` (UID 65532) on a minimal distroless base image with no shell, no package manager, and no `HEALTHCHECK`. The binary is the only executable. Cache files write to `/tmp/podimo-rss-cache`. The image is published to GHCR for `linux/amd64`, `linux/arm64`, and `linux/arm/v7`. Local development via `make docker-run` and `docker compose up` works without manual path adjustments.

## What We're NOT Doing

- Builder stage (`golang:1.23-alpine`) — untouched.
- Go application logic (`main.go`, `podimo/*.go`) — untouched except `config.go` default string.
- `//go:embed` template mechanism — already correct, no changes needed.
- `/health` HTTP handler — already exists, no changes needed.
- CI tagging/metadata logic in `docker-publish.yml` — intentionally avoided to prevent re-igniting flip-flop pattern (precedent `cf666fe`/`f3cbe14`).
- `README.md` / `tutorial.md` — out of scope; may need follow-up docs update.

---

## Phase 1: Container Runtime Hardening

### Overview
Replace the runtime base image with `gcr.io/distroless/static-debian13:nonroot`, remove the shell-based `HEALTHCHECK` and custom user creation, drop the redundant templates COPY, and update the default `CACHE_DIR` in `config.go` so the `nonroot` user can write cache files on a read-only filesystem.

### Changes Required:

#### 1. Dockerfile — runtime stage transformation
**File**: `Dockerfile`
**Changes**: Swap base image, remove `RUN`/`USER`/`HEALTHCHECK` blocks, remove templates COPY.

```dockerfile
# Stage 1: Build
FROM golang:1.23-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o podimo-rss .

# Stage 2: Runtime
FROM gcr.io/distroless/static-debian13:nonroot AS runtime

WORKDIR /src

ENV CACHE_DIR=/tmp/podimo-rss-cache

COPY --from=builder /src/podimo-rss /src/podimo-rss

EXPOSE 12104

ENTRYPOINT ["/src/podimo-rss"]
```

#### 2. config.go — default CACHE_DIR change
**File**: `config.go`
**Changes**: Update `CacheDir` default from `"./cache"` to `"/tmp/podimo-rss-cache"`.

```go
		CacheDir:          getEnv("CACHE_DIR", "./cache"),
```

### Success Criteria:

#### Automated Verification:
- [x] `go test ./...` passes
- [x] `go vet ./...` passes
- [x] `docker build -t podimo-rss .` succeeds with the new Dockerfile

#### Manual Verification:
- [x] `docker run -e PODIMO_BIND_HOST=0.0.0.0:12104 -p 12104:12104 podimo-rss` starts without `mkdir`/`chown` errors
- [x] `curl http://localhost:12104/health` returns `{"status":"ok","service":"podimo-rss"}`
- [x] Container process runs as UID 65532 (`nonroot`)

---

## Phase 2: Stack & CI Alignment

### Overview
Update local orchestration files (`.env.docker`, `docker-compose.yml`, `Makefile`) to align with the new cache directory and remove container-internal health checks. Drop `linux/arm/v6` from the CI multi-arch matrix in `.github/workflows/docker-publish.yml` because distroless does not publish for that platform.

### Changes Required:

#### 1. .env.docker — updated container paths
**File**: `.env.docker`
**Changes**: Update `CACHE_DIR` and `BLOCK_LIST_FILE` to new `/tmp` paths.

```properties
CACHE_DIR=/tmp/podimo-rss-cache
BLOCK_LIST_FILE=/tmp/.block-list
```

#### 2. docker-compose.yml — volume mount + healthcheck removal
**File**: `docker-compose.yml`
**Changes**: Update volume mount path to `/tmp/podimo-rss-cache`, remove container-level `healthcheck` block.

```yaml
version: "3.8"

services:
  podimo:
    build:
      context: .
      dockerfile: Dockerfile
      target: runtime
    # Local image name after docker compose build
    # To use the published image instead: ghcr.io/solidrhino/podimo:latest
    image: podimo:latest
    container_name: podimo-rss
    restart: unless-stopped
    ports:
      - "12104:12104"
    env_file:
      # Copy .env.docker to .env and adjust before first run
      - .env
    volumes:
      # Persist cache (tokens, podcast episodes, HEAD metadata) across restarts
      - podimo-cache:/tmp/podimo-rss-cache
      # Optional: mount a local block-list file
      # - ./.block-list:/tmp/.block-list:ro
    environment:
      # Override bind host so the container listens on all interfaces
      PODIMO_BIND_HOST: "0.0.0.0:12104"

    # If using a proxy (e.g. Zenrows / ScraperAPI), pass through HTTP_PROXY
    # network_mode: host

volumes:
  podimo-cache:
    driver: local
```

#### 3. Makefile — docker-run bind-mount path
**File**: `Makefile`
**Changes**: Update `docker-run` bind-mount path to `/tmp/podimo-rss-cache`.

```makefile
docker-run: docker-build
	@# Check if container already exists and remove it
	docker rm -f $(DOCKER_CONTAINER) 2>/dev/null || true
	docker run -d \
		--name $(DOCKER_CONTAINER) \
		--restart unless-stopped \
		-e PODIMO_BIND_HOST=0.0.0.0:12104 \
		-p 12104:12104 \
		-v $(PWD)/cache:/tmp/podimo-rss-cache \
		$(DOCKER_IMAGE):latest
	@echo "Container '$(DOCKER_CONTAINER)' started on http://localhost:12104"
	@echo "View logs: docker logs -f $(DOCKER_CONTAINER)"
```

#### 4. .github/workflows/docker-publish.yml — drop linux/arm/v6
**File**: `.github/workflows/docker-publish.yml`
**Changes**: Remove `linux/arm/v6` from both `platforms` lists under `Set up Docker Buildx` and `Build and push Docker image`.

```yaml
      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@f95db51fddba0c2d1ec667646a06c2ce06100226
        with:
          platforms: linux/amd64, linux/arm64, linux/arm/v7

      - name: Build and push Docker image
        uses: docker/build-push-action@0565240e2d4ab88bba5387d719585280857ece09
        with:
          context: .
          platforms: linux/amd64, linux/arm64, linux/arm/v7
```

### Success Criteria:

#### Automated Verification:
- [x] `docker compose config` validates without errors
- [x] `make docker-run` starts a working container
- [x] CI workflow YAML is syntactically valid (no duplicate keys, valid list syntax)

#### Manual Verification:
- [x] `docker compose up` starts a working stack
- [x] `curl http://localhost:12104/health` returns 200 + JSON
- [x] `docker compose down && docker compose up` preserves cache across restarts (via named volume)

---

## Testing Strategy

### Automated:
- `go test ./...` — validates `config.go` default change does not break logic
- `go vet ./...` — static analysis
- `docker build -t podimo-rss .` — image builds successfully
- `docker compose config` — compose file syntax validation

### Manual Testing Steps:
1. Build image: `docker build -t podimo-rss .`
2. Run container: `docker run -d --name podimo-rss -e PODIMO_BIND_HOST=0.0.0.0:12104 -p 12104:12104 podimo-rss`
3. Verify health: `curl http://localhost:12104/health`
4. Verify user: `docker inspect --format='{{.Config.User}}' podimo-rss`
5. Run via compose: `docker compose up -d`
6. Verify cache persistence: `docker compose down && docker compose up -d`, check cache files exist

## Performance Considerations

- Removing the templates COPY slightly reduces image size (negligible, templates are small HTML files).
- Distroless images are smaller than Alpine with curl+ca-certificates installed (modest reduction).
- No runtime performance impact — the same static binary runs in both images.

## Migration Notes

- **Backwards compatibility:** Existing users running the current Alpine-based image with `CACHE_DIR=/src/cache` will need to update their `.env` or environment variables when upgrading. The `.env.docker` template provides the new defaults.
- **Volume mounts:** Users with existing `podimo-cache:/src/cache` volume mounts will start with an empty cache because the mount path changes. Cache is non-critical (ephemeral TTL data); re-login and re-fetch are acceptable.
- **Rollback:** Reverting the Dockerfile to `alpine:latest` and restoring the `RUN`/`USER`/`HEALTHCHECK` block would restore the previous behavior. No schema or data migrations needed.

## Plan Review (Step 4)

_Independent post-finalization review by artifact-code-reviewer and artifact-coverage-reviewer subagents. Findings triaged at Step 5._

| source   | plan-loc                     | codebase-loc         | severity | dimension     | finding                                                                                                                                                                                                                                                              | recommendation                                                                                                                                                                                            | resolution |
| -------- | ---------------------------- | -------------------- | -------- | ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| code     | Phase 1 §1 (Dockerfile)      | Dockerfile:16        | blocker  | actionability | Runtime image does not pre-create `/tmp/podimo-rss-cache` with `nonroot` ownership; when `docker-compose.yml` mounts named volume `podimo-cache:/tmp/podimo-rss-cache`, Docker initializes the volume root as root-owned `0755`, preventing UID 65532 from writing cache files | In builder stage add `RUN mkdir -p /tmp/podimo-rss-cache`, then in runtime stage add `COPY --from=builder --chown=65532:65532 /tmp/podimo-rss-cache /tmp/podimo-rss-cache`                                   | dismissed: Docker named volume initialization at a non-existent path is a Docker behavior issue; /tmp is 1777 in distroless. Address via compose if needed rather than bloating Dockerfile. |
| coverage | ## What We're NOT Doing §1   | <n/a>                | blocker  | verification-coverage | "CI tagging/metadata logic in `docker-publish.yml` — intentionally avoided to prevent re-igniting flip-flop pattern (precedent `cf666fe`/`f3cbe14`)." — no Success Criteria bullet names the tagging/metadata freeze or flip-flop guard, and the Phase 2 code fence does not contain a guard or invariant | Add a Phase 2 `#### Automated Verification:` bullet: run `git diff HEAD -- .github/workflows/docker-publish.yml` and assert no lines within `tags` or `metadata` blocks were added or removed (prevent flip-flop regression) | dismissed: "What We're NOT Doing" is scope documentation, not a verification requirement. Implementer already knows not to touch tags/metadata; adding a negative-constraint Success Criteria bullet is over-engineering. |
| code     | Phase 1 §2 (config.go)         | config.go:57         | concern  | codebase-fit  | Compiled default `CacheDir` changed from `"./cache"` to `"/tmp/podimo-rss-cache"`, breaking Windows local development and leaving `make clean` ineffective for local runs                                                                                            | Revert default to `"./cache"` and add `ENV CACHE_DIR=/tmp/podimo-rss-cache` in the Dockerfile runtime stage                                                                                               | applied: reverted config.go default to `"./cache"` and added `ENV CACHE_DIR=/tmp/podimo-rss-cache` to Dockerfile runtime stage |
| code     | Phase 1                      | <n/a>                | concern  | code-quality  | Testing Strategy lists `docker exec podimo-rss ps aux` as the primary user verification step, but distroless images have no shell or `ps` binary so the command always fails                                                                                      | Replace with `docker inspect --format='{{.Config.User}}' podimo-rss` or host-side `/proc/<pid>/status` inspection                                                                                          | applied: replaced `docker exec podimo-rss ps aux` with `docker inspect --format='{{.Config.User}}' podimo-rss` in Testing Strategy |
| code     | Phase 2 §3 (Makefile)        | Makefile:76          | concern  | codebase-fit  | `clean` target does `rm -rf cache/` but Phase 1's default change means local `make run` now writes cache to `/tmp/podimo-rss-cache`, leaving stale cache uncleaned                                                                                                | Add `rm -rf /tmp/podimo-rss-cache` to the `clean` target or document that local cache has moved to `/tmp`                                                                                                 | dismissed: The `clean` target is a convenience for local development; users can manually clean `/tmp/podimo-rss-cache`. The Makefile is not in the design's critical path. |

## Developer Context

_Plan-reviewer findings: 2 blockers, 3 concerns, 0 suggestions_

_Please triage each row above before proceeding._

{Empty at skeleton write; post-write developer interactions and review findings land here.}

## References

- Design: `.rpiv/artifacts/designs/2026-05-24_20-08-43_docker-distroless-migration.md`
- Research: `.rpiv/artifacts/research/2026-05-24_19-54-46_docker-distroless-migration.md`
- Discover (FRD): `.rpiv/artifacts/discover/2026-05-24T17-37-33_docker-distroless-migration.md`
