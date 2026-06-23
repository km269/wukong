// Package mcpapps provides MCP Apps specification implementation.
// It implements the io.modelcontextprotocol/ui specification for embedding
// HTML applications with secure sandbox isolation.
package mcpapps

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// JSONRPCMessage represents a JSON-RPC 2.0 message.
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC error.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("jsonrpc error: %s (code %d)", e.Message, e.Code)
}

// Standard JSON-RPC error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// AppBridge provides bidirectional communication between UI and host.
// Implements JSON-RPC 2.0 over postMessage transport.
type AppBridge struct {
	mu              sync.RWMutex
	requestHandler  RequestHandler
	notifyHandler   NotifyHandler
	messageHandler  MessageHandler
	initialized     bool
	hostContext     *HostContext
	requestID       int64
	pendingReqs     map[int64]*pendingRequest
	messageQueue    []JSONRPCMessage
	queueMu         sync.Mutex
	onSendCallback  func(msg JSONRPCMessage) error
}

// pendingRequest tracks an outgoing request awaiting response.
type pendingRequest struct {
	resolve func(any)
	reject  func(error)
	timer   *time.Timer
}

// RequestHandler handles incoming JSON-RPC requests.
type RequestHandler interface {
	HandleRequest(method string, params json.RawMessage) (any, error)
}

// NotifyHandler handles incoming JSON-RPC notifications.
type NotifyHandler interface {
	HandleNotification(method string, params json.RawMessage)
}

// MessageHandler handles incoming postMessage events.
type MessageHandler interface {
	OnMessage(event *MessageEvent)
}

// MessageEvent represents a postMessage event.
type MessageEvent struct {
	Origin string
	Data   []byte
}

// HostContext contains context from the host.
type HostContext struct {
	Theme   string            `json:"theme,omitempty"`
	Locale  string            `json:"locale,omitempty"`
	Config  map[string]string `json:"config,omitempty"`
	AgentID string            `json:"agentId,omitempty"`
}

// NewAppBridge creates a new AppBridge.
func NewAppBridge() *AppBridge {
	return &AppBridge{
		pendingReqs: make(map[int64]*pendingRequest),
		messageQueue: make([]JSONRPCMessage, 0),
	}
}

// SetRequestHandler sets the handler for incoming requests.
func (b *AppBridge) SetRequestHandler(h RequestHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requestHandler = h
}

// SetNotifyHandler sets the handler for incoming notifications.
func (b *AppBridge) SetNotifyHandler(h NotifyHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notifyHandler = h
}

// SetMessageHandler sets the handler for raw postMessage events.
func (b *AppBridge) SetMessageHandler(h MessageHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messageHandler = h
}

// SetOnSendCallback sets the callback for sending messages.
func (b *AppBridge) SetOnSendCallback(cb func(msg JSONRPCMessage) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onSendCallback = cb
}

// HandleMessage processes an incoming postMessage.
func (b *AppBridge) HandleMessage(data []byte) error {
	var msg JSONRPCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("unmarshal message: %w", err)
	}

	// Notify message handler
	b.mu.RLock()
	handler := b.messageHandler
	b.mu.RUnlock()
	if handler != nil {
		handler.OnMessage(&MessageEvent{Data: data})
	}

	// Handle response
	if msg.ID != nil && msg.Method == "" {
		return b.handleResponse(&msg)
	}

	// Handle request
	if msg.ID != nil && msg.Method != "" {
		return b.handleRequest(&msg)
	}

	// Handle notification
	if msg.ID == nil && msg.Method != "" {
		return b.handleNotification(&msg)
	}

	return errors.New("invalid JSON-RPC message")
}

// handleResponse processes a JSON-RPC response.
func (b *AppBridge) handleResponse(msg *JSONRPCMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	id, err := parseID(msg.ID)
	if err != nil {
		return err
	}

	pr, ok := b.pendingReqs[id]
	if !ok {
		return fmt.Errorf("unknown request ID: %v", id)
	}

	// Stop timer
	if pr.timer != nil {
		pr.timer.Stop()
	}

	delete(b.pendingReqs, id)

	if msg.Error != nil {
		pr.reject(msg.Error)
		return nil
	}

	pr.resolve(msg.Result)
	return nil
}

// handleRequest processes a JSON-RPC request.
func (b *AppBridge) handleRequest(msg *JSONRPCMessage) error {
	b.mu.RLock()
	handler := b.requestHandler
	b.mu.RUnlock()

	if handler == nil {
		return b.sendErrorResponse(msg.ID, ErrCodeMethodNotFound, "Method not found")
	}

	result, err := handler.HandleRequest(msg.Method, msg.Params)
	if err != nil {
		var jsonErr *JSONRPCError
		if errors.As(err, &jsonErr) {
			return b.sendErrorResponse(msg.ID, jsonErr.Code, jsonErr.Message)
		}
		return b.sendErrorResponse(msg.ID, ErrCodeInternalError, err.Error())
	}

	return b.sendResponse(msg.ID, result)
}

