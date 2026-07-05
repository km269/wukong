package clone

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultEnhancedOptions(t *testing.T) {
	opts := DefaultEnhancedOptions()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot get user home directory")
	}
	expectedOutputDir := filepath.Join(home, ".wukong", "apps", "cloned")

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"OutputDir", opts.OutputDir, expectedOutputDir},
		{"Workers", opts.Workers, 4},
		{"AssetWorkers", opts.AssetWorkers, 8},
		{"BrowserPages", opts.BrowserPages, 4},
		{"Timeout", opts.Timeout, 60 * time.Second},
		{"RenderTimeout", opts.RenderTimeout, 60 * time.Second},
		{"Settle", opts.Settle, 1500 * time.Millisecond},
		{"Traversal", opts.Traversal, TraversalBFS},
		{"RespectRobots", opts.RespectRobots, false},
		{"EnableResume", opts.EnableResume, true},
		{"Persist", opts.Persist, true},
		{"DedupContent", opts.DedupContent, true},
		{"MobileReadable", opts.MobileReadable, true},
		{"AssetSameDomain", opts.AssetSameDomain, true},
		{"Incremental", opts.Incremental, true},
		{"CacheMaxAge", opts.CacheMaxAge, 24 * time.Hour},
		{"Headless", opts.Headless, true},
		{"Stealth", opts.Stealth, true},
		{"ChromeProfile", opts.ChromeProfile, "./wukong_chrome_profile"},
		{"AntibotEnabled", opts.AntibotEnabled, true},
		{"AntibotAutoEscalate", opts.AntibotAutoEscalate, true},
		{"DisableDownloads", opts.DisableDownloads, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestDefaultEnhancedOptions_NoRobotsDefault(t *testing.T) {
	opts := DefaultEnhancedOptions()

	if opts.RespectRobots {
		t.Error("RespectRobots should be false by default (--no-robots enabled)")
	}
}

func TestDefaultEnhancedOptions_OutputDir(t *testing.T) {
	opts := DefaultEnhancedOptions()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot get user home directory")
	}

	expected := filepath.Join(home, ".wukong", "apps", "cloned")
	if opts.OutputDir != expected {
		t.Errorf("OutputDir = %q, want %q", opts.OutputDir, expected)
	}
}

func TestDefaultSkipAssetExts(t *testing.T) {
	exts := DefaultSkipAssetExts()

	mediaExts := []string{
		".mp4", ".m4v", ".webm", ".avi", ".mov", ".mkv", ".wmv", ".flv",
		".m3u8", ".ts",
		".mp3", ".wav", ".ogg", ".flac", ".aac", ".m4a", ".wma", ".oga",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".zip", ".tar", ".gz", ".bz2", ".7z", ".rar", ".tgz", ".xz",
		".dmg", ".iso",
		".exe", ".msi", ".apk", ".pkg", ".deb", ".rpm", ".appimage",
	}

	for _, ext := range mediaExts {
		t.Run(ext, func(t *testing.T) {
			if !exts[ext] {
				t.Errorf("DefaultSkipAssetExts should include %q", ext)
			}
		})
	}

	allowedExts := []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".css", ".woff", ".woff2", ".ttf", ".ico"}
	for _, ext := range allowedExts {
		t.Run(ext+":allowed", func(t *testing.T) {
			if exts[ext] {
				t.Errorf("DefaultSkipAssetExts should NOT include %q (page assets should be downloaded)", ext)
			}
		})
	}
}

func TestDefaultEnhancedOptions_SkipAssetExts(t *testing.T) {
	opts := DefaultEnhancedOptions()

	if opts.SkipAssetExts == nil {
		t.Error("SkipAssetExts should be initialized with default extensions")
	}

	if !opts.SkipAssetExts[".mp4"] {
		t.Error("SkipAssetExts should skip .mp4 by default")
	}
	if !opts.SkipAssetExts[".pdf"] {
		t.Error("SkipAssetExts should skip .pdf by default")
	}
	if !opts.SkipAssetExts[".zip"] {
		t.Error("SkipAssetExts should skip .zip by default")
	}
	if opts.SkipAssetExts[".png"] {
		t.Error("SkipAssetExts should NOT skip .png by default")
	}
	if opts.SkipAssetExts[".css"] {
		t.Error("SkipAssetExts should NOT skip .css by default")
	}
}
