# Stage 1: Build
FROM golang:1.26.5-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o podimo-rss .

# Pre-create cache dir with nonroot ownership so named-volume init preserves it
RUN mkdir -p /tmp/podimo-rss-cache && chown 65532:65532 /tmp/podimo-rss-cache && touch /tmp/podimo-rss-cache/.keep

# Stage 2: Runtime (scratch — zero attack surface: no shell, no package manager, no libs)
FROM scratch

# CA certificates for outbound HTTPS (Podimo GraphQL API + audio URL HEAD requests)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Static binary
COPY --from=builder /src/podimo-rss /podimo-rss

# Cache directory (correct ownership for named-volume initialization)
COPY --from=builder --chown=65532:65532 /tmp/podimo-rss-cache /tmp/podimo-rss-cache

ENV PODIMO_CACHE_DIR=/tmp/podimo-rss-cache

USER 65532:65532

EXPOSE 12104

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/podimo-rss", "healthcheck"]

ENTRYPOINT ["/podimo-rss"]