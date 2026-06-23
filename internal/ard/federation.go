// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// FederationConfig holds configuration for federated search.
type FederationConfig struct {
	// Timeout for each registry query
	Timeout time.Duration

	// MaxRegistries is the maximum number of registries to query
	MaxRegistries int

	// MaxDepth is the maximum referral depth for discovery
	MaxDepth int

	// EnableReferrals enables referral-based discovery
	EnableReferrals bool

	// TrustPolicy controls which registries to trust
	TrustPolicy TrustPolicy

	// LocalRegistryURL is the local registry URL (skipped in federation)
	LocalRegistryURL string
}

// TrustPolicy defines how to trust remote registries.
type TrustPolicy int

const (
	// TrustPolicyAny allows any registry
	TrustPolicyAny TrustPolicy = iota
	// TrustPolicyKnown allows only known registries
	TrustPolicyKnown
	// TrustPolicyVerified requires verification
	TrustPolicyVerified
)

// FederationMetrics holds metrics for federated operations.
type FederationMetrics struct {
	mu            sync.RWMutex
	registryCount int
	entryCount    int64
	latencies     map[string]time.Duration
	errors        map[string]int
}

// NewFederationMetrics creates a new metrics collector.
func NewFederationMetrics() *FederationMetrics {
	return &FederationMetrics{
		latencies: make(map[string]time.Duration),
		errors:    make(map[string]int),
	}
}

// RecordLatency records a latency for a registry.
func (m *FederationMetrics) RecordLatency(registry string, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latencies[registry] = latency
}

// RecordError records an error for a registry.
func (m *FederationMetrics) RecordError(registry string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[registry]++
}

// IncrementRegistryCount increments the registry count.
func (m *FederationMetrics) IncrementRegistryCount() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registryCount++
}

// AddEntries adds entries to the count.
func (m *FederationMetrics) AddEntries(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entryCount += count
}

// GetStats returns current statistics.
func (m *FederationMetrics) GetStats() (registryCount int, entryCount int64, avgLatency time.Duration, errorRate float64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	registryCount = m.registryCount
	entryCount = m.entryCount

	if len(m.latencies) > 0 {
		var total time.Duration
		for _, lat := range m.latencies {
			total += lat
		}
		avgLatency = total / time.Duration(len(m.latencies))
	}

	if registryCount > 0 {
		var totalErrors int
		for _, err := range m.errors {
			totalErrors += err
		}
		errorRate = float64(totalErrors) / float64(registryCount)
	}

	return
}

// FederationResult represents a federated search result.
type FederationResult struct {
	Results    []FederatedSearchResult
	TotalCount int
	RegistryCount int
	Metrics    *FederationMetrics
	Errors     []FederationError
}

// FederatedSearchResult extends SearchResult with registry info.
type FederatedSearchResult struct {
	SearchResult
	RegistryURL  string
	RegistryName string
	TrustLevel   TrustLevel
	Latency      time.Duration
}

// TrustLevel represents the trust level of a registry.
type TrustLevel int

const (
	// TrustLevelUnknown unknown trust level
	TrustLevelUnknown TrustLevel = iota
	// TrustLevelDirect direct trust
	TrustLevelDirect
	// TrustLevelIndirect indirect trust via referral
	TrustLevelIndirect
	// TrustLevelUntrusted untrusted registry
	TrustLevelUntrusted
)

// FederationError represents an error from a federated operation.
type FederationError struct {
	RegistryURL string
	Error       error
	Timestamp   time.Time
}

// Federator handles federated search across multiple registries.
type Federator struct {
	client  *Client
	config  *FederationConfig
	metrics *FederationMetrics

	// Known registries cache
	knownRegistries map[string]*RegistryInfo
	knownMu         sync.RWMutex
}

// RegistryInfo holds information about a known registry.
type RegistryInfo struct {
	URL         string
	Name        string
	TrustLevel  TrustLevel
	LastSeen    time.Time
	ReferralURL string
}

