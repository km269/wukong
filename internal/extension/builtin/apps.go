// Package builtin provides the Apps tool set for managing custom HTML apps.
package builtin

import (
	"context"
	"fmt"

	"github.com/km269/wukong/internal/apps"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// AppsToolSet provides tools for creating and managing custom HTML apps.
type AppsToolSet struct {
	tools  []tool.Tool
	mgr    *apps.Manager
	inited bool
	closed bool
}

// NewAppsToolSet creates the apps tool set.
func NewAppsToolSet(mgr *apps.Manager) *AppsToolSet {
	ts := &AppsToolSet{mgr: mgr}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.createApp,
			function.WithName("app_create"),
			function.WithDescription(
				"Create a new standalone HTML application. "+
					"Provide a name and full HTML content.",
			),
		),
		function.NewFunctionTool(
			ts.listApps,
			function.WithName("app_list"),
			function.WithDescription(
				"List all created HTML applications.",
			),
		),
		function.NewFunctionTool(
			ts.getApp,
			function.WithName("app_get"),
			function.WithDescription(
				"Get details of a specific application.",
			),
		),
		function.NewFunctionTool(
			ts.updateApp,
			function.WithName("app_update"),
			function.WithDescription(
				"Update an existing application's HTML content.",
			),
		),
		function.NewFunctionTool(
			ts.deleteApp,
			function.WithName("app_delete"),
			function.WithDescription(
				"Delete an application.",
			),
		),
	}
	return ts
}

// Tools returns the apps tools.
func (ts *AppsToolSet) Tools(ctx context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *AppsToolSet) Name() string {
	return "apps"
}

// Init initializes the tool set.
func (ts *AppsToolSet) Init(ctx context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *AppsToolSet) Close() error {
	ts.closed = true
	return nil
}

// AppCreateReq is the input for creating an app.
type AppCreateReq struct {
	Name        string `json:"name" jsonschema:"description=App name (used as filename)"`
	Description string `json:"description,omitempty" jsonschema:"description=Short description of the app"`
	HTML        string `json:"html" jsonschema:"description=Full HTML content for the app"`
}

// AppCreateRsp is the output for creating an app.
type AppCreateRsp struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (ts *AppsToolSet) createApp(
	ctx context.Context, req AppCreateReq,
) (AppCreateRsp, error) {
	app, err := ts.mgr.CreateApp(
		req.Name, req.Description, req.HTML,
	)
	if err != nil {
		return AppCreateRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return AppCreateRsp{
		Success:  true,
		FilePath: app.FilePath,
		Message:  fmt.Sprintf("App %q created at %s", req.Name, app.FilePath),
	}, nil
}

// AppListReq is the input for listing apps.
type AppListReq struct{}

// AppListRsp is the output for listing apps.
type AppListRsp struct {
	Success bool           `json:"success"`
	Apps    []apps.AppInfo `json:"apps,omitempty"`
	Count   int            `json:"count"`
}

func (ts *AppsToolSet) listApps(
	ctx context.Context, req AppListReq,
) (AppListRsp, error) {
	apps := ts.mgr.ListApps()
	return AppListRsp{
		Success: true,
		Apps:    apps,
		Count:   len(apps),
	}, nil
}

// AppGetReq is the input for getting an app.
type AppGetReq struct {
	Name string `json:"name" jsonschema:"description=App name"`
}

// AppGetRsp is the output for getting an app.
type AppGetRsp struct {
	Success bool         `json:"success"`
	App     *apps.AppInfo `json:"app,omitempty"`
	Error   string       `json:"error,omitempty"`
}

func (ts *AppsToolSet) getApp(
	ctx context.Context, req AppGetReq,
) (AppGetRsp, error) {
	app, ok := ts.mgr.GetApp(req.Name)
	if !ok {
		return AppGetRsp{
			Success: false,
			Error:   fmt.Sprintf("app %q not found", req.Name),
		}, nil
	}
	return AppGetRsp{
		Success: true,
		App:     &app,
	}, nil
}

// AppUpdateReq is the input for updating an app.
type AppUpdateReq struct {
	Name        string `json:"name" jsonschema:"description=App name to update"`
	HTML        string `json:"html" jsonschema:"description=New HTML content"`
}

// AppUpdateRsp is the output for updating an app.
type AppUpdateRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *AppsToolSet) updateApp(
	ctx context.Context, req AppUpdateReq,
) (AppUpdateRsp, error) {
	_, err := ts.mgr.UpdateApp(req.Name, req.HTML)
	if err != nil {
		return AppUpdateRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return AppUpdateRsp{
		Success: true,
		Message: fmt.Sprintf("App %q updated", req.Name),
	}, nil
}

// AppDeleteReq is the input for deleting an app.
type AppDeleteReq struct {
	Name string `json:"name" jsonschema:"description=App name to delete"`
}

// AppDeleteRsp is the output for deleting an app.
type AppDeleteRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *AppsToolSet) deleteApp(
	ctx context.Context, req AppDeleteReq,
) (AppDeleteRsp, error) {
	if err := ts.mgr.DeleteApp(req.Name); err != nil {
		return AppDeleteRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return AppDeleteRsp{
		Success: true,
		Message: fmt.Sprintf("App %q deleted", req.Name),
	}, nil
}
