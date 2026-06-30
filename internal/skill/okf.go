// Package skill OKF compatibility layer.
//
// This file provides OKF (Open Knowledge Format) interoperability
// for the skill system. SKILL.md files are already structurally
// compatible with OKF (YAML frontmatter + Markdown body), needing
// only a `type: skill` field for full compliance.
//
// Key features:
//   - EnsureOKFType: ensures a SKILL.md has `type: skill`
//   - ExportSkillsAsOKF: exports all skills as an OKF Bundle
//   - ImportOKFSkills: imports OKF concepts as skills
//
// OKF consumer tolerance is applied: SKILL.md files without a
// `type` field are still loaded (defaulting to "concept"), but
// ExportSkillsAsOKF will add `type: skill` for full compliance.
package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/km269/wukong/internal/okf"
	"github.com/km269/wukong/internal/util"

	"gopkg.in/yaml.v3"
)

// SkillOKFType is the OKF concept type for Wukong skills.
const SkillOKFType = "skill"

// EnsureOKFType ensures that a SKILL.md file contains the
// `type: skill` field in its frontmatter for OKF compliance.
// If the file already has a `type` field, it is left unchanged.
// If not, the field is added and the file is rewritten.
//
// This is a no-op for files without frontmatter (they will be
// handled by OKF consumers with default type tolerance).
//
// Returns true if the file was modified, false if already compliant.
func EnsureOKFType(skillMDPath string) (bool, error) {
	content, err := os.ReadFile(skillMDPath)
	if err != nil {
		return false, fmt.Errorf("read skill file: %w", err)
	}

	str := string(content)

	// Check if file has frontmatter.
	if !strings.HasPrefix(str, "---\n") &&
		!strings.HasPrefix(str, "---\r\n") {
		// No frontmatter — add minimal OKF-compliant frontmatter.
		newContent := fmt.Sprintf(
			"---\ntype: %s\n---\n\n%s",
			SkillOKFType, str)
		if err := os.WriteFile(
			skillMDPath, []byte(newContent), 0644,
		); err != nil {
			return false, fmt.Errorf("write skill file: %w", err)
		}
		return true, nil
	}

	// Parse frontmatter to check for existing type field.
	rest := str
	if strings.HasPrefix(rest, "---\n") {
		rest = rest[4:]
	} else {
		rest = rest[5:]
	}

	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		// Malformed frontmatter — skip.
		return false, nil
	}

	yamlPart := rest[:endIdx]

	var fm map[string]any
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		// Can't parse — skip to avoid corruption.
		return false, nil
	}

	// Check if type field already exists.
	if existingType, ok := fm["type"]; ok {
		if typeStr, ok := existingType.(string); ok &&
			typeStr != "" {
			// Already has a type — compliant.
			return false, nil
		}
	}

	// Add type field after the opening ---.
	newContent := strings.Replace(str,
		"---\n", fmt.Sprintf("---\ntype: %s\n", SkillOKFType), 1)
	// Handle CRLF case.
	if newContent == str {
		newContent = strings.Replace(str,
			"---\r\n", fmt.Sprintf("---\r\ntype: %s\r\n", SkillOKFType), 1)
	}

	if newContent == str {
		return false, nil
	}

	if err := os.WriteFile(
		skillMDPath, []byte(newContent), 0644,
	); err != nil {
		return false, fmt.Errorf("write skill file: %w", err)
	}
	return true, nil
}

