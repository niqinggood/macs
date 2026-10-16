// Package assets owns repository-level embedded resources used by the
// cmd/openkitty executable. Keeping embeds at the module root lets source code
// live under cmd/ without duplicating the public web and skill trees.
package assets

import "embed"

//go:embed web/*
var WebFS embed.FS

//go:embed tools/pdf_extract.py
var PDFExtractScript string

//go:embed tools/unstructured_extract.py
var UnstructuredExtractScript string

//go:embed all:default-skills
var DefaultSkillsFS embed.FS

//go:embed all:builtin
var BuiltinSkillsFS embed.FS

//go:embed openkitty-config.template.yaml
var DefaultConfigYAML []byte
