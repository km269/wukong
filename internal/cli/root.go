// Package cli provides the command-line interface for wukong.
package cli

import (
	"github.com/spf13/cobra"
)

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}

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
	}

	cmd.AddCommand(newSessionCmd())
	cmd.AddCommand(newConfigureCmd())
	cmd.AddCommand(newVersionCmd())

	return cmd
}
