# Stage 1: Build
FROM golang:1.26-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o podimo-rss .

# Stage 2: Runtime
FROM gcr.io/distroless/static-debian13:nonroot AS runtime

WORKDIR /src

ENV PODIMO_CACHE_DIR=/tmp/podimo-rss-cache

COPY --from=builder /src/podimo-rss /src/podimo-rss

EXPOSE 12104

ENTRYPOINT ["/src/podimo-rss"]
