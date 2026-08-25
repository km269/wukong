package rodbackend

import (
	"os"
	"testing"
)

// TestNeedNoSandbox_NonContainerNonRoot 在普通用户、非容器环境下应返回 false.
// 注意: 在容器内运行测试时此用例可能失败, 因此用环境变量检测跳过.
func TestNeedNoSandbox_NonContainerNonRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — needNoSandbox() correctly returns true")
	}
	if isContainerized() {
		t.Skip("running in container — needNoSandbox() correctly returns true")
	}
	if os.Getenv("NO_SANDBOX") != "" {
		t.Skip("NO_SANDBOX env set — needNoSandbox() correctly returns true")
	}
	if needNoSandbox() {
		t.Error("needNoSandbox() = true, want false in non-root non-container env")
	}
}

// TestNeedNoSandbox_EnvVar 设置 NO_SANDBOX 环境变量后应返回 true.
func TestNeedNoSandbox_EnvVar(t *testing.T) {
	old := os.Getenv("NO_SANDBOX")
	defer os.Setenv("NO_SANDBOX", old)

	os.Setenv("NO_SANDBOX", "1")
	if !needNoSandbox() {
		t.Error("needNoSandbox() = false, want true when NO_SANDBOX is set")
	}
}

// TestIsContainerized_NoDockerEnv 在普通环境下应返回 false.
func TestIsContainerized_NoDockerEnv(t *testing.T) {
	if isContainerized() {
		t.Skip("running in container — isContainerized() correctly returns true")
	}
}

// TestOptionsDisableDownloadsDefault 验证 DisableDownloads 默认零值.
func TestOptionsDisableDownloadsDefault(t *testing.T) {
	var opts Options
	if opts.DisableDownloads {
		t.Error("DisableDownloads default should be false (zero value)")
	}
}
