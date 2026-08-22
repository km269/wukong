// Package agent provides a Dify AI platform integration wrapping the
// Dify Chat API as a tRPC-Agent-Go compatible Agent.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// DifyAgent implements agent.Agent for the Dify Chat API.
type DifyAgent struct {
	name    string
	baseURL string
	secret  string
	stream  bool
	timeout time.Duration
}

// NewDifyAgent creates a DifyAgent from configuration.
func NewDifyAgent(cfg *config.DifyConfig) (*DifyAgent, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("dify base_url is required")
	}
	if cfg.APISecret == "" {
		return nil, fmt.Errorf("dify api_secret is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &DifyAgent{
		name:    cfg.AgentName,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		secret:  cfg.APISecret,
		stream:  cfg.EnableStreaming,
		timeout: timeout,
	}, nil
}

func (a *DifyAgent) Run(
	ctx context.Context, inv *agent.Invocation,
) (<-chan *event.Event, error) {
	if a.stream {
		return a.runStream(ctx, inv)
	}
	return a.runBlocking(ctx, inv)
}

func (a *DifyAgent) Info() agent.Info {
	return agent.Info{Name: a.name, Description: "Dify AI Platform Agent"}
}
func (a *DifyAgent) SubAgents() []agent.Agent        { return nil }
func (a *DifyAgent) FindSubAgent(string) agent.Agent { return nil }
func (a *DifyAgent) Tools() []tool.Tool              { return nil }

// --- blocking ---

func (a *DifyAgent) runBlocking(
	ctx context.Context, inv *agent.Invocation,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event, 1)
	go func() {
		defer close(out)
		q := extractDifyQuery(inv)
		result, err := a.chatBlocking(ctx, q)
		if err != nil {
			out <- event.New(inv.InvocationID, a.name,
				event.WithObject("error"),
				event.WithResponse(&model.Response{
					Error: &model.ResponseError{Message: err.Error()},
				}))
			return
		}
		out <- event.NewResponseEvent(inv.InvocationID, a.name,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage(result),
				}},
			})
	}()
	return out, nil
}

// --- streaming SSE ---

func (a *DifyAgent) runStream(
	ctx context.Context, inv *agent.Invocation,
) (<-chan *event.Event, error) {
	out := make(chan *event.Event, 64)
	go func() {
		defer close(out)
		q := extractDifyQuery(inv)
		body, err := a.chatStreaming(ctx, q)
		if err != nil {
			select {
			case out <- event.New(inv.InvocationID, a.name,
				event.WithObject("error"),
				event.WithResponse(&model.Response{
					Error: &model.ResponseError{Message: err.Error()},
				})):
			case <-ctx.Done():
			}
			return
		}
		defer body.Close()

		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var full string
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				break
			}
			var c struct {
				Event  string `json:"event"`
				Answer string `json:"answer"`
			}
			if json.Unmarshal([]byte(payload), &c) != nil {
				continue
			}
			if c.Event == "error" {
				select {
				case out <- event.New(inv.InvocationID, a.name,
					event.WithObject("error"),
					event.WithResponse(&model.Response{
						Error: &model.ResponseError{Message: "dify SSE error"},
					})):
				case <-ctx.Done():
				}
				return
			}
			if c.Event == "message" || c.Event == "agent_message" {
				full += c.Answer
				choice := model.Choice{Index: 0}
				choice.Delta.Content = c.Answer
				select {
				case out <- event.NewResponseEvent(inv.InvocationID,
					a.name,
					&model.Response{Choices: []model.Choice{choice}}):
				case <-ctx.Done():
					return
				}
			}
		}
		select {
		case out <- event.NewResponseEvent(inv.InvocationID, a.name,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage(full),
				}},
			}):
		case <-ctx.Done():
		}
	}()
	return out, nil
}

// --- API calls ---

func (a *DifyAgent) chatBlocking(
	ctx context.Context, query string,
) (string, error) {
	payload := map[string]any{
		"query":         query,
		"response_mode": "blocking",
		"user":          "wukong-dify",
	}
	body, err := a.doRequest(ctx, payload)
	if err != nil {
		return "", err
	}
	defer body.Close()
	var result struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		return "", fmt.Errorf("dify parse: %w", err)
	}
	return result.Answer, nil
}

func (a *DifyAgent) chatStreaming(
	ctx context.Context, query string,
) (io.ReadCloser, error) {
	payload := map[string]any{
		"query":         query,
		"response_mode": "streaming",
		"user":          "wukong-dify",
	}
	return a.doRequest(ctx, payload)
}

func (a *DifyAgent) doRequest(
	ctx context.Context, payload map[string]any,
) (io.ReadCloser, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, a.baseURL+"/chat-messages",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("dify build: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.secret)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: a.timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dify request: %w", err)
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("dify %d: %s", resp.StatusCode, string(b))
	}
	return resp.Body, nil
}

func extractDifyQuery(inv *agent.Invocation) string {
	if inv == nil || inv.Message.Content == "" {
		return "Hello"
	}
	return inv.Message.Content
}

// BuildDify creates a Dify agent from Wukong config.
func BuildDify(ctx context.Context, cfg *config.WukongConfig) (agent.Agent, error) {
	ag, err := NewDifyAgent(&cfg.Dify)
	if err != nil {
		return nil, fmt.Errorf("create dify agent: %w", err)
	}
	util.Logger.Info("dify agent: initialized",
		slog.String("base_url", cfg.Dify.BaseURL))
	return ag, nil
}
