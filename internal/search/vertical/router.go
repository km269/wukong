// Package vertical implements query-intent-based routing to
// specialised search backends (arXiv, GitHub, Wikipedia, Reddit,
// Hacker News). Inspired by AnySearch's vertical search
// infrastructure, it detects query intent via keyword patterns
// and dispatches to the matching platform's search API —
// bypassing the local knowledge base for fresh, authoritative
// results when the query targets a known vertical.
package vertical

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/logutil"
)

// Intent represents a detected search vertical.
type Intent string

const (
	// IntentGeneral means no specific vertical matched; the
	// caller should use the local knowledge base.
	IntentGeneral Intent = "general"
	// IntentAcademic targets research papers (arXiv).
	IntentAcademic Intent = "academic"
	// IntentCode targets source code and repositories (GitHub).
	IntentCode Intent = "code"
	// IntentEncyclopedia targets factual/reference lookups
	// (Wikipedia).
	IntentEncyclopedia Intent = "encyclopedia"
	// IntentDiscussion targets community discussions (Reddit,
	// Hacker News).
	IntentDiscussion Intent = "discussion"
)

// Result is a single vertical search hit.
type Result struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Preview string  `json:"preview"`
	Score   float64 `json:"score"`
	Source  string  `json:"source"` // backend name
}

// Backend performs a search on a specific platform.
type Backend interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

// MergeMode constants control how vertical results combine
// with local retrieval results.
const (
	MergePrepend = "prepend" // vertical results first (default)
	MergeAppend  = "append"  // local results first
	MergeReplace = "replace" // vertical results only
)

// Config controls vertical routing behaviour.
type Config struct {
	Enabled bool `mapstructure:"enabled"`
	// TopN is the max results fetched per backend.
	TopN int `mapstructure:"top_n"`
	// Timeout is the per-backend deadline.
	Timeout time.Duration `mapstructure:"timeout"`
	// GitHubAPIKey enables higher GitHub rate limits when set.
	GitHubAPIKey string `mapstructure:"github_api_key"`
	// MergeMode controls how vertical results combine with
	// local retrieval. One of MergePrepend|MergeAppend|MergeReplace.
	MergeMode string `mapstructure:"merge_mode"`
}

// EffectiveMergeMode returns the merge mode, defaulting to prepend.
func (c Config) EffectiveMergeMode() string {
	switch c.MergeMode {
	case MergePrepend, MergeAppend, MergeReplace:
		return c.MergeMode
	default:
		return MergePrepend
	}
}

// Router detects query intent and dispatches to specialised
// backends. When no vertical matches, Search returns nil and
// the caller falls back to the local knowledge base.
type Router struct {
	cfg      Config
	backends map[Intent]Backend
	detector *IntentDetector
}

// NewRouter builds a Router from config. Returns nil (disabled)
// when cfg.Enabled is false.
func NewRouter(cfg Config) *Router {
	if !cfg.Enabled {
		return nil
	}
	if cfg.TopN <= 0 {
		cfg.TopN = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	r := &Router{
		cfg:      cfg,
		backends: make(map[Intent]Backend),
		detector: NewIntentDetector(),
	}
	r.registerBackends()
	return r
}

func (r *Router) registerBackends() {
	r.backends[IntentAcademic] = &arXivBackend{}
	r.backends[IntentCode] = &githubBackend{
		apiKey: r.cfg.GitHubAPIKey,
	}
	r.backends[IntentEncyclopedia] = &wikipediaBackend{}
	r.backends[IntentDiscussion] = &redditBackend{}
}

// Enabled reports whether vertical routing is active.
func (r *Router) Enabled() bool { return r != nil }

// Search detects the query's intent and dispatches to the
// matching backend. Returns nil, nil when no vertical matches
// (caller should fall back to local retrieval).
func (r *Router) Search(
	ctx context.Context, query string, limit int,
) ([]Result, error) {
	if r == nil || !r.cfg.Enabled {
		return nil, nil
	}
	intent := r.detector.Detect(query)
	if intent == IntentGeneral {
		return nil, nil
	}
	backend, ok := r.backends[intent]
	if !ok {
		return nil, nil
	}
	if limit <= 0 || limit > r.cfg.TopN {
		limit = r.cfg.TopN
	}

	cctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	results, err := backend.Search(cctx, query, limit)
	if err != nil {
		logutil.Debug("vertical backend failed",
			"intent", string(intent),
			"backend", backend.Name(),
			"err", err.Error())
		return nil, err
	}
	logutil.Debug("vertical routing hit",
		"intent", string(intent),
		"backend", backend.Name(),
		"results", len(results))
	return results, nil
}

// SearchAll dispatches to every enabled backend concurrently,
// merging results by descending score. Used when the caller
// wants broad coverage regardless of intent (e.g., a "deep"
// recall mode).
func (r *Router) SearchAll(
	ctx context.Context, query string, limit int,
) ([]Result, error) {
	if r == nil || !r.cfg.Enabled {
		return nil, nil
	}
	if limit <= 0 || limit > r.cfg.TopN {
		limit = r.cfg.TopN
	}

	type backendResult struct {
		results []Result
		err     error
		name    string
	}

	var wg sync.WaitGroup
	resultsCh := make(chan backendResult, len(r.backends))
	for intent, backend := range r.backends {
		wg.Add(1)
		go func(i Intent, b Backend) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
			defer cancel()
			res, err := b.Search(cctx, query, limit)
			resultsCh <- backendResult{results: res, err: err, name: b.Name()}
			_ = i
		}(intent, backend)
	}
	wg.Wait()
	close(resultsCh)

	var all []Result
	for br := range resultsCh {
		if br.err != nil {
			continue
		}
		all = append(all, br.results...)
	}
	// Sort by score descending.
	for i := 0; i < len(all)-1; i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].Score > all[i].Score {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// DetectIntent exposes the intent detector for testing/inspection.
func (r *Router) DetectIntent(query string) Intent {
	if r == nil {
		return IntentGeneral
	}
	return r.detector.Detect(query)
}

// SanitizeQuery strips operator characters that could break
// platform search APIs. Kept simple to avoid over-engineering.
func SanitizeQuery(q string) string {
	q = strings.TrimSpace(q)
	// Collapse whitespace.
	for strings.Contains(q, "  ") {
		q = strings.ReplaceAll(q, "  ", " ")
	}
	return q
}