// handleNotification processes a JSON-RPC notification.
func (b *AppBridge) handleNotification(msg *JSONRPCMessage) error {
	b.mu.RLock()
	handler := b.notifyHandler
	b.mu.RUnlock()

	if handler != nil {
		handler.HandleNotification(msg.Method, msg.Params)
	}
	return nil
}

// sendResponse sends a JSON-RPC response.
func (b *AppBridge) sendResponse(id any, result any) error {
	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	return b.send(msg)
}

// sendErrorResponse sends a JSON-RPC error response.
func (b *AppBridge) sendErrorResponse(id any, code int, message string) error {
	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	return b.send(msg)
}

// Send sends a JSON-RPC message via the configured transport.
func (b *AppBridge) Send(msg JSONRPCMessage) error {
	return b.send(msg)
}

// send is the internal send method that uses the configured callback.
func (b *AppBridge) send(msg JSONRPCMessage) error {
	b.mu.RLock()
	callback := b.onSendCallback
	b.mu.RUnlock()

	if callback != nil {
		return callback(msg)
	}

	// If no callback, queue the message
	b.queueMu.Lock()
	b.messageQueue = append(b.messageQueue, msg)
	b.queueMu.Unlock()

	return nil
}

// GetQueuedMessages returns and clears queued messages.
func (b *AppBridge) GetQueuedMessages() []JSONRPCMessage {
	b.queueMu.Lock()
	messages := make([]JSONRPCMessage, len(b.messageQueue))
	copy(messages, b.messageQueue)
	b.messageQueue = b.messageQueue[:0]
	b.queueMu.Unlock()
	return messages
}

// Request sends a JSON-RPC request and waits for response.
func (b *AppBridge) Request(method string, params any) (any, error) {
	b.mu.Lock()
	id := b.requestID
	b.requestID++
	timeout := 30000 // 30 seconds default timeout
	b.mu.Unlock()

	// Create promise
	type result struct {
		val any
		err error
	}
	resultCh := make(chan result, 1)

	pr := &pendingRequest{
		resolve: func(v any) {
			resultCh <- result{val: v}
		},
		reject: func(e error) {
			resultCh <- result{err: e}
		},
	}

	b.mu.Lock()
	b.pendingReqs[id] = pr
	b.mu.Unlock()

	// Create request message
	paramJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramJSON,
	}

	// Send request
	if err := b.send(msg); err != nil {
		b.mu.Lock()
		delete(b.pendingReqs, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Wait for response with timeout
	select {
	case r := <-resultCh:
		return r.val, r.err
	case <-time.After(time.Duration(timeout) * time.Millisecond):
		b.mu.Lock()
		delete(b.pendingReqs, id)
		b.mu.Unlock()
		return nil, errors.New("request timed out")
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (b *AppBridge) Notify(method string, params any) error {
	paramJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramJSON,
	}

	return b.send(msg)
}

// Initialize initializes the app with the host.
func (b *AppBridge) Initialize() (*HostContext, error) {
	b.mu.Lock()
	if b.initialized {
		b.mu.Unlock()
		return nil, errors.New("already initialized")
	}
	b.mu.Unlock()

	result, err := b.Request("ui/initialize", map[string]any{
		"capabilities": map[string]any{},
		"clientInfo": map[string]string{
			"name":    "wukong-mcp-app",
			"version": "1.0.0",
		},
		"protocolVersion": ProtocolVersion,
	})
	if err != nil {
		return nil, err
	}

	// Parse host context from result
	if m, ok := result.(map[string]any); ok {
		b.mu.Lock()
		if hostCtx, ok := m["hostContext"].(map[string]any); ok {
			b.hostContext = &HostContext{}
			if theme, ok := hostCtx["theme"].(string); ok {
				b.hostContext.Theme = theme
			}
			if locale, ok := hostCtx["locale"].(string); ok {
				b.hostContext.Locale = locale
			}
		}
		b.initialized = true
		b.mu.Unlock()
	}

	b.Notify("ui/notifications/initialized", map[string]any{})

	return b.hostContext, nil
}

// SendMessage sends a message to the chat.
func (b *AppBridge) SendMessage(content map[string]any) error {
	_, err := b.Request("ui/message", map[string]any{
		"content": content,
	})
	return err
}

// ReportSize reports the UI size to the host.
func (b *AppBridge) ReportSize(height int) error {
	return b.Notify("ui/notifications/size-changed", map[string]any{
		"height": height,
	})
}

// UpdateHostContext updates the host context.
func (b *AppBridge) UpdateHostContext(ctx *HostContext) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hostContext = ctx
}

// IsInitialized returns whether the bridge is initialized.
func (b *AppBridge) IsInitialized() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.initialized
}

