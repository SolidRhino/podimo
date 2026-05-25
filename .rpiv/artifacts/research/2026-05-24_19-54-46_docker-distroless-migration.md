---
date: 2026-05-24T19:54:46+0200
author: SolidRhino
commit: e06d65d
branch: go-rewrite
repository: podimo
topic: "Docker Distroless Migration"
tags: [research, docker, distroless, security, go-rewrite]
status: complete
last_updated: 2026-05-24T19:54:46+0200
last_updated_by: SolidRhino
---

# Research: Docker Distroless Migration

## Research Question
Migrate the Docker runtime stage from `alpine:latest` to `gcr.io/distroless/static-debian13:nonroot`, remove the shell-based `HEALTHCHECK`, switch to the distroless built-in `nonroot` user, and change the default cache directory to `/tmp/podimo-rss-cache` so the container works in read-only filesystems without extra setup.

## Summary
The migration is a runtime-stage-only change. The builder stage (`golang:1.23-alpine`) remains untouched because it already produces a `CGO_ENABLED=0` static binary. Key findings:

1. **CA certificates are included** in `gcr.io/distroless/static-debian13:nonroot` — no builder-stage copy needed.
2. **Templates are embedded** via `//go:embed` — the `COPY --from=builder /src/templates` instruction is redundant and can be removed.
3. **`linux/arm/v6` is unsupported** by the distroless image and must be dropped from the CI multi-arch matrix.
4. **`.env.docker` and `docker-compose.yml` must be updated** because they hardcode `/src/cache` and `/src/.block-list`, and define a `curl`-based HEALTHCHECK that is impossible in a shell-less image.
5. **The `nonroot` user (UID 65532) can execute the static binary** (owned by root, `0755`) and write to `/tmp/podimo-rss-cache` (`/tmp` is `1777` in the base image) without any Dockerfile ownership commands.

## Detailed Findings

### Dockerfile Runtime Stage Transformation
- `Dockerfile:16` — `FROM alpine:latest AS runtime` is replaced with `FROM gcr.io/distroless/static-debian13:nonroot AS runtime`.
- `Dockerfile:19` — `RUN apk add --no-cache curl ca-certificates` is **deleted**. Distroless has no package manager, and CA certs are pre-installed.
- `Dockerfile:21-22` — `COPY --from=builder` for binary and templates are **preserved** (templates COPY is redundant but harmless; can be removed to reduce image size).
- `Dockerfile:26-28` — The `addgroup`/`adduser`/`mkdir`/`chown` block is **deleted**. The `:nonroot` base image already provides the `nonroot` user (UID 65532).
- `Dockerfile:30` — `USER podimo` is **deleted**; the base image already sets `USER nonroot:nonroot`.
- `Dockerfile:31-32` — `HEALTHCHECK ... CMD curl ...` is **deleted**. The image has no shell, no `curl`, and no `wget`.
- `Dockerfile:35` — `ENTRYPOINT ["/src/podimo-rss"]` is **preserved**; the static binary path and permissions are unchanged.

### Cache Directory and User Permissions
- `config.go:57` — Default `CACHE_DIR` fallback must change from `"./cache"` to `"/tmp/podimo-rss-cache"`.
- `config.go:91` — `os.MkdirAll(cfg.CacheDir, 0755)` is the fail-fast gate. With the new default, it creates `/tmp/podimo-rss-cache` at startup.
- `main.go:114-126` — Three `podimo.NewFileCache` calls create subdirectories (`tokens_cache`, `podcast_cache`, `head_cache`) under `cfg.CacheDir`.
- `podimo/cache.go:23` — Each `FileCache` constructor calls `os.MkdirAll(dir, 0755)` again, ensuring self-contained subdirectory creation.
- The `nonroot` user can create directories in `/tmp` (image-level `1777`) and owns the created directories, so `0755` directories and `0644` cache files are fully writable without Dockerfile `chown`.

