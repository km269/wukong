package mcpapps

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

// captureBridge creates a bridge whose outbound messages are captured by the
// caller via a channel and returnable for feeding back as inbound messages.
type captureBridge struct {
	bridge    *AppBridge
	sent      chan JSONRPCMessage
	sendError error
}

func newCaptureBridge() *captureBridge {
	c := &captureBridge{
		bridge: NewAppBridge(),
		sent:   make(chan JSONRPCMessage, 64),
	}
	c.bridge.SetOnSendCallback(func(msg JSONRPCMessage) error {
		if c.sendError != nil {
			return c.sendError
		}
		c.sent <- msg
		return nil
	})
	return c
}

// feedResponse feeds a JSON-RPC response for the given request back into the
// bridge. It must be called from a goroutine when unblocking a blocking
// Request call.
func (c *captureBridge) feedResponse(t *testing.T, out JSONRPCMessage, result json.RawMessage, errMsg *JSONRPCError) {
	t.Helper()
	resp := JSONRPCMessage{JSONRPC: "2.0", ID: out.ID, Result: result, Error: errMsg}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Errorf("marshal response: %v", err)
		return
	}
	if err := c.bridge.HandleMessage(data); err != nil {
		t.Errorf("HandleMessage() error = %v", err)
	}
}

func TestNewAppBridge(t *testing.T) {
	b := NewAppBridge()
	if b == nil {
		t.Fatal("NewAppBridge() = nil")
	}
	if b.IsInitialized() {
		t.Error("IsInitialized() = true on fresh bridge")
	}
}

func TestJSONRPCError_Error(t *testing.T) {
	e := &JSONRPCError{Code: ErrCodeMethodNotFound, Message: "Method not found"}
	if got := e.Error(); got != "jsonrpc error: Method not found (code -32601)" {
		t.Errorf("Error() = %q", got)
	}
}

