// agui_test.go — tests for the AG-UI SSE endpoint contract and the
// embedded reference client (P1-6).
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/cors"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// fakeRunner replays a scripted event channel.
type fakeRunner struct {
	events []*event.Event
}

func (f *fakeRunner) Run(
	context.Context, string, string, model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (f *fakeRunner) Close() error { return nil }

var _ runner.Runner = (*fakeRunner)(nil)

// deltaEvt builds a streaming text-delta event.
func deltaEvt(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Delta: model.Message{
					Role: model.RoleAssistant, Content: content,
				},
			}},
		},
	}
}

func toolCallEvt() *event.Event {
	return &event.Event{
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Function: model.FunctionDefinitionParam{
						Name:      "web_search",
						Arguments: []byte(`{"query":"go"}`),
					},
				}},
			},
		}}},
	}
}

// TestHandleChatSSEFrames verifies the wire contract the reference
// client (and any external AG-UI consumer) depends on: request body
// parsing, text_delta streaming, tool_calls, and the terminating
// done event that carries session continuity.
func TestHandleChatSSEFrames(t *testing.T) {
	srv, err := NewAGUIServer(&AGUIConfig{
		Runner: &fakeRunner{events: []*event.Event{
			deltaEvt("Hello"),
			deltaEvt(", world"),
			toolCallEvt(),
		}},
	})
	if err != nil {
		t.Fatalf("NewAGUIServer: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/agui", "application/json",
		strings.NewReader(`{"user_id":"u1","session_id":"s1","message":"hi"}`),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	body := readAll(t, resp)
	for _, want := range []string{
		"event: text_delta",
		`{"content":"Hello"}`,
		`{"content":", world"}`,
		"event: tool_calls",
		`"name":"web_search"`,
		"event: done",
		`"session_id":"s1"`,
		`"full_text":"Hello, world"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q\ngot: %s", want, body)
		}
	}
}

func TestHandleChatValidation(t *testing.T) {
	srv, _ := NewAGUIServer(&AGUIConfig{Runner: &fakeRunner{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Empty message → 400.
	resp, err := http.Post(ts.URL+"/agui", "application/json",
		strings.NewReader(`{"message":""}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty message status = %d, want 400", resp.StatusCode)
	}

	// Runner error surfaces as an SSE error event.
	errSrv, _ := NewAGUIServer(&AGUIConfig{Runner: &errRunner{}})
	errTS := httptest.NewServer(errSrv.Handler())
	defer errTS.Close()
	resp2, err := http.Post(errTS.URL+"/agui", "application/json",
		strings.NewReader(`{"message":"hi"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp2.Body.Close()
	if body := readAll(t, resp2); !strings.Contains(body, "event: error") {
		t.Fatalf("body = %q, want error event", body)
	}
}

// errRunner always fails, exercising the SSE error path.
type errRunner struct{ fakeRunner }

func (e *errRunner) Run(
	context.Context, string, string, model.Message,
	...agent.RunOption,
) (<-chan *event.Event, error) {
	return nil, context.DeadlineExceeded
}

// TestHandlerIndexReferenceClient verifies the embedded reference
// client is served at "/" and unknown paths still 404.
func TestHandlerIndexReferenceClient(t *testing.T) {
	srv, _ := NewAGUIServer(&AGUIConfig{Runner: &fakeRunner{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	page := readAll(t, resp)
	for _, marker := range []string{
		"Wukong AG-UI Console",
		"text_delta",
		"tool_calls",
		"X-API-Key",
	} {
		if !strings.Contains(page, marker) {
			t.Errorf("reference client missing marker %q", marker)
		}
	}

	// Unknown path → 404 (not the index page).
	resp2, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", resp2.StatusCode)
	}

	// Health endpoint still wired.
	resp3, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp3.Body.Close()
	if !strings.Contains(readAll(t, resp3), `"status":"ok"`) {
		t.Error("health endpoint broken")
	}
}

// TestCORSLocalhostOnly sanity-checks the CORS helper the SSE
// headers rely on.
func TestCORSLocalhostOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "http://localhost:8080/agui", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	cors.SetLocalhostOnly(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("localhost origin should be allowed")
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	body := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		body = append(body, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(body)
}
