package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// ChannelInfo holds metadata about a registered channel.
type ChannelInfo struct {
	Name      string
	RoutePath string
	Enabled   bool
}

// ChannelRouter manages the lifecycle and routing of all registered
// Channel implementations. It provides thread-safe registration,
// lookup by name or path, and HTTP handler generation.
type ChannelRouter struct {
	mu       sync.RWMutex
	channels map[string]Channel // name → Channel
	paths    map[string]Channel // path prefix → Channel
}

// NewChannelRouter creates a new empty ChannelRouter.
func NewChannelRouter() *ChannelRouter {
	return &ChannelRouter{
		channels: make(map[string]Channel),
		paths:    make(map[string]Channel),
	}
}

// Register adds a channel to the router. Returns an error if a channel
// with the same name or route path is already registered.
func (r *ChannelRouter) Register(ch Channel) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := ch.Name()
	path := ch.RoutePath()

	if _, exists := r.channels[name]; exists {
		return fmt.Errorf(
			"gateway: channel %q already registered", name)
	}
	if _, exists := r.paths[path]; exists {
		return fmt.Errorf(
			"gateway: route path %q already registered", path)
	}

	// Ensure path starts with "/"
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	r.channels[name] = ch
	r.paths[path] = ch
	return nil
}

// Unregister removes a channel by name. Returns an error if the
// channel is not found.
func (r *ChannelRouter) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch, exists := r.channels[name]
	if !exists {
		return fmt.Errorf(
			"gateway: channel %q not found", name)
	}

	delete(r.channels, name)
	delete(r.paths, ch.RoutePath())
	return nil
}

// Lookup finds a channel by name. Returns nil if not found.
func (r *ChannelRouter) Lookup(name string) Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.channels[name]
}

// Route matches a URL path to a registered channel. The matching is
// prefix-based: if a channel is registered with path "/feishu", then
// "/feishu/callback" will match it.
func (r *ChannelRouter) Route(urlPath string) (Channel, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for prefix, ch := range r.paths {
		if strings.HasPrefix(urlPath, prefix) {
			// Extract the sub-path after the prefix.
			subPath := urlPath[len(prefix):]
			if subPath == "" {
				subPath = "/"
			}
			return ch, subPath
		}
	}
	return nil, ""
}

// List returns metadata for all registered channels.
func (r *ChannelRouter) List() []ChannelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ChannelInfo, 0, len(r.channels))
	for _, ch := range r.channels {
		result = append(result, ChannelInfo{
			Name:      ch.Name(),
			RoutePath: ch.RoutePath(),
			Enabled:   true,
		})
	}
	return result
}

// ListChannels returns the raw Channel interface values for all
// registered channels.
func (r *ChannelRouter) ListChannels() []Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Channel, 0, len(r.channels))
	for _, ch := range r.channels {
		result = append(result, ch)
	}
	return result
}

// Handler returns an http.Handler that dispatches incoming requests
// to the appropriate Channel based on the URL path.
func (r *ChannelRouter) Handler(
	processFn func(Channel, http.ResponseWriter, *http.Request),
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ch, subPath := r.Route(req.URL.Path)
		if ch == nil {
			http.NotFound(w, req)
			return
		}

		// Rewrite the URL path so the Channel handler sees a
		// clean sub-path (e.g., "/callback" instead of
		// "/feishu/callback").
		req.URL.Path = subPath

		processFn(ch, w, req)
	})
}
