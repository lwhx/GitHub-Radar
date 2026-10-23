# syntax=docker/dockerfile:1

# ---------- 前端构建 ----------
FROM node:22-alpine AS frontend
WORKDIR /build
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---------- 后端构建（纯 Go SQLite，无 CGO） ----------
FROM golang:1.22-alpine AS backend
WORKDIR /build
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/radar ./cmd/radar

# ---------- 运行时（非 root 单容器） ----------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 radar \
 && adduser -S -u 10001 -G radar radar \
 && mkdir -p /app/public /app/data \
 && chown -R radar:radar /app

COPY --from=backend /out/radar /app/radar
COPY --from=frontend /build/dist /app/public

USER radar
ENV APP_PORT=8080 \
    DATABASE_PATH=/app/data/radar.db \
    STATIC_DIR=/app/public \
    APP_ENV=production

EXPOSE 8080
VOLUME ["/app/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["/app/radar", "healthcheck"]

ENTRYPOINT ["/app/radar"]
CMD ["serve"]
