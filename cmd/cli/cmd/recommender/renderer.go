package recommender

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/klog/v2"
)

// RenderDeploymentYAML generates RBG deployment YAML from generator config
func RenderDeploymentYAML(plan *DeploymentPlan) error {
	var yamlContent string
	var err error

	switch plan.Mode {
	case "agg":
		yamlContent, err = renderAggYAML(plan)
	case "disagg":
		yamlContent, err = renderDisaggYAML(plan)
	default:
		return fmt.Errorf("unknown deployment mode: %s", plan.Mode)
	}

	if err != nil {
		return fmt.Errorf("failed to render %s YAML: %w", plan.Mode, err)
	}

	// Write YAML to file
	if err := os.WriteFile(plan.OutputPath, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("failed to write YAML to %s: %w", plan.OutputPath, err)
	}

	klog.V(2).Infof("Successfully generated %s deployment YAML: %s", plan.Mode, plan.OutputPath)
	return nil
}

// renderDisaggYAML generates YAML for Prefill-Decode disaggregated mode
func renderDisaggYAML(plan *DeploymentPlan) (string, error) {
	config := plan.Config
	prefillParams := GetWorkerParams(config.Params.Prefill)
	decodeParams := GetWorkerParams(config.Params.Decode)

	// Get base name for the deployment，要加一个随机时间戳
	baseName := getDeployName(plan.ModelName, plan.BackendName, "pd")
	modelPath := getModelPath(plan.ModelName, plan.HuggingFaceID)
	image := getImage(plan.BackendName) // 格式化

	// Build RoleBasedGroup spec
	rbg := map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha1",
		"kind":       "RoleBasedGroup",
		"metadata": map[string]interface{}{
			"name": baseName,
		},
		"spec": map[string]interface{}{
			"roles": []interface{}{
				buildRouterRole(baseName, image, plan.BackendName),
				buildPrefillRole(baseName, image, modelPath, plan.BackendName, config.Workers.PrefillWorkers, prefillParams),
				buildDecodeRole(baseName, image, modelPath, plan.BackendName, config.Workers.DecodeWorkers, decodeParams),
			},
		},
	}

	// Build Service
	service := buildService(baseName, "router")

	// Combine RBG and Service
	return marshalMultiDocYAML(rbg, service)
}

// renderAggYAML generates YAML for aggregated mode
func renderAggYAML(plan *DeploymentPlan) (string, error) {
	config := plan.Config
	aggParams := GetWorkerParams(config.Params.Agg)

	baseName := getDeployName(plan.ModelName, plan.BackendName, "agg")
	modelPath := getModelPath(plan.ModelName, plan.HuggingFaceID)
	image := getImage(plan.BackendName)

	// Build RoleBasedGroup spec
	rbg := map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha1",
		"kind":       "RoleBasedGroup",
		"metadata": map[string]interface{}{
			"name": baseName,
		},
		"spec": map[string]interface{}{
			"roles": []interface{}{
				buildWorkerRole(baseName, image, modelPath, plan.BackendName, config.Workers.AggWorkers, aggParams),
			},
		},
	}

	// Build Service
	service := buildService(baseName, "worker")

	return marshalMultiDocYAML(rbg, service)
}

// buildRouterRole creates the router role configuration for sglang
func buildRouterRole(baseName, image, backend string) map[string]interface{} {
	if backend != "sglang" {
		// For non-sglang backends, router might not be needed or different
		klog.V(1).Infof("Router role configuration for backend %s not fully implemented", backend)
	}

	return map[string]interface{}{
		"name":     "router",
		"replicas": 1,
		"template": map[string]interface{}{
			"spec": map[string]interface{}{
				"volumes": []interface{}{
					map[string]interface{}{
						"name": "model",
						"persistentVolumeClaim": map[string]interface{}{
							"claimName": "llm-model",
						},
					},
				},
				"containers": []interface{}{
					map[string]interface{}{
						"name":  "scheduler",
						"image": "lmsysorg/sglang-router:v0.2.2",
						"command": []string{
							"sh",
							"-c",
							fmt.Sprintf("python3 -m sglang_router.launch_router --log-level debug --pd-disaggregation "+
								"--host 0.0.0.0 --port 8000 "+
								"--prefill http://%s-prefill-0.s-%s-prefill:8000 34000 "+
								"--decode http://%s-decode-0.s-%s-decode:8000 "+
								"--policy random --prometheus-host 0.0.0.0 --prometheus-port 9090",
								baseName, baseName, baseName, baseName),
						},
						"volumeMounts": []interface{}{
							map[string]interface{}{
								"mountPath": "/models/",
								"name":      "model",
							},
						},
					},
				},
			},
		},
	}
}

