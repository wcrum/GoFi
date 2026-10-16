#!/bin/sh
set -eu
# Run from the repository root after installing python/requirements-dev.txt.
python -m grpc_tools.protoc -I proto \
  --python_out=python --pyi_out=python \
  --plugin=protoc-gen-connectrpc="$(command -v protoc-gen-connectrpc)" \
  --connectrpc_out=protobuf=google:python proto/gofi/v1/sandbox.proto
