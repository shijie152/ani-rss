# Go-only multi-platform image. Buildx supplies TARGETOS/TARGETARCH/TARGETVARIANT
# so the same Dockerfile produces amd64, arm64 and arm/v7 images.
FROM --platform=$BUILDPLATFORM node:22-alpine AS ui
WORKDIR /src/ani-rss-ui
COPY ani-rss-ui/package.json ani-rss-ui/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY ani-rss-ui/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG ANI_RSS_VERSION=dev
WORKDIR /src/go-backend
COPY go-backend/go.mod go-backend/go.sum ./
RUN go mod download
COPY go-backend/ ./
RUN set -eux; \
    export GOARM=""; \
    if [ "${TARGETARCH}" = "arm" ] && [ "${TARGETVARIANT}" = "v7" ]; then export GOARM=7; fi; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" GOARM="${GOARM}" \
      go build -trimpath -ldflags "-s -w -X main.version=${ANI_RSS_VERSION}" -o /ani-rss ./cmd/ani-rss

FROM alpine:3.22
RUN apk add --no-cache ca-certificates su-exec tzdata
COPY --from=backend /ani-rss /usr/app/ani-rss
COPY --from=ui /src/ani-rss-ui/dist /usr/app/ui
COPY docker/run.sh /run.sh
COPY docker/exec.sh /exec.sh
WORKDIR /usr/app
VOLUME /config
ENV PUID=0 PGID=0 UMASK=022
ENV SERVER_PORT=7789 CONFIG=/config TZ=Asia/Shanghai SWAGGER_ENABLED=false MCP_ENABLED=false
EXPOSE 7789
RUN chmod 0755 /usr/app/ani-rss /run.sh /exec.sh
ENTRYPOINT ["/exec.sh"]
