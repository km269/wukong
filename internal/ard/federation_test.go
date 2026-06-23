// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"testing"
	"time"
)

func TestFederationConfig(t *testing.T) {
	config := &FederationConfig{
		Timeout:         30 * time.Second,
		MaxRegistries:   10,
		MaxDepth:        3,
		EnableReferrals: true,
		TrustPolicy:     TrustPolicyKnown,
		LocalRegistryURL: "http://localhost:8080",
	}

	if config.Timeout != 30*time.Second {
		t.Errorf("FederationConfig.Timeout = %v, want 30s", config.Timeout)
	}

	if config.MaxRegistries != 10 {
		t.Errorf("FederationConfig.MaxRegistries = %v, want 10", config.MaxRegistries)
	}

	if config.MaxDepth != 3 {
		t.Errorf("FederationConfig.MaxDepth = %v, want 3", config.MaxDepth)
	}

	if !config.EnableReferrals {
		t.Error("FederationConfig.EnableReferrals should be true")
	}

	if config.TrustPolicy != TrustPolicyKnown {
		t.Errorf("FederationConfig.TrustPolicy = %v, want TrustPolicyKnown", config.TrustPolicy)
	}
}

func TestTrustPolicy(t *testing.T) {
	tests := []struct {
		name  string
		level TrustPolicy
	}{
		{"TrustPolicyAny", TrustPolicyAny},
		{"TrustPolicyKnown", TrustPolicyKnown},
		{"TrustPolicyVerified", TrustPolicyVerified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if int(tt.level) < 0 || int(tt.level) > 2 {
				t.Errorf("TrustPolicy %s has invalid value", tt.name)
			}
		})
	}
}

func TestTrustLevel(t *testing.T) {
	tests := []struct {
		name  string
		level TrustLevel
	}{
		{"TrustLevelUnknown", TrustLevelUnknown},
		{"TrustLevelDirect", TrustLevelDirect},
		{"TrustLevelIndirect", TrustLevelIndirect},
		{"TrustLevelUntrusted", TrustLevelUntrusted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if int(tt.level) < 0 || int(tt.level) > 3 {
				t.Errorf("TrustLevel %s has invalid value", tt.name)
			}
		})
	}
}

func TestFederationMetrics(t *testing.T) {
	metrics := NewFederationMetrics()

	// Record some latencies
	metrics.RecordLatency("http://reg1.com", 100*time.Millisecond)
	metrics.RecordLatency("http://reg2.com", 200*time.Millisecond)

	// Record errors
	metrics.RecordError("http://reg1.com")
	metrics.RecordError("http://reg1.com")
	metrics.RecordError("http://reg3.com")

	// Increment counts
	metrics.IncrementRegistryCount()
	metrics.IncrementRegistryCount()
	metrics.AddEntries(100)
	metrics.AddEntries(50)

	// Get stats
	regCount, entryCount, avgLatency, errorRate := metrics.GetStats()

	if regCount != 2 {
		t.Errorf("FederationMetrics registry count = %v, want 2", regCount)
	}

	if entryCount != 150 {
		t.Errorf("FederationMetrics entry count = %v, want 150", entryCount)
	}

	expectedAvgLatency := 150 * time.Millisecond
	if avgLatency != expectedAvgLatency {
		t.Errorf("FederationMetrics avg latency = %v, want %v", avgLatency, expectedAvgLatency)
	}

	// errorRate = errors / registries = 3 / 2 = 1.5
	if errorRate != 1.5 {
		t.Errorf("FederationMetrics error rate = %v, want 1.5", errorRate)
	}
}

