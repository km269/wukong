// Package extension provides the Extension Manager tool set.
// It gives the agent tools to dynamically discover, enable,
// and disable extensions.
package extension

import (
	"context"
	"fmt"
	"strings"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ManagerToolSet provides tools for the agent to manage extensions.
type ManagerToolSet struct {
	tools  []tool.Tool
	mgr    *Manager
	cfg    *config.WukongConfig
	inited bool
	closed bool
}

// NewManagerToolSet creates the extension manager tool set.
func NewManagerToolSet(
	mgr *Manager, cfg *config.WukongConfig,
) *ManagerToolSet {
	ts := &ManagerToolSet{mgr: mgr, cfg: cfg}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.listExtensions,
			function.WithName("extension_list"),
			function.WithDescription(
				"List all registered extensions with their "+
					"status (enabled/disabled/error) and tool count.",
			),
		),
		function.NewFunctionTool(
			ts.enableExtension,
			function.WithName("extension_enable"),
			function.WithDescription(
				"Enable a disabled extension by name. "+
					"The extension must be configured in config.yaml.",
			),
		),
		function.NewFunctionTool(
			ts.disableExtension,
			function.WithName("extension_disable"),
			function.WithDescription(
				"Disable an enabled extension by name.",
			),
		),
		function.NewFunctionTool(
			ts.addDeeplink,
			function.WithName("extension_add_deeplink"),
			function.WithDescription(
				"Add an extension via deeplink URL. "+
					"Format: wukong://extension?name=...&type=...&transport=...",
			),
		),
	}
	return ts
}

// Tools returns the extension manager tools.
func (ts *ManagerToolSet) Tools(
	ctx context.Context,
) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *ManagerToolSet) Name() string {
	return "extension_manager"
}

// Init initializes the tool set.
func (ts *ManagerToolSet) Init(ctx context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *ManagerToolSet) Close() error {
	ts.closed = true
	return nil
}

// ExtensionListReq is the input for listing extensions.
type ExtensionListReq struct{}

// ExtensionListRsp is the output for listing extensions.
type ExtensionListRsp struct {
	Success    bool            `json:"success"`
	Extensions []ExtensionInfo `json:"extensions,omitempty"`
	Count      int             `json:"count"`
	Message    string          `json:"message,omitempty"`
}

func (ts *ManagerToolSet) listExtensions(
	ctx context.Context, req ExtensionListReq,
) (ExtensionListRsp, error) {
	exts := ts.mgr.ListExtensions()

	var sb strings.Builder
	sb.WriteString("Available extensions:\n")
	for _, ext := range exts {
		statusIcon := "○"
		switch ext.Status {
		case StatusEnabled:
			statusIcon = "●"
		case StatusError:
			statusIcon = "✕"
		}
		fmt.Fprintf(&sb,
			"  %s %s [%s] (%d tools)",
			statusIcon, ext.Name, ext.Type, ext.ToolCount,
		)
		if ext.Error != "" {
			fmt.Fprintf(&sb,
				" - Error: %s", ext.Error,
			)
		}
		sb.WriteString("\n")
	}

	return ExtensionListRsp{
		Success:    true,
		Extensions: exts,
		Count:      len(exts),
		Message:    sb.String(),
	}, nil
}

// ExtensionEnableReq is the input for enabling an extension.
type ExtensionEnableReq struct {
	Name string `json:"name" jsonschema:"description=Name of the extension to enable"`
}

// ExtensionEnableRsp is the output for enabling.
type ExtensionEnableRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *ManagerToolSet) enableExtension(
	ctx context.Context, req ExtensionEnableReq,
) (ExtensionEnableRsp, error) {
	if err := ts.mgr.EnableExtension(ctx, req.Name); err != nil {
		return ExtensionEnableRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return ExtensionEnableRsp{
		Success: true,
		Message: fmt.Sprintf(
			"Extension %q enabled successfully", req.Name,
		),
	}, nil
}

// ExtensionDisableReq is the input for disabling.
type ExtensionDisableReq struct {
	Name string `json:"name" jsonschema:"description=Name of the extension to disable"`
}

// ExtensionDisableRsp is the output for disabling.
type ExtensionDisableRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *ManagerToolSet) disableExtension(
	ctx context.Context, req ExtensionDisableReq,
) (ExtensionDisableRsp, error) {
	if err := ts.mgr.DisableExtension(req.Name); err != nil {
		return ExtensionDisableRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return ExtensionDisableRsp{
		Success: true,
		Message: fmt.Sprintf(
			"Extension %q disabled successfully", req.Name,
		),
	}, nil
}

// DeeplinkReq is the input for adding via deeplink.
type DeeplinkReq struct {
	URL string `json:"url" jsonschema:"description=Deeplink URL for the extension (wukong://extension?name=...)"`
}

// DeeplinkRsp is the output for deeplink addition.
type DeeplinkRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *ManagerToolSet) addDeeplink(
	ctx context.Context, req DeeplinkReq,
) (DeeplinkRsp, error) {
	if err := ts.mgr.RegisterFromDeeplink(ctx, req.URL); err != nil {
		return DeeplinkRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return DeeplinkRsp{
		Success: true,
		Message: fmt.Sprintf(
			"Extension from deeplink added successfully",
		),
	}, nil
}