// buildPrefillRole creates the prefill role configuration
func buildPrefillRole(baseName, image, modelPath, backend string, replicas int, params WorkerParams) map[string]interface{} {
	shmSize := fmt.Sprintf("%dGi", params.TensorParallelSize*32)

	command := buildPrefillCommand(backend, modelPath, params)

	return map[string]interface{}{
		"name":     "prefill",
		"replicas": replicas,
		"template": map[string]interface{}{
			"spec": map[string]interface{}{
				"volumes": []interface{}{
					map[string]interface{}{
						"name": "model",
						"persistentVolumeClaim": map[string]interface{}{
							"claimName": "llm-model",
						},
					},
					map[string]interface{}{
						"name": "shm",
						"emptyDir": map[string]interface{}{
							"medium":    "Memory",
							"sizeLimit": shmSize,
						},
					},
				},
				"containers": []interface{}{
					map[string]interface{}{
						"name":            fmt.Sprintf("%s-prefill", backend),
						"image":           image,
						"imagePullPolicy": "Always",
						"env": []interface{}{
							map[string]interface{}{
								"name": "POD_IP",
								"valueFrom": map[string]interface{}{
									"fieldRef": map[string]interface{}{
										"fieldPath": "status.podIP",
									},
								},
							},
							map[string]interface{}{
								"name":  "SGLANG_PORT",
								"value": "8000",
							},
						},
						"command": command,
						"ports": []interface{}{
							map[string]interface{}{"containerPort": 8000},
							map[string]interface{}{"containerPort": 34000},
						},
						"readinessProbe": map[string]interface{}{
							"initialDelaySeconds": 30,
							"periodSeconds":       10,
							"tcpSocket": map[string]interface{}{
								"port": 8000,
							},
						},
						"resources": map[string]interface{}{
							"limits": map[string]interface{}{
								"nvidia.com/gpu": fmt.Sprintf("%d", params.TensorParallelSize),
							},
							"requests": map[string]interface{}{
								"nvidia.com/gpu": fmt.Sprintf("%d", params.TensorParallelSize),
							},
						},
						"volumeMounts": []interface{}{
							map[string]interface{}{
								"mountPath": "/models/",
								"name":      "model",
							},
							map[string]interface{}{
								"mountPath": "/dev/shm",
								"name":      "shm",
							},
						},
					},
				},
			},
		},
	}
}

// buildDecodeRole creates the decode role configuration
func buildDecodeRole(baseName, image, modelPath, backend string, replicas int, params WorkerParams) map[string]interface{} {
	shmSize := fmt.Sprintf("%dGi", params.TensorParallelSize*32)

	command := buildDecodeCommand(backend, modelPath, params)

	return map[string]interface{}{
		"name":     "decode",
		"replicas": replicas,
		"template": map[string]interface{}{
			"spec": map[string]interface{}{
				"volumes": []interface{}{
					map[string]interface{}{
						"name": "model",
						"persistentVolumeClaim": map[string]interface{}{
							"claimName": "llm-model",
						},
					},
					map[string]interface{}{
						"name": "shm",
						"emptyDir": map[string]interface{}{
							"medium":    "Memory",
							"sizeLimit": shmSize,
						},
					},
				},
				"containers": []interface{}{
					map[string]interface{}{
						"name":            fmt.Sprintf("%s-decode", backend),
						"image":           image,
						"imagePullPolicy": "Always",
						"env": []interface{}{
							map[string]interface{}{
								"name": "POD_IP",
								"valueFrom": map[string]interface{}{
									"fieldRef": map[string]interface{}{
										"fieldPath": "status.podIP",
									},
								},
							},
							map[string]interface{}{
								"name":  "SGLANG_PORT",
								"value": "8000",
							},
						},
						"command": command,
						"ports": []interface{}{
							map[string]interface{}{"containerPort": 8000},
						},
						"readinessProbe": map[string]interface{}{
							"initialDelaySeconds": 30,
							"periodSeconds":       10,
							"tcpSocket": map[string]interface{}{
								"port": 8000,
							},
						},
						"resources": map[string]interface{}{
							"limits": map[string]interface{}{
								"nvidia.com/gpu": fmt.Sprintf("%d", params.TensorParallelSize),
							},
							"requests": map[string]interface{}{
								"nvidia.com/gpu": fmt.Sprintf("%d", params.TensorParallelSize),
							},
						},
						"volumeMounts": []interface{}{
							map[string]interface{}{
								"mountPath": "/models/",
								"name":      "model",
							},
							map[string]interface{}{
								"mountPath": "/dev/shm",
								"name":      "shm",
							},
						},
					},
				},
			},
		},
	}
}