func TestAppBridge_Request_ResolvesWithResult(t *testing.T) {
	c := newCaptureBridge()

	go func() {
		out := <-c.sent
		if out.ID == nil {
			t.Error("outgoing request missing ID")
			return
		}
		if out.Method != "ui/get-something" {
			t.Errorf("Method = %q", out.Method)
		}
		var params map[string]any
		if err := json.Unmarshal(out.Params, &params); err != nil {
			t.Errorf("unmarshal params: %v", err)
			return
		}
		if params["key"] != "value" {
			t.Errorf("params = %v", params)
		}
		c.feedResponse(t, out, json.RawMessage(`{"ok": true}`), nil)
	}()

	result, err := c.bridge.Request("ui/get-something", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok || resultMap["ok"] != true {
		t.Errorf("result = %v, want {\"ok\":true}", result)
	}
}

func TestAppBridge_Request_ErrorResponse(t *testing.T) {
	c := newCaptureBridge()

	go func() {
		out := <-c.sent
		c.feedResponse(t, out, nil, &JSONRPCError{
			Code:    ErrCodeInternalError,
			Message: "boom",
		})
	}()

	_, err := c.bridge.Request("ui/fail", nil)
	if err == nil {
		t.Fatal("Request() error = nil, want JSON-RPC error")
	}
	var jsonErr *JSONRPCError
	if !errors.As(err, &jsonErr) {
		t.Fatalf("Request() error type = %T, want *JSONRPCError", err)
	}
	if jsonErr.Code != ErrCodeInternalError || jsonErr.Message != "boom" {
		t.Errorf("error = %v", jsonErr)
	}
}

func TestAppBridge_Request_SendFailure(t *testing.T) {
	c := newCaptureBridge()
	c.sendError = errors.New("transport down")

	_, err := c.bridge.Request("ui/x", nil)
	if err == nil || !strings.Contains(err.Error(), "send request: transport down") {
		t.Fatalf("Request() error = %v, want containing %q", err, "send request: transport down")
	}
	// pendingReqs must be cleaned up, so a response for that ID is unknown.
	resp := JSONRPCMessage{JSONRPC: "2.0", ID: int64(0)}
	data, _ := json.Marshal(resp)
	if err := c.bridge.HandleMessage(data); err == nil || !strings.Contains(err.Error(), "unknown request ID") {
		t.Errorf("HandleMessage() after failed send = %v, want unknown request ID", err)
	}
}

func TestAppBridge_Request_MarshalParamsError(t *testing.T) {
	c := newCaptureBridge()

	_, err := c.bridge.Request("ui/x", func() {}) // func is not JSON-marshalable.
	if err == nil || !strings.Contains(err.Error(), "marshal params:") {
		t.Fatalf("Request() error = %v, want containing %q", err, "marshal params:")
	}
}

func TestAppBridge_Notify(t *testing.T) {
	c := newCaptureBridge()

	if err := c.bridge.Notify("ui/notifications/size-changed", map[string]any{"height": 42}); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	out := <-c.sent
	if out.ID != nil {
		t.Errorf("notification ID = %v, want nil", out.ID)
	}
	if out.Method != "ui/notifications/size-changed" {
		t.Errorf("Method = %q", out.Method)
	}
	var params map[string]any
	if err := json.Unmarshal(out.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["height"] != float64(42) {
		t.Errorf("params = %v", params)
	}
}

func TestAppBridge_Send_QueuesWithoutCallback(t *testing.T) {
	b := NewAppBridge()

	if err := b.Send(JSONRPCMessage{JSONRPC: "2.0", Method: "ui/x"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	msgs := b.GetQueuedMessages()
	if len(msgs) != 1 || msgs[0].Method != "ui/x" {
		t.Fatalf("GetQueuedMessages() = %v", msgs)
	}
	// Queue is drained after GetQueuedMessages.
	if msgs := b.GetQueuedMessages(); len(msgs) != 0 {
		t.Errorf("GetQueuedMessages() after drain = %v, want empty", msgs)
	}
}

func TestAppBridge_HandleMessage_InvalidJSON(t *testing.T) {
	b := NewAppBridge()

	err := b.HandleMessage([]byte("{not json"))
	if err == nil || !strings.Contains(err.Error(), "unmarshal message:") {
		t.Fatalf("HandleMessage() error = %v, want containing %q", err, "unmarshal message:")
	}
}

func TestAppBridge_HandleMessage_InvalidMessage(t *testing.T) {
	b := NewAppBridge()

	err := b.HandleMessage([]byte(`{"jsonrpc": "2.0"}`))
	if err == nil || err.Error() != "invalid JSON-RPC message" {
		t.Fatalf("HandleMessage() error = %v, want %q", err, "invalid JSON-RPC message")
	}
}

func TestAppBridge_HandleMessage_UnknownResponseID(t *testing.T) {
	b := NewAppBridge()

	err := b.HandleMessage([]byte(`{"jsonrpc": "2.0", "id": 99, "result": null}`))
	if err == nil || !strings.Contains(err.Error(), "unknown request ID: 99") {
		t.Fatalf("HandleMessage() error = %v, want containing %q", err, "unknown request ID: 99")
	}
}

type fakeRequestHandler struct {
	mu       sync.Mutex
	calls    []string
	result   any
	err      error
	jsonErr  *JSONRPCError
	gotParam json.RawMessage
}

func (f *fakeRequestHandler) HandleRequest(method string, params json.RawMessage) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method)
	if f.jsonErr != nil {
		return nil, f.jsonErr
	}
	if f.err != nil {
		return nil, f.err
	}
	f.gotParam = append([]byte(nil), params...)
	return f.result, nil
}

func TestAppBridge_HandleRequest_Success(t *testing.T) {
	b := NewAppBridge()
	h := &fakeRequestHandler{result: map[string]any{"value": 1}}
	b.SetRequestHandler(h)

	data := []byte(`{"jsonrpc": "2.0", "id": 7, "method": "ui/echo", "params": {"text": "hi"}}`)
	if err := b.HandleMessage(data); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	out := b.GetQueuedMessages()[0]
	if out.ID != float64(7) {
		t.Errorf("ID = %v, want 7", out.ID)
	}
	if out.Error != nil {
		t.Errorf("Error = %v, want nil on success", out.Error)
	}
	// No send callback configured, so the message is queued as-is and Result
	// retains the handler's original value.
	result, ok := out.Result.(map[string]any)
	if !ok || result["value"] != 1 {
		t.Errorf("result = %v", out.Result)
	}
	if len(h.calls) != 1 || h.calls[0] != "ui/echo" {
		t.Errorf("handler calls = %v", h.calls)
	}
	if string(h.gotParam) != `{"text": "hi"}` {
		t.Errorf("handler params = %s", h.gotParam)
	}
}

func TestAppBridge_HandleRequest_NoHandler(t *testing.T) {
	b := NewAppBridge()

	data := []byte(`{"jsonrpc": "2.0", "id": 1, "method": "ui/unknown"}`)
	if err := b.HandleMessage(data); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	out := b.GetQueuedMessages()[0]
	if out.Error == nil {
		t.Fatal("Error = nil, want Method not found")
	}
	if out.Error.Code != ErrCodeMethodNotFound || out.Error.Message != "Method not found" {
		t.Errorf("error = %v", out.Error)
	}
}

func TestAppBridge_HandleRequest_HandlerInternalError(t *testing.T) {
	b := NewAppBridge()
	b.SetRequestHandler(&fakeRequestHandler{err: errors.New("handler explosion")})

	data := []byte(`{"jsonrpc": "2.0", "id": 2, "method": "ui/x"}`)
	if err := b.HandleMessage(data); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	out := b.GetQueuedMessages()[0]
	if out.Error == nil || out.Error.Code != ErrCodeInternalError ||
		out.Error.Message != "handler explosion" {
		t.Errorf("error = %v, want code %d message %q", out.Error, ErrCodeInternalError, "handler explosion")
	}
}

func TestAppBridge_HandleRequest_HandlerJSONRPCError(t *testing.T) {
	b := NewAppBridge()
	b.SetRequestHandler(&fakeRequestHandler{
		jsonErr: &JSONRPCError{Code: ErrCodeInvalidParams, Message: "bad params"},
	})

	data := []byte(`{"jsonrpc": "2.0", "id": 3, "method": "ui/x"}`)
	if err := b.HandleMessage(data); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	out := b.GetQueuedMessages()[0]
	if out.Error == nil || out.Error.Code != ErrCodeInvalidParams ||
		out.Error.Message != "bad params" {
		t.Errorf("error = %v", out.Error)
	}
}

type fakeNotifyHandler struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeNotifyHandler) HandleNotification(method string, params json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method)
}

