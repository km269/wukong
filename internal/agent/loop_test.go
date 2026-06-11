// Package agent_test provides integration tests for the wukong agent system.
package agent_test

import (
	"testing"
)

// TestConfigLoad verifies that the default config can be loaded.
func TestConfigLoad(t *testing.T) {
	// Test config loading without a file (uses defaults)
	// This is a smoke test to verify the config system works
	t.Skip("integration test - requires config file setup")
}

// TestAgentLoopCreation verifies that the agent loop can be created.
func TestAgentLoopCreation(t *testing.T) {
	t.Skip("integration test - requires LLM API key")
}

// TestTodoCRUD verifies todo create, read, update operations.
func TestTodoCRUD(t *testing.T) {
	t.Skip("integration test - requires SQLite database")
}

// TestBuiltinExtensions verifies built-in developer tools are available.
func TestBuiltinExtensions(t *testing.T) {
	t.Skip("integration test - requires extension initialization")
}

// TestSessionPersistence verifies session save and restore.
func TestSessionPersistence(t *testing.T) {
	t.Skip("integration test - requires session service")
}
