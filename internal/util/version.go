// Package util provides small shared helpers (logging, database,
// pointers) used across wukong packages.
package util

// Version information set at build time via ldflags.
var (
	Version   = "0.3.3"
	GitCommit = "fix commit"
	BuildDate = "2026-08-29"
)
