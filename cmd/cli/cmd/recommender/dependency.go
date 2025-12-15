package recommender

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

// CheckAIConfiguratorAvailability checks if aiconfigurator is installed
func CheckAIConfiguratorAvailability() error {
	// Check if aiconfigurator command exists in PATH
	cmd := exec.Command("which", "aiconfigurator")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("aiconfigurator is not installed\n\n" +
			"Please install it using one of the following methods:\n" +
			"  pip install aiconfigurator\n" +
			"Or visit: https://github.com/ai-dynamo/aiconfigurator")
	}

	// Try to get version information
	versionCmd := exec.Command("aiconfigurator", "version")
	output, err := versionCmd.CombinedOutput()
	if err != nil {
		// Version command failed, but tool exists - continue with warning
		klog.Warning("Could not determine aiconfigurator version, but tool is available")
		return nil
	}

	// Extract version number from output using regex
	// Expected format: "aiconfigurator 0.5.0" or just "0.5.0"
	outputStr := string(output)
	klog.V(2).Infof("aiconfigurator version output: %s", outputStr)

	versionRegex := regexp.MustCompile(`(\d+\.\d+\.\d+)`)
	matches := versionRegex.FindStringSubmatch(outputStr)

	if len(matches) < 2 {
		klog.Warning("Could not parse aiconfigurator version from output, but tool is available")
		return nil
	}

	versionStr := matches[1]
	klog.Infof("Found aiconfigurator version: %s", versionStr)

	// Check if version >= 0.5.0
	if err := checkMinVersion(versionStr, "0.5.0"); err != nil {
		return fmt.Errorf("aiconfigurator version %s is too old (minimum required: 0.5.0)\n\n"+
			"Please upgrade using:\n"+
			"  pip install --upgrade aiconfigurator", versionStr)
	}

	return nil
}

// checkMinVersion checks if actual version >= required version
// Both versions should be in format "x.y.z"
func checkMinVersion(actual, required string) error {
	actualParts := strings.Split(actual, ".")
	requiredParts := strings.Split(required, ".")

	if len(actualParts) != 3 || len(requiredParts) != 3 {
		return fmt.Errorf("invalid version format")
	}

	for i := 0; i < 3; i++ {
		actualNum, err := strconv.Atoi(actualParts[i])
		if err != nil {
			return fmt.Errorf("invalid version number: %s", actual)
		}

		requiredNum, err := strconv.Atoi(requiredParts[i])
		if err != nil {
			return fmt.Errorf("invalid version number: %s", required)
		}

		if actualNum > requiredNum {
			return nil // actual version is higher
		}
		if actualNum < requiredNum {
			return fmt.Errorf("version too old") // actual version is lower
		}
		// If equal, continue to check next part
	}

	// All parts are equal, version is exactly the required version
	return nil
}
