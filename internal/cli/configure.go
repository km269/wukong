package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newConfigureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure wukong settings interactively",
		Long: `Configure model providers, extensions, and other settings.
This command opens an interactive configuration wizard.`,
		RunE: runConfigure,
	}

	return cmd
}

func runConfigure(cmd *cobra.Command, args []string) error {
	fmt.Println("Starting configuration wizard... (coming soon)")
	return nil
}
