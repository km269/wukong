// Package mcpapps provides MCP Apps specification implementation.
package mcpapps

import (
	"fmt"
	"sync"
)

// SandboxedHost provides a secure sandbox for rendering UI apps.
// It wraps the View and communicates with it through an intermediate Sandbox proxy.
type SandboxedHost struct {
	mu       sync.RWMutex
	resources map[string]*UIResource
	content   map[string]string // URI -> HTML content
	tools     map[string]*ToolRegistration
	csp       *CSPConfig
}

// ToolRegistration represents a tool registration for MCP Apps.
type ToolRegistration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema any           `json:"inputSchema"`
	Meta        *ToolMetaHost  `json:"_meta,omitempty"`
}

// ToolMetaHost contains tool metadata for UI association.
type ToolMetaHost struct {
	UI *ToolUIMetaHost `json:"ui,omitempty"`
}

// ToolUIMetaHost contains UI-specific tool metadata.
type ToolUIMetaHost struct {
	ResourceURI string       `json:"resourceUri,omitempty"`
	Visibility  []Visibility `json:"visibility,omitempty"`
}

// NewSandboxedHost creates a new SandboxedHost.
func NewSandboxedHost() *SandboxedHost {
	return &SandboxedHost{
		resources: make(map[string]*UIResource),
		content:   make(map[string]string),
		tools:     make(map[string]*ToolRegistration),
		csp:       GenerateDefaultCSP(),
	}
}

