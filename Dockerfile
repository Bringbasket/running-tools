FROM node:22.14-alpine AS web
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./frontend/
RUN cd frontend && npm ci
COPY frontend ./frontend
COPY modules/mail/frontend ./modules/mail/frontend
COPY modules/tools/frontend ./modules/tools/frontend
RUN cd frontend && npm run build

FROM golang:1.24.13-alpine AS backend
ARG RUNNING_VERSION=0.0.1
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY modules ./modules
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -tags embed -trimpath -ldflags="-s -w" -o /out/running-tools ./cmd/server

FROM alpine:3.22
ARG RUNNING_VERSION=0.0.1
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 running \
    && adduser -S -D -H -u 10001 -G running running \
    && mkdir -p /data/mail/state /data/system \
    && chown -R running:running /data
COPY --from=backend /out/running-tools /usr/local/bin/running-tools
ENV RUNNING_ADDR=:8000 RUNNING_DATA_DIR=/data RUNNING_REVISION=${RUNNING_VERSION}
USER running:running
EXPOSE 8000
HEALTHCHECK --interval=15s --timeout=4s --start-period=10s --retries=4 CMD wget -qO- http://127.0.0.1:8000/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/running-tools"]
