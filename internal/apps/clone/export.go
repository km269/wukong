package clone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/km269/wukong/internal/apps/sanitize"
)

// ExportStructuredData exports cloning results as structured JSON.
func ExportStructuredData(outputDir string, seedURL string, host string,
	pageResults []PageResult, errors []string) (string, error) {
	exportData := StructuredExportData{
		SeedURL:    seedURL,
		Host:       host,
		OutputDir:  outputDir,
		ExportedAt: time.Now().Format(time.RFC3339),
		TotalPages: len(pageResults),
		Pages:      make([]StructuredPageData, 0, len(pageResults)),
		Errors:     errors,
	}

	for _, pr := range pageResults {
		pageData := StructuredPageData{
			URL:   pr.URL,
			Title: pr.Title,
			Depth: pr.Depth,
			Links: pr.LinksFound,
			Assets: pr.AssetsFound,
			Error: pr.Error,
		}

		if pr.FilePath != "" {
			relPath, _ := filepath.Rel(outputDir, pr.FilePath)
			if relPath == "" {
				relPath = pr.FilePath
			}
			pageData.HTMLPath = relPath
		}

		if pr.Markdown != "" {
			pageData.Markdown = pr.Markdown
		}

		exportData.Pages = append(exportData.Pages, pageData)
	}

	exportPath := filepath.Join(outputDir, "structured_export.json")

	jsonData, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export data: %w", err)
	}

	if err := os.WriteFile(exportPath, jsonData, 0644); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}

	return exportPath, nil
}

// GenerateMarkdownForPage reads the HTML file and converts it to Markdown.
func GenerateMarkdownForPage(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read HTML file: %w", err)
	}

	markdown, err := sanitize.ExtractMainContentMarkdown(string(content))
	if err != nil {
		return "", fmt.Errorf("convert to markdown: %w", err)
	}

	return markdown, nil
}

// ExportWithMarkdown reads all pages and exports with Markdown content.
func ExportWithMarkdown(outputDir string, seedURL string, host string,
	pageResults []PageResult, errors []string) (string, error) {
	exportData := StructuredExportData{
		SeedURL:    seedURL,
		Host:       host,
		OutputDir:  outputDir,
		ExportedAt: time.Now().Format(time.RFC3339),
		TotalPages: len(pageResults),
		Pages:      make([]StructuredPageData, 0, len(pageResults)),
		Errors:     errors,
	}

	for _, pr := range pageResults {
		pageData := StructuredPageData{
			URL:   pr.URL,
			Title: pr.Title,
			Depth: pr.Depth,
			Links: pr.LinksFound,
			Assets: pr.AssetsFound,
			Error: pr.Error,
		}

		if pr.FilePath != "" {
			relPath, _ := filepath.Rel(outputDir, pr.FilePath)
			if relPath == "" {
				relPath = pr.FilePath
			}
			pageData.HTMLPath = relPath

			markdown, err := GenerateMarkdownForPage(pr.FilePath)
			if err == nil && markdown != "" {
				const maxMarkdownLen = 100000
				if len(markdown) > maxMarkdownLen {
					markdown = markdown[:maxMarkdownLen] +
						fmt.Sprintf("\n\n... (truncated, %d chars total)", len(markdown))
				}
				pageData.Markdown = markdown
			}
		}

		exportData.Pages = append(exportData.Pages, pageData)
	}

	exportPath := filepath.Join(outputDir, "structured_export.json")

	jsonData, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export data: %w", err)
	}

	if err := os.WriteFile(exportPath, jsonData, 0644); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}

	return exportPath, nil
}
