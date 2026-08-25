// Package knowledge provides RAG (Retrieval-Augmented Generation) knowledge
// base management built on tRPC-Agent-Go's knowledge module.
//
// It supports:
//   - OpenAI-compatible embedding models for text vectorization
//   - In-memory vector store with cosine similarity search
//   - Local directory, file, and URL document sources
//   - Knowledge search tool integration with the agent
package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	embedderopenai "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	knowledgesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Manager wraps tRPC-Agent-Go's BuiltinKnowledge to provide RAG capabilities
// with a simplified configuration interface.
type Manager struct {
	cfg        *config.KnowledgeConfig
	kb         *knowledge.BuiltinKnowledge
	searchTool tool.Tool
	loaded     bool
}

// NewManager creates a knowledge manager from configuration.
// When cfg.Enabled is false, returns nil with no error (graceful disable).
func NewManager(
	knowledgeCfg *config.KnowledgeConfig,
	wukongCfg *config.WukongConfig,
) (*Manager, error) {
	if !knowledgeCfg.Enabled {
		util.Logger.Info("knowledge: disabled by config")
		return nil, nil
	}

	m := &Manager{cfg: knowledgeCfg}

	// Resolve embedder credentials from the Wukong provider configuration.
	embedderModel := knowledgeCfg.EmbedderModel
	if embedderModel == "" {
		embedderModel = "text-embedding-3-small"
	}

	apiKey, baseURL := resolveEmbedderCredentials(
		knowledgeCfg.EmbedderProvider, wukongCfg,
	)

	embedder := embedderopenai.New(
		embedderopenai.WithModel(embedderModel),
		embedderopenai.WithAPIKey(apiKey),
		embedderopenai.WithBaseURL(baseURL),
	)
	util.Logger.Info("knowledge: embedder created",
		slog.String("model", embedderModel),
	)

	// Create in-memory vector store.
	vs := inmemory.New()

	// Collect document sources from config.
	sources, err := m.collectSources()
	if err != nil {
		return nil, fmt.Errorf("collect sources: %w", err)
	}

	if len(sources) == 0 {
		util.Logger.Warn("knowledge: no sources configured, " +
			"knowledge base will be empty until sources are added")
	}

	// Build the knowledge base.
	var opts []knowledge.Option
	opts = append(opts, knowledge.WithEmbedder(embedder))
	opts = append(opts, knowledge.WithVectorStore(vs))
	opts = append(opts, knowledge.WithEnableSourceSync(
		knowledgeCfg.EnableSourceSync,
	))
	if len(sources) > 0 {
		opts = append(opts, knowledge.WithSources(sources))
	}

	kb := knowledge.New(opts...)

	// Load and index all documents.
	if len(sources) > 0 {
		util.Logger.Info("knowledge: loading documents...",
			slog.Int("source_count", len(sources)),
		)
		if err := kb.Load(
			context.Background(),
			knowledge.WithShowProgress(true),
		); err != nil {
			return nil, fmt.Errorf("load knowledge: %w", err)
		}
		util.Logger.Info("knowledge: documents loaded successfully")
	}

	// Create the search tool.
	searchToolName := knowledgeCfg.SearchToolName
	if searchToolName == "" {
		searchToolName = "knowledge_search"
	}
	searchTool := knowledgetool.NewKnowledgeSearchTool(
		kb,
		knowledgetool.WithToolName(searchToolName),
		knowledgetool.WithToolDescription(
			"Search the knowledge base for documents relevant to the query. "+
				"Returns the most relevant text passages with relevance scores.",
		),
	)

	m.kb = kb
	if ct, ok := searchTool.(tool.CallableTool); ok {
		m.searchTool = &gracefulSearchTool{inner: ct}
	} else {
		m.searchTool = searchTool
	}
	m.loaded = true

	util.Logger.Info("knowledge: manager initialized",
		slog.String("tool", searchToolName),
		slog.Int("sources", len(sources)),
	)

	return m, nil
}

// SearchTool returns the knowledge search tool for integration
// into the agent's tool list. Returns nil if knowledge is disabled.
func (m *Manager) SearchTool() tool.Tool {
	if m == nil || !m.loaded {
		return nil
	}
	return m.searchTool
}

// IsEnabled reports whether the knowledge manager is active.
func (m *Manager) IsEnabled() bool {
	return m != nil && m.loaded
}

// Close releases resources held by the knowledge manager.
func (m *Manager) Close() error {
	if m == nil || m.kb == nil {
		return nil
	}
	return m.kb.Close()
}

// collectSources builds the list of document sources from configuration.
func (m *Manager) collectSources() ([]knowledgesource.Source, error) {
	var sources []knowledgesource.Source

	// Directory sources: each path is scanned recursively for
	// supported formats (txt, md, pdf, csv, json, docx).
	for _, dirPath := range m.cfg.Sources {
		if dirPath == "" {
			continue
		}
		src := dir.New([]string{dirPath},
			dir.WithRecursive(true),
		)
		sources = append(sources, src)
	}

	// URL sources: fetch and index remote documents.
	for _, u := range m.cfg.SourceURLs {
		if u == "" {
			continue
		}
		src := url.New([]string{u})
		sources = append(sources, src)
	}

	return sources, nil
}

// resolveEmbedderCredentials determines embedder API credentials.
// Priority:
//  1. knowledge.embedder_provider (must match a name in providers[])
//  2. cortex.embedding_base_url + cortex.embedding_api_key (when cortex.enabled)
//  3. default LLM provider
//
// The cortex fallback lets knowledge share the embedding service configured
// for CortexDB without requiring a duplicate provider entry.
func resolveEmbedderCredentials(
	providerName string,
	wukongCfg *config.WukongConfig,
) (apiKey, baseURL string) {
	if providerName != "" {
		p := wukongCfg.FindProvider(providerName)
		if p != nil {
			return p.APIKey, p.BaseURL
		}
	}

	// Fall back to CortexDB embedding settings (shared embedding service).
	if wukongCfg.Cortex.Enabled {
		if wukongCfg.Cortex.EmbeddingBaseURL != "" {
			return wukongCfg.Cortex.EmbeddingAPIKey,
				wukongCfg.Cortex.EmbeddingBaseURL
		}
	}

	// Final fallback: default LLM provider.
	if dp := wukongCfg.DefaultProviderConfig(); dp != nil {
		return dp.APIKey, dp.BaseURL
	}

	return "", ""
}

// gracefulSearchTool wraps the knowledge search tool so that
// "no relevant documents found" is returned as a normal (non-error)
// result. This lets the agent gracefully fall back to other tools
// (e.g. web search) instead of treating an empty knowledge base hit
// as a tool-call failure.
type gracefulSearchTool struct {
	inner tool.CallableTool
}

func (g *gracefulSearchTool) Declaration() *tool.Declaration {
	return g.inner.Declaration()
}

func (g *gracefulSearchTool) Call(
	ctx context.Context, jsonArgs []byte,
) (any, error) {
	result, err := g.inner.Call(ctx, jsonArgs)
	if err != nil &&
		strings.Contains(err.Error(), "no relevant documents found") {
		return "No relevant documents found in the knowledge base " +
			"for this query. Consider using web search instead.", nil
	}
	return result, err
}
