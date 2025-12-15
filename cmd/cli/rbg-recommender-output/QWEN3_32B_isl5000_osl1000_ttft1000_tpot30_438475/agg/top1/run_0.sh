#!/bin/bash
set -e
trap 'echo "Cleaning up..."; kill 0 2>/dev/null || true' EXIT INT TERM

export MODEL_PATH=${MODEL_PATH:-"QWEN3_32B"}
export SERVED_MODEL_NAME=${SERVED_MODEL_NAME:-"QWEN3_32B"}
export HEAD_NODE_IP=${HEAD_NODE_IP:-"0.0.0.0"}
export ETCD_ENDPOINTS="${HEAD_NODE_IP}:2379"
export NATS_SERVER="nats://${HEAD_NODE_IP}:4222"

FRONTEND_SYSTEM_PORT=${FRONTEND_SYSTEM_PORT:-8080}
AGG_SYSTEM_PORT=${AGG_SYSTEM_PORT:-8081}
PREFILL_WORKERS=0
DECODE_WORKERS=0
PREFILL_SYSTEM_PORT_BASE=${PREFILL_SYSTEM_PORT_BASE:-8082}
DECODE_SYSTEM_PORT_BASE=${DECODE_SYSTEM_PORT_BASE:-$((PREFILL_SYSTEM_PORT_BASE + PREFILL_WORKERS))}

OTEL_SERVICE_NAME=dynamo-frontend \
python3 -m dynamo.frontend --http-port "8000" &

OTEL_SERVICE_NAME=dynamo-worker \
DYN_SYSTEM_PORT="${AGG_SYSTEM_PORT}" \
python3 -m dynamo.sglang \
  --model-path "$MODEL_PATH" \
  --served-model-name "$SERVED_MODEL_NAME" \
  --tensor-parallel-size "4" --pipeline-parallel-size "1" --data-parallel-size "1" --kv-cache-dtype "fp8_e5m2" --max-running-requests "32" --expert-parallel-size "1" --moe-dense-tp-size "1" --cuda-graph-bs 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 \
  --host "0.0.0.0" \
  --enable-metrics &
wait