// buildWorkerRole creates the worker role for aggregated mode
func buildWorkerRole(baseName, image, modelPath, backend string, replicas int, params WorkerParams) map[string]interface{} {
	shmSize := fmt.Sprintf("%dGi", params.TensorParallelSize*32)

	command := buildAggCommand(backend, modelPath, params)

	return map[string]interface{}{
		"name":     "worker",
		"replicas": replicas,
		"template": map[string]interface{}{
			"spec": map[string]interface{}{
				"volumes": []interface{}{
					map[string]interface{}{
						"name": "model",
						"persistentVolumeClaim": map[string]interface{}{
							"claimName": "llm-model",
						},
					},
					map[string]interface{}{
						"name": "shm",
						"emptyDir": map[string]interface{}{
							"medium":    "Memory",
							"sizeLimit": shmSize,
						},
					},
				},
				"containers": []interface{}{
					map[string]interface{}{
						"name":            fmt.Sprintf("%s-worker", backend),
						"image":           image,
						"imagePullPolicy": "Always",
						"env": []interface{}{
							map[string]interface{}{
								"name": "POD_IP",
								"valueFrom": map[string]interface{}{
									"fieldRef": map[string]interface{}{
										"fieldPath": "status.podIP",
									},
								},
							},
						},
						"command": command,
						"ports": []interface{}{
							map[string]interface{}{"containerPort": 8000},
						},
						"readinessProbe": map[string]interface{}{
							"initialDelaySeconds": 30,
							"periodSeconds":       10,
							"tcpSocket": map[string]interface{}{
								"port": 8000,
							},
						},
						"resources": map[string]interface{}{
							"limits": map[string]interface{}{
								"nvidia.com/gpu": fmt.Sprintf("%d", params.TensorParallelSize),
							},
							"requests": map[string]interface{}{
								"nvidia.com/gpu": fmt.Sprintf("%d", params.TensorParallelSize),
							},
						},
						"volumeMounts": []interface{}{
							map[string]interface{}{
								"mountPath": "/models/",
								"name":      "model",
							},
							map[string]interface{}{
								"mountPath": "/dev/shm",
								"name":      "shm",
							},
						},
					},
				},
			},
		},
	}
}

// buildService creates a Kubernetes Service resource
func buildService(baseName, targetRole string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"app": baseName,
			},
			"name":      baseName,
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"ports": []interface{}{
				map[string]interface{}{
					"name":       "http",
					"port":       8000,
					"protocol":   "TCP",
					"targetPort": 8000,
				},
			},
			"selector": map[string]interface{}{
				"rolebasedgroup.workloads.x-k8s.io/name": baseName,
				"rolebasedgroup.workloads.x-k8s.io/role": targetRole,
			},
			"type": "ClusterIP",
		},
	}
}

// buildPrefillCommand constructs the prefill worker command
func buildPrefillCommand(backend, modelPath string, params WorkerParams) []string {
	if backend == "sglang" {
		cmd := fmt.Sprintf(
			"python3 -m sglang.launch_server --model-path %s --enable-metrics "+
				"--disaggregation-mode prefill --port 8000 --disaggregation-bootstrap-port 34000 "+
				"--host 0.0.0.0 --tp-size %d",
			modelPath, params.TensorParallelSize,
		)
		return []string{"sh", "-c", cmd}
	}
	// Add support for other backends as needed
	return []string{"sh", "-c", fmt.Sprintf("echo 'Backend %s not yet supported'", backend)}
}

