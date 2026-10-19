#!/usr/bin/env bash
# edge-footprint.sh: measure GoFi's footprint as an edge binary.
#
# Runs the static edge binary in Docker with --memory=128m --cpus=0.5 and
# GOFI_PROFILE=edge, seeds FLOWS TCP line/JSON flows, opens IDLE idle TCP
# connections, then sends BURST JSON messages, and records startup time,
# RSS (peak and steady), CPU and whether the kernel OOM-killed the container.
#
#   scripts/edge-footprint.sh                      # linux/amd64
#   PLATFORM=linux/arm64 scripts/edge-footprint.sh # needs qemu binfmt (tonistiigi/binfmt)
#
# Knobs: PLATFORM MEMORY CPUS FLOWS BURST IDLE EDGE_TAGS OUT KEEP (=1 keeps the container)
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
PLATFORM=${PLATFORM:-linux/amd64}
MEMORY=${MEMORY:-128m}
CPUS=${CPUS:-0.5}
FLOWS=${FLOWS:-5}
BURST=${BURST:-10000}
IDLE=${IDLE:-500}
EDGE_TAGS=${EDGE_TAGS:-gofi_nosql,gofi_nokafka,gofi_nos3}
ARCH=${PLATFORM#linux/}
ARCH=${ARCH%%/*}
OUT=${OUT:-$ROOT/bin/footprint-$ARCH}
NAME=gofi-footprint-$ARCH
API=127.0.0.1:${API_PORT:-19090}
BASE_PORT=${BASE_PORT:-17000}
IMAGE=${IMAGE:-alpine:3.21}
export PATH=$HOME/.local/go/bin:$PATH GOWORK=off CGO_ENABLED=0

mkdir -p "$OUT/data"
chmod 777 "$OUT/data"
BIN=$ROOT/bin/gofi-edge-linux-$ARCH
if [ ! -x "$BIN" ] || [ "${REBUILD:-0}" = 1 ]; then
	echo "building $BIN (tags: $EDGE_TAGS)"
	(cd "$ROOT" && GOOS=linux GOARCH=$ARCH go build -trimpath -ldflags="-s -w" -tags "$EDGE_TAGS" -o "$BIN" .)
fi
BIN_BYTES=$(stat -c %s "$BIN")

# ---- seed flows -----------------------------------------------------------
{
	echo 'apiVersion: v1'
	echo 'kind: List'
	echo 'items:'
	for i in $(seq 1 "$FLOWS"); do
		cat <<EOF
  - apiVersion: gofi.wcrum.dev/v1alpha1
    kind: Flow
    metadata:
      name: fp-$i
    spec:
      source:
        listen:
          network: tcp
          port: $((7000 + i))
      framing:
        mode: line
      parser:
        format: json
        errorPolicy: drop
      sinks:
        - type: stdout
EOF
	done
} >"$OUT/data/listeners.yaml"
# Previous runs may have left root-owned files; clear them through a container.
rm -rf "$OUT/data/history" "$OUT/data/listeners.yaml.bak" 2>/dev/null || docker run --rm -v "$OUT/data:/data" "$IMAGE" sh -c 'rm -rf /data/history /data/listeners.yaml.bak' >/dev/null 2>&1 || true

docker rm -f "$NAME" >/dev/null 2>&1 || true
PORTS=()
for i in $(seq 1 "$FLOWS"); do PORTS+=(-p "127.0.0.1:$((BASE_PORT + i)):$((7000 + i))"); done

echo "starting $NAME ($PLATFORM, --memory=$MEMORY --cpus=$CPUS, $FLOWS flows)"
T0=$(date +%s%N)
docker run -d --name "$NAME" --platform "$PLATFORM" \
	--memory="$MEMORY" --memory-swap="$MEMORY" --cpus="$CPUS" \
	--read-only --tmpfs /tmp --user "$(id -u):$(id -g)" \
	-e GOFI_PROFILE=edge -e GOFI_DRIVER=inprocess -e GOFI_LOG_FORMAT=text \
	-e GOFI_CONFIG_FILE=/data/listeners.yaml -e INGRESS_API_ADDR=:9090 \
	-v "$BIN:/gofi:ro" -v "$OUT/data:/data" \
	-p "$API:9090" "${PORTS[@]}" \
	--entrypoint /gofi "$IMAGE" >/dev/null

# ---- startup --------------------------------------------------------------
READY=""
for _ in $(seq 1 600); do
	if curl -fs -o /dev/null "http://$API/healthz" 2>/dev/null; then
		READY=$(date +%s%N)
		break
	fi
	if [ "$(docker inspect -f '{{.State.Running}}' "$NAME")" != true ]; then
		echo "container exited early:"
		docker logs "$NAME" | tail -20
		exit 1
	fi
	sleep 0.05
done
[ -n "$READY" ] || { echo "API never became ready"; docker logs "$NAME" | tail -20; exit 1; }
STARTUP_MS=$(((READY - T0) / 1000000))
# Flows bind shortly after the API; wait until every port accepts.
for i in $(seq 1 "$FLOWS"); do
	for _ in $(seq 1 200); do
		if (exec 3<>"/dev/tcp/127.0.0.1/$((BASE_PORT + i))") 2>/dev/null; then break; fi
		sleep 0.05
	done
done
FLOWS_READY_MS=$((($(date +%s%N) - T0) / 1000000))

cg_mem() { docker exec "$NAME" cat /sys/fs/cgroup/memory.current 2>/dev/null || docker exec "$NAME" cat /sys/fs/cgroup/memory/memory.usage_in_bytes 2>/dev/null || echo 0; }
cg_peak() { docker exec "$NAME" cat /sys/fs/cgroup/memory.peak 2>/dev/null || docker exec "$NAME" cat /sys/fs/cgroup/memory/memory.max_usage_in_bytes 2>/dev/null || echo 0; }
cg_cpu_us() { docker exec "$NAME" sh -c 'awk "/^usage_usec/ {print \$2}" /sys/fs/cgroup/cpu.stat 2>/dev/null || echo 0'; }
# RSS of the gofi process itself (anon + file-backed pages), from /proc.
proc_rss_kb() { docker exec "$NAME" sh -c 'awk "/^VmRSS/ {print \$2}" /proc/1/status'; }
proc_hwm_kb() { docker exec "$NAME" sh -c 'awk "/^VmHWM/ {print \$2}" /proc/1/status'; }

sleep 2
MEM_IDLE=$(cg_mem)
RSS_IDLE_KB=$(proc_rss_kb)
GOMEMLIMIT_LINE=$(docker logs "$NAME" 2>&1 | grep -m1 'runtime limits applied' || true)

# ---- load generator -------------------------------------------------------
LOADGEN="$OUT/loadgen.py"
cat >"$LOADGEN" <<'PY'
import json, socket, sys, time
host, base, flows, idle, burst, phase = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5]), sys.argv[6]
ports = [base + i for i in range(1, flows + 1)]
if phase == "idle":
    socks = []
    for i in range(idle):
        s = socket.create_connection((host, ports[i % flows]), timeout=5)
        socks.append(s)
    sys.stdout.write("idle-open\n"); sys.stdout.flush()
    # Hold the connections open until killed (or 10 minutes).
    try:
        time.sleep(600)
    finally:
        for s in socks: s.close()
elif phase == "burst":
    conns = [socket.create_connection((host, p), timeout=5) for p in ports for _ in range(4)]
    t0 = time.time()
    per = burst // len(conns)
    for n, c in enumerate(conns):
        buf = b"".join(json.dumps({"seq": n * per + k, "app": "payments", "status": 200, "temp": 21.5}).encode() + b"\n" for k in range(per))
        c.sendall(buf)
    rem = burst - per * len(conns)
    for k in range(rem):
        conns[0].sendall(json.dumps({"seq": burst - rem + k, "app": "payments", "status": 200}).encode() + b"\n")
    for c in conns:
        c.close()
    print(f"burst-sent {burst} in {time.time()-t0:.3f}s")
PY

echo "opening $IDLE idle connections"
: >"$OUT/idle.log"
python3 -I "$LOADGEN" 127.0.0.1 "$BASE_PORT" "$FLOWS" "$IDLE" "$BURST" idle >"$OUT/idle.log" 2>&1 </dev/null &
IDLE_PID=$!
for _ in $(seq 1 300); do
	grep -q idle-open "$OUT/idle.log" && break
	kill -0 "$IDLE_PID" 2>/dev/null || { echo "idle connections failed:"; cat "$OUT/idle.log"; docker logs "$NAME" | tail; exit 1; }
	sleep 0.1
done
grep -q idle-open "$OUT/idle.log" || { echo "idle connections not established"; cat "$OUT/idle.log"; exit 1; }
sleep 2
MEM_IDLECONNS=$(cg_mem)
RSS_IDLECONNS_KB=$(proc_rss_kb)

echo "sending $BURST messages"
CPU0=$(cg_cpu_us)
TB0=$(date +%s%N)
python3 -I "$LOADGEN" 127.0.0.1 "$BASE_PORT" "$FLOWS" "$IDLE" "$BURST" burst
# Wait until every message reached the stdout sink (one JSON line each).
DELIVERED=0
for _ in $(seq 1 1200); do
	DELIVERED=$(docker logs "$NAME" 2>/dev/null | grep -c '"seq"' || true)
	[ "$DELIVERED" -ge "$BURST" ] && break
	sleep 0.1
done
TB1=$(date +%s%N)
CPU1=$(cg_cpu_us)
BURST_MS=$(((TB1 - TB0) / 1000000))
CPU_BURST_MS=$(((CPU1 - CPU0) / 1000))
MEM_BURST=$(cg_mem)
RSS_BURST_KB=$(proc_rss_kb)
kill "$IDLE_PID" 2>/dev/null || true
sleep 1
PEAK=$(cg_peak)
RSS_HWM_KB=$(proc_hwm_kb)
OOM=$(docker inspect -f '{{.State.OOMKilled}}' "$NAME")
RUNNING=$(docker inspect -f '{{.State.Running}}' "$NAME")
RESTARTS=$(docker inspect -f '{{.RestartCount}}' "$NAME")
UNAME=$(docker exec "$NAME" uname -m 2>/dev/null || echo "?")

mib() { awk -v b="$1" 'BEGIN { printf "%.1f", b / 1048576 }'; }
kib_mib() { awk -v k="$1" 'BEGIN { printf "%.1f", k / 1024 }'; }

REPORT="$OUT/report.md"
{
	echo "# GoFi edge footprint ($PLATFORM / $UNAME)"
	echo
	echo "Binary: \`$(basename "$BIN")\` $(mib "$BIN_BYTES") MiB (tags: $EDGE_TAGS); container limits --memory=$MEMORY --cpus=$CPUS; GOFI_PROFILE=edge; $FLOWS TCP line/JSON flows -> stdout sink."
	echo
	echo "| Metric | Value |"
	echo "|---|---|"
	echo "| Startup to /healthz | ${STARTUP_MS} ms |"
	echo "| Startup until all $FLOWS flow ports accept | ${FLOWS_READY_MS} ms |"
	echo "| RSS idle (process VmRSS / cgroup) | $(kib_mib "$RSS_IDLE_KB") MiB / $(mib "$MEM_IDLE") MiB |"
	echo "| RSS with $IDLE idle connections | $(kib_mib "$RSS_IDLECONNS_KB") MiB / $(mib "$MEM_IDLECONNS") MiB |"
	echo "| RSS after ${BURST}-message burst | $(kib_mib "$RSS_BURST_KB") MiB / $(mib "$MEM_BURST") MiB |"
	echo "| Peak (process VmHWM / cgroup memory.peak) | $(kib_mib "$RSS_HWM_KB") MiB / $(mib "$PEAK") MiB |"
	echo "| Burst wall time (send + deliver $DELIVERED/$BURST) | ${BURST_MS} ms |"
	echo "| CPU time consumed during burst | ${CPU_BURST_MS} ms (of a ${CPUS}-core budget) |"
	echo "| OOM killed / still running / restarts | $OOM / $RUNNING / $RESTARTS |"
	echo
	echo "Runtime line: \`${GOMEMLIMIT_LINE:-n/a}\`"
} | tee "$REPORT"

if [ "$OOM" != false ] || [ "$RUNNING" != true ] || [ "$DELIVERED" -lt "$BURST" ]; then
	echo "FOOTPRINT CHECK FAILED (oom=$OOM running=$RUNNING delivered=$DELIVERED/$BURST)"
	docker logs "$NAME" 2>&1 | grep -v '"seq"' | tail -30
	[ "${KEEP:-0}" = 1 ] || docker rm -f "$NAME" >/dev/null
	exit 1
fi
[ "${KEEP:-0}" = 1 ] || docker rm -f "$NAME" >/dev/null
echo "report: $REPORT"
