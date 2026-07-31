package rodbackend

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// FindChromePath attempts to find Chrome or Chromium executable on the system.
// Returns the path to the executable if found, or empty string if not found.
func FindChromePath() string {
	// Check if a custom Chrome path is set via environment variable
	if customPath := os.Getenv("CHROME_PATH"); customPath != "" {
		if _, err := os.Stat(customPath); err == nil {
			return customPath
		}
	}

	// Try to find Chrome using the launcher's built-in detection
	// Check common paths based on OS
	var candidates []string

	switch runtime.GOOS {
	case "windows":
		// Common Chrome paths on Windows
		chromePaths := []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Chromium\Application\chromium.exe`,
			`C:\Program Files (x86)\Chromium\Application\chromium.exe`,
		}
		// Also check AppData for user-installed Chrome
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData != "" {
			chromePaths = append(chromePaths,
				filepath.Join(localAppData, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(localAppData, "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
		candidates = chromePaths

	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}

	case "linux":
		candidates = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
			"/opt/google/chrome/google-chrome",
		}
	}

	// Check each candidate
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Try to find Chrome in PATH
	if path, err := exec.LookPath("google-chrome"); err == nil {
		return path
	}
	if path, err := exec.LookPath("google-chrome-stable"); err == nil {
		return path
	}
	if path, err := exec.LookPath("chromium"); err == nil {
		return path
	}
	if path, err := exec.LookPath("chromium-browser"); err == nil {
		return path
	}

	return ""
}
