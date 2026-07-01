// Package summon provides A2A (Agent-to-Agent) protocol integration
// for sub-agent delegation and remote agent communication.
//
// Uses tRPC-Agent-Go's server/a2a package for the server side and
// a2aagent for the client side, providing full protocol support
// with streaming, session management, and authentication.
package summon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// AuthConfig holds authentication configuration for A2A connections.
// Used by the credential rotator (auth.go) for connection security.
type AuthConfig struct {
	Type              string   `yaml:"type"` // jwt, api_key, oauth2
	APIKey            string   `yaml:"api_key"`
	APIKeyHeader      string   `yaml:"api_key_header"`
	JWTSecret         string   `yaml:"jwt_secret"`
	JWTAudience       string   `yaml:"jwt_audience"`
	JWTIssuer         string   `yaml:"jwt_issuer"`
	JWTLifetime       string   `yaml:"jwt_lifetime"`
	OAuthTokenURL     string   `yaml:"oauth_token_url"`
	OAuthClientID     string   `yaml:"oauth_client_id"`
	OAuthClientSecret string   `yaml:"oauth_client_secret"`
	OAuthScopes       []string `yaml:"oauth_scopes"`
}

// A2AServer wraps tRPC-Agent-Go's A2A server to expose a local
// agent as an A2A-compatible service endpoint.
//
// Unlike the previous low-level trpc-a2a-go implementation, this
// uses the official server/a2a wrapper which provides:
//   - Automatic message/event protocol conversion
//   - Streaming support (TaskArtifactUpdate or Message mode)
//   - Session and Memory service propagation
//   - AgentCard auto-generation with tool discovery
//   - Customizable hooks for message processing
type A2AServer struct {
	server   *http.Server
	a2aAgent agent.Agent
	address  string
}

// A2AServerConfig configures the A2A server.
type A2AServerConfig struct {
	// Agent is the agent to expose via A2A protocol.
	Agent agent.Agent
	// Runner is the runner to use (auto-created if nil).
	Runner runner.Runner
	// SessionService is the session service for conversation state.
	SessionService session.Service
	// Name is the agent name in the AgentCard.
	Name string
	// Description is the agent description in the AgentCard.
	Description string
	// Host is the server host (e.g., "localhost:8080").
	Host string
	// Streaming enables streaming responses.
	Streaming bool
}

// NewA2AServer creates an A2A server using tRPC-Agent-Go's
// official server/a2a package.
//
// The tRPC-A2A-Go API has two modes:
//  1. WithAgent(agent, streaming) — auto-creates a Runner; must
//     NOT be combined with WithRunner.
//  2. WithAgentCard(card) + WithRunner(runner) — manually provides
//     a Runner with an AgentCard for discovery metadata.
//
// When a Runner is provided (shared with the main agent loop),
// we use mode 2 to avoid the conflict. Otherwise, mode 1 is used
// for standalone A2A servers.
func NewA2AServer(cfg *A2AServerConfig) (*A2AServer, error) {
	if cfg.Agent == nil {
		return nil, fmt.Errorf("agent is required for A2A server")
	}

	var opts []a2aserver.Option

	if cfg.Runner != nil {
		// Mode 2: AgentCard + Runner (shared runner mode).
		// Build an AgentCard from config metadata so the server
		// can advertise its capabilities for A2A discovery.
		agentCard, err := a2aserver.NewAgentCard(
			cfg.Name, cfg.Description, cfg.Host, cfg.Streaming,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"create agent card: %w", err)
		}

		opts = append(opts,
			a2aserver.WithAgentCard(agentCard),
			a2aserver.WithRunner(cfg.Runner),
			a2aserver.WithHost(cfg.Host),
		)
	} else {
		// Mode 1: Agent only (auto-creates Runner internally).
		opts = append(opts,
			a2aserver.WithAgent(cfg.Agent, cfg.Streaming),
			a2aserver.WithHost(cfg.Host),
		)
	}

	if cfg.SessionService != nil {
		opts = append(opts,
			a2aserver.WithSessionService(cfg.SessionService))
	}

	// Enable streaming via TaskArtifactUpdate events (default).
	if cfg.Streaming {
		opts = append(opts,
			a2aserver.WithStreamingEventType(
				a2aserver.StreamingEventTypeTaskArtifactUpdate,
			),
		)
	}

	srv, err := a2aserver.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create a2a server: %w", err)
	}

	return &A2AServer{
		address:  cfg.Host,
		a2aAgent: cfg.Agent,
		server: &http.Server{
			Addr:    cfg.Host,
			Handler: srv.Handler(),
		},
	}, nil
}

// Start begins listening for A2A connections in a new goroutine.
func (s *A2AServer) Start(addr string) {
	if addr != "" {
		s.address = addr
		s.server.Addr = addr
	}

	util.Logger.Info("A2A server starting",
		slog.String("address", s.address),
	)

	go func() {
		if err := s.server.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			util.Logger.Error("A2A server error",
				slog.String("error", err.Error()))
		}
	}()
}

