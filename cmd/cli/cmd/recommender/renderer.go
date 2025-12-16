package recommender

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/klog/v2"
	applyconfiguration "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha1"
	"sigs.k8s.io/rbgs/pkg/utils"
)

// RenderDeploymentYAML generates RBG deployment YAML from generator config
func RenderDeploymentYAML(plan *DeploymentPlan) error {
	var yamlContent string
	var err error

	switch plan.Mode {
	case "disagg":
		yamlContent, err = renderDisaggYAML(plan)
	case "agg":
		yamlContent, err = renderAggYAML(plan)
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

	// Get base name for the deployment
	baseName := getDeployName(plan.ModelName, plan.BackendName, "pd")
	modelPath := getModelPath(plan.ModelName, plan.HuggingFaceID)
	image := getImage(plan.BackendName)

	// Build RoleBasedGroup using builder pattern
	gkv := utils.GetRbgGVK()
	rbg := applyconfiguration.RoleBasedGroup(baseName, "default").
		WithKind(gkv.Kind).
		WithAPIVersion(gkv.GroupVersion().String()).
		WithSpec(applyconfiguration.RoleBasedGroupSpec().
			WithRoles(
				buildRouterRoleSpec(baseName, image, modelPath, plan.BackendName, plan),
				buildPrefillRoleSpec(image, modelPath, plan.BackendName, config.Workers.PrefillWorkers, prefillParams, plan),
				buildDecodeRoleSpec(image, modelPath, plan.BackendName, config.Workers.DecodeWorkers, decodeParams, plan),
			))

	// Build Service
	service := buildServiceSpec(baseName, "router")

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

	// Build RoleBasedGroup using builder pattern
	gkv := utils.GetRbgGVK()
	rbg := applyconfiguration.RoleBasedGroup(baseName, "default").
		WithKind(gkv.Kind).
		WithAPIVersion(gkv.GroupVersion().String()).
		WithSpec(applyconfiguration.RoleBasedGroupSpec().
			WithRoles(
				buildWorkerRoleSpec(image, modelPath, plan.BackendName, config.Workers.AggWorkers, aggParams, plan),
			))

	// Build Service
	service := buildServiceSpec(baseName, "worker")

	return marshalMultiDocYAML(rbg, service)
}

// buildRouterRoleSpec creates the router role spec using builder pattern
func buildRouterRoleSpec(baseName, image, modelPath, backend string, plan *DeploymentPlan) *applyconfiguration.RoleSpecApplyConfiguration {
	if backend != "sglang" {
		klog.Fatalf("Router role configuration for backend %s not implemented", backend)
	}

	// Build command with dynamic prefill and decode endpoints
	command := []string{
		"python3",
		"-m",
		"sglang_router.launch_router",
		"--pd-disaggregation",
	}

	// Add all prefill worker endpoints
	prefillReplicas := plan.Config.Workers.PrefillWorkers
	command = append(command, "--prefill")
	for i := 0; i < prefillReplicas; i++ {
		command = append(command, fmt.Sprintf("http://%s-prefill-%d.s-%s-prefill:8000", baseName, i, baseName))
	}

	// Add all decode worker endpoints
	command = append(command, "--decode")
	decodeReplicas := plan.Config.Workers.DecodeWorkers
	for i := 0; i < decodeReplicas; i++ {
		command = append(command, fmt.Sprintf("http://%s-decode-%d.s-%s-decode:8000", baseName, i, baseName))
	}

	// Add common parameters
	command = append(command,
		"--host",
		"0.0.0.0",
		"--port",
		"8000",
	)

	podTemplate := applycorev1.PodTemplateSpec().
		WithSpec(applycorev1.PodSpec().
			WithVolumes(
				applycorev1.Volume().
					WithName("model").
					WithPersistentVolumeClaim(applycorev1.PersistentVolumeClaimVolumeSource().
						WithClaimName(normalizeModelName(plan.ModelName))),
			).
			WithContainers(
				applycorev1.Container().
					WithName("schedule").
					WithImage(image).
					WithCommand(command...).
					WithVolumeMounts(
						applycorev1.VolumeMount().
							WithName("model").
							WithMountPath(modelPath),
					),
			))

	return applyconfiguration.RoleSpec().
		WithName("router").
		WithReplicas(1).
		WithTemplate(podTemplate)
}

// buildPrefillRoleSpec creates the prefill role spec using builder pattern
func buildPrefillRoleSpec(image, modelPath, backend string, replicas int, params WorkerParams, plan *DeploymentPlan) *applyconfiguration.RoleSpecApplyConfiguration {
	shmSize := resource.MustParse("30Gi")
	gpuQuantity := resource.MustParse(fmt.Sprintf("%d", params.TensorParallelSize))
	command := buildPrefillCommand(backend, modelPath, params)

	podTemplate := applycorev1.PodTemplateSpec().
		WithSpec(applycorev1.PodSpec().
			WithVolumes(
				applycorev1.Volume().
					WithName("model").
					WithPersistentVolumeClaim(applycorev1.PersistentVolumeClaimVolumeSource().
						WithClaimName(normalizeModelName(plan.ModelName))),
				applycorev1.Volume().
					WithName("shm").
					WithEmptyDir(applycorev1.EmptyDirVolumeSource().
						WithMedium(corev1.StorageMediumMemory).
						WithSizeLimit(shmSize)),
			).
			WithContainers(
				applycorev1.Container().
					WithName(fmt.Sprintf("%s-prefill", backend)).
					WithImage(image).
					WithImagePullPolicy(corev1.PullAlways).
					WithEnv(
						applycorev1.EnvVar().
							WithName("POD_IP").
							WithValueFrom(applycorev1.EnvVarSource().
								WithFieldRef(applycorev1.ObjectFieldSelector().
									WithFieldPath("status.podIP"))),
					).
					WithCommand(command...).
					WithPorts(
						applycorev1.ContainerPort().WithContainerPort(8000).WithName("http"),
					).
					WithReadinessProbe(applycorev1.Probe().
						WithInitialDelaySeconds(30).
						WithPeriodSeconds(10).
						WithTCPSocket(applycorev1.TCPSocketAction().
							WithPort(intstr.FromInt(8000)))).
					WithResources(applycorev1.ResourceRequirements().
						WithLimits(corev1.ResourceList{
							"nvidia.com/gpu": gpuQuantity,
						}).
						WithRequests(corev1.ResourceList{
							"nvidia.com/gpu": gpuQuantity,
						})).
					WithVolumeMounts(
						applycorev1.VolumeMount().WithName("model").WithMountPath(modelPath),
						applycorev1.VolumeMount().WithName("shm").WithMountPath("/dev/shm"),
					),
			))

	return applyconfiguration.RoleSpec().
		WithName("prefill").
		WithReplicas(int32(replicas)).
		WithTemplate(podTemplate)
}

// buildDecodeRoleSpec creates the decode role spec using builder pattern
func buildDecodeRoleSpec(image, modelPath, backend string, replicas int, params WorkerParams, plan *DeploymentPlan) *applyconfiguration.RoleSpecApplyConfiguration {
	shmSize := resource.MustParse("30Gi")
	gpuQuantity := resource.MustParse(fmt.Sprintf("%d", params.TensorParallelSize))
	command := buildDecodeCommand(backend, modelPath, params)

	podTemplate := applycorev1.PodTemplateSpec().
		WithSpec(applycorev1.PodSpec().
			WithVolumes(
				applycorev1.Volume().
					WithName("model").
					WithPersistentVolumeClaim(applycorev1.PersistentVolumeClaimVolumeSource().
						WithClaimName(normalizeModelName(plan.ModelName))),
				applycorev1.Volume().
					WithName("shm").
					WithEmptyDir(applycorev1.EmptyDirVolumeSource().
						WithMedium(corev1.StorageMediumMemory).
						WithSizeLimit(shmSize)),
			).
			WithContainers(
				applycorev1.Container().
					WithName(fmt.Sprintf("%s-decode", backend)).
					WithImage(image).
					WithImagePullPolicy(corev1.PullAlways).
					WithEnv(
						applycorev1.EnvVar().
							WithName("POD_IP").
							WithValueFrom(applycorev1.EnvVarSource().
								WithFieldRef(applycorev1.ObjectFieldSelector().
									WithFieldPath("status.podIP"))),
					).
					WithCommand(command...).
					WithPorts(
						applycorev1.ContainerPort().WithContainerPort(8000).WithName("http"),
					).
					WithReadinessProbe(applycorev1.Probe().
						WithInitialDelaySeconds(30).
						WithPeriodSeconds(10).
						WithTCPSocket(applycorev1.TCPSocketAction().
							WithPort(intstr.FromInt(8000)))).
					WithResources(applycorev1.ResourceRequirements().
						WithLimits(corev1.ResourceList{
							"nvidia.com/gpu": gpuQuantity,
						}).
						WithRequests(corev1.ResourceList{
							"nvidia.com/gpu": gpuQuantity,
						})).
					WithVolumeMounts(
						applycorev1.VolumeMount().WithName("model").WithMountPath(modelPath),
						applycorev1.VolumeMount().WithName("shm").WithMountPath("/dev/shm"),
					),
			))

	return applyconfiguration.RoleSpec().
		WithName("decode").
		WithReplicas(int32(replicas)).
		WithTemplate(podTemplate)
}

// buildWorkerRoleSpec creates the worker role spec for aggregated mode using builder pattern
func buildWorkerRoleSpec(image, modelPath, backend string, replicas int, params WorkerParams, plan *DeploymentPlan) *applyconfiguration.RoleSpecApplyConfiguration {
	gpuQuantity := resource.MustParse(fmt.Sprintf("%d", params.TensorParallelSize))
	command := buildAggCommand(backend, modelPath, params)

	podTemplate := applycorev1.PodTemplateSpec().
		WithSpec(applycorev1.PodSpec().
			WithVolumes(
				applycorev1.Volume().
					WithName("model").
					WithPersistentVolumeClaim(applycorev1.PersistentVolumeClaimVolumeSource().
						WithClaimName(normalizeModelName(plan.ModelName))),
				applycorev1.Volume().
					WithName("shm").
					WithEmptyDir(applycorev1.EmptyDirVolumeSource().
						WithMedium(corev1.StorageMediumMemory)),
			).
			WithContainers(
				applycorev1.Container().
					WithName(fmt.Sprintf("%s-worker", backend)).
					WithImage(image).
					WithEnv(
						applycorev1.EnvVar().
							WithName("POD_IP").
							WithValueFrom(applycorev1.EnvVarSource().
								WithFieldRef(applycorev1.ObjectFieldSelector().
									WithFieldPath("status.podIP"))),
					).
					WithCommand(command...).
					WithPorts(
						applycorev1.ContainerPort().WithContainerPort(8000).WithName("http"),
					).
					WithReadinessProbe(applycorev1.Probe().
						WithInitialDelaySeconds(30).
						WithPeriodSeconds(10).
						WithTCPSocket(applycorev1.TCPSocketAction().
							WithPort(intstr.FromInt(8000)))).
					WithResources(applycorev1.ResourceRequirements().
						WithLimits(corev1.ResourceList{
							"nvidia.com/gpu": gpuQuantity,
						}).
						WithRequests(corev1.ResourceList{
							"nvidia.com/gpu": gpuQuantity,
						})).
					WithVolumeMounts(
						applycorev1.VolumeMount().WithName("model").WithMountPath(modelPath),
						applycorev1.VolumeMount().WithName("shm").WithMountPath("/dev/shm"),
					),
			))

	return applyconfiguration.RoleSpec().
		WithName("worker").
		WithReplicas(int32(replicas)).
		WithTemplate(podTemplate)
}

// buildServiceSpec creates a Kubernetes Service resource using builder pattern
func buildServiceSpec(baseName, targetRole string) *applycorev1.ServiceApplyConfiguration {
	return applycorev1.Service(baseName, "default").
		WithAPIVersion("v1").
		WithKind("Service").
		WithLabels(map[string]string{
			"app": baseName,
		}).
		WithSpec(applycorev1.ServiceSpec().
			WithPorts(
				applycorev1.ServicePort().
					WithName("http").
					WithPort(8000).
					WithProtocol(corev1.ProtocolTCP).
					WithTargetPort(intstr.FromInt(8000)),
			).
			WithSelector(map[string]string{
				"rolebasedgroup.workloads.x-k8s.io/name": baseName,
				"rolebasedgroup.workloads.x-k8s.io/role": targetRole,
			}).
			WithType(corev1.ServiceTypeClusterIP))
}

// buildPrefillCommand constructs the prefill worker command
func buildPrefillCommand(backend, modelPath string, params WorkerParams) []string {
	if backend == "sglang" {
		// Build command arguments for prefill mode
		args := []string{
			"-m",
			"sglang.launch_server",
			"--model-path",
			modelPath,
			"--enable-metrics",
			"--disaggregation-mode",
			"prefill",
			"--port",
			"8000",
			"--disaggregation-bootstrap-port",
			"34000",
			"--host",
			"$(POD_IP)",
		}

		// Add tensor-parallel-size
		if params.TensorParallelSize > 0 {
			args = append(args, "--tensor-parallel-size", fmt.Sprintf("%d", params.TensorParallelSize))
		}

		// Add pipeline-parallel-size
		if params.PipelineParallelSize > 0 {
			args = append(args, "--pipeline-parallel-size", fmt.Sprintf("%d", params.PipelineParallelSize))
		}

		// Add data-parallel-size
		if params.DataParallelSize > 0 {
			args = append(args, "--data-parallel-size", fmt.Sprintf("%d", params.DataParallelSize))
		}

		// Add kv-cache-dtype
		if params.KVCacheDtype != "" {
			args = append(args, "--kv-cache-dtype", params.KVCacheDtype)
		}

		// Add max-running-requests (mapped from MaxBatchSize)
		if params.MaxBatchSize > 0 {
			args = append(args, "--max-running-requests", fmt.Sprintf("%d", params.MaxBatchSize))
		}

		// Add expert-parallel-size
		if params.MoEExpertParallelSize > 0 {
			args = append(args, "--expert-parallel-size", fmt.Sprintf("%d", params.MoEExpertParallelSize))
		}

		// Add moe-dense-tp-size
		if params.MoETensorParallelSize > 0 {
			args = append(args, "--moe-dense-tp-size", fmt.Sprintf("%d", params.MoETensorParallelSize))
		}

		return append([]string{"python3"}, args...)
	}
	// Add support for other backends as needed
	return []string{"echo", fmt.Sprintf("Backend %s not yet supported", backend)}
}

// buildDecodeCommand constructs the decode worker command
func buildDecodeCommand(backend, modelPath string, params WorkerParams) []string {
	if backend == "sglang" {
		// Build command arguments for decode mode
		args := []string{
			"-m",
			"sglang.launch_server",
			"--model-path",
			modelPath,
			"--enable-metrics",
			"--disaggregation-mode",
			"decode",
			"--port",
			"8000",
			"--host",
			"$(POD_IP)",
		}

		// Add tensor-parallel-size
		if params.TensorParallelSize > 0 {
			args = append(args, "--tensor-parallel-size", fmt.Sprintf("%d", params.TensorParallelSize))
		}

		// Add pipeline-parallel-size
		if params.PipelineParallelSize > 0 {
			args = append(args, "--pipeline-parallel-size", fmt.Sprintf("%d", params.PipelineParallelSize))
		}

		// Add data-parallel-size
		if params.DataParallelSize > 0 {
			args = append(args, "--data-parallel-size", fmt.Sprintf("%d", params.DataParallelSize))
		}

		// Add kv-cache-dtype
		if params.KVCacheDtype != "" {
			args = append(args, "--kv-cache-dtype", params.KVCacheDtype)
		}

		// Add max-running-requests (mapped from MaxBatchSize)
		if params.MaxBatchSize > 0 {
			args = append(args, "--max-running-requests", fmt.Sprintf("%d", params.MaxBatchSize))
		}

		// Add expert-parallel-size
		if params.MoEExpertParallelSize > 0 {
			args = append(args, "--expert-parallel-size", fmt.Sprintf("%d", params.MoEExpertParallelSize))
		}

		// Add moe-dense-tp-size
		if params.MoETensorParallelSize > 0 {
			args = append(args, "--moe-dense-tp-size", fmt.Sprintf("%d", params.MoETensorParallelSize))
		}

		// Add mem-fraction-static (for KV cache)
		if params.KVCacheFreeGPUMemoryFraction > 0 {
			args = append(args, "--mem-fraction-static", fmt.Sprintf("%.2f", params.KVCacheFreeGPUMemoryFraction))
		}

		return append([]string{"python3"}, args...)
	}
	return []string{"echo", fmt.Sprintf("Backend %s not yet supported", backend)}
}

// buildAggCommand constructs the aggregated mode worker command
func buildAggCommand(backend, modelPath string, params WorkerParams) []string {
	if backend == "sglang" {
		// Build command arguments for aggregated mode
		args := []string{
			"-m",
			"sglang.launch_server",
			"--model-path",
			modelPath,
			"--enable-metrics",
			"--port",
			"8000",
			"--host",
			"$(POD_IP)",
		}

		// Add tensor-parallel-size
		if params.TensorParallelSize > 0 {
			args = append(args, "--tensor-parallel-size", fmt.Sprintf("%d", params.TensorParallelSize))
		}

		// Add pipeline-parallel-size
		if params.PipelineParallelSize > 0 {
			args = append(args, "--pipeline-parallel-size", fmt.Sprintf("%d", params.PipelineParallelSize))
		}

		// Add data-parallel-size
		if params.DataParallelSize > 0 {
			args = append(args, "--data-parallel-size", fmt.Sprintf("%d", params.DataParallelSize))
		}

		// Add kv-cache-dtype
		if params.KVCacheDtype != "" {
			args = append(args, "--kv-cache-dtype", params.KVCacheDtype)
		}

		// Add max-running-requests (mapped from MaxBatchSize)
		if params.MaxBatchSize > 0 {
			args = append(args, "--max-running-requests", fmt.Sprintf("%d", params.MaxBatchSize))
		}

		// Add expert-parallel-size
		if params.MoEExpertParallelSize > 0 {
			args = append(args, "--expert-parallel-size", fmt.Sprintf("%d", params.MoEExpertParallelSize))
		}

		// Add moe-dense-tp-size (mapped from MoETensorParallelSize)
		if params.MoETensorParallelSize > 0 {
			args = append(args, "--moe-dense-tp-size", fmt.Sprintf("%d", params.MoETensorParallelSize))
		}

		// Add mem-fraction-static (for KV cache)
		if params.KVCacheFreeGPUMemoryFraction > 0 {
			args = append(args, "--mem-fraction-static", fmt.Sprintf("%.2f", params.KVCacheFreeGPUMemoryFraction))
		}

		return append([]string{"python3"}, args...)
	}
	return []string{"echo", fmt.Sprintf("Backend %s not yet supported", backend)}
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
// Handles both regular Kubernetes objects and ApplyConfiguration objects
func marshalMultiDocYAML(docs ...interface{}) (string, error) {
	var result strings.Builder

	for i, doc := range docs {
		if i > 0 {
			result.WriteString("---\n")
		}

		// Convert ApplyConfiguration to unstructured format
		unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(doc)
		if err != nil {
			return "", fmt.Errorf("failed to convert document %d to unstructured: %w", i, err)
		}

		// Use yaml encoder with custom indentation for more compact output
		var buf strings.Builder
		encoder := yaml.NewEncoder(&buf)
		encoder.SetIndent(2) // 2 spaces indentation for compact format

		if err := encoder.Encode(unstructuredObj); err != nil {
			return "", fmt.Errorf("failed to marshal document %d: %w", i, err)
		}
		encoder.Close()

		result.WriteString(buf.String())
	}

	return result.String(), nil
}