// GetHostContext returns the current host context.
func (b *AppBridge) GetHostContext() *HostContext {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.hostContext
}

// parseID parses a JSON-RPC ID.
func parseID(id any) (int64, error) {
	switch v := id.(type) {
	case float64:
		return int64(v), nil
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case string:
		var i int64
		fmt.Sscanf(v, "%d", &i)
		return i, nil
	default:
		return 0, fmt.Errorf("invalid ID type: %T", id)
	}
}

// PostMessageScript returns JavaScript code that can be injected into an iframe
// to set up postMessage communication with the parent frame.
func PostMessageScript() string {
	return `
(function() {
  // Bridge state
  var pendingRequests = {};
  var requestId = 0;
  var initialized = false;

  // Initialize the bridge
  function init() {
    window.addEventListener('message', handleMessage);
    // Signal ready
    window.parent.postMessage({type: 'mcp-app-ready'}, '*');
  }

  // Handle incoming messages
  function handleMessage(event) {
    var data = event.data;
    if (!data || !data.jsonrpc) return;

    if (data.type === 'mcp-app-ready') {
      // App is ready, send initialization
      sendRequest('ui/initialize', {
        capabilities: {},
        clientInfo: { name: 'wukong-mcp-app', version: '1.0.0' },
        protocolVersion: '2026-01-26'
      }).then(function(result) {
        initialized = true;
        // Notify initialization complete
        sendNotification('ui/notifications/initialized', {});
      }).catch(function(err) {
        console.error('Initialization failed:', err);
      });
      return;
    }

    if (data.id !== undefined) {
      // Response to our request
      var resolve = pendingRequests[data.id];
      if (resolve) {
        if (data.error) {
          resolve.reject(data.error);
        } else {
          resolve.resolve(data.result);
        }
        delete pendingRequests[data.id];
      }
    }
  }

  // Send a request and return a promise
  function sendRequest(method, params) {
    return new Promise(function(resolve, reject) {
      var id = ++requestId;
      pendingRequests[id] = { resolve: resolve, reject: reject };
      window.parent.postMessage({
        jsonrpc: '2.0',
        id: id,
        method: method,
        params: params
      }, '*');
    });
  }

  // Send a notification (no response expected)
  function sendNotification(method, params) {
    window.parent.postMessage({
      jsonrpc: '2.0',
      method: method,
      params: params
    }, '*');
  }

  // Expose global API for the app
  window.__mcpBridge = {
    sendMessage: function(content) {
      return sendRequest('ui/message', { content: content });
    },
    reportSize: function(height) {
      sendNotification('ui/notifications/size-changed', { height: height });
    },
    request: sendRequest,
    notify: sendNotification,
    isInitialized: function() { return initialized; }
  };

  // Start initialization
  init();
})();
`
}

// ReceiveMessageScript returns JavaScript code for the parent frame to receive messages.
func ReceiveMessageScript() string {
	return `
(function() {
  // Store for pending requests from parent
  var pendingRequests = {};
  var requestId = 0;

  // Handle messages from iframe
  window.addEventListener('message', function(event) {
    var data = event.data;
    if (!data || !data.jsonrpc) return;

    // Handle responses to our requests
    if (data.id !== undefined && !data.method) {
      var resolve = pendingRequests[data.id];
      if (resolve) {
        if (data.error) {
          resolve.reject(data.error);
        } else {
          resolve.resolve(data.result);
        }
        delete pendingRequests[data.id];
      }
      return;
    }

    // Handle requests from iframe
    if (data.method) {
      // Dispatch to registered handlers
      handleRequest(data.method, data.params, data.id, event.source);
    }
  });

  // Placeholder for request handlers - will be set by Go code
  window.__parentBridge = {
    handleRequest: function(method, params, id, source) {
      console.log('Unhandled request:', method, params);
      // Send error response
      source.postMessage({
        jsonrpc: '2.0',
        id: id,
        error: { code: -32601, message: 'Method not found' }
      }, '*');
    },
    // Send request to iframe
    request: function(method, params) {
      return new Promise(function(resolve, reject) {
        var id = ++requestId;
        pendingRequests[id] = { resolve: resolve, reject: reject };
        var iframe = document.querySelector('iframe[data-mcp-app]');
        if (iframe && iframe.contentWindow) {
          iframe.contentWindow.postMessage({
            jsonrpc: '2.0',
            id: id,
            method: method,
            params: params
          }, '*');
        } else {
          reject(new Error('No iframe found'));
        }
      });
    },
    // Send notification to iframe
    notify: function(method, params) {
      var iframe = document.querySelector('iframe[data-mcp-app]');
      if (iframe && iframe.contentWindow) {
        iframe.contentWindow.postMessage({
          jsonrpc: '2.0',
          method: method,
          params: params
        }, '*');
      }
    }
  };
})();
`
}
