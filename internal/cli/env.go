// Package cli provides the "wukong env" command for displaying
// runtime environment information.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
	"github.com/km269/wukong/pkg/sandbox"
)

// newEnvCmd creates the "wukong env" command for environment inspection.
func newEnvCmd() *cobra.Command {
	var (
		configPath string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "env",
		Short: "Display runtime environment information",
		Long: `Show detailed information about the wukong runtime
environment including OS, architecture, paths, sandbox
status, and configuration.

Examples:
  wukong env
  wukong env --json    # JSON output for automation`,
		RunE: runEnv,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)")
	cmd.Flags().BoolVar(
		&jsonOutput, "json", false,
		"Output as JSON")

	return cmd
}

func runEnv(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	info := buildEnvInfo(configPath)

	if jsonOutput {
		printEnvJSON(info)
	} else {
		printEnvTable(info)
	}

	return nil
}

// envInfo holds the complete runtime environment information.
type envInfo struct {
	Version       string
	GitCommit     string
	BuildDate     string
	GoVersion     string
	OS            string
	Arch          string
	CPUs          int
	Goroutines    int
	WorkDir       string
	HomeDir       string
	ConfigFile    string
	ConfigDir     string
	DataDir       string
	DBFile        string
	SandboxStatus string
	UserID        string
	DefaultProv   string
	ProviderCount int
	LogLevel      string
}

func buildEnvInfo(configPath string) envInfo {
	info := envInfo{
		Version:    util.Version,
		GitCommit:  util.GitCommit,
		BuildDate:  util.BuildDate,
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		CPUs:       runtime.NumCPU(),
		Goroutines: runtime.NumGoroutine(),
		UserID:     resolveUserID(),
	}

	// Working directory
	if wd, err := os.Getwd(); err == nil {
		info.WorkDir = wd
	}

	// Home directory
	if home, err := os.UserHomeDir(); err == nil {
		info.HomeDir = home
		info.ConfigDir = filepath.Join(home, ".config", "wukong")
		info.DataDir = filepath.Join(home, ".config", "wukong", "data")
	}

	// Config file — the loader reports the file it actually read;
	// fall back to the conventional user-level path when running
	// purely on built-in defaults.
	loader, loadErr := config.NewLoader(configPath)
	if loadErr == nil {
		if used := loader.ConfigFileUsed(); used != "" {
			info.ConfigFile = used
		}
	}
	if info.ConfigFile == "" {
		if info.ConfigDir != "" {
			info.ConfigFile = filepath.Join(info.ConfigDir, "config.yaml")
		}
	}

	// Sandbox
	sb := sandbox.Probe()
	if sb.Sandboxed {
		info.SandboxStatus = fmt.Sprintf("active (%s)", sb.Backend)
	} else {
		info.SandboxStatus = fmt.Sprintf("unavailable (%s)", sb.Backend)
	}

	// Config-dependent info
	if loadErr == nil {
		wukongCfg, cfgErr := loader.Load()
		if cfgErr == nil {
			info.DefaultProv = wukongCfg.DefaultProvider
			info.ProviderCount = len(wukongCfg.Providers)
			info.LogLevel = wukongCfg.LogLevel
			dbPath := wukongCfg.Session.DBPath
			if dbPath != "" {
				info.DBFile = config.ResolvePath(dbPath)
			}
		}
	}

	return info
}

func printEnvTable(info envInfo) {
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println("  Wukong Environment")
	fmt.Println(strings.Repeat("═", 60))

	// Build information
	fmt.Println("\n  [Build]")
	fmt.Printf("    Version:     %s\n", info.Version)
	fmt.Printf("    Git commit:  %s\n", info.GitCommit)
	fmt.Printf("    Build date:  %s\n", info.BuildDate)
	fmt.Printf("    Go version:  %s\n", info.GoVersion)

	// System information
	fmt.Println("\n  [System]")
	fmt.Printf("    OS:          %s/%s\n", info.OS, info.Arch)
	fmt.Printf("    CPUs:        %d (goroutines: %d)\n", info.CPUs, info.Goroutines)
	fmt.Printf("    Sandbox:     %s\n", info.SandboxStatus)

	// Paths
	fmt.Println("\n  [Paths]")
	fmt.Printf("    Work dir:    %s\n", info.WorkDir)
	fmt.Printf("    Config file: ")
	if _, err := os.Stat(info.ConfigFile); err == nil {
		fmt.Printf("%s ✓\n", info.ConfigFile)
	} else {
		fmt.Printf("%s (not found)\n", info.ConfigFile)
	}
	fmt.Printf("    Config dir:  %s\n", info.ConfigDir)
	if info.DBFile != "" {
		fmt.Printf("    Database:    %s\n", info.DBFile)
	}

	// Configuration summary
	fmt.Println("\n  [Configuration]")
	fmt.Printf("    User:        %s\n", info.UserID)
	if info.DefaultProv != "" {
		fmt.Printf("    Provider:    %s (%d configured)\n",
			info.DefaultProv, info.ProviderCount)
	}
	if info.LogLevel != "" {
		fmt.Printf("    Log level:   %s\n", info.LogLevel)
	}

	fmt.Println()
}

func printEnvJSON(info envInfo) {
	fmt.Println("{")
	fmt.Printf("  \"version\": \"%s\",\n", info.Version)
	fmt.Printf("  \"git_commit\": \"%s\",\n", info.GitCommit)
	fmt.Printf("  \"build_date\": \"%s\",\n", info.BuildDate)
	fmt.Printf("  \"go_version\": \"%s\",\n", info.GoVersion)
	fmt.Printf("  \"os\": \"%s\",\n", info.OS)
	fmt.Printf("  \"arch\": \"%s\",\n", info.Arch)
	fmt.Printf("  \"cpus\": %d,\n", info.CPUs)
	fmt.Printf("  \"goroutines\": %d,\n", info.Goroutines)
	fmt.Printf("  \"sandbox\": \"%s\",\n", info.SandboxStatus)
	fmt.Printf("  \"work_dir\": \"%s\",\n", info.WorkDir)
	fmt.Printf("  \"config_file\": \"%s\",\n", info.ConfigFile)
	fmt.Printf("  \"config_dir\": \"%s\",\n", info.ConfigDir)
	fmt.Printf("  \"db_file\": \"%s\",\n", info.DBFile)
	fmt.Printf("  \"user_id\": \"%s\",\n", info.UserID)
	fmt.Printf("  \"default_provider\": \"%s\",\n", info.DefaultProv)
	fmt.Printf("  \"provider_count\": %d,\n", info.ProviderCount)
	fmt.Printf("  \"log_level\": \"%s\"\n", info.LogLevel)
	fmt.Println("}")
}