func TestAppBridge_HandleNotification_Dispatch(t *testing.T) {
	b := NewAppBridge()
	n := &fakeNotifyHandler{}
	b.SetNotifyHandler(n)

	data := []byte(`{"jsonrpc": "2.0", "method": "ui/notifications/size-changed", "params": {"height": 10}}`)
	if err := b.HandleMessage(data); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	if len(n.calls) != 1 || n.calls[0] != "ui/notifications/size-changed" {
		t.Fatalf("notify handler calls = %v", n.calls)
	}
}

func TestAppBridge_HandleNotification_NoHandlerNoError(t *testing.T) {
	b := NewAppBridge()

	err := b.HandleMessage([]byte(`{"jsonrpc": "2.0", "method": "ui/notifications/x"}`))
	if err != nil {
		t.Fatalf("HandleMessage() error = %v, want nil", err)
	}
}

type fakeMessageHandler struct {
	mu    sync.Mutex
	event *MessageEvent
}

func (f *fakeMessageHandler) OnMessage(event *MessageEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.event = event
}

func TestAppBridge_HandleMessage_FiresMessageHandler(t *testing.T) {
	b := NewAppBridge()
	h := &fakeMessageHandler{}
	b.SetMessageHandler(h)

	data := []byte(`{"jsonrpc": "2.0", "method": "ui/notifications/ping"}`)
	if err := b.HandleMessage(data); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.event == nil {
		t.Fatal("OnMessage not called")
	}
	if string(h.event.Data) != string(data) {
		t.Errorf("event.Data = %s, want %s", h.event.Data, data)
	}
}