// NewFederator creates a new federator.
func NewFederator(client *Client, config *FederationConfig) *Federator {
	if config == nil {
		config = &FederationConfig{
			Timeout:         30 * time.Second,
			MaxRegistries:   10,
			MaxDepth:        3,
			EnableReferrals: true,
			TrustPolicy:     TrustPolicyKnown,
		}
	}

	return &Federator{
		client:          client,
		config:          config,
		metrics:         NewFederationMetrics(),
		knownRegistries: make(map[string]*RegistryInfo),
	}
}

// FederatedSearch performs a federated search across multiple registries.
func (f *Federator) FederatedSearch(ctx context.Context, req *SearchRequest) (*FederationResult, error) {
	result := &FederationResult{
		Metrics: f.metrics,
	}

	// Discover registries
	registries, err := f.DiscoverRegistriesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover registries: %w", err)
	}

	if len(registries) == 0 {
		return result, nil
	}

	// Limit registries
	if len(registries) > f.config.MaxRegistries {
		registries = registries[:f.config.MaxRegistries]
	}

	// Search in parallel
	type searchResult struct {
		registryURL string
		resp        *SearchResponse
		err         error
		latency     time.Duration
	}

	searchCh := make(chan searchResult, len(registries))
	var wg sync.WaitGroup

	for _, url := range registries {
		// Skip local registry
		if url == f.config.LocalRegistryURL {
			continue
		}

		wg.Add(1)
		go func(regURL string) {
			defer wg.Done()

			start := time.Now()
			resp, err := f.client.Search(ctx, regURL, req)
			latency := time.Since(start)

			f.metrics.RecordLatency(regURL, latency)

			if err != nil {
				f.metrics.RecordError(regURL)
				result.Errors = append(result.Errors, FederationError{
					RegistryURL: regURL,
					Error:       err,
					Timestamp:   time.Now(),
				})
			}

			searchCh <- searchResult{
				registryURL: regURL,
				resp:        resp,
				err:         err,
				latency:     latency,
			}
		}(url)
	}

	// Close channel when done
	go func() {
		wg.Wait()
		close(searchCh)
	}()

	// Collect results
	seen := make(map[string]bool)
	for sr := range searchCh {
		result.RegistryCount++
		f.metrics.IncrementRegistryCount()

		if sr.err != nil {
			continue
		}

		for _, r := range sr.resp.Results {
			if !seen[r.Identifier] {
				seen[r.Identifier] = true

				// Get registry info
				registryInfo := f.getRegistryInfo(sr.registryURL)

				result.Results = append(result.Results, FederatedSearchResult{
					SearchResult: r,
					RegistryURL:  sr.registryURL,
					RegistryName: registryInfo.Name,
					TrustLevel:   registryInfo.TrustLevel,
					Latency:      sr.latency,
				})

				result.TotalCount++
			}
		}

		f.metrics.AddEntries(int64(len(sr.resp.Results)))
	}

	// Sort by score
	sort.Slice(result.Results, func(i, j int) bool {
		return result.Results[i].Score > result.Results[j].Score
	})

	return result, nil
}

// DiscoverRegistriesWithContext discovers registries using the configured discovery mechanism.
func (f *Federator) DiscoverRegistriesWithContext(ctx context.Context) ([]string, error) {
	var registries []string

	// Get known registries
	f.knownMu.RLock()
	for url := range f.knownRegistries {
		registries = append(registries, url)
	}
	f.knownMu.RUnlock()

	// If referrals enabled, discover more
	if f.config.EnableReferrals {
		discovered, err := f.discoverViaReferrals(ctx, registries)
		if err == nil {
			registries = append(registries, discovered...)
		}
	}

	return registries, nil
}

