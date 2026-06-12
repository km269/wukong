// Package health provides health check endpoints and status reporting
// for wukong services. It supports both HTTP handler integration and
// programmatic health status queries.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/km269/wukong/internal/util"
)

// Status represents the health status of a component.
type Status string

const (
	// StatusHealthy indicates the component is functioning normally.
	StatusHealthy Status = "healthy"
	// StatusDegraded indicates the component is operational but
	// experiencing issues.
	StatusDegraded Status = "degraded"
	// StatusUnhealthy indicates the component is not functioning.
	StatusUnhealthy Status = "unhealthy"
	// StatusUnknown indicates the component status cannot be determined.
	StatusUnknown Status = "unknown"
)

// ComponentHealth represents the health status of a single component.
type ComponentHealth struct {
	Name      string    `json:"name"`
	Status    Status    `json:"status"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	LatencyMs int64     `json:"latency_ms,omitempty"`
}

// CheckResult is the overall health check result.
type CheckResult struct {
	Status     Status             `json:"status"`
	Version    string             `json:"version"`
	Uptime     string             `json:"uptime"`
	Components []ComponentHealth  `json:"components"`
	Timestamp  time.Time          `json:"timestamp"`
}

// Checker is a function that performs a health check on a component.
// It returns the component's health status and an optional message.
type Checker func(ctx context.Context) ComponentHealth

// Registry manages health checkers and provides the overall health status.
type Registry struct {
	mu        sync.RWMutex
	checkers  map[string]Checker
	startTime time.Time
	version   string
}

// NewRegistry creates a new health check registry.
func NewRegistry(version string) *Registry {
	return &Registry{
		checkers:  make(map[string]Checker),
		startTime: time.Now(),
		version:   version,
	}
}

// Register adds a health checker for a named component.
func (r *Registry) Register(name string, checker Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers[name] = checker
}

// Unregister removes a health checker.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.checkers, name)
}

// Check runs all registered health checks and returns the overall result.
func (r *Registry) Check(ctx context.Context) CheckResult {
	r.mu.RLock()
	checkers := make(map[string]Checker, len(r.checkers))
	for name, c := range r.checkers {
		checkers[name] = c
	}
	r.mu.RUnlock()

	components := make([]ComponentHealth, 0, len(checkers))
	overallStatus := StatusHealthy

	for name, checker := range checkers {
		start := time.Now()
		component := checker(ctx)
		component.Name = name
		component.CheckedAt = time.Now()
		component.LatencyMs = time.Since(start).Milliseconds()

		components = append(components, component)

		// Degraded or unhealthy components reduce overall status
		switch component.Status {
		case StatusUnhealthy:
			if overallStatus != StatusUnhealthy {
				overallStatus = StatusUnhealthy
			}
		case StatusDegraded:
			if overallStatus == StatusHealthy {
				overallStatus = StatusDegraded
			}
		}
	}

	if len(components) == 0 {
		overallStatus = StatusUnknown
	}

	return CheckResult{
		Status:     overallStatus,
		Version:    r.version,
		Uptime:     time.Since(r.startTime).String(),
		Components: components,
		Timestamp:  time.Now(),
	}
}

// HTTPHandler returns an http.Handler that serves health check results
// as JSON. Supports ?verbose=true query parameter for detailed output.
func (r *Registry) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()

		result := r.Check(ctx)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

		statusCode := http.StatusOK
		switch result.Status {
		case StatusUnhealthy:
			statusCode = http.StatusServiceUnavailable
		case StatusDegraded:
			statusCode = http.StatusOK // Still return 200 for degraded
		}
		w.WriteHeader(statusCode)

		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			util.Logger.Warn("health check: failed to encode response",
				"error", err.Error(),
			)
		}
	})
}

// SimpleHTTPHandler returns a minimal health check handler for use
// without a full registry. It returns {"status":"healthy"} with 200 OK.
func SimpleHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})
}

// LivenessHandler returns a handler that always returns 200 OK.
// This is suitable for Kubernetes liveness probes.
func LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"alive":true}`))
	})
}

// ReadinessHandler returns a handler that checks all components and
// returns 200 only if all are healthy, 503 otherwise.
// This is suitable for Kubernetes readiness probes.
func (r *Registry) ReadinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()

		result := r.Check(ctx)

		w.Header().Set("Content-Type", "application/json")

		if result.Status == StatusHealthy {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ready":true}`))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			data, _ := json.Marshal(map[string]any{
				"ready":  false,
				"status": result.Status,
			})
			w.Write(data)
		}
	})
}

// DBChecker creates a health checker for a database connection.
// The ping function should verify the database is reachable.
func DBChecker(name string, ping func(context.Context) error) Checker {
	return func(ctx context.Context) ComponentHealth {
		start := time.Now()
		err := ping(ctx)
		latency := time.Since(start).Milliseconds()

		if err != nil {
			return ComponentHealth{
				Name:      name,
				Status:    StatusUnhealthy,
				Message:   fmt.Sprintf("database ping failed: %v", err),
				LatencyMs: latency,
			}
		}
		return ComponentHealth{
			Name:      name,
			Status:    StatusHealthy,
			Message:   "database is reachable",
			LatencyMs: latency,
		}
	}
}

// ModelChecker creates a health checker for a model provider.
// The check function should verify the model is available.
func ModelChecker(name string, check func(context.Context) error) Checker {
	return func(ctx context.Context) ComponentHealth {
		start := time.Now()
		err := check(ctx)
		latency := time.Since(start).Milliseconds()

		if err != nil {
			return ComponentHealth{
				Name:      name,
				Status:    StatusDegraded,
				Message:   fmt.Sprintf("model check failed: %v", err),
				LatencyMs: latency,
			}
		}
		return ComponentHealth{
			Name:      name,
			Status:    StatusHealthy,
			Message:   "model provider is available",
			LatencyMs: latency,
		}
	}
}

// ExtensionChecker creates a health checker for an extension.
func ExtensionChecker(name string, active bool, toolCount int) Checker {
	return func(ctx context.Context) ComponentHealth {
		if !active {
			return ComponentHealth{
				Name:    name,
				Status:  StatusHealthy,
				Message: "extension is disabled",
			}
		}
		return ComponentHealth{
			Name:    name,
			Status:  StatusHealthy,
			Message: fmt.Sprintf("extension active with %d tools", toolCount),
		}
	}
}

// A2AServerChecker creates a health checker for the A2A server.
func A2AServerChecker(enabled bool, address string) Checker {
	return func(ctx context.Context) ComponentHealth {
		if !enabled {
			return ComponentHealth{
				Name:    "a2a_server",
				Status:  StatusHealthy,
				Message: "A2A server is disabled",
			}
		}
		// Try to connect to the A2A server
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://localhost" + address + "/.well-known/agent.json")
		if err != nil {
			return ComponentHealth{
				Name:    "a2a_server",
				Status:  StatusDegraded,
				Message: fmt.Sprintf("A2A server not reachable: %v", err),
			}
		}
		resp.Body.Close()
		return ComponentHealth{
			Name:    "a2a_server",
			Status:  StatusHealthy,
			Message: fmt.Sprintf("A2A server listening on %s", address),
		}
	}
}
