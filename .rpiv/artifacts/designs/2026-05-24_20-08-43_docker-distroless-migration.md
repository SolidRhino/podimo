---
date: 2026-05-24T20:08:43+0200
author: SolidRhino
commit: e06d65d
branch: go-rewrite
repository: podimo
topic: "Docker Distroless Migration"
tags: [design, docker, distroless, security, go-rewrite]
status: ready
parent: .rpiv/artifacts/research/2026-05-24_19-54-46_docker-distroless-migration.md
last_updated: 2026-05-24T20:08:43+0200
last_updated_by: SolidRhino
---

# Design: Docker Distroless Migration

## Summary

Migrate the Docker runtime stage from `alpine:latest` to `gcr.io/distroless/static-debian13:nonroot`, remove the shell-based `HEALTHCHECK`, switch to the distroless built-in `nonroot` user, and change the default cache directory to `/tmp/podimo-rss-cache` so the container works in read-only filesystems without extra setup. The builder stage (`golang:1.23-alpine`) and all Go application logic remain untouched.

## Requirements

- Runtime base image must be `gcr.io/distroless/static-debian13:nonroot` (developer-requested for security hardening).
- Static binary compatibility: keep `CGO_ENABLED=0` and `-ldflags="-s -w"`.
- CA certificates must work for HTTPS GraphQL calls without builder-stage copy.
- The `nonroot` user (UID 65532) must be able to write cache files without Dockerfile `chown`/`mkdir` commands.
- Docker `HEALTHCHECK` must be removed (distroless has no shell or curl).
- Container must remain functional on read-only root filesystems.
- Templates are embedded via `//go:embed` — no runtime filesystem dependency.
- CI multi-arch matrix must drop `linux/arm/v6` (unsupported by distroless).
- Local orchestration files (`.env.docker`, `docker-compose.yml`, `Makefile`) must align with new paths.
- The `/health` HTTP endpoint remains available for external orchestrator probes.

## Current State Analysis

The current Dockerfile uses a two-stage build with `golang:1.23-alpine` builder and `alpine:latest` runtime. The runtime stage installs `curl` and `ca-certificates`, creates a custom `podimo` user, creates `/src/cache`, runs a `HEALTHCHECK` via `curl`, and sets `USER podimo`.

### Key Discoveries

- `Dockerfile:16` — `FROM alpine:latest AS runtime` is the target for replacement.
- `Dockerfile:19` — `RUN apk add --no-cache curl ca-certificates` will fail in a shell-less image.
- `Dockerfile:22` — `COPY --from=builder /src/templates /src/templates` is redundant because templates are embedded (`main.go:26-27`, `main.go:102-108`).
- `Dockerfile:26-28` — Custom user creation block is unnecessary with distroless `:nonroot`.
- `Dockerfile:31-32` — `HEALTHCHECK` using `curl` is impossible without `/bin/sh`.
- `config.go:57` — Default `CACHE_DIR` is `"./cache"`, which is not writable for `nonroot` on a read-only filesystem.
- `config.go:91` — `os.MkdirAll(cfg.CacheDir, 0755)` is the fail-fast gate at startup.
- `podimo/cache.go:23` — Each `FileCache` constructor calls `os.MkdirAll(dir, 0755)` again for self-contained subdirectory creation.
- `.github/workflows/docker-publish.yml:41` and `:58` — Both declare `linux/arm/v6`, which distroless does not publish.
- `.env.docker:57` — `CACHE_DIR=/src/cache` must move to a world-writable path.
- `.env.docker:61` — `BLOCK_LIST_FILE=/src/.block-list` must also move.
- `docker-compose.yml:17` — Volume mount `podimo-cache:/src/cache` must align with new cache path.
- `docker-compose.yml:30` — Compose-level `healthcheck` using `curl` inside the container must be removed.
- `Makefile:96` — `docker-run` bind-mounts `$(PWD)/cache:/src/cache` and must use the new path.

