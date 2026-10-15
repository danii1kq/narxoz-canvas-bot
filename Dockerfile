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
    LOG_LEVEL=info

ENTRYPOINT ["/app/bot"]
