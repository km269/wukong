// Package pack provides application packaging functionality.
package pack

import (
	"time"
)

// Result holds the outcome of a packaging operation.
type Result struct {
	// Success indicates whether the packaging completed successfully.
	Success bool

	// Format is the output format used.
	Format Format

	// OutputPath is the path to the generated output.
	OutputPath string

	// SizeBytes is the size of the generated output.
	SizeBytes int64

	// Duration is the time taken for packaging.
	Duration time.Duration

	// FilesProcessed is the number of files processed.
	FilesProcessed int

	// AssetsIncluded is the number of assets included.
	AssetsIncluded int

	// Errors lists any errors encountered during packaging.
	Errors []string

	// StartTime is when the packaging operation began.
	StartTime time.Time

	// EndTime is when the packaging operation finished.
	EndTime time.Time
}

// Progress provides real-time progress updates during packaging.
type Progress struct {
	// CurrentPhase is the current packaging phase.
	CurrentPhase string

	// FilesProcessed is the count of files processed so far.
	FilesProcessed int

	// TotalFiles is the estimated total files to process.
	TotalFiles int

	// Percentage is the estimated completion percentage (0-100).
	Percentage float64

	// Message is a human-readable progress message.
	Message string
}

// PackPhase represents a phase in the packaging process.
type PackPhase string

const (
	PhaseScanning    PackPhase = "scanning"     // Scanning source files
	PhaseCollecting  PackPhase = "collecting"   // Collecting assets
	PhaseProcessing  PackPhase = "processing"   // Processing HTML files
	PhaseCompressing PackPhase = "compressing"  // Compressing content
	PhaseWriting     PackPhase = "writing"      // Writing output
	PhaseFinalizing  PackPhase = "finalizing"   // Finalizing package
)