// discoverViaReferrals discovers registries via referral chain.
func (f *Federator) discoverViaReferrals(ctx context.Context, seed []string) ([]string, error) {
	seen := make(map[string]bool)

	// Add seed registries to seen
	for _, url := range seed {
		seen[url] = true
	}

	var discovered []string
	queue := make([]string, len(seed))
	copy(queue, seed)

	for depth := 0; depth < f.config.MaxDepth && len(queue) > 0 && len(discovered) < f.config.MaxRegistries; depth++ {
		var nextQueue []string

		for _, url := range queue {
			if seen[url] {
				continue
			}
			seen[url] = true

			// Fetch catalog
			catalog, err := f.client.FetchCatalog(ctx, url)
			if err != nil {
				continue
			}

			// Register this registry
			f.registerRegistry(url, catalog)

			// Look for registry referrals
			for _, entry := range catalog.Entries {
				if entry.Type == MediaTypeAIRegistry && entry.URL != "" && !seen[entry.URL] {
					nextQueue = append(nextQueue, entry.URL)
					discovered = append(discovered, entry.URL)
				}
			}
		}

		queue = nextQueue
	}

	return discovered, nil
}

// registerRegistry registers a discovered registry.
func (f *Federator) registerRegistry(url string, catalog *AICatalog) {
	f.knownMu.Lock()
	defer f.knownMu.Unlock()

	info, exists := f.knownRegistries[url]
	if !exists {
		info = &RegistryInfo{
			URL:        url,
			TrustLevel: TrustLevelUnknown,
		}
		f.knownRegistries[url] = info
	}

	info.Name = catalog.Host.DisplayName
	info.LastSeen = time.Now()
}

// getRegistryInfo returns info for a registry.
func (f *Federator) getRegistryInfo(url string) *RegistryInfo {
	f.knownMu.RLock()
	defer f.knownMu.RUnlock()

	if info, exists := f.knownRegistries[url]; exists {
		return info
	}

	return &RegistryInfo{
		URL:        url,
		TrustLevel: TrustLevelUnknown,
	}
}

// AddKnownRegistry adds a known registry.
func (f *Federator) AddKnownRegistry(url, name string, trustLevel TrustLevel) {
	f.knownMu.Lock()
	defer f.knownMu.Unlock()

	f.knownRegistries[url] = &RegistryInfo{
		URL:        url,
		Name:       name,
		TrustLevel: trustLevel,
		LastSeen:   time.Now(),
	}
}

// RemoveKnownRegistry removes a known registry.
func (f *Federator) RemoveKnownRegistry(url string) {
	f.knownMu.Lock()
	defer f.knownMu.Unlock()

	delete(f.knownRegistries, url)
}

// GetKnownRegistries returns all known registries.
func (f *Federator) GetKnownRegistries() []*RegistryInfo {
	f.knownMu.RLock()
	defer f.knownMu.RUnlock()

	infos := make([]*RegistryInfo, 0, len(f.knownRegistries))
	for _, info := range f.knownRegistries {
		infos = append(infos, info)
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].URL < infos[j].URL
	})

	return infos
}

// GetMetrics returns federation metrics.
func (f *Federator) GetMetrics() *FederationMetrics {
	return f.metrics
}



// ReferralMode represents how referrals are followed.
type ReferralMode int

const (
	// ReferralModeNone no referrals
	ReferralModeNone ReferralMode = iota
	// ReferralModeDirect direct referrals only
	ReferralModeDirect
	// ReferralModeChain full chain of referrals
	ReferralModeChain
)

// FederationOptions holds options for federated operations.
type FederationOptions struct {
	// ReferralMode controls how referrals are followed
	ReferralMode ReferralMode

	// IncludeMetrics includes metrics in the response
	IncludeMetrics bool

	// IncludeErrors includes errors in the response
	IncludeErrors bool

	// TimeoutPerRegistry sets timeout per registry (overrides config)
	TimeoutPerRegistry time.Duration

	// MaxResultsPerRegistry limits results per registry
	MaxResultsPerRegistry int
}

// DefaultFederationOptions returns default federation options.
func DefaultFederationOptions() *FederationOptions {
	return &FederationOptions{
		ReferralMode:        ReferralModeChain,
		IncludeMetrics:      true,
		IncludeErrors:       true,
		TimeoutPerRegistry:  10 * time.Second,
		MaxResultsPerRegistry: 50,
	}
}