## Scope

### Building

- `Dockerfile` — runtime stage transformation (base image swap, remove RUN/USER/HEALTHCHECK, remove templates COPY).
- `config.go` — change `CACHE_DIR` default from `"./cache"` to `"/tmp/podimo-rss-cache"`.
- `.env.docker` — update `CACHE_DIR` and `BLOCK_LIST_FILE` paths.
- `docker-compose.yml` — update volume mount path, remove container-internal `healthcheck`.
- `Makefile` — update `docker-run` volume bind-mount path.
- `.github/workflows/docker-publish.yml` — drop `linux/arm/v6` from both platforms lists.

### Not Building

- Builder stage (`golang:1.23-alpine`) — untouched.
- Go application logic (`main.go`, `podimo/*.go`) — untouched except `config.go` default string.
- `//go:embed` template mechanism — already correct, no changes needed.
- `/health` HTTP handler — already exists, no changes needed.
- CI tagging/metadata logic in `docker-publish.yml` — intentionally avoided to prevent re-igniting flip-flop pattern (precedent `cf666fe`/`f3cbe14`).
- `README.md` / `tutorial.md` — out of scope for this design; may need follow-up docs update.

## Decisions

### Base Image

**Decision:** Use `gcr.io/distroless/static-debian13:nonroot` as the runtime base image.

- **Evidence:** Developer explicitly requested this image during discover phase for security hardening.
- **Rationale:** The Go binary is static (`CGO_ENABLED=0`), so the `static` image family (no glibc, no musl) is sufficient. The `:nonroot` variant provides the `nonroot` user (UID 65532) without custom `adduser`/`addgroup` commands.

### CA Certificates

**Decision:** No builder-stage copy of CA certificates needed.

- **Evidence:** GoogleContainerTools/distroless `static` image family explicitly includes `ca-certificates` along with `tzdata` and `/tmp`.
- **Rationale:** If CA certs were missing, every HTTPS GraphQL call to `https://podimo.com/graphql` (`podimo/graphql.go:42`) would fail with `x509: certificate signed by unknown authority`. Verified against official distroless documentation and Go Dockerfile examples.

### Container User

**Decision:** Use distroless `nonroot` (UID 65532), drop custom `podimo` user.

- **Evidence:** The `:nonroot` base image already sets `USER nonroot:nonroot`. Prior DHI migration (`4c630c7`) showed that custom user creation via `RUN` fails in shell-less images.
- **Rationale:** Eliminates the `addgroup`/`adduser`/`mkdir`/`chown` block entirely.

### Health Checking

**Decision:** Remove Docker `HEALTHCHECK` entirely from Dockerfile and docker-compose.yml.

- **Evidence:** Distroless lacks `/bin/sh`, `curl`, and all shell utilities. The `/health` endpoint (`main.go:164`, `main.go:197-200`) is already registered without rate limiting and returns `{"status":"ok","service":"podimo-rss"}`.
- **Rationale:** External orchestrators (Kubernetes `httpGet` probe, Docker Compose external health check, ALB target-group checks) replace the internal Docker HEALTHCHECK. `TestHealthHandler` (`main_test.go:63-69`) protects against regression.

### Cache Directory

**Decision:** Change default `CACHE_DIR` to `/tmp/podimo-rss-cache`.

- **Evidence:** `/tmp` is `1777` (world-writable with sticky bit) in the distroless base image. The `nonroot` user can create subdirectories and write files there without any Dockerfile ownership commands. Prior DHI follow-up (`4c630c7`) was forced to move cache to `/tmp` for exactly this reason.
- **Rationale:** Avoids needing `RUN mkdir`/`chown` in the runtime stage. Makes the container functional on read-only root filesystems.

### Templates COPY

**Decision:** Remove redundant `COPY --from=builder /src/templates /src/templates`.

