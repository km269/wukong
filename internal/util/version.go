// Package util provides small shared helpers (logging, database,
// pointers) used across wukong packages.
package util

import (
	"runtime/debug"
	"sync"
)

// Build information. The canonical values are injected at link time
// via -ldflags "-X github.com/km269/wukong/internal/util.Version=…"
// (Makefile, Taskfile, goreleaser — all sourced from git). When a
// binary is built without injection the values fall back to the Go
// build info: `go install …@vX.Y.Z` reports the module version and
// VCS metadata automatically; a plain `go build`/`go test` degrades
// to "dev". See docs/YAO_COMPARISON_AND_ROADMAP.md §5 (P2 release
// hygiene): the version must come from git, never from a hardcoded
// string that silently drifts.
var (
	Version   = ""
	GitCommit = ""
	BuildDate = ""
)

var versionOnce sync.Once

// resolveVersion fills in any value the linker did not inject,
// preferring the Go build info (module version for go-install
// binaries, VCS revision/time for repo builds).
func resolveVersion() {
	bi, ok := debug.ReadBuildInfo()

	if Version == "" {
		version := "dev"
		if ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
		Version = version
	}

	settings := map[string]string{}
	if ok {
		for _, s := range bi.Settings {
			settings[s.Key] = s.Value
		}
	}

	if GitCommit == "" {
		GitCommit = shortCommit(orUnknown(settings["vcs.revision"]))
	}
	if BuildDate == "" {
		BuildDate = orUnknown(settings["vcs.time"])
	}
}

// shortCommit trims a full VCS revision to the conventional 12-char
// short form.
func shortCommit(rev string) string {
	if rev == "" {
		return "unknown"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func init() {
	versionOnce.Do(resolveVersion)
}
