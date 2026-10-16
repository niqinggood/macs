# Stage 1: Build
FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go run ./cmd/releaseguard && \
    CGO_ENABLED=0 go vet ./... && \
    CGO_ENABLED=0 go test ./... -count=1 && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o openkitty ./cmd/macs

# Stage 2: Runtime
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata python3 git curl && \
    ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime

WORKDIR /app
COPY --from=builder /build/openkitty .

# Default config; production deployments normally mount their own file here.
COPY openkitty-config.template.yaml /app/openkitty-config.yaml

RUN mkdir -p /app/data /app/logs /app/storage /app/skills /app/memory /app/workspace

ENV OPENKITTY_LISTEN_ADDR=0.0.0.0:8080 \
    OPENKITTY_WORKSPACE=/app/workspace \
    OPENKITTY_FIXED_PORT=true \
    OPENKITTY_HEADLESS=true \
    OPENKITTY_DISABLE_HTTPS=true \
    OPENKITTY_SQLITE_PATH=/app/data/openkitty.db

EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -fsS http://localhost:8080/readyz >/dev/null || exit 1

VOLUME ["/app/data", "/app/logs", "/app/storage", "/app/skills", "/app/memory"]

CMD ["./openkitty", "--config", "/app/openkitty-config.yaml", "--path", "/app/workspace"]
