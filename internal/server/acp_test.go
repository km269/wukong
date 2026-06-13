package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func TestNewACPServer_MissingRunner(t *testing.T) {
	_, err := NewACPServer(&ACPServerConfig{
		Runner: nil,
	})
	if err == nil {
		t.Error("expected error for nil runner")
	}
}

func TestACPServer_Health(t *testing.T) {
	mdl := openai.New("test-model")
	ag := llmagent.New("test-agent",
		llmagent.WithModel(mdl),
	)
	r := runner.NewRunner("test-app", ag)

	srv, err := NewACPServer(&ACPServerConfig{
		Runner: r,
		Agent:  ag,
		Path:   "/acp",
	})
	if err != nil {
		t.Fatalf("NewACPServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/acp/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health = %d, want 200", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
}

func TestACPServer_AgentCard(t *testing.T) {
	mdl := openai.New("test-model")
	ag := llmagent.New("test-agent",
		llmagent.WithModel(mdl),
	)
	r := runner.NewRunner("test-app", ag)

	srv, err := NewACPServer(&ACPServerConfig{
		Runner: r,
		Agent:  ag,
		Path:   "/acp",
	})
	if err != nil {
		t.Fatalf("NewACPServer: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodGet, "/acp/.well-known/agent.json", nil,
	)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("agent card = %d, want 200", w.Code)
	}

	var card map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&card); err != nil {
		t.Fatalf("decode card: %v", err)
	}

	if card["name"] != "test-agent" {
		t.Errorf("name = %q, want test-agent", card["name"])
	}
}

func TestACPServer_ToolsList(t *testing.T) {
	mdl := openai.New("test-model")
	ag := llmagent.New("test-agent",
		llmagent.WithModel(mdl),
	)
	r := runner.NewRunner("test-app", ag)

	srv, err := NewACPServer(&ACPServerConfig{
		Runner: r,
		Agent:  ag,
		Path:   "/acp",
	})
	if err != nil {
		t.Fatalf("NewACPServer: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodGet, "/acp/tools/list", nil,
	)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("tools list = %d, want 200", w.Code)
	}
}

func TestACPServer_MessageSend_MissingMessage(t *testing.T) {
	mdl := openai.New("test-model")
	ag := llmagent.New("test-agent",
		llmagent.WithModel(mdl),
	)
	r := runner.NewRunner("test-app", ag)

	srv, err := NewACPServer(&ACPServerConfig{
		Runner: r,
		Agent:  ag,
		Path:   "/acp",
	})
	if err != nil {
		t.Fatalf("NewACPServer: %v", err)
	}

	body := `{"user_id":"test","message":""}`
	req := httptest.NewRequest(
		http.MethodPost, "/acp/message/send",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing message = %d, want 400", w.Code)
	}
}

func TestACPServer_CORSHeaders(t *testing.T) {
	mdl := openai.New("test-model")
	ag := llmagent.New("test-agent",
		llmagent.WithModel(mdl),
	)
	r := runner.NewRunner("test-app", ag)

	srv, err := NewACPServer(&ACPServerConfig{
		Runner: r,
		Agent:  ag,
		Path:   "/acp",
	})
	if err != nil {
		t.Fatalf("NewACPServer: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodOptions, "/acp/message/send", nil,
	)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS header missing")
	}
}

func TestACPSSEEvent_JSON(t *testing.T) {
	evt := ACPSSEEvent{
		EventType: "text_delta",
		Data: map[string]string{
			"content": "hello world",
		},
	}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Logf("SSE event: %s", string(data))
}

func TestBuildAgentCardFromAgent(t *testing.T) {
	mdl := openai.New("test-model")
	ag := llmagent.New("test-agent",
		llmagent.WithModel(mdl),
		llmagent.WithDescription("A test agent"),
	)

	card := BuildAgentCardFromAgent(ag, "http://localhost:9091")
	if card["name"] != "test-agent" {
		t.Errorf("name = %q", card["name"])
	}
}