func TestFederatedSearchResult(t *testing.T) {
	result := FederatedSearchResult{
		SearchResult: SearchResult{
			Identifier:  "urn:air:test.com:agent:test",
			DisplayName: "Test Agent",
			Type:        MediaTypeA2AAgentCard,
			URL:         "https://test.com/agent.json",
			Description: "A test agent",
			Score:       0.95,
		},
		RegistryURL:  "http://registry.com",
		RegistryName: "Test Registry",
		TrustLevel:   TrustLevelDirect,
		Latency:      50 * time.Millisecond,
	}

	if result.RegistryURL != "http://registry.com" {
		t.Errorf("FederatedSearchResult.RegistryURL = %v, want http://registry.com", result.RegistryURL)
	}

	if result.RegistryName != "Test Registry" {
		t.Errorf("FederatedSearchResult.RegistryName = %v, want Test Registry", result.RegistryName)
	}

	if result.TrustLevel != TrustLevelDirect {
		t.Errorf("FederatedSearchResult.TrustLevel = %v, want TrustLevelDirect", result.TrustLevel)
	}

	if result.Latency != 50*time.Millisecond {
		t.Errorf("FederatedSearchResult.Latency = %v, want 50ms", result.Latency)
	}
}

func TestFederationResult(t *testing.T) {
	result := &FederationResult{
		Results: []FederatedSearchResult{
			{
				SearchResult: SearchResult{
					Identifier:  "urn:air:reg1.com:agent:a",
					DisplayName: "Agent A",
					Score:       0.9,
				},
				RegistryURL: "http://reg1.com",
				TrustLevel:  TrustLevelDirect,
			},
			{
				SearchResult: SearchResult{
					Identifier:  "urn:air:reg2.com:agent:b",
					DisplayName: "Agent B",
					Score:       0.8,
				},
				RegistryURL: "http://reg2.com",
				TrustLevel:  TrustLevelIndirect,
			},
		},
		TotalCount:    2,
		RegistryCount: 2,
		Metrics:       NewFederationMetrics(),
		Errors: []FederationError{
			{
				RegistryURL: "http://reg3.com",
				Error:       nil,
				Timestamp:   time.Now(),
			},
		},
	}

	if len(result.Results) != 2 {
		t.Errorf("FederationResult.Results length = %v, want 2", len(result.Results))
	}

	if result.TotalCount != 2 {
		t.Errorf("FederationResult.TotalCount = %v, want 2", result.TotalCount)
	}

	if result.RegistryCount != 2 {
		t.Errorf("FederationResult.RegistryCount = %v, want 2", result.RegistryCount)
	}

	if len(result.Errors) != 1 {
		t.Errorf("FederationResult.Errors length = %v, want 1", len(result.Errors))
	}
}

func TestFederator(t *testing.T) {
	client := NewClient(10 * time.Second)
	config := &FederationConfig{
		Timeout:         10 * time.Second,
		MaxRegistries:   5,
		MaxDepth:        2,
		EnableReferrals: true,
		TrustPolicy:     TrustPolicyKnown,
	}

	federator := NewFederator(client, config)

	if federator == nil {
		t.Fatal("NewFederator returned nil")
	}

	if federator.metrics == nil {
		t.Error("Federator.metrics is nil")
	}

	if federator.client == nil {
		t.Error("Federator.client is nil")
	}
}

func TestFederatorKnownRegistries(t *testing.T) {
	client := NewClient(10 * time.Second)
	federator := NewFederator(client, nil)

	// Add known registries
	federator.AddKnownRegistry("http://reg1.com", "Registry 1", TrustLevelDirect)
	federator.AddKnownRegistry("http://reg2.com", "Registry 2", TrustLevelIndirect)

	// Get known registries
	known := federator.GetKnownRegistries()

	if len(known) != 2 {
		t.Errorf("GetKnownRegistries() returned %v registries, want 2", len(known))
	}

	// Check registry info
	found := false
	for _, info := range known {
		if info.URL == "http://reg1.com" && info.Name == "Registry 1" && info.TrustLevel == TrustLevelDirect {
			found = true
			break
		}
	}

	if !found {
		t.Error("Registry 1 not found with correct info")
	}
}

func TestFederatorRemoveKnownRegistry(t *testing.T) {
	client := NewClient(10 * time.Second)
	federator := NewFederator(client, nil)

	// Add and remove
	federator.AddKnownRegistry("http://temp.com", "Temp Registry", TrustLevelDirect)
	federator.RemoveKnownRegistry("http://temp.com")

	known := federator.GetKnownRegistries()

	for _, info := range known {
		if info.URL == "http://temp.com" {
			t.Error("Temp registry should have been removed")
		}
	}
}

