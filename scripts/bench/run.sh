#!/usr/bin/env bash
# Runs the data-plane benchmarks (framing, codec, parser, filter, pipeline,
# sink, listener) with -count for benchstat and writes the raw output to $1.
set -euo pipefail
OUT="${1:-testenv/reports/bench.txt}"
COUNT="${BENCH_COUNT:-6}"
mkdir -p "$(dirname "$OUT")"
GOWORK=off go test -run '^$' -bench . -benchmem -count "$COUNT" -timeout 30m \
  ./framing/... ./codec/... ./parser/... ./filter/... ./pipeline/... ./sink/... ./listener/... ./message/... | tee "$OUT"
