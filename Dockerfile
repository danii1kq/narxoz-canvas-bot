# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.27-alpine AS build
WORKDIR /app

# git is only needed if go ever fetches VCS deps; kept for safety.
RUN apk add --no-cache git ca-certificates

# Cache module download separately from source for faster rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# modernc.org/sqlite is pure Go, so CGO stays disabled.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

# ---- runtime stage ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

# Database lives on a mounted volume so it survives restarts.
VOLUME ["/data"]

COPY --from=build /out/bot /app/bot

# Defaults; can be overridden via environment / .env.
ENV DB_PATH=/data/bot.db \
    TZ=Asia/Almaty \
    LOG_LEVEL=info \
    WEBAPP_ADDR=:8080

# The Mini App exposes a public /healthz endpoint; use it to detect a stuck
# process. No curl needed: busybox wget ships with alpine. The port is taken
# from WEBAPP_ADDR (":8080" -> "8080") so a custom port keeps the check valid.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:$${WEBAPP_ADDR##*:}/healthz" || exit 1

ENTRYPOINT ["/app/bot"]
