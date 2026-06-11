// Package main is the entry point for the wukong AI agent CLI application.
// Wukong is a local-first, extensible AI agent platform built with
// tRPC-Agent-Go, tRPC-MCP-Go and tRPC-A2A-Go frameworks.
package main

import (
	"fmt"
	"os"

	"github.com/km269/wukong/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "wukong: %v\n", err)
		os.Exit(1)
	}
}
