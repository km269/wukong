package tune

import (
	"context"
	"fmt"

	"github.com/km269/wukong/internal/cortex"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/search"
)

// ----------------------------------------------------------------------------
// Searcher adapters: wrap existing search backends to satisfy the
// tune.Searcher interface.
// ----------------------------------------------------------------------------

// CortexSearcher adapts cortex.CortexStore to the Searcher interface.
type CortexSearcher struct {
	store *cortex.CortexStore
}

// NewCortexSearcher creates a Searcher backed by CortexStore.
func NewCortexSearcher(store *cortex.CortexStore) *CortexSearcher {
	return &CortexSearcher{store: store}
}

// SearchWithGenome applies the genome to the CortexStore and
// executes a search, returning ranked items.
func (s *CortexSearcher) SearchWithGenome(
	ctx context.Context,
	query string,
	genome search.SearchGenome,
) ([]search.RankedItem, error) {
	// Temporarily apply the genome for this search.
	s.store.SetGenome(genome)

	results, err := s.store.Search(query, "", genome.EffectiveTopK())
	if err != nil {
		return nil, fmt.Errorf("cortex searcher: %w", err)
	}

	items := make([]search.RankedItem, len(results))
	for i, r := range results {
		// Use message ID or preview hash as the item ID.
		id := r.Preview
		if id == "" {
			id = fmt.Sprintf("result_%d", i)
		}
		items[i] = search.RankedItem{
			ID:    id,
			Score: r.Score,
		}
	}
	return items, nil
}

// RecallSearcher adapts recall.Store to the Searcher interface.
type RecallSearcher struct {
	store *recall.Store
}

// NewRecallSearcher creates a Searcher backed by recall.Store.
func NewRecallSearcher(store *recall.Store) *RecallSearcher {
	return &RecallSearcher{store: store}
}

// SearchWithGenome applies the genome and executes a hybrid or
// lexical search depending on the genome's recall mode.
func (s *RecallSearcher) SearchWithGenome(
	ctx context.Context,
	query string,
	genome search.SearchGenome,
) ([]search.RankedItem, error) {
	s.store.SetGenome(genome)

	var results []recall.SearchResult
	var err error

	switch {
	case genome.IsHybrid() && s.store.HasHybridSearch():
		results, err = s.store.SearchHybrid(
			ctx, query, "", genome.EffectiveTopK())
	case genome.IsLexicalOnly():
		results, err = s.store.Search(
			query, "", genome.EffectiveTopK())
	default:
		if s.store.HasHybridSearch() {
			results, err = s.store.SearchHybrid(
				ctx, query, "", genome.EffectiveTopK())
		} else {
			results, err = s.store.Search(
				query, "", genome.EffectiveTopK())
		}
	}

	if err != nil {
		return nil, fmt.Errorf("recall searcher: %w", err)
	}

	items := make([]search.RankedItem, len(results))
	for i, r := range results {
		// Use message ID as item ID for label matching.
		id := fmt.Sprintf("msg_%d", r.Message.ID)
		if id == "msg_0" {
			id = r.Preview
		}
		items[i] = search.RankedItem{
			ID:    id,
			Score: r.Score,
		}
	}
	return items, nil
}
