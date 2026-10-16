package skillpath

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"gopkg.in/yaml.v3"
)

const InterfaceMetadataFile = "agents/openai.yaml"

// SkillInterfaceMetadata is optional presentation metadata for a Skill.
// It never participates in loading priority, routing, permissions, or runtime
// execution; SKILL.md remains the authoritative capability definition.
type SkillInterfaceMetadata struct {
	DisplayName      string `json:"display_name,omitempty" yaml:"display_name"`
	ShortDescription string `json:"short_description,omitempty" yaml:"short_description"`
	DefaultPrompt    string `json:"default_prompt,omitempty" yaml:"default_prompt"`
}

type skillOpenAIConfig struct {
	Interface SkillInterfaceMetadata `yaml:"interface"`
}

func ParseInterfaceMetadata(data []byte) (SkillInterfaceMetadata, error) {
	if !utf8.Valid(data) {
		decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
		if err != nil {
			return SkillInterfaceMetadata{}, err
		}
		data = decoded
	}
	var config skillOpenAIConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return SkillInterfaceMetadata{}, err
	}
	config.Interface.DisplayName = strings.TrimSpace(config.Interface.DisplayName)
	config.Interface.ShortDescription = strings.TrimSpace(config.Interface.ShortDescription)
	config.Interface.DefaultPrompt = strings.TrimSpace(config.Interface.DefaultPrompt)
	return config.Interface, nil
}

func LoadInterfaceMetadataFromDisk(skillDir string) SkillInterfaceMetadata {
	path := filepath.Join(skillDir, filepath.FromSlash(InterfaceMetadataFile))
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("[WARN] read optional Skill interface metadata %q: %v", path, err)
		}
		return SkillInterfaceMetadata{}
	}
	metadata, err := ParseInterfaceMetadata(data)
	if err != nil {
		log.Printf("[WARN] ignore invalid optional Skill interface metadata %q: %v", path, err)
		return SkillInterfaceMetadata{}
	}
	return metadata
}

func LoadInterfaceMetadataFromFS(fsys fs.FS, skillDir string) SkillInterfaceMetadata {
	path := filepath.ToSlash(filepath.Join(skillDir, InterfaceMetadataFile))
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("[WARN] read optional embedded Skill interface metadata %q: %v", path, err)
		}
		return SkillInterfaceMetadata{}
	}
	metadata, err := ParseInterfaceMetadata(data)
	if err != nil {
		log.Printf("[WARN] ignore invalid optional embedded Skill interface metadata %q: %v", path, err)
		return SkillInterfaceMetadata{}
	}
	return metadata
}
