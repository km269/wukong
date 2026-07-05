package rodbackend

import (
	"os"
	"strings"
)

func isContainerized() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/proc/self/cgroup"); err == nil {
		data, _ := os.ReadFile("/proc/self/cgroup")
		if strings.Contains(string(data), "docker") || strings.Contains(string(data), "containerd") {
			return true
		}
	}
	if _, err := os.Stat("/proc/1/cgroup"); err == nil {
		data, _ := os.ReadFile("/proc/1/cgroup")
		if strings.Contains(string(data), "docker") || strings.Contains(string(data), "containerd") {
			return true
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if os.Getenv("CONTAINER_NAME") != "" {
		return true
	}
	return false
}

func needNoSandbox() bool {
	if os.Getuid() == 0 {
		return true
	}
	if isContainerized() {
		return true
	}
	if os.Getenv("NO_SANDBOX") != "" {
		return true
	}
	return false
}
