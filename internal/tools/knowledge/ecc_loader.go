package knowledge

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"mac/internal/skillpath"
)

// ====================== ECC Skill Format Loader ======================

// ECCSkill represents a skill in ECC (Enterprise Coding Companion) format.
type ECCSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tools       []string `json:"tools,omitempty"`
	Prompt      string   `json:"prompt"`
	Examples    []string `json:"examples,omitempty"`
	Model       string   `json:"model,omitempty"`
}

// ECCManifest represents an ECC skill package manifest.
type ECCManifest struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Description string     `json:"description"`
	Skills      []ECCSkill `json:"skills"`
}

// LoadECCSkills loads skills from an ECC-format manifest or directory.
func LoadECCSkills(path string, logger *log.Logger) ([]ECCSkill, error) {
	var skills []ECCSkill

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("ECC path not found: %s", path)
	}

	if info.IsDir() {
		// Scan directory for .json or .yaml skill files
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext == ".json" {
				skillPath := filepath.Join(path, entry.Name())
				data, err := os.ReadFile(skillPath)
				if err != nil {
					logger.Printf("[ECC] Skip %s: %v", entry.Name(), err)
					continue
				}
				// Try manifest first
				var manifest ECCManifest
				if err := json.Unmarshal(data, &manifest); err == nil && len(manifest.Skills) > 0 {
					skills = append(skills, manifest.Skills...)
					logger.Printf("[ECC] Loaded %d skills from manifest: %s", len(manifest.Skills), entry.Name())
					continue
				}
				// Try single skill
				var skill ECCSkill
				if err := json.Unmarshal(data, &skill); err == nil && skill.Name != "" {
					skills = append(skills, skill)
					logger.Printf("[ECC] Loaded skill: %s", skill.Name)
				}
			}
		}
	} else {
		// Single manifest file
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var manifest ECCManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("invalid ECC manifest: %w", err)
		}
		skills = manifest.Skills
	}

	return skills, nil
}

// ConvertECCToOpenKitty converts an ECC skill to OpenKitty SKILL.md format.
func ConvertECCToOpenKitty(skill ECCSkill) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", skill.Name))
	sb.WriteString(fmt.Sprintf("**Category**: %s\n\n", skill.Category))
	sb.WriteString(fmt.Sprintf("**Description**: %s\n\n", skill.Description))

	if len(skill.Tools) > 0 {
		sb.WriteString("## Recommended Tools\n\n")
		for _, t := range skill.Tools {
			sb.WriteString(fmt.Sprintf("- `%s`\n", t))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Instructions\n\n")
	sb.WriteString(skill.Prompt)
	sb.WriteString("\n")

	if len(skill.Examples) > 0 {
		sb.WriteString("\n## Examples\n\n")
		for _, ex := range skill.Examples {
			sb.WriteString(fmt.Sprintf("- %s\n", ex))
		}
	}

	if skill.Model != "" {
		sb.WriteString(fmt.Sprintf("\n## Preferred Model\n\n`%s`\n", skill.Model))
	}

	return sb.String()
}

// ImportECCSkills loads ECC skills and writes them to the skills directory.
func ImportECCSkills(eccPath, outputDir string, logger *log.Logger) (int, error) {
	skills, err := LoadECCSkills(eccPath, logger)
	if err != nil {
		return 0, err
	}

	os.MkdirAll(outputDir, 0755)
	count := 0

	for _, skill := range skills {
		dirName := sanitizeSkillName(skill.Name)
		skillDir := filepath.Join(outputDir, dirName)
		os.MkdirAll(skillDir, 0755)

		content := ConvertECCToOpenKitty(skill)
		skillFile := filepath.Join(skillDir, "SKILL.md")
		if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
			logger.Printf("[ECC] Failed to write %s: %v", skillFile, err)
			continue
		}
		logger.Printf("[ECC] Imported: %s -> %s", skill.Name, skillFile)
		count++
	}

	return count, nil
}

func sanitizeSkillName(name string) string {
	return skillpath.SanitizeName(name)
}
