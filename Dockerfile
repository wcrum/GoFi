FROM --platform=$BUILDPLATFORM node:24.22.0-alpine3.23@sha256:e44c87c7dd212720eb00469aeac89504ca2be77b8f0d6dbffb30665d5fcc59e1 AS ui-builder
WORKDIR /build/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Multi-arch: `docker buildx build --platform linux/amd64,linux/arm64 .` sets
# TARGETOS/TARGETARCH; the Go stage cross-compiles on the build platform.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine3.23@sha256:9e45f0eb4a63ed37ad6e604950407558b7c738e34e015ae77a5d4ad369cde079 AS go-builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT
# GOFI_BUILD_TAGS=gofi_nosql,gofi_nokafka,gofi_nos3 builds the slim edge image.
ARG GOFI_BUILD_TAGS=""
ENV GOWORK=off CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /build/ui/dist ./ui/dist
RUN GOARM=${TARGETVARIANT#v} go build -trimpath -ldflags="-s -w" -tags "$GOFI_BUILD_TAGS" -o /gofi . \
    && mkdir -p /runtime/data /runtime/tmp \
    && chown 65532:65532 /runtime/data \
    && chmod 1777 /runtime/tmp

# A static binary with CA roots supports HTTPS sinks without a runtime shell.
# Compose overrides USER for its explicitly configured Docker socket access.
FROM scratch
LABEL org.opencontainers.image.source="https://github.com/wcrum/gofi" \
      org.opencontainers.image.title="GoFi" \
      org.opencontainers.image.version="0.2.0"
COPY --from=go-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-builder /runtime/ /
COPY --from=go-builder /gofi /gofi
WORKDIR /data
USER 65532:65532
# API/UI and listener-only tap relay; data ports come from listener definitions.
EXPOSE 9090 9091
ENTRYPOINT ["/gofi"]
