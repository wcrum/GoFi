# The UI depends on a sibling repo (@cnak-us/design-system, file:../../cnak-design-system
# in ui/package.json) that lives OUTSIDE this repo's directory and therefore outside the
# default Docker build context — "COPY ../cnak-design-system ..." cannot work no matter
# how it's written, since Docker never has access to paths above the context root. This
# is provided instead as a named BuildKit build context (see Taskfile.yml's docker:build
# task / docker-compose.yml's additional_contexts), referenced here as "designsystem".
# Building with plain `docker build .` (no --build-context) will fail at the COPY below;
# use `task docker:build` / `task docker:up`, or pass
# `--build-context designsystem=../cnak-design-system` yourself.
#
# The copy destination below is "/cnak-design-system", NOT "/build/cnak-design-system":
# ui/package.json's dependency is "file:../../cnak-design-system", resolved relative to
# WORKDIR /build/ui — on the host that's two levels up from .../Coding/GoFi/ui to the
# sibling repo under .../Coding/, but /build/ui is only one level under /build, so the
# same "../.." lands at the image root, not under /build. Match that, don't "fix" it to
# /build/cnak-design-system or npm's file: symlink (created during npm ci, before this
# COPY runs) will point at a path that's never populated.
FROM node:22-alpine AS ui-builder
WORKDIR /build/ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci
COPY ui/ ./
COPY --from=designsystem . /cnak-design-system
RUN npm run build

FROM golang:1.26-alpine AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY --from=ui-builder /build/ui/dist ./ui/dist
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o gofi .

# NOTE: intentionally NOT using the "-nonroot" distroless tag here.
#
# In GOFI_DEPLOY_MODE=compose, the controller acts as an operator: it talks to
# the Docker Engine API over /var/run/docker.sock to spawn one sibling
# container per listener definition (see driver/docker.go). On a typical
# Docker host that socket is owned root:docker, mode 0660 — a container
# running as the distroless "nonroot" UID (65532), which has no matching
# group inside the image, cannot open it no matter how the socket is bind
# mounted. There's no runtime-mountable fix for that short of baking a
# matching GID into the image and hoping it lines up with the host's
# "docker" GID (it often won't), or running a separate socket-proxy sidecar
# (more correct, but more moving parts than a default dev-friendly compose
# setup warrants). Running this final stage as root is the simplest reliable
# fix and a defensible tradeoff given the image's job is to manage sibling
# containers, which inherently requires elevated Docker access.
#
# This only matters for the Docker driver (compose mode); GOFI_DEPLOY_MODE=
# kubernetes and =binary never touch the socket. If you build your own image
# for binary-mode-only deployments and want a non-root final stage, swap this
# back to gcr.io/distroless/static:nonroot — you don't need docker.sock
# access in that mode. Do not "fix" this back to nonroot for the default
# image without first solving docker.sock access some other way.
FROM gcr.io/distroless/static:latest
COPY --from=go-builder /build/gofi /gofi
# 9090: controller REST API + embedded UI. 9091: per-listener tap relay
# (TAP_RELAY_ADDR default, see taprelay/ and driver/docker.go's tapRelayPort) —
# only listens in listener mode (LISTENER_SPEC set), reached by the controller
# over the shared Compose network by container name.
EXPOSE 9090 9091
ENTRYPOINT ["/gofi"]
