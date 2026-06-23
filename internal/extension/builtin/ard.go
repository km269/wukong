// Package builtin provides built-in extension tool sets for Wukong.
package builtin

import (
	"context"

	"github.com/km269/wukong/internal/ard"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ARDToolSet provides Agentic Resource Discovery tools.
type ARDToolSet struct {
	tools     []tool.Tool
	ardToolSet *ard.ToolSet
	inited     bool
	closed     bool
}

// NewARDToolSet creates a new ARD tool set.
func NewARDToolSet(registryURL, catalogPath string) (*ARDToolSet, error) {
	ts := &ARDToolSet{}
	
	ardTS, err := ard.NewToolSet(registryURL, catalogPath)
	if err != nil {
		return nil, err
	}
	ts.ardToolSet = ardTS
	
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.ardSearch,
			function.WithName("ard_search"),
			function.WithDescription(
				"搜索 ARD 目录中的 AI Agent 和 MCP Server 资源。使用自然语言查询查找匹配的工具或能力。",
			),
		),
		function.NewFunctionTool(
			ts.ardDiscover,
			function.WithName("ard_discover"),
			function.WithDescription(
				"从远程 ARD Registry 发现资源。支持联邦搜索跨多个注册表查询。",
			),
		),
		function.NewFunctionTool(
			ts.ardList,
			function.WithName("ard_list"),
			function.WithDescription(
				"列出 ARD 目录中的所有注册资源。",
			),
		),
		function.NewFunctionTool(
			ts.ardGet,
			function.WithName("ard_get"),
			function.WithDescription(
				"获取指定 URN 标识符的资源详情。URN 格式：urn:air:<publisher>:<namespace>:<name>",
			),
		),
		function.NewFunctionTool(
			ts.ardRegister,
			function.WithName("ard_register"),
			function.WithDescription(
				"向 ARD 目录注册新的 Agent 或 MCP Server。",
			),
		),
		function.NewFunctionTool(
			ts.ardUnregister,
			function.WithName("ard_unregister"),
			function.WithDescription(
				"从 ARD 目录取消注册资源。",
			),
		),
		function.NewFunctionTool(
			ts.ardExport,
			function.WithName("ard_export"),
			function.WithDescription(
				"导出 ARD 目录为 JSON 格式。",
			),
		),
	}
	
	ts.inited = true
	return ts, nil
}

// Tools returns the available tools.
func (a *ARDToolSet) Tools(ctx context.Context) []tool.Tool {
	return a.tools
}

// Name returns the tool set name.
func (a *ARDToolSet) Name() string {
	return "ard"
}

// Init initializes the tool set.
func (a *ARDToolSet) Init(ctx context.Context) error {
	return nil
}

// Close closes the tool set.
func (a *ARDToolSet) Close() error {
	a.closed = true
	return nil
}

// SearchReq is the input for ARD search.
type SearchReq struct {
	Query string `json:"query" jsonschema:"description=搜索查询，如 'html应用' 或 '浏览器自动化'"`
	Type  string `json:"type,omitempty" jsonschema:"description=资源类型过滤：application/a2a-agent-card+json 或 application/mcp-server-card+json"`
}

// SearchRsp is the output for ARD search.
type SearchRsp struct {
	Results []SearchResult `json:"results"`
	Total   int           `json:"total"`
}

// SearchResult is a single search result.
type SearchResult struct {
	Identifier  string  `json:"identifier"`
	DisplayName string `json:"display_name"`
	Type       string `json:"type"`
	URL        string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
	Score      float64 `json:"score"`
}

// ardSearch searches for resources in the ARD catalog.
func (a *ARDToolSet) ardSearch(ctx context.Context, req SearchReq) (SearchRsp, error) {
	filters := make(map[string]any)
	if req.Type != "" {
		filters["type"] = req.Type
	}
	
	resp, err := a.ardToolSet.Search(ctx, req.Query, filters)
	if err != nil {
		return SearchRsp{}, err
	}
	
	results := make([]SearchResult, len(resp.Results))
	for i, r := range resp.Results {
		results[i] = SearchResult{
			Identifier:  r.Identifier,
			DisplayName: r.DisplayName,
			Type:       r.Type,
			URL:        r.URL,
			Description: r.Description,
			Score:      r.Score,
		}
	}
	
	return SearchRsp{
		Results: results,
		Total:   resp.Total,
	}, nil
}

// DiscoverReq is the input for ARD discover.
type DiscoverReq struct {
	Query string `json:"query" jsonschema:"description=发现查询"`
}

// DiscoverRsp is the output for ARD discover.
type DiscoverRsp struct {
	Results []SearchResult `json:"results"`
}

