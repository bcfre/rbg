/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package recommender

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"
)

// NewRecommenderCmd creates the recommender command
func NewRecommenderCmd() *cobra.Command {
	config := &TaskConfig{
		// Set defaults
		BackendName:  "sglang",
		ISL:          4000,
		OSL:          1000,
		Prefix:       0,
		TTFT:         1000,
		TPOT:         50,
		DatabaseMode: "SILICON",
		SaveDir:      "./rbg-recommender-output",
		ExtraArgs:    make(map[string]string),
	}

	cmd := &cobra.Command{
		Use:   "recommender",
		Short: "Generate optimized RBG deployment configurations using AI Configurator",
		Long: `The recommender command integrates with AI Configurator to generate optimized
deployment configurations for AI model serving. It supports both Prefill-Decode
disaggregated mode and aggregated mode deployments.

Example:
  rbgctl recommender --model QWEN3_32B --system h200_sxm --total-gpus 8 \
    --backend sglang --isl 5000 --osl 1000 --ttft 1000 --tpot 10

This will:
  1. Check if aiconfigurator is installed
  2. Run AI Configurator optimization
  3. Parse the generated configurations
  4. Generate RBG-compatible YAML files for both deployment modes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecommender(config)
		},
	}

	// Core required parameters
	cmd.Flags().StringVar(&config.ModelName, "model", "", "Model name (required)")
	cmd.Flags().StringVar(&config.SystemName, "system", "", "GPU system type (required)")
	cmd.Flags().IntVar(&config.TotalGPUs, "total-gpus", 0, "Total number of GPUs for deployment (required)")

	// Core optional parameters
	cmd.Flags().StringVar(&config.HuggingFaceID, "hf-id", "", "HuggingFace model ID (e.g., Qwen/Qwen2.5-7B)")
	cmd.Flags().StringVar(&config.DecodeSystemName, "decode-system", "", "GPU system for decode workers (defaults to --system)")
	cmd.Flags().StringVar(&config.BackendName, "backend", "sglang", "Inference backend (sglang, vllm, trtllm)")
	cmd.Flags().StringVar(&config.BackendVersion, "backend-version", "", "Backend version")
	cmd.Flags().IntVar(&config.ISL, "isl", 4000, "Input sequence length")
	cmd.Flags().IntVar(&config.OSL, "osl", 1000, "Output sequence length")
	cmd.Flags().IntVar(&config.Prefix, "prefix", 0, "Prefix cache length")
	cmd.Flags().Float64Var(&config.TTFT, "ttft", 1000, "Time to first token in milliseconds")
	cmd.Flags().Float64Var(&config.TPOT, "tpot", 50, "Time per output token in milliseconds")
	cmd.Flags().Float64Var(&config.RequestLatency, "request-latency", 0, "End-to-end request latency target in milliseconds")
	cmd.Flags().StringVar(&config.DatabaseMode, "database-mode", "SILICON", "Database mode (SILICON, HYBRID, EMPIRICAL, SOL)")
	cmd.Flags().StringVar(&config.SaveDir, "save-dir", "./rbg-recommender-output", "Directory to save results")
	cmd.Flags().BoolVar(&config.Debug, "debug", false, "Enable debug mode")

	// Mark required flags
	cmd.MarkFlagRequired("model")
	cmd.MarkFlagRequired("system")
	cmd.MarkFlagRequired("total-gpus")

	return cmd
}

// runRecommender executes the main recommender workflow
func runRecommender(config *TaskConfig) error {
	fmt.Println("=== RBG Deployment Recommender ===")

	// Step 1: Validate configuration
	if err := validateConfig(config); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	// Step 2: Check aiconfigurator availability
	fmt.Println("Checking dependencies...")
	if err := CheckAIConfiguratorAvailability(); err != nil {
		return err
	}
	fmt.Println()

	// Step 3: Execute aiconfigurator
	if err := ExecuteAIConfigurator(config); err != nil {
		return err
	}
	fmt.Println()

	// Step 4: Locate output directory
	fmt.Println("Locating generated configurations...")
	outputDir, err := LocateOutputDirectory(config)
	if err != nil {
		return err
	}
	klog.V(1).Infof("Using output directory: %s", outputDir)
	fmt.Println()

	// Step 5: Parse generator configurations
	fmt.Println("Parsing AI Configurator output...")
	aggConfig, disaggConfig, err := ParseGeneratorConfigs(outputDir)
	if err != nil {
		return err
	}
	fmt.Println()

	// Step 6: Generate RBG YAML files
	fmt.Println("Generating RBG deployment YAMLs...")

	// Create deployment plans
	disaggPlan := &DeploymentPlan{
		Mode:          "disagg",
		Config:        disaggConfig,
		OutputPath:    filepath.Join(config.SaveDir, fmt.Sprintf("%s-%s-disagg.yaml", normalizeModelName(config.ModelName), config.BackendName)),
		ModelName:     config.ModelName,
		BackendName:   config.BackendName,
		HuggingFaceID: config.HuggingFaceID,
	}

	aggPlan := &DeploymentPlan{
		Mode:          "agg",
		Config:        aggConfig,
		OutputPath:    filepath.Join(config.SaveDir, fmt.Sprintf("%s-%s-agg.yaml", normalizeModelName(config.ModelName), config.BackendName)),
		ModelName:     config.ModelName,
		BackendName:   config.BackendName,
		HuggingFaceID: config.HuggingFaceID,
	}

	// Render YAML files
	if err := RenderDeploymentYAML(disaggPlan); err != nil {
		return fmt.Errorf("failed to generate disaggregated mode YAML: %w", err)
	}

	if err := RenderDeploymentYAML(aggPlan); err != nil {
		return fmt.Errorf("failed to generate aggregated mode YAML: %w", err)
	}

	fmt.Println()

	// Step 7: Display results
	displayResults(config, disaggPlan, aggPlan, disaggConfig, aggConfig)

	return nil
}

// validateConfig validates the TaskConfig
func validateConfig(config *TaskConfig) error {
	if config.ModelName == "" {
		return fmt.Errorf("--model is required")
	}
	if config.SystemName == "" {
		return fmt.Errorf("--system is required")
	}
	if config.TotalGPUs <= 0 {
		return fmt.Errorf("--total-gpus must be greater than 0")
	}

	// Validate enum values
	validBackends := map[string]bool{"sglang": true, "vllm": true, "trtllm": true}
	if !validBackends[config.BackendName] {
		return fmt.Errorf("invalid backend %s, must be one of: sglang, vllm, trtllm", config.BackendName)
	}

	validSystems := map[string]bool{
		"h100_sxm": true, "a100_sxm": true, "b200_sxm": true,
		"gb200_sxm": true, "l40s": true, "h200_sxm": true,
	}
	if !validSystems[config.SystemName] {
		return fmt.Errorf("invalid system %s, must be one of: h100_sxm, a100_sxm, b200_sxm, gb200_sxm, l40s, h200_sxm", config.SystemName)
	}

	validDatabaseModes := map[string]bool{"SILICON": true, "HYBRID": true, "EMPIRICAL": true, "SOL": true}
	if !validDatabaseModes[config.DatabaseMode] {
		return fmt.Errorf("invalid database-mode %s, must be one of: SILICON, HYBRID, EMPIRICAL, SOL", config.DatabaseMode)
	}

	return nil
}

// displayResults shows the generated deployment plans to the user
func displayResults(config *TaskConfig, disaggPlan, aggPlan *DeploymentPlan, disaggConfig, aggConfig *GeneratorConfig) {
	fmt.Println("✓ Successfully generated 2 deployment recommendations:")
	fmt.Println()

	// Disaggregated mode summary
	fmt.Println("Plan 1: Prefill-Decode Disaggregated Mode")
	fmt.Printf("  File: %s\n", disaggPlan.OutputPath)
	fmt.Println("  Configuration:")

	prefillParams := GetWorkerParams(disaggConfig.Params.Prefill)
	decodeParams := GetWorkerParams(disaggConfig.Params.Decode)

	prefillTotalGPUs := disaggConfig.Workers.PrefillWorkers * prefillParams.TensorParallelSize
	decodeTotalGPUs := disaggConfig.Workers.DecodeWorkers * decodeParams.TensorParallelSize

	fmt.Printf("    - Prefill Workers: %d (each using %d GPUs)\n",
		disaggConfig.Workers.PrefillWorkers, prefillParams.TensorParallelSize)
	fmt.Printf("    - Decode Workers: %d (each using %d GPUs)\n",
		disaggConfig.Workers.DecodeWorkers, decodeParams.TensorParallelSize)
	fmt.Printf("    - Total GPU Usage: %d\n", prefillTotalGPUs+decodeTotalGPUs)
	fmt.Println()

	// Aggregated mode summary
	fmt.Println("Plan 2: Aggregated Mode")
	fmt.Printf("  File: %s\n", aggPlan.OutputPath)
	fmt.Println("  Configuration:")

	aggParams := GetWorkerParams(aggConfig.Params.Agg)
	aggTotalGPUs := aggConfig.Workers.AggWorkers * aggParams.TensorParallelSize

	fmt.Printf("    - Workers: %d (each using %d GPUs)\n",
		aggConfig.Workers.AggWorkers, aggParams.TensorParallelSize)
	fmt.Printf("    - Total GPU Usage: %d\n", aggTotalGPUs)
	fmt.Println()

	// Deployment instructions
	fmt.Println("To deploy, run:")
	fmt.Printf("  kubectl apply -f %s\n", disaggPlan.OutputPath)
	fmt.Println("or")
	fmt.Printf("  kubectl apply -f %s\n", aggPlan.OutputPath)
	fmt.Println()
	fmt.Println("Note: Ensure the 'llm-model' PVC exists in your cluster before deploying.")
}

// normalizeModelName converts model name to a valid Kubernetes resource name
func normalizeModelName(name string) string {
	// Convert to lowercase and replace underscores/dots with hyphens
	result := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result += string(c)
		} else if c >= 'A' && c <= 'Z' {
			result += string(c + 32) // Convert to lowercase
		} else if c == '_' || c == '.' {
			result += "-"
		}
	}
	return result
}
