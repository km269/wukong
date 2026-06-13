// Package provider provides an ACP (Agent Client Protocol) provider
// that enables using ACP-compatible coding agents as LLM providers
// within Wukong.
//
// The ACP Provider communicates with remote ACP agents via the
// message/send endpoint and exposes Wukong extensions as an MCP
// Server so that ACP agents can directly call system tools.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
)

// ACPProvider implements model.Model for ACP-compatible agents.
// It translates between tRPC request/response format and the
// ACP message/send protocol over HTTP.
type ACPProvider struct {
	name     string
	agentURL string
	modelID  string
	client   *http.Client
	mcpAddr  string // MCP bridge address for tool passthrough
}

// NewACPProvider creates an ACP provider from configuration.
func NewACPProvider(
	p *config.ProviderConfig,
	mcpAddr string,
) (*ACPProvider, error) {
	if p.AgentURL == "" {
		return nil, fmt.Errorf(
			"acp provider requires agent_url")
	}

	timeout := 300 * time.Second

	return &ACPProvider{
		name:     p.Name,
		agentURL: p.AgentURL,
		modelID:  p.Model,
		client: &http.Client{
			Timeout: timeout,
		},
		mcpAddr: mcpAddr,
	}, nil
}

// Info returns model metadata.
func (p *ACPProvider) Info() model.Info {
	modelName := p.modelID
	if modelName == "" {
		modelName = "acp-default"
	}
	return model.Info{
		Name: modelName,
	}
}

// GenerateContent sends a request to the ACP agent and returns
// the response as a tRPC-compatible response channel.
func (p *ACPProvider) GenerateContent(
	ctx context.Context, req *model.Request,
) (<-chan *model.Response, error) {
	// Extract the last user message for the ACP agent.
	userContent := extractLastUserContent(req.Messages)
	if userContent == "" {
		return nil, fmt.Errorf(
			"acp provider: no user message found")
	}

	// Build the ACP message/send request body, including
	// MCP bridge info for tool discovery.
	acpReq := buildACPRequest(userContent, p.mcpAddr)

	acpResp, err := p.sendACPRequest(ctx, acpReq)
	if err != nil {
		return nil, fmt.Errorf(
			"acp provider: send request: %w", err)
	}

	// Return the ACP response as a single tRPC Response.
	out := make(chan *model.Response, 1)
	go func() {
		defer close(out)
		select {
		case out <- acpResp:
		case <-ctx.Done():
		}
	}()

	return out, nil
}

// ACPRequest is the request body for the ACP message/send endpoint.
type ACPRequest struct {
	Message string `json:"message"`
	// MCPConfig provides the MCP server endpoint so the ACP agent
	// can discover and call Wukong extension tools.
	MCPConfig *ACPMCPConfig `json:"mcp_config,omitempty"`
	// SessionID is optional for multi-turn conversations.
	SessionID string `json:"session_id,omitempty"`
}

// ACPMCPConfig is embedded in ACP requests to inform the agent
// about available MCP tools.
type ACPMCPConfig struct {
	ServerURL string   `json:"server_url"`
	Tools     []string `json:"tool_names,omitempty"`
}

// ACPResponse is the response from the ACP agent's message/send.
type ACPResponse struct {
	Content    string            `json:"content,omitempty"`
	ToolCalls  []ACPToolCall     `json:"tool_calls,omitempty"`
	Error      string            `json:"error,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ACPToolCall describes a tool call requested by the ACP agent.
type ACPToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// buildACPRequest constructs an ACP request body.
func buildACPRequest(
	userContent string, mcpAddr string,
) *ACPRequest {
	req := &ACPRequest{
		Message: userContent,
	}
	if mcpAddr != "" {
		req.MCPConfig = &ACPMCPConfig{
			ServerURL: mcpAddr,
		}
	}
	return req
}

// sendACPRequest sends the request to the ACP agent endpoint.
func (p *ACPProvider) sendACPRequest(
	ctx context.Context, acpReq *ACPRequest,
) (*model.Response, error) {
	body, err := json.Marshal(acpReq)
	if err != nil {
		return nil, fmt.Errorf("marshal acp request: %w", err)
	}

	url := p.agentURL + "/message/send"
	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url, bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent",
		"Wukong-ACP-Provider/1.0")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf(
			"acp agent returned %d: %s",
			resp.StatusCode, string(bodyBytes),
		)
	}

	var acpResp ACPResponse
	if err := json.NewDecoder(resp.Body).Decode(
		&acpResp,
	); err != nil {
		return nil, fmt.Errorf(
			"decode acp response: %w", err)
	}

	if acpResp.Error != "" {
		return &model.Response{
			Error: &model.ResponseError{
				Message: acpResp.Error,
			},
			Done: true,
		}, nil
	}

	// Build tRPC Response from ACP response.
	tResp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: acpResp.Content,
			},
		}},
	}

	// Map ACP tool calls to tRPC tool calls.
	// The tRPC model.ToolCall uses a Function field containing
	// Name (string) and Arguments ([]byte).
	if len(acpResp.ToolCalls) > 0 {
		tResp.Choices[0].Message.ToolCalls = make(
			[]model.ToolCall, len(acpResp.ToolCalls),
		)
		for i, tc := range acpResp.ToolCalls {
			tResp.Choices[0].Message.ToolCalls[i] = model.ToolCall{
				ID: tc.ID,
			}
			// Set function name and arguments on the tool call.
			tResp.Choices[0].Message.ToolCalls[i].Function.Name = tc.Name
			tResp.Choices[0].Message.ToolCalls[i].Function.Arguments =
				[]byte(tc.Arguments)
		}
	}

	util.Logger.Debug("acp provider: response received",
		"agent", p.name,
		"content_len", len(acpResp.Content),
		"tool_calls", len(acpResp.ToolCalls),
	)

	return tResp, nil
}

// extractLastUserContent extracts the last user message from
// a conversation.
func extractLastUserContent(msgs []model.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == model.RoleUser &&
			msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}