// Stop gracefully shuts down the A2A server.
func (s *A2AServer) Stop(ctx context.Context) error {
	util.Logger.Info("A2A server stopping")
	return s.server.Shutdown(ctx)
}

// A2AAgent wraps a remote A2A agent as a local agent.Agent that can
// be used as a sub-agent or tool. Uses tRPC-Agent-Go's a2aagent package.
type A2AAgent struct {
	a2aAgent   agent.Agent
	name       string
	serverURL  string
}

// NewA2AAgent creates a client-side proxy for a remote A2A service.
// The returned agent implements the agent.Agent interface and can be
// used directly with a Runner or as a sub-agent in workflows.
func NewA2AAgent(cfg *A2AAgentConfig) (*A2AAgent, error) {
	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("server_url is required for A2A agent")
	}

	var opts []a2aagent.Option
	opts = append(opts,
		a2aagent.WithAgentCardURL(cfg.ServerURL),
	)

	if cfg.EnableStreaming {
		opts = append(opts,
			a2aagent.WithEnableStreaming(true),
		)
	}

	// Transfer state keys (metadata propagation).
	if len(cfg.TransferStateKeys) > 0 {
		opts = append(opts,
			a2aagent.WithTransferStateKey(cfg.TransferStateKeys...),
		)
	}

	// Custom HTTP headers for authentication.
	if len(cfg.Headers) > 0 {
		opts = append(opts,
			a2aagent.WithUserIDHeader(cfg.UserIDHeaderName),
		)
	}

	ag, err := a2aagent.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create a2a agent: %w", err)
	}

	return &A2AAgent{
		a2aAgent:  ag,
		name:      cfg.Name,
		serverURL: cfg.ServerURL,
	}, nil
}

// Agent returns the underlying agent.Agent instance.
func (a *A2AAgent) Agent() agent.Agent {
	return a.a2aAgent
}

// A2AAgentConfig configures an A2A agent client.
type A2AAgentConfig struct {
	// Name is the unique identifier for this remote agent.
	Name string
	// ServerURL is the A2A server URL
	// (e.g., "http://localhost:8080").
	ServerURL string
	// EnableStreaming enables streaming from the remote agent.
	EnableStreaming bool
	// TransferStateKeys specifies which state keys to propagate
	// via metadata to the remote agent.
	TransferStateKeys []string
	// Headers are custom HTTP headers (e.g., auth tokens).
	Headers map[string]string
	// UserIDHeaderName overrides the default X-User-ID header.
	UserIDHeaderName string
}

// NewA2AAgentFromConfig creates an A2A agent from a Wukong
// A2ARemoteConfig.
func NewA2AAgentFromConfig(
	remote config.A2ARemoteConfig,
) (*A2AAgent, error) {
	if remote.Name == "" {
		return nil, fmt.Errorf("remote agent name is required")
	}

	agentCfg := &A2AAgentConfig{
		Name:            remote.Name,
		ServerURL:       remote.ServerURL,
		EnableStreaming: true,
	}

	// Build auth headers from the remote config.
	if remote.AuthType != "" {
		headers := buildAuthHeaders(remote)
		agentCfg.Headers = headers
	}

	return NewA2AAgent(agentCfg)
}

// buildAuthHeaders constructs HTTP headers for A2A authentication.
func buildAuthHeaders(remote config.A2ARemoteConfig) map[string]string {
	headers := make(map[string]string)

	switch remote.AuthType {
	case "api_key":
		headerName := remote.APIKeyHeader
		if headerName == "" {
			headerName = "X-API-Key"
		}
		headers[headerName] = remote.APIKey
	case "jwt":
		if remote.JWTSecret != "" {
			headers["Authorization"] = "Bearer " + remote.JWTSecret
		}
	case "oauth2":
		// OAuth2 tokens are managed by the credential rotator
		// at runtime. The initial client_secret is used.
		if remote.OAuthClientSecret != "" {
			headers["Authorization"] =
				"Bearer " + remote.OAuthClientSecret
		}
	}

	return headers
}

// RemoteDelegateTool creates a tool from a remote A2A agent that
// the main agent can call to delegate tasks.
//
// The returned tool uses agenttool.NewTool to wrap the A2A agent,
// enabling the parent agent to invoke it as a callable sub-agent
// with final-only response mode (no inner streaming to parent).
func RemoteDelegateTool(
	name, description string, a2aAgent agent.Agent,
) tool.Tool {
	return agenttool.NewTool(
		a2aAgent,
		agenttool.WithSkipSummarization(false),
		agenttool.WithStreamInner(false),
		agenttool.WithResponseMode(
			agenttool.ResponseModeFinalOnly,
		),
	)
}

// ContainsKeyword checks if a string contains a keyword (case-insensitive).
func ContainsKeyword(s, keyword string) bool {
	return strings.Contains(
		strings.ToLower(s), strings.ToLower(keyword),
	)
}