// ExportSkillsAsOKF exports all loaded skills as an OKF Bundle
// to the given output directory. Each skill becomes a concept
// file with `type: skill` and the skill's metadata as frontmatter.
//
// The bundle includes:
//   - index.md: auto-generated listing all skills
//   - log.md: auto-generated changelog
//   - <skill-name>/SKILL.md: each skill as an OKF concept
func (m *Manager) ExportSkillsAsOKF(outputDir string) error {
	if m.repository == nil {
		return fmt.Errorf("skill repository not initialized")
	}

	bundle := &okf.Bundle{
		RootDir:  outputDir,
		Concepts: make([]*okf.Concept, 0, len(m.summaries)),
	}

	for _, summary := range m.summaries {
		// Get the full skill to access the body.
		sk, err := m.GetSkill(context.Background(), summary.Name)
		if err != nil {
			util.Logger.Warn("okf: skip skill in export",
				"name", summary.Name,
				"error", err.Error())
			continue
		}

		// Build OKF frontmatter.
		fm := okf.Frontmatter{
			Type:        SkillOKFType,
			Title:       summary.Name,
			Description: summary.Description,
			Resource: fmt.Sprintf("wukong://skill/%s",
				summary.Name),
			Tags:      []string{"wukong", "skill"},
			Timestamp: okf.FormatNow(),
			Extra: map[string]any{
				"name": summary.Name,
			},
		}

		concept := &okf.Concept{
			ID:          "skills/" + summary.Name,
			FilePath:    filepath.Join("skills", summary.Name, "SKILL.md"),
			Frontmatter: fm,
			Body:        sk.Body,
		}

		bundle.Concepts = append(bundle.Concepts, concept)
	}

	opts := okf.DefaultWriteOptions()
	if err := okf.WriteBundle(bundle, outputDir, opts); err != nil {
		return fmt.Errorf("write OKF bundle: %w", err)
	}

	util.Logger.Info("okf: skills exported as OKF bundle",
		"output_dir", outputDir,
		"concept_count", len(bundle.Concepts))

	return nil
}

// ImportOKFSkills imports OKF concept files with `type: skill`
// from a bundle directory into the skills directory. Non-skill
// concepts are skipped.
//
// This enables loading skills from any OKF-compatible knowledge
// bundle, including those produced by other tools.
func (m *Manager) ImportOKFSkills(bundleDir string) (int, error) {
	bundle, warnings := okf.LoadBundle(bundleDir)

	for _, w := range warnings {
		util.Logger.Warn("okf: bundle load warning",
			"warning", w)
	}

	skillsDir := m.SkillsDir()
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return 0, fmt.Errorf("create skills dir: %w", err)
	}

	imported := 0
	for _, concept := range bundle.Concepts {
		// Only import concepts with type=skill.
		if concept.Frontmatter.Type != SkillOKFType {
			continue
		}

		// Derive skill name from concept ID or frontmatter.
		skillName := concept.Frontmatter.Title
		if skillName == "" {
			if name, ok := concept.Frontmatter.Extra["name"]; ok {
				if ns, ok := name.(string); ok {
					skillName = ns
				}
			}
		}
		if skillName == "" {
			// Use the last path segment of the concept ID.
			parts := strings.Split(concept.ID, "/")
			skillName = parts[len(parts)-1]
		}

		skillDir := filepath.Join(skillsDir, skillName)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			util.Logger.Warn("okf: create skill dir failed",
				"skill", skillName,
				"error", err.Error())
			continue
		}

		// Write SKILL.md with OKF-compliant frontmatter.
		content := okf.FormatConcept(concept, true)
		skillPath := filepath.Join(skillDir, "SKILL.md")
		if err := os.WriteFile(
			skillPath, []byte(content), 0644,
		); err != nil {
			util.Logger.Warn("okf: write skill failed",
				"skill", skillName,
				"error", err.Error())
			continue
		}

		imported++
		util.Logger.Info("okf: imported skill from bundle",
			"skill", skillName,
			"source", concept.FilePath)
	}

	// Refresh the repository to pick up imported skills.
	if m.repository != nil {
		if err := m.Refresh(); err != nil {
			util.Logger.Warn("okf: refresh after import failed",
				"error", err.Error())
		}
	}

	return imported, nil
}

// EnsureAllOKFCompliant ensures all SKILL.md files in the
// repository have the `type: skill` field for OKF compliance.
// This is a batch version of EnsureOKFType.
//
// Returns the number of files modified.
func (m *Manager) EnsureAllOKFCompliant() (int, error) {
	if m.repository == nil {
		return 0, fmt.Errorf("skill repository not initialized")
	}

	skillsDir := m.SkillsDir()
	modified := 0

	err := filepath.Walk(skillsDir,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip errors
			}
			if info.IsDir() || info.Name() != "SKILL.md" {
				return nil
			}

			changed, err := EnsureOKFType(path)
			if err != nil {
				util.Logger.Warn("okf: ensure type failed",
					"file", path,
					"error", err.Error())
				return nil
			}
			if changed {
				modified++
			}
			return nil
		})

	if err != nil {
		return modified, fmt.Errorf("walk skills dir: %w", err)
	}

	if modified > 0 {
		util.Logger.Info("okf: ensured OKF type compliance",
			"modified_count", modified,
			"skills_dir", skillsDir)
	}

	return modified, nil
}
