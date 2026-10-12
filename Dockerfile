# syntax=docker/dockerfile:1.7
# The single-binary image is the one deployment artifact core ships. The
# hosted edition's split processes are built by its own Dockerfile.
FROM --platform=$BUILDPLATFORM node:25-alpine AS frontend
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./frontend/
RUN --mount=type=cache,target=/root/.npm cd frontend && npm ci
COPY scripts/third-party-notices.sh ./scripts/third-party-notices.sh
RUN sh scripts/third-party-notices.sh npm frontend > /src/frontend-notices.txt
COPY frontend ./frontend
RUN cd frontend && npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-src
WORKDIR /src
RUN apk add --no-cache git ca-certificates
ENV GOPRIVATE=github.com/agentapprovalprotocol/agentapprovalprotocol
COPY scripts/private-go-modules.sh ./scripts/private-go-modules.sh
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=secret,id=github_token \
    sh scripts/private-go-modules.sh go mod download
# The whole module, not an enumerated list of package directories: a top-level
# package that is missing from the image build fails only at release time.
# .dockerignore keeps non-build files out of the context to limit cache
# invalidation.
COPY . .
COPY --from=frontend /src/webapp/assets ./webapp/assets
COPY --from=frontend /src/frontend-notices.txt /out/frontend-notices.txt
ARG TARGETOS TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH

FROM go-src AS bundle-builder
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/withhuman ./cmd/withhuman
# The image redistributes every module the binary compiles in and every
# package the frontend bundles, so it carries their licence texts.
RUN --mount=type=cache,target=/go/pkg/mod \
    { sh scripts/third-party-notices.sh go ./cmd/withhuman && cat /out/frontend-notices.txt; } > /out/THIRD_PARTY_NOTICES

# The bundle runs embedded PostgreSQL, whose zonky.io binaries are
# glibc-linked, so this stage cannot use alpine. initdb refuses to run as
# root, so the non-root user is required for correctness, not just hygiene.
FROM debian:13-slim AS bundle
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system withhuman \
    && useradd --system --gid withhuman --home-dir /data withhuman \
    && mkdir /data && chown withhuman:withhuman /data
COPY --from=bundle-builder /out/withhuman /usr/local/bin/
COPY LICENSE /usr/share/doc/withhuman/LICENSE
COPY --from=bundle-builder /out/THIRD_PARTY_NOTICES /usr/share/doc/withhuman/THIRD_PARTY_NOTICES
USER withhuman
ENV WITHHUMAN_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["withhuman"]
