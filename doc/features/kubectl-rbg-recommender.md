# RBG Recommender Command

## Overview

The `kubectl-rbg recommender` command integrates with [AI Configurator](https://github.com/ai-dynamo/aiconfigurator) to automatically generate optimized RoleBasedGroup deployment configurations for AI model serving.

## Prerequisites

1. **Install aiconfigurator**:
   ```bash
   pip install aiconfigurator
   ```

2. **Verify installation**:
   ```bash
   aiconfigurator version

   # version >= 0.5.0
   ```
3. **参考 [kubectl-rbg](doc/features/kubectl-rbg.md) 在本地安装kubectl-rbg插件:

```shell
# Download source code
$ git clone https://github.com/sgl-project/rbg.git
# Build locally
$ make build-cli
# Install
$ chmod +x bin/kubectl-rbg
$ sudo mv bin/kubectl-rbg /usr/local/bin/
```

## Usage

### Basic Command

```bash
kubectl rbg recommender \
  --model QWEN3_32B \
  --system h200_sxm \
  --total-gpus 8 \
  --backend sglang \
  --isl 5000 \
  --osl 1000 \
  --ttft 1000 \
  --tpot 10
```

### Command Flags

#### Required Flags

- `--model`: Model name (e.g., QWEN3_32B, LLAMA3.1_70B)
- `--system`: GPU system type (h100_sxm, a100_sxm, b200_sxm, gb200_sxm, l40s, h200_sxm)
- `--total-gpus`: Total number of GPUs to use for deployment

#### Optional Flags

- `--backend`: Inference backend (current only supported: "sglang")
- `--hf-id`: HuggingFace model ID (e.g., "Qwen/Qwen2.5-7B")
- `--decode-system`: GPU system for decode workers (defaults to --system)
- `--isl`: Input sequence length (default: 4000)
- `--osl`: Output sequence length (default: 1000)
- `--prefix`: Prefix cache length (default: 0)
- `--ttft`: Time to first token in ms (default: 1000)
- `--tpot`: Time per output token in ms (default: 50)
- `--request-latency`: End-to-end request latency target in ms
- `--database-mode`: Performance database mode (default: "SILICON")
  - Options: SILICON, HYBRID, EMPIRICAL, SOL
- `--save-dir`: Directory to save results (default: "./rbg-recommender-output")


## Workflow

The command executes the following steps:

1. **Validation**: Validates all input parameters
2. **Dependency Check**: Verifies aiconfigurator is installed
3. **Optimization**: Runs AI Configurator to generate optimal configurations
4. **Parsing**: Locates and parses the generated configuration files
5. **Rendering**: Generates two RBG deployment YAML files:
   - Prefill-Decode disaggregated mode
   - Aggregated mode
6. **Output**: Displays deployment recommendations and file paths

## Output

The command generates two YAML files in the specified `--save-dir`:

1. **`{model}-{backend}-disagg.yaml`**: Prefill-Decode disaggregated deployment
   - Separate prefill and decode workers
   - Includes router component
   - Optimized for high throughput

2. **`{model}-{backend}-agg.yaml`**: Aggregated deployment
   - Single worker role
   - Simpler architecture
   - Optimized for lower latency

## Example Output

```
=== RBG Deployment Recommender ===

Checking dependencies...
Found aiconfigurator: 0.4.0

Running AI Configurator optimization... This may take a few minutes.
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

## Deployment

Before deploying the generated YAML:

1. **Create model PVC**:
   ```bash
   kubectl apply -f - <<EOF
   apiVersion: v1
   kind: PersistentVolumeClaim
   metadata:
     name: llm-model
   spec:
     accessModes:
       - ReadOnlyMany
     resources:
       requests:
         storage: 100Gi
     storageClassName: your-storage-class
   EOF
   ```

2. **Deploy the recommended configuration**:
   ```bash
   kubectl apply -f ./rbg-recommender-output/qwen3-32b-sglang-disagg.yaml
   ```

3. **Monitor deployment**:
   ```bash
   kubectl get rbg
   kubectl get pods
   ```


## Troubleshooting

### aiconfigurator not found

```
Error: aiconfigurator is not installed

Please install it using one of the following methods:
  pip install aiconfigurator
Or visit: https://github.com/ai-dynamo/aiconfigurator
```

**Solution**: Install aiconfigurator using pip.

### No output directory found

```
Error: no output directory found matching pattern: QWEN3_32B_isl5000_osl1000_ttft1000_tpot10_*
```

**Solution**: 
- Check if aiconfigurator executed successfully
- Verify the `--save-dir` path is correct
- Enable `--debug` mode for detailed logs

### Invalid system type

```
Error: invalid system invalid_system, must be one of: h100_sxm, a100_sxm, b200_sxm, gb200_sxm, l40s, h200_sxm
```

**Solution**: Use one of the supported GPU system types.

## Architecture

The recommender command consists of several modules:

- **types.go**: Data structures for configuration and parameters
- **dependency.go**: Checks for aiconfigurator availability
- **executor.go**: Builds and executes aiconfigurator commands
- **parser.go**: Locates and parses generator configurations
- **renderer.go**: Renders RBG YAML templates
- **recommender.go**: Main command orchestration

