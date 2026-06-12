package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNewRegistry verifies registry creation with defaults.
func TestNewRegistry(t *testing.T) {
	r := NewRegistry("1.0.0")
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", r.version)
	}
	if r.startTime.IsZero() {
		t.Error("start time should not be zero")
	}
}

// TestRegistry_Check_NoCheckers verifies behavior with no checkers.
func TestRegistry_Check_NoCheckers(t *testing.T) {
	r := NewRegistry("1.0.0")
	result := r.Check(context.Background())

	if result.Status != StatusUnknown {
		t.Errorf("expected status unknown, got %s", result.Status)
	}
	if result.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", result.Version)
	}
	if result.Uptime == "" {
		t.Error("uptime should not be empty")
	}
	if len(result.Components) != 0 {
		t.Errorf("expected 0 components, got %d", len(result.Components))
	}
}

// TestRegistry_Check_AllHealthy verifies all-healthy scenario.
func TestRegistry_Check_AllHealthy(t *testing.T) {
	r := NewRegistry("1.0.0")

	r.Register("db", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusHealthy, Message: "ok"}
	})
	r.Register("cache", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusHealthy, Message: "ok"}
	})

	result := r.Check(context.Background())

	if result.Status != StatusHealthy {
		t.Errorf("expected status healthy, got %s", result.Status)
	}
	if len(result.Components) != 2 {
		t.Errorf("expected 2 components, got %d", len(result.Components))
	}
}

// TestRegistry_Check_Unhealthy verifies unhealthy propagates.
func TestRegistry_Check_Unhealthy(t *testing.T) {
	r := NewRegistry("1.0.0")

	r.Register("db", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusHealthy, Message: "ok"}
	})
	r.Register("api", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{
			Status:  StatusUnhealthy,
			Message: "connection refused",
		}
	})

	result := r.Check(context.Background())

	if result.Status != StatusUnhealthy {
		t.Errorf("expected status unhealthy, got %s", result.Status)
	}
}

// TestRegistry_Check_Degraded verifies degraded propagates.
func TestRegistry_Check_Degraded(t *testing.T) {
	r := NewRegistry("1.0.0")

	r.Register("db", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusHealthy, Message: "ok"}
	})
	r.Register("api", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{
			Status:  StatusDegraded,
			Message: "high latency",
		}
	})

	result := r.Check(context.Background())

	if result.Status != StatusDegraded {
		t.Errorf("expected status degraded, got %s", result.Status)
	}
}

// TestRegistry_Check_LatencyTracking verifies latency is tracked.
func TestRegistry_Check_LatencyTracking(t *testing.T) {
	r := NewRegistry("1.0.0")

	r.Register("slow", func(ctx context.Context) ComponentHealth {
		time.Sleep(50 * time.Millisecond)
		return ComponentHealth{Status: StatusHealthy}
	})

	result := r.Check(context.Background())

	if len(result.Components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(result.Components))
	}
	if result.Components[0].LatencyMs < 50 {
		t.Errorf("expected latency >= 50ms, got %dms",
			result.Components[0].LatencyMs)
	}
}

// TestRegistry_HTTPHandler_Healthy verifies the HTTP handler returns 200
// when all components are healthy.
func TestRegistry_HTTPHandler_Healthy(t *testing.T) {
	r := NewRegistry("1.0.0")
	r.Register("test", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusHealthy, Message: "ok"}
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	r.HTTPHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result CheckResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.Status != StatusHealthy {
		t.Errorf("expected status healthy, got %s", result.Status)
	}
}

