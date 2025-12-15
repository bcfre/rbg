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
PREFILL_WORKERS=4
DECODE_WORKERS=1
PREFILL_SYSTEM_PORT_BASE=${PREFILL_SYSTEM_PORT_BASE:-8082}
DECODE_SYSTEM_PORT_BASE=${DECODE_SYSTEM_PORT_BASE:-$((PREFILL_SYSTEM_PORT_BASE + PREFILL_WORKERS))}

OTEL_SERVICE_NAME=dynamo-frontend \
python3 -m dynamo.frontend --http-port "8000" &

PREFILL_GPU=1
for ((w=0; w<PREFILL_WORKERS; w++)); do
  BASE=$(( w * PREFILL_GPU ))
  GPU_LIST=$(seq -s, $BASE $((BASE+PREFILL_GPU-1)))
  WORKER_IDX=$(( w + 1 ))
  SYSTEM_PORT=$(( PREFILL_SYSTEM_PORT_BASE + w ))
  WORKER_NAME="dynamo-worker-prefill"
  if (( PREFILL_WORKERS > 1 )); then
    WORKER_NAME="${WORKER_NAME}-${WORKER_IDX}"
  fi
  CUDA_VISIBLE_DEVICES=$GPU_LIST \
  OTEL_SERVICE_NAME="${WORKER_NAME}" \
  DYN_SYSTEM_PORT="${SYSTEM_PORT}" \
    python3 -m dynamo.sglang \
      --model-path "$MODEL_PATH" \
      --served-model-name "$SERVED_MODEL_NAME" \
      --tensor-parallel-size "1" --pipeline-parallel-size "1" --data-parallel-size "1" --kv-cache-dtype "fp8_e5m2" --max-running-requests "1" --expert-parallel-size "1" --moe-dense-tp-size "1" --cuda-graph-bs 1 --disaggregation-mode prefill \
      --host "0.0.0.0" \
      --enable-metrics &
done

DECODE_GPU=4
DECODE_GPU_OFFSET=4
for ((w=0; w<DECODE_WORKERS; w++)); do
  BASE=$(( DECODE_GPU_OFFSET + w * DECODE_GPU ))
  GPU_LIST=$(seq -s, $BASE $((BASE+DECODE_GPU-1)))
  WORKER_IDX=$(( w + 1 ))
  SYSTEM_PORT=$(( DECODE_SYSTEM_PORT_BASE + w ))
  WORKER_NAME="dynamo-worker-decode"
  if (( DECODE_WORKERS > 1 )); then
    WORKER_NAME="${WORKER_NAME}-${WORKER_IDX}"
  fi
  CUDA_VISIBLE_DEVICES=$GPU_LIST \
  OTEL_SERVICE_NAME="${WORKER_NAME}" \
  DYN_SYSTEM_PORT="${SYSTEM_PORT}" \
    python3 -m dynamo.sglang \
      --model-path "$MODEL_PATH" \
      --served-model-name "$SERVED_MODEL_NAME" \
      --tensor-parallel-size "4" --pipeline-parallel-size "1" --data-parallel-size "1" --kv-cache-dtype "fp8_e5m2" --max-running-requests "32" --expert-parallel-size "1" --moe-dense-tp-size "1" --cuda-graph-bs 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 --disaggregation-mode decode \
      --host "0.0.0.0" \
      --enable-metrics &
done
wait
