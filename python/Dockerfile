# Python worker images. Build with buildx for amd64/arm64:
#   docker buildx build --platform linux/amd64,linux/arm64 -f python/Dockerfile \
#     --target worker  -t ghcr.io/wcrum/gofi-python:0.2.0 .
#   docker buildx build --platform linux/amd64,linux/arm64 -f python/Dockerfile \
#     --target tooling -t ghcr.io/wcrum/gofi-python-tooling:0.2.0 .
# `worker` is the slim runtime for Sandbox libraries (no ruff); `tooling` adds
# ruff for the private format/validate worker. The default (last) stage is
# `tooling`, so an untargeted build keeps serving both roles.
FROM python:3.14.7-slim-bookworm@sha256:82bc3c539b8813ada9d68c63b40158fa002f7f33de9bf3312a3dfdc0620dff56 AS worker
ARG TARGETPLATFORM
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /opt/gofi
COPY python/requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt
COPY python/gofi ./gofi
LABEL org.opencontainers.image.source="https://github.com/wcrum/gofi" \
      org.opencontainers.image.title="GoFi Python worker" \
      org.opencontainers.image.version="0.2.0"
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["python", "-m", "gofi.serve"]

FROM worker AS tooling
USER 0:0
COPY python/requirements-tooling.txt ./requirements-tooling.txt
RUN pip install --no-cache-dir -r requirements-tooling.txt
LABEL org.opencontainers.image.title="GoFi Python tooling worker"
USER 65532:65532
