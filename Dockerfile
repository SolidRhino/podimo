# Stage 1: Build
FROM golang:1.23-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o podimo-rss .

# Stage 2: Runtime
FROM alpine:latest AS runtime

WORKDIR /src

RUN apk add --no-cache curl ca-certificates

COPY --from=builder /src/podimo-rss /src/podimo-rss
COPY --from=builder /src/templates /src/templates

RUN addgroup -S podimo && adduser -S podimo -G podimo \
    && mkdir -p /src/cache \
    && chown -R podimo:podimo /src

USER podimo

EXPOSE 12104

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:12104/health || exit 1

ENTRYPOINT ["/src/podimo-rss"]
