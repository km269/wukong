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
				"创建新的 HTML 应用。提供名称和完整 HTML 内容。",
			),
		),
		function.NewFunctionTool(
			ts.createAppWithTemplate,
			function.WithName("app_create_with_template"),
			function.WithDescription(
				"使用预定义模板创建 HTML 应用。支持模板类型：blank（空白）、calculator（计算器）、dashboard（仪表盘）、form（表单）、notes（笔记）。",
			),
		),
		function.NewFunctionTool(
			ts.listTemplates,
			function.WithName("app_template_list"),
			function.WithDescription(
				"列出所有可用的应用模板类型及其描述。",
			),
		),
		function.NewFunctionTool(
			ts.listApps,
			function.WithName("app_list"),
			function.WithDescription(
				"列出所有已创建的 HTML 应用。",
			),
		),
		function.NewFunctionTool(
			ts.getApp,
			function.WithName("app_get"),
			function.WithDescription(
				"获取指定应用的详细信息。",
			),
		),
		function.NewFunctionTool(
			ts.updateApp,
			function.WithName("app_update"),
			function.WithDescription(
				"更新应用的 HTML 内容。",
			),
		),
		function.NewFunctionTool(
			ts.updateAppStatus,
			function.WithName("app_update_status"),
			function.WithDescription(
				"更新应用状态。支持状态：draft（草稿）、active（活跃）、archived（归档）。",
			),
		),
		function.NewFunctionTool(
			ts.importApp,
			function.WithName("app_import"),
			function.WithDescription(
				"从外部 HTML 内容导入应用。",
			),
		),
		function.NewFunctionTool(
			ts.deleteApp,
			function.WithName("app_delete"),
			function.WithDescription(
				"删除应用。",
			),
		),
		function.NewFunctionTool(
			ts.cloneApp,
			function.WithName("app_clone"),
			function.WithDescription(
				"克隆网站并创建离线 HTML 应用。支持设置页面数量限制、深度限制、子域名包含等选项。",
			),
		),
		function.NewFunctionTool(
			ts.packApp,
			function.WithName("app_pack"),
			function.WithDescription(
				"打包应用为多种格式：HTML 目录、ZIM 包（维基百科离线格式）、自包含二进制、桌面 App。",
			),
		),
		function.NewFunctionTool(
			ts.previewApp,
			function.WithName("app_preview"),
			function.WithDescription(
				"启动本地预览服务器，在浏览器中预览应用。返回预览 URL。",
			),
		),
		function.NewFunctionTool(
			ts.exportApp,
			function.WithName("app_export"),
			function.WithDescription(
				"导出应用为单个 HTML 文件，方便分享和离线使用。",
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
	Name        string `json:"name" jsonschema:"description=应用名称（用作文件名）"`
	Description string `json:"description,omitempty" jsonschema:"description=应用描述"`
	HTML        string `json:"html" jsonschema:"description=完整的 HTML 内容"`
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
		Message:  fmt.Sprintf("应用 %q 已创建于 %s", req.Name, app.FilePath),
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
	Name string `json:"name" jsonschema:"description=应用名称"`
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
			Error:   fmt.Sprintf("应用 %q 不存在", req.Name),
		}, nil
	}
	return AppGetRsp{
		Success: true,
		App:     &app,
	}, nil
}

// AppUpdateReq is the input for updating an app.
type AppUpdateReq struct {
	Name        string `json:"name" jsonschema:"description=要更新的应用名称"`
	HTML        string `json:"html" jsonschema:"description=新的 HTML 内容"`
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
	app, err := ts.mgr.UpdateApp(req.Name, req.HTML)
	if err != nil {
		return AppUpdateRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return AppUpdateRsp{
		Success: true,
		Message: fmt.Sprintf("应用 %q 已更新，版本 %s", app.Name, app.Version),
	}, nil
}

// AppDeleteReq is the input for deleting an app.
type AppDeleteReq struct {
	Name string `json:"name" jsonschema:"description=要删除的应用名称"`
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
		Message: fmt.Sprintf("应用 %q 已删除", req.Name),
	}, nil
}

// AppCreateWithTemplateReq is the input for creating an app with template.
type AppCreateWithTemplateReq struct {
	Name        string `json:"name" jsonschema:"description=应用名称（用作文件名）"`
	Description string `json:"description,omitempty" jsonschema:"description=应用描述"`
	Template    string `json:"template" jsonschema:"description=模板类型：blank、calculator、dashboard、form、notes"`
}

