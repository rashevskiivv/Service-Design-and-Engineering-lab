# syntax=docker/dockerfile:1
#
# local-generative-ai gateway: one static Go binary on distroless (no shell,
# no package manager, uid 65532). State is one SQLite file on the /data volume.
#
#   docker build -t local-generative-ai:dev .
#   docker compose up --build            # laptop mode, see docker-compose.yml
#   docker buildx build --platform linux/amd64 -t local-generative-ai:lab .   # from an arm64 Mac
#
# Image pinning (security review #22). Both images are pinned to a patch tag.
# For the lab build also pin the digest, so a re-pushed tag cannot change the
# image under us: look it up, then write FROM <image>:<tag>@sha256:<digest>.
#   docker buildx imagetools inspect golang:1.26.8-bookworm
#   docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot
# Multi-arch index digests observed on 2026-09-30 (re-check before use):
#   golang:1.26.8-bookworm                    sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
#   gcr.io/distroless/static-debian12:nonroot sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
# go 1.26.8 is the latest 1.26 patch on that date; bump both tag and digest together.

ARG GO_IMAGE=golang:1.26.8-bookworm
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

# ---- build: runs on the build machine's platform, cross-compiles ----------
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
WORKDIR /src
# Static binary; go.sum must be complete (no silent module updates); never
# download a different toolchain than the pinned image's.
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway
# /data must exist and belong to nonroot (65532) in the image, so that a new
# named volume inherits that ownership; a root-owned volume makes SQLite fail
# with "readonly database". 0700: only the gateway reads its database.
RUN install -d -m 0700 /out/data

# ---- runtime ------------------------------------------------------------------
FROM ${RUNTIME_IMAGE}
COPY --from=build /out/gateway /gateway
COPY --from=build --chown=65532:65532 /out/data /data
# Container defaults: listen on all interfaces inside the container (publish the
# port on the host as 127.0.0.1:8080:8080 or behind the proxy only) and keep the
# DB on the volume. LGAI_ADMIN_ADDR keeps the gateway default (127.0.0.1:8081,
# unreachable from outside the container); set LGAI_ADMIN_ADDR=:8081 and publish
# 127.0.0.1:8081:8081 to use the admin API from the host, as compose does.
ENV LGAI_ADDR=:8080 \
    LGAI_DB_PATH=/data/gateway.db
# 65532 is "nonroot" in distroless; numeric so runAsNonRoot checks can verify it.
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
# Exec form: distroless has no shell and no curl. `gateway healthcheck` GETs
# http://127.0.0.1:<LGAI_ADDR port>/healthz and exits 0 or 1.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/gateway", "healthcheck"]
ENTRYPOINT ["/gateway"]
