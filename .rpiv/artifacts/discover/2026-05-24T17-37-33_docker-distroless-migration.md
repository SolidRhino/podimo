---
date: 2026-05-24T17:37:33+0000
author: SolidRhino
commit: e06d65d
branch: go-rewrite
repository: SolidRhino/podimo
topic: "docker-distroless-migration"
tags: [intent, frd, docker, distroless, security]
status: complete
last_updated: 2026-05-24T17:37:33+0000
last_updated_by: SolidRhino
---

# FRD: Docker Distroless Migration

## Summary
Migrate the Docker runtime stage from `alpine:latest` to `gcr.io/distroless/static-debian13:nonroot` to reduce attack surface and image size. Remove the shell-based `HEALTHCHECK`, switch to the distroless built-in `nonroot` user, and change the default cache directory to `/tmp` so the container works in read-only filesystems without extra setup.

## Problem & Intent
The developer is maintaining this project and wants to harden the published Docker image and pass security scans. They want to follow container security best practices for the official Docker image published to GHCR. Success looks like a smaller runtime image with no shell, no package manager, and a reduced CVE surface — something that passes container security scanners without manual exceptions.

## Goals
- Reduce the runtime image attack surface by removing Alpine Linux, its package manager, and all shell utilities
- Produce a smaller image suitable for security scanning and minimal container runtimes
- Maintain the existing multi-arch CI build matrix (`linux/amd64`, `linux/arm64`, `linux/arm/v7`, `linux/arm/v6`)
- Keep the application fully functional after the migration with no regression in behavior

## Non-Goals
- Changing the builder stage (`golang:1.23-alpine` remains as-is)
- Adding new application features or endpoints
- Supporting a debug/shell variant of distroless for runtime troubleshooting
- Modifying the GitHub Actions workflow structure beyond adapting to the new base image

## Functional Requirements
1. The Dockerfile runtime stage **SHALL** use `gcr.io/distroless/static-debian13:nonroot` as its base image.
2. The container **SHALL** run as the built-in `nonroot` user (UID 65532) without creating custom users at build time.
3. The Docker `HEALTHCHECK` instruction **SHALL** be removed because distroless static images lack `curl`, `wget`, and a shell.
4. The application default cache directory **SHALL** change from `./cache` to `/tmp/podimo-rss-cache` so it works in read-only root filesystems without extra setup.
5. The CI workflow (`.github/workflows/docker-publish.yml`) **SHALL** continue building and pushing multi-platform images successfully.

## Non-Functional Requirements
- **Security**: Final image must contain no shell, no package manager, and no unnecessary filesystem entries.
- **Reliability**: The `/health` endpoint must remain available and functional for external orchestrator probes even though the Docker `HEALTHCHECK` is removed.
- **Performance**: No specific performance constraints — image size reduction is a welcome side effect, not a hard target.
- **UX / Accessibility**: No user-facing changes beyond the internal hardening.

## Constraints & Assumptions
- The Go binary must remain statically linked (`CGO_ENABLED=0`) because `gcr.io/distroless/static` does not include libc.
- The developer believes the chosen distroless image includes CA certificates. This must be verified before implementation; if false, the builder stage must copy them into the final image.
- The current multi-arch CI build matrix must be preserved without platform loss.
- Cache data is ephemeral and safe to store in `/tmp`; users who need persistence already configure `CACHE_DIR` via environment variable.

## Acceptance Criteria
- [ ] `docker build` produces an image whose final stage is based on `gcr.io/distroless/static-debian13:nonroot`.
- [ ] `docker run` starts the application successfully and `GET /health` returns HTTP 200.
- [ ] `docker inspect` shows the container running as user `nonroot` (UID 65532).
- [ ] The GitHub Actions `docker-publish.yml` workflow completes without errors for all declared platforms.
- [ ] `go test ./...` passes after any code changes to the default cache path.
- [ ] `docker run` with no `CACHE_DIR` override writes cache files successfully under `/tmp/podimo-rss-cache`.