// buildDecodeCommand constructs the decode worker command
func buildDecodeCommand(backend, modelPath string, params WorkerParams) []string {
	if backend == "sglang" {
		cmd := fmt.Sprintf(
			"python3 -m sglang.launch_server --model-path %s --enable-metrics "+
				"--disaggregation-mode decode --port 8000 --host 0.0.0.0 "+
				"--mem-fraction-static %.2f --tp-size %d",
			modelPath, params.KVCacheFreeGPUMemoryFraction, params.TensorParallelSize,
		)
		return []string{"sh", "-c", cmd}
	}
	return []string{"sh", "-c", fmt.Sprintf("echo 'Backend %s not yet supported'", backend)}
}

// buildAggCommand constructs the aggregated mode worker command
func buildAggCommand(backend, modelPath string, params WorkerParams) []string {
	if backend == "sglang" {
		cmd := fmt.Sprintf(
			"python3 -m sglang.launch_server --model-path %s --enable-metrics "+
				"--port 8000 --host 0.0.0.0 --tp-size %d",
			modelPath, params.TensorParallelSize,
		)
		if params.KVCacheFreeGPUMemoryFraction > 0 {
			cmd += fmt.Sprintf(" --mem-fraction-static %.2f", params.KVCacheFreeGPUMemoryFraction)
		}
		return []string{"sh", "-c", cmd}
	}
	return []string{"sh", "-c", fmt.Sprintf("echo 'Backend %s not yet supported'", backend)}
}

// getDeployName generates a deploy name with a random suffix to avoid conflicts
// The suffix is a 5-character lowercase hex string that complies with DNS naming rules
func getDeployName(modelName, backend, suffix string) string {
	// Convert model name to lowercase and replace underscores
	name := strings.ToLower(strings.ReplaceAll(modelName, "_", "-"))
	// Generate a random 5-character suffix (DNS-safe: lowercase letters and numbers)
	randomSuffix := generateRandomSuffix(5)
	return fmt.Sprintf("%s-%s-%s-%s", name, backend, suffix, randomSuffix)
}

// generateRandomSuffix generates a random lowercase hex string of specified length
// Uses timestamp as seed to ensure uniqueness across different runs
func generateRandomSuffix(length int) string {
	// Use current timestamp (nanoseconds) as seed for randomness
	source := rand.NewSource(time.Now().UnixNano())
	rng := rand.New(source)

	// Calculate how many random bytes we need (2 hex chars per byte)
	bytes := make([]byte, (length+1)/2)
	for i := range bytes {
		bytes[i] = byte(rng.Intn(256))
	}

	hexString := hex.EncodeToString(bytes)
	return hexString[:length]
}

// getModelPath determines the model path based on HuggingFace ID or model name
func getModelPath(modelName, hfID string) string {
	if hfID != "" {
		// Use HuggingFace ID if provided
		parts := strings.Split(hfID, "/")
		if len(parts) > 0 {
			return fmt.Sprintf("/models/%s/", parts[len(parts)-1])
		}
	}
	// Fallback to model name
	return fmt.Sprintf("/models/%s/", modelName)
}

// getImage selects the appropriate container image
func getImage(backend string) string {
	// Default images per backend
	switch backend {
	case "sglang":
		return "lmsysorg/sglang:latest"
	case "vllm":
		return "vllm/vllm-openai:latest"
	case "trtllm":
		return "nvcr.io/nvidia/ai-dynamo/tensorrtllm-runtime:latest"
	default:
		return "lmsysorg/sglang:latest"
	}
}

// marshalMultiDocYAML marshals multiple documents into a YAML string
func marshalMultiDocYAML(docs ...interface{}) (string, error) {
	var result strings.Builder

	for i, doc := range docs {
		if i > 0 {
			result.WriteString("---\n")
		}

		data, err := yaml.Marshal(doc)
		if err != nil {
			return "", fmt.Errorf("failed to marshal document %d: %w", i, err)
		}
		result.Write(data)
	}

	return result.String(), nil
}
