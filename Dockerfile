# ---- Build stage ----------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /app

# Cache module downloads separately from the source so code-only changes
# don't re-download dependencies.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
    -o /app/archive-service \
    ./cmd/server

# ---- Runtime stage --------------------------------------------------------
FROM alpine:3.22

ARG VERSION=dev

LABEL org.opencontainers.image.title="YFS Archive API" \
      org.opencontainers.image.description="Builds zip/tar archives of YFS folders and streams them to clients" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/Yukthi-Systems/YFS-Archive-API" \
      org.opencontainers.image.url="https://hub.docker.com/r/rjyspl/yfs-archive-api" \
      org.opencontainers.image.vendor="Yukthi Systems" \
      org.opencontainers.image.licenses="GPL-3.0"

# ca-certificates: HTTPS calls to storage servers / TLS to PostgreSQL.
# tzdata: correct local timestamps if TZ is set.
# wget (busybox) is already present and is used by HEALTHCHECK.
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=build /app/archive-service /app/archive-service
RUN mkdir -p /app/archives /app/logs

ENV APP_ENV=production \
    SERVER_HOST=0.0.0.0 \
    SERVER_PORT=8080 \
    LOG_OUTPUT=stdout \
    LOG_FILE=/app/logs/archive-service.log \
    ARCHIVE_TEMP_DIR=/app/archives

EXPOSE 8080

VOLUME ["/app/archives", "/app/logs"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${SERVER_PORT}/healthz" || exit 1

# The app handles SIGTERM itself (graceful shutdown of in-flight jobs), so
# run it as PID 1 directly.
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/archive-service"]