## Recommended Approach
Update the Dockerfile runtime stage to `FROM gcr.io/distroless/static-debian13:nonroot`, remove the `HEALTHCHECK`, `addgroup`/`adduser`, and `apk add` `RUN` commands, and change the application default `CACHE_DIR` to `/tmp/podimo-rss-cache`. Copy the templates directory from the builder stage. No changes to the builder stage or CI workflow structure are needed beyond the base image name.

## Decisions

### Base image
**Question**: Pre-resolved from codebase evidence — the developer stated intent to use `gcr.io/distroless/static-debian13:nonroot`.
**Recommended**: n/a — intent-level decision
**Chosen**: `gcr.io/distroless/static-debian13:nonroot`
**Rationale**: Developer explicitly requested this image for security hardening of the published container.

### Static binary build
**Question**: The builder already compiles a fully static binary (`CGO_ENABLED=0`, `-ldflags="-s -w"`) at `Dockerfile:12`. Keep this for the distroless image?
**Recommended**: Keep as-is
**Chosen**: Keep as-is
**Rationale**: evidence: `Dockerfile:12` + confirmed. Static linking is required for `gcr.io/distroless/static` because it contains no libc.

### CA certificates
**Question**: The runtime stage currently installs `ca-certificates` via `apk` (`Dockerfile:19`). Distroless images don't have a package manager. How should TLS certificates be provided?
**Recommended**: Copy `ca-certificates.crt` from the builder stage into the final image at `/etc/ssl/certs/`.
**Chosen**: The developer believes `gcr.io/distroless/static-debian13:nonroot` (or `rootless`) already contains `ca-certificates.crt`.
**Rationale**: Developer stated belief that the chosen base image includes certificates; `research` must verify this before implementation proceeds.

### Container user
**Question**: The current image creates a `podimo` user (`Dockerfile:26-28`) and runs as non-root. Distroless `static-debian13:nonroot` already includes a `nonroot` user (UID 65532). Which user should the container run as?
**Recommended**: Use distroless `nonroot`
**Chosen**: Use distroless `nonroot`
**Rationale**: evidence: `Dockerfile:28` + confirmed. Eliminates custom user creation commands and aligns with distroless conventions.

### Health checking
**Question**: Since distroless `static` has no `curl`, `wget`, or shell, how should container health checking work?
**Recommended**: Remove the Docker `HEALTHCHECK` entirely. Orchestrators (Kubernetes, Docker Compose) can probe `/health` externally.
**Chosen**: Remove entirely
**Rationale**: Simplifies the image and aligns with modern container orchestrator best practices for external probes.

### Writable cache directory
**Question**: The current Dockerfile creates `/src/cache` and `chown`s it to the runtime user (`Dockerfile:27`). Distroless has no `mkdir`/`chown` at build time. How should the writable cache directory be handled?
**Recommended**: Change the application default to `/tmp/podimo-rss-cache` so it works out of the box in a read-only container without configuration.
**Chosen**: Change default to `/tmp`
**Rationale**: Avoids needing to pre-create and permission directories in the Dockerfile; `/tmp` is writable by default in most container runtimes.

## Open Questions
- Does `gcr.io/distroless/static-debian13:nonroot` actually include CA certificates? If not, the builder stage must be modified to copy `/etc/ssl/certs/ca-certificates.crt` from the Alpine builder into the final image.

## Suggested Follow-ups
- The `.dockerignore` still excludes Python-era artifacts (`__pycache__`, `*.pyc`, `venv`, `.venv`) from the prior Python implementation of the project. This is harmless but could be cleaned up in a separate maintenance pass.
- The current runtime uses `alpine:latest` without a digest pin. While this changes with distroless, consider pinning the distroless digest in a future hardening pass for reproducible builds.

## References
- `Dockerfile`
- `.github/workflows/docker-publish.yml`
- `.dockerignore`
- `config.go` (for `CACHE_DIR` default)