### CA Certificates
- `podimo/graphql.go:42` — `c.client.Do(req)` initiates TLS to `https://podimo.com/graphql`.
- Google's distroless `static` image family explicitly includes `ca-certificates` (along with `tzdata`, `/tmp`, and a root passwd entry).
- Verified via [GoogleContainerTools/distroless base README](https://github.com/GoogleContainerTools/distroless/blob/main/base/README.md) and the official Go Dockerfile example, which contains no `COPY` of CA certs.
- If the CA bundle were missing, every HTTPS GraphQL call would fail with `x509: certificate signed by unknown authority`, rendering the app non-functional.

### Templates Delivery
- `main.go:26-27` — `//go:embed templates/*` bundles templates into the compiled binary at build time.
- `main.go:102-108` — `template.ParseFS(templatesFS, ...)` reads from the embedded filesystem, not from disk.
- `main.go:396/417/434` — Runtime rendering uses the pre-parsed in-memory `*template.Template` objects.
- `Dockerfile:22` — `COPY --from=builder /src/templates /src/templates` is **redundant**; no runtime code reads templates from disk. It can be removed to slightly reduce image size.
- `.dockerignore` — No pattern excludes `templates/`; it reaches the builder stage unfiltered.

### Health-Checking Seam
- `Dockerfile:31-32` — Docker `HEALTHCHECK` must be removed because distroless lacks `curl`, `/bin/sh`, and all shell utilities.
- `main.go:164` — `r.Get("/health", a.handleHealth)` registers the handler without `rateLimitMiddleware`, so orchestrator probes are never throttled.
- `main.go:197-200` — `handleHealth` writes hardcoded `{"status":"ok","service":"podimo-rss"}` with zero external dependencies.
- `main_test.go:63-69` — `TestHealthHandler` enforces the 200 + JSON contract, preventing accidental regression.
- External orchestrators (Kubernetes `httpGet` probe, Docker Compose external health check, ALB target-group checks) replace the internal Docker HEALTHCHECK.

### CI Multi-Arch Matrix
- `.github/workflows/docker-publish.yml:41` — `docker/setup-buildx-action` platforms list.
- `.github/workflows/docker-publish.yml:58` — `docker/build-push-action` platforms list.
- Both declare `linux/amd64, linux/arm64, linux/arm/v7, linux/arm/v6`.
- `gcr.io/distroless/static-debian13:nonroot` publishes manifests for `linux/amd64`, `linux/arm64`, and `linux/arm` (which Docker resolves as `v7`). It does **not** publish a `linux/arm/v6` manifest.
- The builder stage (`golang:1.23-alpine`) supports all four platforms and needs no changes.
- **Required change**: Remove `linux/arm/v6` from both platforms lists in the CI workflow.

### Supporting Files That Must Change
- `.env.docker:57` — `CACHE_DIR=/src/cache` must change to `CACHE_DIR=/tmp/podimo-rss-cache`.
- `.env.docker:61` — `BLOCK_LIST_FILE=/src/.block-list` must change to `BLOCK_LIST_FILE=/tmp/.block-list`.
- `docker-compose.yml:17` — Volume mount `podimo-cache:/src/cache` must change to `podimo-cache:/tmp/podimo-rss-cache`.
- `docker-compose.yml:30` — The `healthcheck` using `curl` inside the container must be removed or replaced with an external probe mechanism.

## Code References
- `Dockerfile:16` — Current runtime base image (`alpine:latest`)
- `Dockerfile:19` — Alpine package installation (to be deleted)
- `Dockerfile:21` — Static binary COPY (preserve)
- `Dockerfile:22` — Templates COPY (redundant, can remove)
- `Dockerfile:26-28` — Custom user creation (to be deleted)
- `Dockerfile:30` — `USER podimo` (to be deleted)
- `Dockerfile:31-32` — Docker HEALTHCHECK with curl (to be deleted)
- `config.go:57` — `CACHE_DIR` default fallback
- `config.go:91` — `os.MkdirAll(cfg.CacheDir, 0755)` fail-fast gate
- `main.go:26-27` — `//go:embed templates/*`
- `main.go:102-108` — `template.ParseFS` startup parsing
- `main.go:114-126` — Three `podimo.NewFileCache` instantiations
- `main.go:164` — `/health` route registration (no rate limiter)
- `main.go:197-200` — `handleHealth` handler
- `podimo/cache.go:23` — `FileCache` constructor with `MkdirAll`
- `podimo/graphql.go:42` — TLS HTTP call site
- `.github/workflows/docker-publish.yml:41` — Buildx platforms setup
- `.github/workflows/docker-publish.yml:58` — Build-push platforms list
- `.env.docker:57` — `CACHE_DIR=/src/cache`
- `.env.docker:61` — `BLOCK_LIST_FILE=/src/.block-list`
- `docker-compose.yml:17` — Cache volume mount
- `docker-compose.yml:30` — Compose-level HEALTHCHECK using curl

## Integration Points

### Inbound References
- `.github/workflows/docker-publish.yml:52` — CI builds and pushes the Dockerfile; must succeed after the base image swap.
- `docker-compose.yml` — Local orchestration uses the built image; HEALTHCHECK and volume paths must align.
- `.env.docker` — Sourced by docker-compose; path defaults must match the new writable location.
- `main_test.go:63-69` — `TestHealthHandler` asserts the endpoint contract that replaces the Docker HEALTHCHECK.

### Outbound Dependencies
- `gcr.io/distroless/static-debian13:nonroot` — New runtime base image; provides `nonroot` user, CA certs, and `/tmp`.
- `golang:1.23-alpine` — Unchanged builder base image.
- `https://podimo.com/graphql` — HTTPS target; relies on the distroless-included CA bundle.

### Infrastructure Wiring
- `Dockerfile:16` — Runtime base image (DI of the OS layer).
- `Dockerfile:35` — `ENTRYPOINT` wires the static binary as PID 1.
- `config.go:52` — `PODIMO_BIND_HOST` defaults to `127.0.0.1:12104`; containers override to `0.0.0.0:12104`.
- `main.go:159-172` — Chi router wires `/health` without rate limiting.

## Architecture Insights
- **Double `MkdirAll` is a feature, not redundancy.** `config.go:91` fails fast if the filesystem is read-only; `podimo/cache.go:23` makes each cache namespace self-provisioning.
- **Embedded templates eliminate a runtime filesystem dependency.** The `//go:embed` pattern means the runtime image needs only the binary, not the templates directory.
- **Static binary + distroless = minimal attack surface.** The Go binary has no libc dependency, so the `static` image (no glibc, no musl) is sufficient.
- **Rate-limiting exclusion for `/health`** is intentional and load-bearing for orchestrator compatibility.

## Precedents & Lessons
4 similar past changes analyzed.

### Precedent: Docker Hardened Images (DHI) migration on `harden-image` branch
**Commit(s)**: `7edf976` — "build(docker): switch to Docker Hardened Images (dhi.io/python)" (2026-05-21)
**Blast radius**: 5 files across build + config + docs layers
  - `Dockerfile` — switched builder + runtime to dhi.io/python; removed HEALTHCHECK
  - `.env.docker` — updated paths (/src → /app)
  - `docker-compose.yml` — switched healthcheck to Python urllib-based check
  - `README.md`, `tutorial.md` — updated references

**Follow-up fixes**:
- `4c630c7` — "build(docker): avoid RUN in runtime stage, use /tmp for cache" (2026-05-21, 30 min later)
  - Runtime DHI image has **no shell**, so all `RUN` instructions failed.
  - Fixed by removing all `RUN` from runtime stage and moving `CACHE_DIR` to `/tmp/podimo-cache`.
  - Also updated `.env.docker`, `docker-compose.yml`, `README.md`, `tutorial.md`.
- `565760c` — "fix(docker): add lxml runtime libs and fix PYTHONPATH warning" (2026-05-20)
  - Missing runtime shared libraries caused `OSError` at runtime.

**Lessons from docs**:
- `.rpiv/artifacts/discover/2026-05-24T17-37-33_docker-distroless-migration.md` — flags the exact same risk: "hardened/minimal runtime image with no shell will break every RUN instruction."

**Takeaway**: A shell-less runtime image breaks every RUN, HEALTHCHECK, adduser, mkdir, and chown in the runtime stage; the prior DHI attempt needed an emergency follow-up within 30 minutes.

### Precedent: Go rewrite + Dockerfile update on `go-rewrite` branch
**Commit(s)**: `4539b58` — "feat: rewrite entire service from Python to Go" (2026-05-24)
`a46d14d` — "build: update Dockerfile and Makefile for Go build" (2026-05-24, 1 min later)
**Blast radius**: 30 files across all layers

**Follow-up fixes**:
- `01647d5` — "fix: address validation findings — dead code and structured logging" (2026-05-24, 8 min later)
  - Removed unreachable branch from `buildFeedURL`
  - Added `*slog.Logger` to `PodimoClient` to avoid nil-deref panics

**Takeaway**: Big Dockerfile/stack changes trigger immediate follow-up fixes. Budget time for a validation pass right after the migration.

### Precedent: CI `docker-publish.yml` tagging policy flip-flops
**Commit(s)**: `cf666fe`, `d369379`, `5be492d`, `f3cbe14` — all 2026-05-21
**Blast radius**: 1 file each time

**Takeaway**: `:latest` tagging policy is volatile. Avoid touching the workflow's tagging/metadata logic unless necessary.

### Composite Lessons
- **Hardened/minimal runtime = zero shell, zero package manager, zero RUN commands.** Every RUN, HEALTHCHECK, addgroup, adduser, mkdir, and chown in the runtime stage must be removed.
- **Cache directory must move to `/tmp` or another world-writable path.** The prior DHI follow-up (`4c630c7`) was forced because `nonroot` could not write to `/src/cache` without `RUN chown`.
- **Big Dockerfile changes trigger immediate follow-up fixes.** The Go rewrite had a follow-up 8 minutes later; the DHI migration had one 30 minutes later.
- **Avoid touching `.github/workflows/docker-publish.yml` tagging/metadata logic** to prevent re-igniting the flip-flop pattern.

## Historical Context (from `.rpiv/artifacts/`)
- `.rpiv/artifacts/discover/2026-05-24T17-37-33_docker-distroless-migration.md` — FRD documenting the planned distroless migration, acceptance criteria, and open CA-certificates question.

## Developer Context
**Q (discover: Base image):** `gcr.io/distroless/static-debian13:nonroot`
A: Developer explicitly requested this image for security hardening of the published container.

**Q (discover: Static binary build):** Keep `CGO_ENABLED=0` and `-ldflags="-s -w"`?
A: Keep as-is. Static linking is required for `gcr.io/distroless/static` because it contains no libc.

**Q (discover: CA certificates):** Does distroless include CA certs, or must the builder copy them?
A: Research verified that `gcr.io/distroless/static-debian13:nonroot` already includes `ca-certificates`. No builder-stage copy needed.

**Q (discover: Container user):** Use distroless `nonroot` or keep custom `podimo` user?
A: Use distroless `nonroot` (UID 65532). Eliminates custom user creation commands and aligns with distroless conventions.

**Q (discover: Health checking):** Remove Docker HEALTHCHECK entirely?
A: Remove entirely. Orchestrators (Kubernetes, Docker Compose) can probe `/health` externally.

**Q (discover: Writable cache directory):** Change default to `/tmp`?
A: Change default to `/tmp/podimo-rss-cache`. Avoids needing to pre-create and permission directories in the Dockerfile; `/tmp` is writable by default in most container runtimes.

**Q (docker-publish.yml:41,58):** `linux/arm/v6` is NOT supported by distroless static-debian13:nonroot. How to handle?
A: Drop `linux/arm/v6` from both the Buildx setup and build-push action platforms list.

**Q (.env.docker:57, docker-compose.yml:17,30):** .env.docker and docker-compose.yml hardcode /src paths and define a curl-based HEALTHCHECK. How to update?
A: Update .env.docker `CACHE_DIR` to `/tmp/podimo-rss-cache` and `BLOCK_LIST_FILE` to `/tmp/.block-list`. Update docker-compose.yml volume mount to `/tmp/podimo-rss-cache` and remove the container-internal HEALTHCHECK.

## Related Research
- None yet.

## Open Questions
- None remaining. All questions from the FRD and the developer checkpoint have been resolved.
