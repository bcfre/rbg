# Quick Start: kubectl-rbg recommender

## Installation

1. Build kubectl-rbg:
   ```bash
   cd /path/to/rbg
   go build -o bin/kubectl-rbg ./cmd/cli
   ```

2. Install aiconfigurator:
   ```bash
   pip install aiconfigurator
   ```

## Usage

### Basic Example

```bash
./bin/kubectl-rbg recommender \
  --model QWEN3_32B \
  --system h200_sxm \
  --total-gpus 8 \
  --backend sglang \
  --isl 5000 \
  --osl 1000 \
  --ttft 1000 \
  --tpot 10
```

### Expected Output

```
=== RBG Deployment Recommender ===

Checking dependencies...
Found aiconfigurator: 0.4.0

Running AI Configurator optimization... This may take a few minutes.
[aiconfigurator output...]
✓ AI Configurator optimization completed successfully

Locating generated configurations...

Parsing AI Configurator output...

Generating RBG deployment YAMLs...

✓ Successfully generated 2 deployment recommendations:

Plan 1: Prefill-Decode Disaggregated Mode
  File: ./rbg-recommender-output/qwen3-32b-sglang-disagg.yaml
  Configuration:
    - Prefill Workers: 4 (each using 1 GPUs)
    - Decode Workers: 1 (each using 4 GPUs)
    - Total GPU Usage: 8
  
Plan 2: Aggregated Mode
  File: ./rbg-recommender-output/qwen3-32b-sglang-agg.yaml
  Configuration:
    - Workers: 1 (each using 4 GPUs)
    - Total GPU Usage: 4

To deploy, run:
  kubectl apply -f ./rbg-recommender-output/qwen3-32b-sglang-disagg.yaml
or
  kubectl apply -f ./rbg-recommender-output/qwen3-32b-sglang-agg.yaml

Note: Ensure the 'llm-model' PVC exists in your cluster before deploying.
```

## Generated YAML Structure

### Disaggregated Mode (PD Split)

```yaml
apiVersion: workloads.x-k8s.io/v1alpha1
kind: RoleBasedGroup
metadata:
  name: qwen3-32b-sglang-pd
spec:
  roles:
  - name: router
    replicas: 1
    # Router configuration for sglang
  - name: prefill
    replicas: 4
    # Prefill worker configuration
  - name: decode
    replicas: 1
    # Decode worker configuration
---
apiVersion: v1
kind: Service
# Service exposing the router
```

### Aggregated Mode

```yaml
apiVersion: workloads.x-k8s.io/v1alpha1
kind: RoleBasedGroup
metadata:
  name: qwen3-32b-sglang-agg
spec:
  roles:
  - name: worker
    replicas: 1
    # Worker configuration
---
apiVersion: v1
kind: Service
# Service exposing the worker
```

## Deployment

1. Create PVC for model storage:
   ```bash
   kubectl create pvc llm-model --size=100Gi
   ```

2. Deploy:
   ```bash
   kubectl apply -f ./rbg-recommender-output/qwen3-32b-sglang-disagg.yaml
   ```

3. Check status:
   ```bash
   kubectl get rbg
   kubectl get pods
   ```

## Troubleshooting

### Command not found: aiconfigurator

Install it:
```bash
pip install aiconfigurator
```

### Invalid parameters

Check supported values:
- Models: QWEN3_32B, LLAMA3.1_70B, etc.
- Systems: h200_sxm, a100_sxm, h100_sxm, etc.
- Backends: sglang, vllm, trtllm

### Debug mode

Enable verbose output:
```bash
./bin/kubectl-rbg recommender --debug --model QWEN3_32B --system h200_sxm --total-gpus 8
```

## More Information

See [README.md](./README.md) for detailed documentation.