func TestFederatorGetRegistryInfo(t *testing.T) {
	client := NewClient(10 * time.Second)
	federator := NewFederator(client, nil)

	// Add known registry
	federator.AddKnownRegistry("http://known.com", "Known Registry", TrustLevelDirect)

	// Get info for known registry
	info := federator.getRegistryInfo("http://known.com")
	if info.Name != "Known Registry" {
		t.Errorf("getRegistryInfo() name = %v, want Known Registry", info.Name)
	}

	// Get info for unknown registry
	info = federator.getRegistryInfo("http://unknown.com")
	if info.TrustLevel != TrustLevelUnknown {
		t.Errorf("getRegistryInfo() trust level = %v, want TrustLevelUnknown", info.TrustLevel)
	}
}

func TestDefaultFederationOptions(t *testing.T) {
	opts := DefaultFederationOptions()

	if opts.ReferralMode != ReferralModeChain {
		t.Errorf("DefaultFederationOptions.ReferralMode = %v, want ReferralModeChain", opts.ReferralMode)
	}

	if !opts.IncludeMetrics {
		t.Error("DefaultFederationOptions.IncludeMetrics should be true")
	}

	if !opts.IncludeErrors {
		t.Error("DefaultFederationOptions.IncludeErrors should be true")
	}

	if opts.TimeoutPerRegistry != 10*time.Second {
		t.Errorf("DefaultFederationOptions.TimeoutPerRegistry = %v, want 10s", opts.TimeoutPerRegistry)
	}

	if opts.MaxResultsPerRegistry != 50 {
		t.Errorf("DefaultFederationOptions.MaxResultsPerRegistry = %v, want 50", opts.MaxResultsPerRegistry)
	}
}

func TestReferralMode(t *testing.T) {
	tests := []struct {
		name string
		mode ReferralMode
	}{
		{"ReferralModeNone", ReferralModeNone},
		{"ReferralModeDirect", ReferralModeDirect},
		{"ReferralModeChain", ReferralModeChain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if int(tt.mode) < 0 || int(tt.mode) > 2 {
				t.Errorf("ReferralMode %s has invalid value", tt.name)
			}
		})
	}
}

func TestFederationError(t *testing.T) {
	err := FederationError{
		RegistryURL: "http://fail.com",
		Error:       nil,
		Timestamp:   time.Now(),
	}

	if err.RegistryURL != "http://fail.com" {
		t.Errorf("FederationError.RegistryURL = %v, want http://fail.com", err.RegistryURL)
	}
}

func TestRegistryInfo(t *testing.T) {
	info := &RegistryInfo{
		URL:         "http://test.com",
		Name:        "Test Registry",
		TrustLevel:  TrustLevelDirect,
		LastSeen:    time.Now(),
		ReferralURL: "http://referral.com",
	}

	if info.URL != "http://test.com" {
		t.Errorf("RegistryInfo.URL = %v, want http://test.com", info.URL)
	}

	if info.Name != "Test Registry" {
		t.Errorf("RegistryInfo.Name = %v, want Test Registry", info.Name)
	}

	if info.TrustLevel != TrustLevelDirect {
		t.Errorf("RegistryInfo.TrustLevel = %v, want TrustLevelDirect", info.TrustLevel)
	}
}

func TestFederatorMetrics(t *testing.T) {
	client := NewClient(10 * time.Second)
	federator := NewFederator(client, nil)

	metrics := federator.GetMetrics()

	if metrics == nil {
		t.Error("GetMetrics() returned nil")
	}

	// Record some data
	metrics.RecordLatency("http://test.com", 100*time.Millisecond)
	metrics.RecordError("http://test.com")
	metrics.IncrementRegistryCount() // Need to increment count for it to be reflected

	// Get stats again
	regCount, _, _, _ := metrics.GetStats()

	if regCount != 1 {
		t.Errorf("After recording, registry count = %v, want 1", regCount)
	}
}

func TestMediaTypeAIRegistry(t *testing.T) {
	if MediaTypeAIRegistry != "application/ai-registry+json" {
		t.Errorf("MediaTypeAIRegistry = %v, want application/ai-registry+json", MediaTypeAIRegistry)
	}
}
