package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version information set at build time via ldflags.
var (
	Version   = "0.1.6"
	GitCommit = "unknown"
	BuildDate = "2026-06-14"
)

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  `Print the wukong version, git commit, and build date.`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("wukong %s\n", Version)
			fmt.Printf("  git commit: %s\n", GitCommit)
			fmt.Printf("  build date: %s\n", BuildDate)
		},
	}

	return cmd
}