// AppCreateWithTemplateRsp is the output for creating an app with template.
type AppCreateWithTemplateRsp struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (ts *AppsToolSet) createAppWithTemplate(
	ctx context.Context, req AppCreateWithTemplateReq,
) (AppCreateWithTemplateRsp, error) {
	templateType := apps.TemplateType(req.Template)
	app, err := ts.mgr.CreateAppWithTemplate(req.Name, req.Description, templateType)
	if err != nil {
		return AppCreateWithTemplateRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return AppCreateWithTemplateRsp{
		Success:  true,
		FilePath: app.FilePath,
		Message:  fmt.Sprintf("应用 %q 已使用模板 %s 创建于 %s", req.Name, req.Template, app.FilePath),
	}, nil
}

// AppTemplateListReq is the input for listing templates.
type AppTemplateListReq struct{}

// AppTemplateListRsp is the output for listing templates.
type AppTemplateListRsp struct {
	Success   bool              `json:"success"`
	Templates []apps.TemplateInfo `json:"templates,omitempty"`
	Count     int               `json:"count"`
}

func (ts *AppsToolSet) listTemplates(
	ctx context.Context, req AppTemplateListReq,
) (AppTemplateListRsp, error) {
	templates := apps.ListTemplates()
	return AppTemplateListRsp{
		Success:   true,
		Templates: templates,
		Count:     len(templates),
	}, nil
}

// AppUpdateStatusReq is the input for updating app status.
type AppUpdateStatusReq struct {
	Name   string `json:"name" jsonschema:"description=应用名称"`
	Status string `json:"status" jsonschema:"description=新状态：draft、active、archived"`
}

// AppUpdateStatusRsp is the output for updating app status.
type AppUpdateStatusRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *AppsToolSet) updateAppStatus(
	ctx context.Context, req AppUpdateStatusReq,
) (AppUpdateStatusRsp, error) {
	status := apps.AppStatus(req.Status)
	app, err := ts.mgr.UpdateAppStatus(req.Name, status)
	if err != nil {
		return AppUpdateStatusRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return AppUpdateStatusRsp{
		Success: true,
		Message: fmt.Sprintf("应用 %q 状态已更新为 %s，版本 %s", app.Name, app.Status, app.Version),
	}, nil
}

// AppImportReq is the input for importing an app.
type AppImportReq struct {
	Name        string `json:"name" jsonschema:"description=应用名称"`
	Description string `json:"description,omitempty" jsonschema:"description=应用描述"`
	HTML        string `json:"html" jsonschema:"description=导入的 HTML 内容"`
}

// AppImportRsp is the output for importing an app.
type AppImportRsp struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (ts *AppsToolSet) importApp(
	ctx context.Context, req AppImportReq,
) (AppImportRsp, error) {
	app, err := ts.mgr.CreateAppFromImport(req.Name, req.Description, req.HTML)
	if err != nil {
		return AppImportRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return AppImportRsp{
		Success:  true,
		FilePath: app.FilePath,
		Message:  fmt.Sprintf("应用 %q 已导入，保存于 %s", req.Name, app.FilePath),
	}, nil
}

// AppCloneReq is the input for cloning a website.
type AppCloneReq struct {
	URL        string `json:"url" jsonschema:"description=要克隆的网站 URL"`
	MaxPages   int    `json:"max_pages,omitempty" jsonschema:"description=最大页面数量（0=无限制）"`
	MaxDepth   int    `json:"max_depth,omitempty" jsonschema:"description=最大链接深度（0=无限制）"`
	Subdomains bool   `json:"subdomains,omitempty" jsonschema:"description=是否包含子域名"`
	Scroll     bool   `json:"scroll,omitempty" jsonschema:"description=是否滚动加载懒加载内容"`
	Timeout    int    `json:"timeout,omitempty" jsonschema:"description=页面渲染超时秒数"`
	Workers    int    `json:"workers,omitempty" jsonschema:"description=并发工作线程数"`
}

// AppCloneRsp is the output for cloning a website.
type AppCloneRsp struct {
	Success   bool   `json:"success"`
	Host      string `json:"host,omitempty"`
	OutputDir string `json:"output_dir,omitempty"`
	Pages     int    `json:"pages,omitempty"`
	Assets    int    `json:"assets,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Duration  string `json:"duration,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	Errors    []string `json:"errors,omitempty"`
}

func (ts *AppsToolSet) cloneApp(
	ctx context.Context, req AppCloneReq,
) (AppCloneRsp, error) {
	opts := apps.CloneOptions{
		MaxPages:   req.MaxPages,
		MaxDepth:   req.MaxDepth,
		Subdomains: req.Subdomains,
		Scroll:     req.Scroll,
		Timeout:    req.Timeout,
		Workers:    req.Workers,
	}

	app, result, err := ts.mgr.CloneApp(ctx, req.URL, opts)
	if err != nil {
		return AppCloneRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return AppCloneRsp{
		Success:   result.Success,
		Host:      result.Host,
		OutputDir: result.OutputDir,
		Pages:     result.Pages,
		Assets:    result.Assets,
		SizeBytes: result.SizeBytes,
		Duration:  result.Duration,
		Message:   fmt.Sprintf("网站 %s 已克隆，共 %d 页面，%d 资源，保存于 %s", app.SourceURL, app.Pages, app.Assets, app.AppDir),
		Errors:    result.Errors,
	}, nil
}

// AppPackReq is the input for packing an app.
type AppPackReq struct {
	Name        string `json:"name" jsonschema:"description=应用名称"`
	Format      string `json:"format" jsonschema:"description=打包格式：html、zim、binary、app"`
	OutputPath  string `json:"output_path,omitempty" jsonschema:"description=输出路径（可选）"`
	BaseBinary  string `json:"base_binary,omitempty" jsonschema:"description=基础可执行文件路径（用于 binary/app 格式）"`
	IconPath    string `json:"icon_path,omitempty" jsonschema:"description=图标路径（可选）"`
	Compress    bool   `json:"compress,omitempty" jsonschema:"description=是否压缩"`
}

// AppPackRsp is the output for packing an app.
type AppPackRsp struct {
	Success        bool     `json:"success"`
	Format         string   `json:"format,omitempty"`
	OutputPath     string   `json:"output_path,omitempty"`
	SizeBytes      int64    `json:"size_bytes,omitempty"`
	Duration       string   `json:"duration,omitempty"`
	FilesProcessed int      `json:"files_processed,omitempty"`
	AssetsIncluded int      `json:"assets_included,omitempty"`
	Message        string   `json:"message,omitempty"`
	Error          string   `json:"error,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

func (ts *AppsToolSet) packApp(
	ctx context.Context, req AppPackReq,
) (AppPackRsp, error) {
	opts := apps.PackOptions{
		Format:     req.Format,
		OutputPath: req.OutputPath,
		BaseBinary: req.BaseBinary,
		IconPath:   req.IconPath,
		Compress:   req.Compress,
	}

	result, err := ts.mgr.PackApp(ctx, req.Name, opts)
	if err != nil {
		return AppPackRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return AppPackRsp{
		Success:        result.Success,
		Format:         result.Format,
		OutputPath:     result.OutputPath,
		SizeBytes:      result.SizeBytes,
		Duration:       result.Duration,
		FilesProcessed: result.FilesProcessed,
		AssetsIncluded: result.AssetsIncluded,
		Message:        fmt.Sprintf("应用 %s 已打包为 %s 格式，输出于 %s", req.Name, req.Format, result.OutputPath),
		Errors:         result.Errors,
	}, nil
}

// AppPreviewReq is the input for previewing an app.
type AppPreviewReq struct {
	Name string `json:"name" jsonschema:"description=应用名称"`
}

// AppPreviewRsp is the output for previewing an app.
type AppPreviewRsp struct {
	Success bool   `json:"success"`
	URL     string `json:"url,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *AppsToolSet) previewApp(
	ctx context.Context, req AppPreviewReq,
) (AppPreviewRsp, error) {
	result, err := ts.mgr.PreviewApp(ctx, req.Name)
	if err != nil {
		return AppPreviewRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return AppPreviewRsp{
		Success: result.Success,
		URL:     result.URL,
		Message: result.Message,
	}, nil
}

// AppExportReq is the input for exporting an app.
type AppExportReq struct {
	Name       string `json:"name" jsonschema:"description=应用名称"`
	OutputPath string `json:"output_path,omitempty" jsonschema:"description=输出文件路径（可选）"`
}

// AppExportRsp is the output for exporting an app.
type AppExportRsp struct {
	Success    bool   `json:"success"`
	OutputPath string `json:"output_path,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Message    string `json:"message,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (ts *AppsToolSet) exportApp(
	ctx context.Context, req AppExportReq,
) (AppExportRsp, error) {
	result, err := ts.mgr.ExportApp(req.Name, req.OutputPath)
	if err != nil {
		return AppExportRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return AppExportRsp{
		Success:    result.Success,
		OutputPath: result.OutputPath,
		Size:       result.Size,
		Message:    result.Message,
	}, nil
}
