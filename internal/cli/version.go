package cli

import (
	"fmt"

	"github.com/km269/wukong/internal/util"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  `Print the wukong version, git commit, and build date.`,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("wukong %s\n", util.Version)
			fmt.Printf("  git commit: %s\n", util.GitCommit)
			fmt.Printf("  build date: %s\n", util.BuildDate)
		},
	}

	return cmd
}