- **Evidence:** `main.go:26-27` uses `//go:embed templates/*` to bundle templates into the binary at build time. `main.go:102-108` parses templates from the embedded filesystem, not from disk.
- **Rationale:** Slightly reduces image size. No runtime code reads templates from disk.

### CI Multi-Arch Matrix

**Decision:** Drop `linux/arm/v6` from both buildx setup and build-push action.

- **Evidence:** `gcr.io/distroless/static-debian13:nonroot` publishes manifests for `linux/amd64`, `linux/arm64`, and `linux/arm` (resolved as `v7`). It does not publish `linux/arm/v6`. Prior DHI migration (`9db8d7c`) had to narrow platforms for the same reason.
- **Rationale:** Building for an unsupported platform would fail the CI workflow.

## Architecture

### Dockerfile — MODIFY

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

COPY --from=builder /src/podimo-rss /src/podimo-rss

EXPOSE 12104

ENTRYPOINT ["/src/podimo-rss"]
```

### config.go:57 — MODIFY

```go
		CacheDir:          getEnv("CACHE_DIR", "/tmp/podimo-rss-cache"),
```

### .env.docker:57,61 — MODIFY

```properties
CACHE_DIR=/tmp/podimo-rss-cache
BLOCK_LIST_FILE=/tmp/.block-list
```

### docker-compose.yml — MODIFY

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

### Makefile:89-100 — MODIFY

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

### .github/workflows/docker-publish.yml:41,58 — MODIFY

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

## Slices

### Slice 1: Container Runtime Hardening

**Files:** `Dockerfile`, `config.go`

#### Automated Verification:
- [ ] `go test ./...` passes
- [ ] `go vet ./...` passes
- [ ] `docker build -t podimo-rss .` succeeds with the new Dockerfile

#### Manual Verification:
- [ ] `docker run -e PODIMO_BIND_HOST=0.0.0.0:12104 -p 12104:12104 podimo-rss` starts without `mkdir`/`chown` errors
- [ ] `curl http://localhost:12104/health` returns `{"status":"ok","service":"podimo-rss"}`
- [ ] Container process runs as UID 65532 (`nonroot`)

### Slice 2: Stack & CI Alignment

**Files:** `.env.docker`, `docker-compose.yml`, `Makefile`, `.github/workflows/docker-publish.yml`

#### Automated Verification:
- [ ] `docker compose config` validates without errors
- [ ] `make docker-run` starts a working container
- [ ] CI workflow YAML is syntactically valid (no duplicate keys, valid list syntax)

#### Manual Verification:
- [ ] `docker compose up` starts a working stack
- [ ] `curl http://localhost:12104/health` returns 200 + JSON
- [ ] `docker compose down && docker compose up` preserves cache across restarts (via named volume)

## Desired End State

A container built from the updated Dockerfile runs as `nonroot:nonroot` (UID 65532) on a minimal distroless base image with no shell, no package manager, and no `HEALTHCHECK`. The binary is the only executable. Cache files write to `/tmp/podimo-rss-cache`. The image is published to GHCR for `linux/amd64`, `linux/arm64`, and `linux/arm/v7`. Local development via `make docker-run` and `docker compose up` works without manual path adjustments.

### Dockerfile (final runtime stage preview)

```dockerfile
FROM gcr.io/distroless/static-debian13:nonroot AS runtime
WORKDIR /src
COPY --from=builder /src/podimo-rss /src/podimo-rss
EXPOSE 12104
ENTRYPOINT ["/src/podimo-rss"]
```

### Local run example

```bash
docker build -t podimo-rss .
docker run -d \
  --name podimo-rss \
  -e PODIMO_BIND_HOST=0.0.0.0:12104 \
  -p 12104:12104 \
  -v $(PWD)/cache:/tmp/podimo-rss-cache \
  podimo-rss:latest
```

## File Map

