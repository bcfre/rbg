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
	"os"
	"os/exec"
	"strconv"

	"k8s.io/klog/v2"
)

// BuildAIConfiguratorCommand constructs the aiconfigurator CLI command from TaskConfig
func BuildAIConfiguratorCommand(config *TaskConfig) []string {
	args := []string{"cli", "default"}

	// Required parameters
	args = append(args, "--model", config.ModelName)
	args = append(args, "--system", config.SystemName)
	args = append(args, "--total_gpus", strconv.Itoa(config.TotalGPUs))
	args = append(args, "--backend", config.BackendName)
	args = append(args, "--isl", strconv.Itoa(config.ISL))
	args = append(args, "--osl", strconv.Itoa(config.OSL))
	args = append(args, "--ttft", strconv.FormatFloat(config.TTFT, 'f', -1, 64))
	args = append(args, "--tpot", strconv.FormatFloat(config.TPOT, 'f', -1, 64))
	args = append(args, "--save_dir", config.SaveDir)
	// args = append(args, "--database_mode", config.DatabaseMode)

	// Optional parameters
	if config.HuggingFaceID != "" {
		args = append(args, "--hf_id", config.HuggingFaceID)
	}

	if config.DecodeSystemName != "" && config.DecodeSystemName != config.SystemName {
		args = append(args, "--decode_system", config.DecodeSystemName)
	}

	if config.BackendVersion != "" && config.BackendVersion != "latest" {
		args = append(args, "--backend_version", config.BackendVersion)
	}

	if config.Prefix > 0 {
		args = append(args, "--prefix", strconv.Itoa(config.Prefix))
	}

	if config.RequestLatency > 0 {
		args = append(args, "--request_latency", strconv.FormatFloat(config.RequestLatency, 'f', -1, 64))
	}

	if config.Debug {
		args = append(args, "--debug")
	}

	// Add extra arguments
	for key, value := range config.ExtraArgs {
		args = append(args, fmt.Sprintf("--%s", key), value)
	}

	return args
}

// ExecuteAIConfigurator runs the aiconfigurator command with the given configuration
func ExecuteAIConfigurator(config *TaskConfig) error {
	args := BuildAIConfiguratorCommand(config)

	klog.V(2).Infof("Executing aiconfigurator with args: %v", args)

	cmd := exec.Command("aiconfigurator", args...)

	// Set output to stdout/stderr for real-time feedback
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if config.Debug {
		fmt.Printf("\n=== Executing aiconfigurator command ===\n")
		fmt.Printf("aiconfigurator %s\n", joinArgs(args))
		fmt.Printf("========================================\n\n")
	}

	fmt.Println("Running AI Configurator optimization... This may take a few minutes.")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("aiconfigurator execution failed: %w\nPlease check the error output above", err)
	}

	fmt.Println("✓ AI Configurator optimization completed successfully")
	return nil
}

// joinArgs joins command arguments with proper quoting
func joinArgs(args []string) string {
	result := ""
	for i, arg := range args {
		if i > 0 {
			result += " "
		}
		// Quote arguments that contain spaces
		if containsSpace(arg) {
			result += fmt.Sprintf("\"%s\"", arg)
		} else {
			result += arg
		}
	}
	return result
}

// containsSpace checks if a string contains a space
func containsSpace(s string) bool {
	for _, c := range s {
		if c == ' ' {
			return true
		}
	}
	return false
}