// RegisterResource registers a UI resource.
func (h *SandboxedHost) RegisterResource(resource *UIResource) error {
	if err := resource.Validate(); err != nil {
		return fmt.Errorf("validate resource: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.resources[resource.URI] = resource
	return nil
}

// RegisterContent registers the HTML content for a resource.
func (h *SandboxedHost) RegisterContent(uri, html string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Verify resource exists
	if _, ok := h.resources[uri]; !ok {
		return fmt.Errorf("resource not found: %s", uri)
	}

	h.content[uri] = html
	return nil
}

// GetResource returns a registered resource.
func (h *SandboxedHost) GetResource(uri string) (*UIResource, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.resources[uri]
	return r, ok
}

// GetContent returns the HTML content for a resource.
func (h *SandboxedHost) GetContent(uri string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.content[uri]
	return c, ok
}

// ListResources returns all registered resources.
func (h *SandboxedHost) ListResources() []*UIResource {
	h.mu.RLock()
	defer h.mu.RUnlock()

	resources := make([]*UIResource, 0, len(h.resources))
	for _, r := range h.resources {
		resources = append(resources, r)
	}
	return resources
}

// RegisterTool registers a tool with UI association.
func (h *SandboxedHost) RegisterTool(tool *ToolRegistration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tools[tool.Name] = tool
}

// GetTool returns a registered tool.
func (h *SandboxedHost) GetTool(name string) (*ToolRegistration, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	t, ok := h.tools[name]
	return t, ok
}

// ListTools returns all registered tools.
func (h *SandboxedHost) ListTools() []*ToolRegistration {
	h.mu.RLock()
	defer h.mu.RUnlock()

	tools := make([]*ToolRegistration, 0, len(h.tools))
	for _, t := range h.tools {
		tools = append(tools, t)
	}
	return tools
}

// SetCSP updates the default CSP configuration.
func (h *SandboxedHost) SetCSP(csp *CSPConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.csp = csp
}

// GetCSP returns the current CSP configuration.
func (h *SandboxedHost) GetCSP() *CSPConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.csp
}

// BuildCSPHeaders generates CSP headers from configuration.
func (h *SandboxedHost) BuildCSPHeaders(resource *UIResource) string {
	csp := h.csp
	if resource.Meta != nil && resource.Meta.CSP != nil {
		csp = resource.Meta.CSP
	}

	// Build default restrictive CSP
	directives := []string{
		"default-src 'none'",
		"script-src 'self' 'unsafe-inline'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"media-src 'self' data:",
		"connect-src 'none'",
	}

	// Add allowed domains
	if len(csp.ConnectDomains) > 0 {
		directives = append(directives, fmt.Sprintf("connect-src 'self' %s",
			joinDomains(csp.ConnectDomains)))
	}

	if len(csp.ResourceDomains) > 0 {
		directives = append(directives, fmt.Sprintf("script-src 'self' 'unsafe-inline' %s",
			joinDomains(csp.ResourceDomains)))
		directives = append(directives, fmt.Sprintf("style-src 'self' 'unsafe-inline' %s",
			joinDomains(csp.ResourceDomains)))
		directives = append(directives, fmt.Sprintf("img-src 'self' data: %s",
			joinDomains(csp.ResourceDomains)))
	}

	if len(csp.FrameDomains) > 0 {
		directives = append(directives, fmt.Sprintf("frame-src %s",
			joinDomains(csp.FrameDomains)))
	} else {
		directives = append(directives, "frame-src 'none'")
	}

	if len(csp.BaseUriDomains) > 0 {
		directives = append(directives, fmt.Sprintf("base-uri 'self' %s",
			joinDomains(csp.BaseUriDomains)))
	} else {
		directives = append(directives, "base-uri 'self'")
	}

	return fmt.Sprintf("Content-Security-Policy: %s", joinDirectives(directives))
}

// joinDomains joins domain strings.
func joinDomains(domains []string) string {
	result := ""
	for i, d := range domains {
		if i > 0 {
			result += " "
		}
		result += d
	}
	return result
}

// joinDirectives joins CSP directives.
func joinDirectives(directives []string) string {
	result := ""
	for i, d := range directives {
		if i > 0 {
			result += "; "
		}
		result += d
	}
	return result
}

// BuildSandboxAttributes generates iframe sandbox attributes.
func (h *SandboxedHost) BuildSandboxAttributes() []string {
	return []string{
		"allow-scripts",
		"allow-same-origin",
	}
}

// GenerateSandboxHTML generates the sandbox proxy HTML with full postMessage bridge.
func (h *SandboxedHost) GenerateSandboxHTML() string {
	// CSP headers are used for the iframe content policy
	// Generated dynamically when setting app content

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>MCP Apps Sandbox</title>
  <style>
    body { margin: 0; padding: 0; overflow: hidden; }
    #app-frame { width: 100%%; height: 100%%; border: none; }
  </style>
</head>
<body>
  <iframe id="app-frame" sandbox="allow-scripts allow-same-origin" srcdoc=""></iframe>

  <script>
    // MCP Apps postMessage Bridge for iframe communication
    (function() {
      // Configuration
      var iframe = document.getElementById('app-frame');
      var pendingRequests = {};
      var requestId = 0;
      var initialized = false;

      // Receive messages from parent frame (host)
      window.addEventListener('message', function(event) {
        var data = event.data;
        if (!data || !data.jsonrpc) return;

        // Handle responses to our requests
        if (data.id !== undefined && !data.method) {
          var resolver = pendingRequests[data.id];
          if (resolver) {
            if (data.error) {
              resolver.reject(data.error);
            } else {
              resolver.resolve(data.result);
            }
            delete pendingRequests[data.id];
          }
          return;
        }

        // Handle requests from host
        if (data.method) {
          handleHostRequest(data.method, data.params, data.id, event.source);
        }
      });

      // Send message to iframe
      function sendToIframe(data) {
        if (iframe && iframe.contentWindow) {
          iframe.contentWindow.postMessage(data, '*');
        }
      }

      // Send message to parent (host)
      function sendToParent(data) {
        window.parent.postMessage(data, '*');
      }

      // Handle requests from host
      function handleHostRequest(method, params, id, source) {
        // Forward to iframe
        sendToIframe({
          jsonrpc: '2.0',
          id: id,
          method: method,
          params: params
        });

        // Wait for iframe response
        // Note: In a full implementation, we'd track iframe responses separately
      }

      // Send request to host and return promise
      function sendRequest(method, params) {
        return new Promise(function(resolve, reject) {
          var id = ++requestId;
          pendingRequests[id] = { resolve: resolve, reject: reject };
          sendToParent({
            jsonrpc: '2.0',
            id: id,
            method: method,
            params: params
          });

          // Timeout after 30 seconds
          setTimeout(function() {
            if (pendingRequests[id]) {
              delete pendingRequests[id];
              reject(new Error('Request timeout'));
            }
          }, 30000);
        });
      }

      // Send notification to host
      function sendNotification(method, params) {
        sendToParent({
          jsonrpc: '2.0',
          method: method,
          params: params
        });
      }

      // Handle messages from iframe
      iframe.addEventListener('load', function() {
        // Inject postMessage script into iframe
        var script = iframe.contentDocument.createElement('script');
        script.textContent = '(function() { ' + getIframeBridgeScript() + ' }());';
        iframe.contentDocument.head.appendChild(script);

        // Signal ready
        sendNotification('ui/notifications/sandbox-ready', {});
      });

      // Listen for messages from iframe
      window.addEventListener('message', function(event) {
        var data = event.data;
        if (!data || !data.jsonrpc) return;
        if (event.source !== iframe.contentWindow) return;

        // Forward to host
        sendToParent(data);
      });

      // Expose global API
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

      // Initialize
      function init() {
        // Signal ready to host
        sendNotification('ui/notifications/sandbox-proxy-ready', {});
      }

      init();
    })();

    // Bridge script to inject into iframe
    function getIframeBridgeScript() {
      return '(' + PostMessageScript.toString() + ')();';
    }
  </script>
</body>
</html>`)
}

// GenerateAppHTML generates HTML content for an app wrapped in the MCP Apps protocol.
func (h *SandboxedHost) GenerateAppHTML(appName, appContent string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>%s</title>
  <style>
    body { margin: 0; padding: 0; overflow: auto; }
    .mcp-app-container { width: 100%%; height: 100%%; }
  </style>
</head>
<body>
  <div class="mcp-app-container">
    %s
  </div>

  <script>
    // Inject postMessage bridge script
    (function() {
      var script = document.createElement('script');
      script.textContent = '(%s)();';
      document.head.appendChild(script);
    })();
  </script>
</body>
</html>`, appName, appContent, PostMessageScript())
}
