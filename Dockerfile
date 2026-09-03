# ── Build stage ──────────────────────────────────────────────────────────────
# Kompilasi dilakukan di sini (statis, tanpa cgo — modernc.org/sqlite murni Go)
# supaya image akhir tidak perlu toolchain Go sama sekali, cuma binary jadi.
FROM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

# ── Runtime stage ────────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -H -u 10001 bot

WORKDIR /app
COPY --from=builder /out/bot ./bot

RUN mkdir -p /app/data /app/watch && chown -R bot:bot /app
USER bot

ENV DB_PATH=/app/data/bot.db \
    WATCHER_DIR=/app/watch

EXPOSE 8080

ENTRYPOINT ["/app/bot"]
