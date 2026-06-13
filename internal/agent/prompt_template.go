// Package agent provides prompt template loading and rendering.
//
// The PromptTemplateManager loads .md files from a configured directory
// and concatenates them to form the agent's system instruction. This
// allows users to customize the agent's behavior without modifying code.
//
// Template files support Go text/template-style variable substitution
// using the syntax {{.VariableName}}. Supported variables:
//
//	{{.WorkingDir}}   — current working directory
//	{{.ModelName}}    — active model name
//	{{.ProviderName}} — active provider name
//	{{.SessionID}}    — current session ID
//	{{.UserName}}     — current user ID
//
// When the template directory is empty or contains no .md files, the
// built-in default instruction is used as a fallback.
package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
)

// TemplateVars holds variables available for substitution in
// prompt template files.
type TemplateVars struct {
	WorkingDir   string
	ModelName    string
	ProviderName string
	SessionID    string
	UserName     string
}

// PromptTemplateManager loads and renders prompt template files.
type PromptTemplateManager struct {
	dir string
}

// NewPromptTemplateManager creates a manager that loads templates
// from the given directory. The directory is expanded (for ~) and
// created if it does not exist.
func NewPromptTemplateManager(
	cfg *config.WukongConfig,
) *PromptTemplateManager {
	dir := cfg.Agent.SystemPromptDir
	if dir == "" {
		return &PromptTemplateManager{dir: ""}
	}

	// Expand ~ to home directory.
	if len(dir) >= 2 && dir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			util.Logger.Warn("prompt_template: cannot resolve home dir",
				"error", err.Error())
			return &PromptTemplateManager{dir: ""}
		}
		dir = filepath.Join(home, dir[2:])
	}

	dir = config.ResolvePath(dir)

	// Create directory if it does not exist (non-fatal).
	if err := os.MkdirAll(dir, 0755); err != nil {
		util.Logger.Warn("prompt_template: cannot create directory",
			"dir", dir, "error", err.Error())
		return &PromptTemplateManager{dir: ""}
	}

	return &PromptTemplateManager{dir: dir}
}

// LoadTemplates scans the template directory for .md files, sorts them
// by filename, reads and concatenates their content, and applies
// variable substitution.
//
// Returns the concatenated prompt text. If the directory is empty or
// contains no .md files, returns an empty string (caller should
// fall back to the built-in default).
func (m *PromptTemplateManager) LoadTemplates(
	vars TemplateVars,
) string {
	if m.dir == "" {
		return ""
	}

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return ""
	}

	// Collect .md files sorted by name.
	var mdFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			mdFiles = append(mdFiles, e.Name())
		}
	}

	if len(mdFiles) == 0 {
		return ""
	}

	sort.Strings(mdFiles)

	var parts []string
	for _, name := range mdFiles {
		content, err := os.ReadFile(
			filepath.Join(m.dir, name))
		if err != nil {
			util.Logger.Warn("prompt_template: cannot read file",
				"file", name, "error", err.Error())
			continue
		}

		rendered := renderTemplate(
			string(content), vars)
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	util.Logger.Info("prompt_template: loaded templates",
		"count", len(parts), "dir", m.dir)
	return strings.Join(parts, "\n\n")
}

// renderTemplate applies variable substitution to template content.
func renderTemplate(content string, vars TemplateVars) string {
	result := content

	// Simple {{.Var}} substitution — sufficient for prompt templates.
	replacer := strings.NewReplacer(
		"{{.WorkingDir}}", vars.WorkingDir,
		"{{.ModelName}}", vars.ModelName,
		"{{.ProviderName}}", vars.ProviderName,
		"{{.SessionID}}", vars.SessionID,
		"{{.UserName}}", vars.UserName,
	)

	result = replacer.Replace(result)

	// Strip any remaining unreplaced {{.Var}} placeholders to avoid
	// confusing the model.
	for _, v := range []string{
		"{{.WorkingDir}}", "{{.ModelName}}",
		"{{.ProviderName}}", "{{.SessionID}}",
		"{{.UserName}}",
	} {
		result = strings.ReplaceAll(result, v, "")
	}

	return strings.TrimSpace(result)
}
