// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CatalogManager manages ARD catalogs with file persistence.
type CatalogManager struct {
	catalog *AICatalog
	path   string
}

// NewCatalogManager creates a new catalog manager.
func NewCatalogManager(path string) (*CatalogManager, error) {
	cm := &CatalogManager{
		path: path,
	}
	
	// Try to load existing catalog
	if path != "" {
		if err := cm.Load(); err != nil {
			// Start with empty catalog
			cm.catalog = NewAICatalog("Wukong", "did:web:wukong.local")
		}
	} else {
		cm.catalog = NewAICatalog("Wukong", "did:web:wukong.local")
	}
	
	return cm, nil
}

// Load loads the catalog from disk.
func (cm *CatalogManager) Load() error {
	if cm.path == "" {
		return fmt.Errorf("no catalog path configured")
	}
	
	data, err := os.ReadFile(cm.path)
	if err != nil {
		return fmt.Errorf("read catalog: %w", err)
	}
	
	catalog := &AICatalog{}
	if err := json.Unmarshal(data, catalog); err != nil {
		return fmt.Errorf("parse catalog: %w", err)
	}
	
	cm.catalog = catalog
	return nil
}

// Save saves the catalog to disk.
func (cm *CatalogManager) Save() error {
	if cm.path == "" {
		return fmt.Errorf("no catalog path configured")
	}
	
	// Ensure directory exists
	dir := filepath.Dir(cm.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	
	data, err := json.MarshalIndent(cm.catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	
	if err := os.WriteFile(cm.path, data, 0644); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	
	return nil
}

// GetCatalog returns the catalog.
func (cm *CatalogManager) GetCatalog() *AICatalog {
	return cm.catalog
}

// SetHost sets the host information.
func (cm *CatalogManager) SetHost(displayName, identifier string) {
	cm.catalog.Host.DisplayName = displayName
	cm.catalog.Host.Identifier = identifier
}

// AddServer adds an MCP server to the catalog.
func (cm *CatalogManager) AddServer(name, description, url string, tools []string, tags []string) error {
	identifier := WukongLocal.Build("server:" + sanitizeName(name))
	
	entry := CatalogEntry{
		Identifier:   identifier.String(),
		DisplayName:  name,
		Type:         MediaTypeMCPServerCard,
		URL:          url,
		Description:  description,
		Capabilities: tools,
		Tags:         tags,
		RepresentativeQueries: BuildRepresentativeQueries(tags, tools),
		Version:      "1.0.0",
		UpdatedAt:    Now(),
	}
	
	cm.catalog.AddEntry(entry)
	return nil
}

// AddAgent adds an A2A agent to the catalog.
func (cm *CatalogManager) AddAgent(name, description, url string, capabilities []string, tags []string) error {
	identifier := WukongLocal.Build("agent:" + sanitizeName(name))
	
	entry := CatalogEntry{
		Identifier:   identifier.String(),
		DisplayName:  name,
		Type:         MediaTypeA2AAgentCard,
		URL:          url,
		Description:  description,
		Capabilities: capabilities,
		Tags:         tags,
		RepresentativeQueries: BuildRepresentativeQueries(tags, capabilities),
		Version:      "1.0.0",
		UpdatedAt:    Now(),
	}
	
	cm.catalog.AddEntry(entry)
	return nil
}

// AddBundle adds a bundled collection to the catalog.
func (cm *CatalogManager) AddBundle(name, description string, entries []CatalogEntry, tags []string) error {
	identifier := WukongLocal.Build("bundle:" + sanitizeName(name))
	
	bundle := AICatalog{
		SpecVersion: SpecVersion,
		Entries:     entries,
	}
	
	bundleData, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}
	
	entry := CatalogEntry{
		Identifier:   identifier.String(),
		DisplayName:  name,
		Type:         MediaTypeAICatalog,
		Description:  description,
		Data:         bundleData,
		Tags:         append(tags, "bundle"),
		UpdatedAt:    Now(),
	}
	
	cm.catalog.AddEntry(entry)
	return nil
}

// Remove removes an entry by identifier.
func (cm *CatalogManager) Remove(identifier string) error {
	if !cm.catalog.RemoveEntry(identifier) {
		return fmt.Errorf("entry not found: %s", identifier)
	}
	return nil
}

// Get returns an entry by identifier.
func (cm *CatalogManager) Get(identifier string) *CatalogEntry {
	return cm.catalog.GetEntry(identifier)
}

// List returns all entries.
func (cm *CatalogManager) List() []CatalogEntry {
	return cm.catalog.Entries
}

// ListByType returns entries filtered by type.
func (cm *CatalogManager) ListByType(mediaType string) []CatalogEntry {
	return cm.catalog.FilterEntries(EntryFilter{Type: mediaType})
}

// Search performs a simple text search.
func (cm *CatalogManager) Search(query string) []CatalogEntry {
	return cm.catalog.FilterEntries(EntryFilter{Query: query})
}

// WukongBuiltInEntries returns the built-in entries for Wukong.
func WukongBuiltInEntries() []CatalogEntry {
	return []CatalogEntry{
		// Server entries
		{
			Identifier:   WukongURNs.AppsServer.String(),
			DisplayName:  "Wukong Apps Manager",
			Type:         MediaTypeMCPServerCard,
			Description:  "HTML application lifecycle platform with create, edit, clone, pack, preview, and export capabilities.",
			Tags:         []string{"html", "apps", "ui", "mcp-apps"},
			Capabilities: []string{"app_create", "app_clone", "app_pack", "app_preview", "app_export"},
			RepresentativeQueries: []string{
				"create an HTML application",
				"clone a website to offline app",
				"build a desktop app from HTML",
			},
			Version:   "1.0.0",
			UpdatedAt: Now(),
		},
		{
			Identifier:   WukongURNs.DeveloperServer.String(),
			DisplayName:  "Developer Tools",
			Type:         MediaTypeMCPServerCard,
			Description:  "File system operations, command execution, and development utilities.",
			Tags:         []string{"developer", "filesystem", "shell"},
			Capabilities: []string{"read_file", "write_file", "list_dir", "run_command"},
			RepresentativeQueries: []string{
				"read a source file",
				"execute a shell command",
				"create a new project file",
			},
			Version:   "1.0.0",
			UpdatedAt: Now(),
		},
		{
			Identifier:   WukongURNs.BrowserServer.String(),
			DisplayName:  "Browser Controller",
			Type:         MediaTypeMCPServerCard,
			Description:  "Headless browser automation via Chrome DevTools Protocol.",
			Tags:         []string{"browser", "automation", "chromedp"},
			Capabilities: []string{"browser_navigate", "browser_screenshot", "browser_click"},
			RepresentativeQueries: []string{
				"take a screenshot of a webpage",
				"automate browser interactions",
				"navigate to a URL",
			},
			Version:   "1.0.0",
			UpdatedAt: Now(),
		},
		{
			Identifier:   WukongURNs.MemoryServer.String(),
			DisplayName:  "Memory Service",
			Type:         MediaTypeMCPServerCard,
			Description:  "Long-term memory storage and retrieval with CortexDB.",
			Tags:         []string{"memory", "knowledge", "vector"},
			Capabilities: []string{"memory_add", "memory_search", "memory_recall"},
			RepresentativeQueries: []string{
				"remember this information",
				"search my memories",
				"what did we discuss before",
			},
			Version:   "1.0.0",
			UpdatedAt: Now(),
		},
		// Agent entries
		{
			Identifier:   WukongURNs.CortexAgent.String(),
			DisplayName:  "Cortex Knowledge Graph",
			Type:         MediaTypeA2AAgentCard,
			Description:  "Knowledge graph and vector search agent powered by CortexDB.",
			Tags:         []string{"knowledge", "graph", "vector", "rag"},
			Capabilities: []string{"kg_query", "vector_search", "entity_extract"},
			RepresentativeQueries: []string{
				"query the knowledge graph",
				"find related concepts",
				"extract entities from text",
			},
			Version:   "1.0.0",
			UpdatedAt: Now(),
		},
		{
			Identifier:   WukongURNs.EvolutionAgent.String(),
			DisplayName:  "Skill Evolution Engine",
			Type:         MediaTypeA2AAgentCard,
			Description:  "Self-improving skill system with LLM-driven analysis and patching.",
			Tags:         []string{"evolution", "skills", "improvement"},
			Capabilities: []string{"skill_analyze", "skill_patch", "skill_evolve"},
			RepresentativeQueries: []string{
				"improve this skill",
				"analyze skill performance",
				"auto-fix a broken tool",
			},
			Version:   "1.0.0",
			UpdatedAt: Now(),
		},
	}
}

// sanitizeName converts a name to a valid identifier component.
func sanitizeName(name string) string {
	// Replace spaces and special chars with underscores
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result = append(result, c)
		} else if c == ' ' || c == '\t' || c == '\n' {
			result = append(result, '_')
		}
	}
	return string(result)
}
