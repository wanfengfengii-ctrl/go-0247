#!/usr/bin/env bash
# TorqueChain smoke test: builds the service, starts it on a local port, drives
# a real lock + query through the public JSON API, and tears everything down.
#
# Requirements honoured:
#   * bash with fail-fast (set -euo pipefail)
#   * a real public CLI/HTTP flow (not `go test`)
#   * deterministic, no external network
#   * every process and temp file is cleaned up
#   * responses are captured into variables and asserted, so the HTTP client is
#     never piped into an early-exiting reader (avoids SIGPIPE under pipefail)
set -euo pipefail

BIN="torquechain"
PORT="${TORQUECHAIN_PORT:-18080}"
ADDR="127.0.0.1:${PORT}"
BASE="http://${ADDR}"
TMPDIR="$(mktemp -d)"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

echo "building service"
go build -o "$TMPDIR/$BIN" ./cmd/torquechain

echo "starting service on $ADDR"
TORQUECHAIN_DB="$TMPDIR/torquechain.db" TORQUECHAIN_ADDR="$ADDR" "$TMPDIR/$BIN" &
SERVER_PID=$!

# Wait for the health endpoint to come up (bounded, deterministic).
ready=0
for _ in $(seq 1 50); do
  resp="$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/v1/health" 2>/dev/null || true)"
  if [[ "$resp" == "200" ]]; then
    ready=1
    break
  fi
  sleep 0.1
done
if [[ "$ready" != "1" ]]; then
  echo "service failed to become healthy" >&2
  exit 1
fi

echo "probing health"
health="$(curl -sS "$BASE/v1/health")"
if [[ "$health" != *'"status":"ok"'* ]]; then
  echo "unexpected health response: $health" >&2
  exit 1
fi

echo "probing catalogue"
nodes="$(curl -sS "$BASE/v1/catalog/nodes")"
if [[ "$nodes" != *'"node_id":"N-01"'* ]]; then
  echo "catalogue nodes missing N-01: $nodes" >&2
  exit 1
fi

echo "locking a task"
lock_status="$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$BASE/v1/tasks" \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"SMOKE-1","generation":1,"operation_no":"smoke-lock","node_ids":["N-01"],"sampling_set":[{"node_id":"N-01","bolt_no":1}],"torque_bounds":{"design_preload_n":240000,"torque_range_nm":{"min":300,"max":700},"angle_range_deg":{"min":30,"max":360}},"recheck_spec":{"trials":3,"coefficient_min":0.08,"coefficient_max":0.20},"device_id":"TD-001","window":{"start_unix":0,"end_unix":4102444800}}')"
if [[ "$lock_status" != "201" ]]; then
  echo "lock returned HTTP $lock_status" >&2
  exit 1
fi

echo "reading the locked task"
task="$(curl -sS "$BASE/v1/tasks/SMOKE-1")"
if [[ "$task" != *'"status":"PAIR_VERIFICATION"'* ]]; then
  echo "task did not enter PAIR_VERIFICATION: $task" >&2
  exit 1
fi

echo "smoke test passed"