func TestAppBridge_Initialize_Success(t *testing.T) {
	c := newCaptureBridge()

	go func() {
		out := <-c.sent
		if out.Method != "ui/initialize" {
			t.Errorf("Method = %q, want ui/initialize", out.Method)
		}
		c.feedResponse(t, out, json.RawMessage(`{"hostContext": {"theme": "dark", "locale": "zh-CN"}}`), nil)
	}()

	ctx, err := c.bridge.Initialize()
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	if !c.bridge.IsInitialized() {
		t.Error("IsInitialized() = false, want true")
	}
	if ctx == nil {
		t.Fatal("Initialize() returned nil host context")
	}
	if ctx.Theme != "dark" || ctx.Locale != "zh-CN" {
		t.Errorf("hostContext = %+v", ctx)
	}
	// After init, the notification is sent via the callback.
	notice := <-c.sent
	if notice.Method != "ui/notifications/initialized" {
		t.Errorf("post-init notification = %v, want method %q", notice, "ui/notifications/initialized")
	}
}

func TestAppBridge_Initialize_ErrorPropagates(t *testing.T) {
	c := newCaptureBridge()

	go func() {
		out := <-c.sent
		c.feedResponse(t, out, nil, &JSONRPCError{Code: ErrCodeInternalError, Message: "init failed"})
	}()

	if _, err := c.bridge.Initialize(); err == nil {
		t.Fatal("Initialize() error = nil, want error")
	}
	if c.bridge.IsInitialized() {
		t.Error("IsInitialized() = true after failed init")
	}
}

func TestAppBridge_Initialize_DoubleInit(t *testing.T) {
	b := NewAppBridge()
	b.mu.Lock()
	b.initialized = true
	b.mu.Unlock()

	if _, err := b.Initialize(); err == nil || err.Error() != "already initialized" {
		t.Fatalf("Initialize() error = %v, want %q", err, "already initialized")
	}
}

func TestAppBridge_SendMessageAndReportSize(t *testing.T) {
	c := newCaptureBridge()

	if err := c.bridge.ReportSize(300); err != nil {
		t.Fatalf("ReportSize() error = %v", err)
	}
	out := <-c.sent
	if out.Method != "ui/notifications/size-changed" {
		t.Errorf("Method = %q", out.Method)
	}

	go func() {
		out := <-c.sent
		c.feedResponse(t, out, json.RawMessage(`null`), nil)
	}()
	if err := c.bridge.SendMessage(map[string]any{"text": "hello"}); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
}

func TestAppBridge_UpdateHostContext(t *testing.T) {
	b := NewAppBridge()
	ctx := &HostContext{Theme: "light", Locale: "en"}
	b.UpdateHostContext(ctx)

	if got := b.GetHostContext(); got != ctx {
		t.Errorf("GetHostContext() = %v, want %v", got, ctx)
	}
}

func TestParseID(t *testing.T) {
	tests := []struct {
		name    string
		id      any
		want    int64
		wantErr bool
	}{
		{name: "float64", id: float64(7), want: 7},
		{name: "int", id: 7, want: 7},
		{name: "int64", id: int64(7), want: 7},
		{name: "string numeric", id: "42", want: 42},
		{name: "string non-numeric", id: "abc", want: 0},
		{name: "nil", id: nil, wantErr: true},
		{name: "map", id: map[string]any{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseID(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseID(%v) error = nil, want error", tt.id)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseID(%v) error = %v", tt.id, err)
			}
			if got != tt.want {
				t.Errorf("parseID(%v) = %d, want %d", tt.id, got, tt.want)
			}
		})
	}
}