- `Dockerfile`                              # MODIFY — runtime stage distroless migration
- `config.go`                               # MODIFY — default CACHE_DIR change
- `.env.docker`                             # MODIFY — updated container paths
- `docker-compose.yml`                      # MODIFY — volume mount + healthcheck removal
- `Makefile`                                # MODIFY — docker-run bind-mount path
- `.github/workflows/docker-publish.yml`    # MODIFY — drop linux/arm/v6 platform

## Ordering Constraints

- Slice 1 (Container Runtime Hardening) must be approved before Slice 2 because Slice 2 path decisions depend on the cache directory chosen in Slice 1.
- No parallel slices — both are sequential.

## Verification Notes

- `go test ./...` must pass after `config.go` change (only a string default changed — no logic impact).
- `go vet ./...` must pass.
- `docker build` must succeed for the new Dockerfile.
- Container must start and respond on `:12104`.
- Container must write cache files to `/tmp/podimo-rss-cache` (e.g., tokens, podcast, head caches).
- `make docker-run` must start a working container.
- CI workflow YAML must be syntactically valid (no duplicate keys, valid list syntax).
- Precedent warning: big Dockerfile changes trigger immediate follow-up fixes. Budget a validation pass right after implementation.

## Performance Considerations

- Removing the templates COPY slightly reduces image size (negligible, templates are small HTML files).
- Distroless images are smaller than Alpine with curl+ca-certificates installed (modest reduction).
- No runtime performance impact — the same static binary runs in both images.

## Migration Notes

- **Backwards compatibility:** Existing users running the current Alpine-based image with `CACHE_DIR=/src/cache` will need to update their `.env` or environment variables when upgrading. The `.env.docker` template provides the new defaults.
- **Volume mounts:** Users with existing `podimo-cache:/src/cache` volume mounts will start with an empty cache because the mount path changes. Cache is non-critical (ephemeral TTL data); re-login and re-fetch are acceptable.
- **Rollback:** Reverting the Dockerfile to `alpine:latest` and restoring the `RUN`/`USER`/`HEALTHCHECK` block would restore the previous behavior. No schema or data migrations needed.

## Pattern References

- `Dockerfile:1-31` — Current multi-stage pattern (builder + runtime) to preserve.
- `.github/workflows/docker-publish.yml:1-77` — Multi-arch publish workflow; only platforms list changes.
- `docker-compose.yml:1-48` — Compose orchestration pattern; healthcheck and volume paths change.
- `Makefile:81-111` — Docker targets pattern; bind-mount path changes.

## Developer Context

All ambiguities were resolved during the discover and research phases. No additional checkpoint questions were needed.

- **Q (discover: Base image):** `gcr.io/distroless/static-debian13:nonroot`
- **Q (discover: Static binary build):** Keep `CGO_ENABLED=0` and `-ldflags="-s -w"`
- **Q (discover: CA certificates):** Distroless includes CA certs; no builder-stage copy needed
- **Q (discover: Container user):** Use distroless `nonroot`
- **Q (discover: Health checking):** Remove Docker HEALTHCHECK entirely
- **Q (discover: Writable cache directory):** Change default to `/tmp/podimo-rss-cache`
- **Q (docker-publish.yml:41,58):** Drop `linux/arm/v6`
- **Q (.env.docker:57, docker-compose.yml:17,30):** Update paths and remove compose healthcheck

## Design History

- Slice 1: Container Runtime Hardening — approved as generated
- Slice 2: Stack & CI Alignment — approved as generated

## References

- `.rpiv/artifacts/research/2026-05-24_19-54-46_docker-distroless-migration.md` — Full research artifact with detailed findings, code references, integration points, architecture insights, precedents, and developer Q/As.
- `.rpiv/artifacts/discover/2026-05-24T17-37-33_docker-distroless-migration.md` — Feature Requirements Document documenting the planned migration and acceptance criteria.
- GoogleContainerTools/distroless base README — CA certificates and `/tmp` inclusion verification.
