// Package extension provides common types for the extension system.
// These types are used by both the extension manager and builtin extensions.
package extension

import (
	"time"

	"github.com/km269/wukong/internal/config"
)

// ExtensionStatus represents the current status of an extension.
type ExtensionStatus string

const (
	StatusEnabled  ExtensionStatus = "enabled"
	StatusDisabled ExtensionStatus = "disabled"
	StatusError    ExtensionStatus = "error"
	StatusLoading  ExtensionStatus = "loading"
)

// ExtensionInfo holds metadata about a registered extension.
type ExtensionInfo struct {
	Name         string                  `json:"name"`
	Type         string                  `json:"type"`
	Status       ExtensionStatus         `json:"status"`
	Transport    string                  `json:"transport,omitempty"`
	ToolCount    int                     `json:"tool_count"`
	Permissions  []config.ToolPermission `json:"permissions,omitempty"`
	Error        string                  `json:"error,omitempty"`
	RegisteredAt time.Time               `json:"registered_at"`
}