// ardDiscover discovers resources from remote ARD registries.
func (a *ARDToolSet) ardDiscover(ctx context.Context, req DiscoverReq) (DiscoverRsp, error) {
	results, err := a.ardToolSet.Discover(ctx, req.Query)
	if err != nil {
		return DiscoverRsp{}, err
	}
	
	searchResults := make([]SearchResult, len(results))
	for i, r := range results {
		searchResults[i] = SearchResult{
			Identifier:  r.Identifier,
			DisplayName: r.DisplayName,
			Type:       r.Type,
			URL:        r.URL,
			Description: r.Description,
			Score:      r.Score,
		}
	}
	
	return DiscoverRsp{Results: searchResults}, nil
}

// ListRsp is the output for ARD list.
type ListRsp struct {
	Entries []EntryInfo `json:"entries"`
	Total   int         `json:"total"`
}

// EntryInfo is entry information.
type EntryInfo struct {
	Identifier  string `json:"identifier"`
	DisplayName string `json:"display_name"`
	Type       string `json:"type"`
	Description string `json:"description,omitempty"`
}

// ardList lists all registered resources.
func (a *ARDToolSet) ardList(ctx context.Context, req struct{}) (ListRsp, error) {
	entries := a.ardToolSet.List()
	
	entryInfos := make([]EntryInfo, len(entries))
	for i, e := range entries {
		entryInfos[i] = EntryInfo{
			Identifier:  e.Identifier,
			DisplayName: e.DisplayName,
			Type:       e.Type,
			Description: e.Description,
		}
	}
	
	return ListRsp{
		Entries: entryInfos,
		Total:   len(entryInfos),
	}, nil
}

// GetReq is the input for ARD get.
type GetReq struct {
	Identifier string `json:"identifier" jsonschema:"description=资源 URN 标识符"`
}

// GetRsp is the output for ARD get.
type GetRsp struct {
	Identifier   string   `json:"identifier"`
	DisplayName  string   `json:"display_name"`
	Type        string   `json:"type"`
	URL         string   `json:"url,omitempty"`
	Description string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// ardGet gets a specific resource by identifier.
func (a *ARDToolSet) ardGet(ctx context.Context, req GetReq) (GetRsp, error) {
	entry := a.ardToolSet.Get(req.Identifier)
	if entry == nil {
		return GetRsp{}, nil
	}
	
	return GetRsp{
		Identifier:   entry.Identifier,
		DisplayName:  entry.DisplayName,
		Type:        entry.Type,
		URL:         entry.URL,
		Description: entry.Description,
		Capabilities: entry.Capabilities,
		Tags:        entry.Tags,
	}, nil
}

// RegisterReq is the input for ARD register.
type RegisterReq struct {
	Identifier  string `json:"identifier" jsonschema:"description=URN 标识符"`
	DisplayName string `json:"display_name" jsonschema:"description=显示名称"`
	Type       string `json:"type" jsonschema:"description=资源类型"`
	URL        string `json:"url,omitempty" jsonschema:"description=资源 URL"`
	Description string `json:"description,omitempty" jsonschema:"description=资源描述"`
}

// RegisterRsp is the output for ARD register.
type RegisterRsp struct {
	Success     bool   `json:"success"`
	Identifier string `json:"identifier,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ardRegister registers a new resource in the ARD catalog.
func (a *ARDToolSet) ardRegister(ctx context.Context, req RegisterReq) (RegisterRsp, error) {
	entry := ard.CatalogEntry{
		Identifier:  req.Identifier,
		DisplayName: req.DisplayName,
		Type:       req.Type,
		URL:        req.URL,
		Description: req.Description,
	}
	
	if err := a.ardToolSet.Register(entry); err != nil {
		return RegisterRsp{Success: false, Error: err.Error()}, nil
	}
	
	return RegisterRsp{Success: true, Identifier: req.Identifier}, nil
}

// UnregisterReq is the input for ARD unregister.
type UnregisterReq struct {
	Identifier string `json:"identifier" jsonschema:"description=资源 URN 标识符"`
}

// UnregisterRsp is the output for ARD unregister.
type UnregisterRsp struct {
	Success     bool   `json:"success"`
	Identifier string `json:"identifier,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ardUnregister unregisters a resource from the ARD catalog.
func (a *ARDToolSet) ardUnregister(ctx context.Context, req UnregisterReq) (UnregisterRsp, error) {
	if err := a.ardToolSet.Unregister(req.Identifier); err != nil {
		return UnregisterRsp{Success: false, Error: err.Error()}, nil
	}
	
	return UnregisterRsp{Success: true, Identifier: req.Identifier}, nil
}

// ExportRsp is the output for ARD export.
type ExportRsp struct {
	Catalog string `json:"catalog"`
	Error  string `json:"error,omitempty"`
}

// ardExport exports the ARD catalog as JSON.
func (a *ARDToolSet) ardExport(ctx context.Context, req struct{}) (ExportRsp, error) {
	data, err := a.ardToolSet.ExportCatalog()
	if err != nil {
		return ExportRsp{Error: err.Error()}, nil
	}
	
	return ExportRsp{Catalog: string(data)}, nil
}