// TestRegistry_HTTPHandler_Unhealthy verifies the HTTP handler returns 503
// when a component is unhealthy.
func TestRegistry_HTTPHandler_Unhealthy(t *testing.T) {
	r := NewRegistry("1.0.0")
	r.Register("broken", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{
			Status:  StatusUnhealthy,
			Message: "service down",
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	r.HTTPHandler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

// TestRegistry_HTTPHandler_Degraded verifies the HTTP handler returns 200
// for degraded status (still operational).
func TestRegistry_HTTPHandler_Degraded(t *testing.T) {
	r := NewRegistry("1.0.0")
	r.Register("slow-svc", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{
			Status:  StatusDegraded,
			Message: "slow response",
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	r.HTTPHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for degraded, got %d", w.Code)
	}
}

// TestSimpleHTTPHandler verifies the simple handler.
func TestSimpleHTTPHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	SimpleHTTPHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["status"] != "healthy" {
		t.Errorf("expected status 'healthy', got %q", result["status"])
	}
}

// TestLivenessHandler verifies the liveness probe handler.
func TestLivenessHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	w := httptest.NewRecorder()

	LivenessHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result map[string]bool
	json.Unmarshal(w.Body.Bytes(), &result)
	if !result["alive"] {
		t.Error("expected alive=true")
	}
}

// TestRegistry_ReadinessHandler_Ready verifies readiness when healthy.
func TestRegistry_ReadinessHandler_Ready(t *testing.T) {
	r := NewRegistry("1.0.0")
	r.Register("db", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusHealthy, Message: "ok"}
	})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	r.ReadinessHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// TestRegistry_ReadinessHandler_NotReady verifies readiness when unhealthy.
func TestRegistry_ReadinessHandler_NotReady(t *testing.T) {
	r := NewRegistry("1.0.0")
	r.Register("db", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{
			Status:  StatusUnhealthy,
			Message: "cannot connect",
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()

	r.ReadinessHandler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

// TestDBChecker verifies the database health checker.
func TestDBChecker(t *testing.T) {
	// Healthy database
	checker := DBChecker("postgres", func(ctx context.Context) error {
		return nil
	})
	result := checker(context.Background())
	if result.Status != StatusHealthy {
		t.Errorf("expected healthy, got %s", result.Status)
	}

	// Unhealthy database
	checker = DBChecker("postgres", func(ctx context.Context) error {
		return errors.New("connection refused")
	})
	result = checker(context.Background())
	if result.Status != StatusUnhealthy {
		t.Errorf("expected unhealthy, got %s", result.Status)
	}
}

// TestModelChecker verifies the model provider health checker.
func TestModelChecker(t *testing.T) {
	// Available model
	checker := ModelChecker("openai", func(ctx context.Context) error {
		return nil
	})
	result := checker(context.Background())
	if result.Status != StatusHealthy {
		t.Errorf("expected healthy, got %s", result.Status)
	}

	// Degraded model
	checker = ModelChecker("openai", func(ctx context.Context) error {
		return errors.New("rate limited")
	})
	result = checker(context.Background())
	if result.Status != StatusDegraded {
		t.Errorf("expected degraded, got %s", result.Status)
	}
}

// TestExtensionChecker verifies the extension health checker.
func TestExtensionChecker(t *testing.T) {
	// Active extension
	checker := ExtensionChecker("developer", true, 5)
	result := checker(context.Background())
	if result.Status != StatusHealthy {
		t.Errorf("expected healthy, got %s", result.Status)
	}

	// Disabled extension
	checker = ExtensionChecker("memory", false, 0)
	result = checker(context.Background())
	if result.Status != StatusHealthy {
		t.Errorf("expected healthy for disabled, got %s", result.Status)
	}
}

// TestA2AServerChecker verifies the A2A server health checker.
func TestA2AServerChecker(t *testing.T) {
	// Disabled server
	checker := A2AServerChecker(false, ":9090")
	result := checker(context.Background())
	if result.Status != StatusHealthy {
		t.Errorf("expected healthy for disabled, got %s", result.Status)
	}

	// Enabled but unreachable (no server running in test)
	checker = A2AServerChecker(true, ":19999")
	result = checker(context.Background())
	if result.Status != StatusDegraded {
		t.Errorf("expected degraded for unreachable, got %s", result.Status)
	}
}

// TestRegistry_RegisterUnregister verifies checker registration lifecycle.
func TestRegistry_RegisterUnregister(t *testing.T) {
	r := NewRegistry("1.0.0")

	r.Register("test", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{Status: StatusHealthy}
	})

	result := r.Check(context.Background())
	if len(result.Components) != 1 {
		t.Errorf("expected 1 component, got %d", len(result.Components))
	}

	r.Unregister("test")

	result = r.Check(context.Background())
	if len(result.Components) != 0 {
		t.Errorf("expected 0 components after unregister, got %d",
			len(result.Components))
	}
}

// TestRegistry_UnregisterNonExistent verifies unregistering a
// non-existent checker is safe.
func TestRegistry_UnregisterNonExistent(t *testing.T) {
	r := NewRegistry("1.0.0")
	// Should not panic
	r.Unregister("nonexistent")
}

// TestHealthResponse_JSONFormat verifies the JSON response format.
func TestHealthResponse_JSONFormat(t *testing.T) {
	r := NewRegistry("2.0.0")
	r.Register("component-a", func(ctx context.Context) ComponentHealth {
		return ComponentHealth{
			Status:  StatusHealthy,
			Message: "all good",
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.HTTPHandler().ServeHTTP(w, req)

	// Verify content type
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	// Verify cache headers
	cc := w.Header().Get("Cache-Control")
	if cc != "no-cache, no-store, must-revalidate" {
		t.Errorf("expected Cache-Control no-cache, got %q", cc)
	}

	// Parse and verify structure
	var result CheckResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %q", result.Version)
	}
	if result.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}
