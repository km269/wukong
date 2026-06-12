// Package cli provides the command-line interface for wukong.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/util"
)

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}

var (
	debugEnabled bool
	quietEnabled bool
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wukong",
		Short: "Wukong - A local-first extensible AI agent platform",
		Long: `Wukong is an open source, extensible AI agent that goes beyond
code suggestions. It can install, execute, edit, and test
with any LLM, all running locally on your machine.

Built with tRPC-Agent-Go, tRPC-MCP-Go and tRPC-A2A-Go.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if debugEnabled {
				util.SetDebugMode()
			} else if quietEnabled {
				util.SetQuietMode()
			}
			return nil
		},
	}

	cmd.PersistentFlags().BoolVar(
		&debugEnabled, "debug", false,
		"Enable debug-level logging",
	)
	cmd.PersistentFlags().BoolVar(
		&quietEnabled, "quiet", false,
		"Suppress all log output (warn and errors only)",
	)

	cmd.AddCommand(newSessionCmd())
	cmd.AddCommand(newConfigureCmd())
	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newExtensionCmd())
	cmd.AddCommand(newCompletionCmd())

	return cmd
}

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `Generate the autocompletion script for the specified shell.

For bash:
  source <(wukong completion bash)

For zsh:
  source <(wukong completion zsh)

For fish:
  wukong completion fish | source

For PowerShell:
  wukong completion powershell | Out-String | Invoke-Expression`,
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletion(cmd.OutOrStdout())
			}
			return nil
		},
	}
}
