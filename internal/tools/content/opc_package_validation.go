package content

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
)

const maxOPCXMLPartBytes = 32 << 20

const (
	opcOfficeRelationshipsNamespace       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	opcStrictOfficeRelationshipsNamespace = "http://purl.oclc.org/ooxml/officeDocument/relationships"
)

type opcRelationship struct {
	ID         string `xml:"Id,attr"`
	Type       string `xml:"Type,attr"`
	Target     string `xml:"Target,attr"`
	TargetMode string `xml:"TargetMode,attr"`
}

type opcRelationships struct {
	Items []opcRelationship `xml:"Relationship"`
}

// validateOfficeOpenXMLPackage validates the package-level invariants shared
// by DOCX, XLSX and PPTX files. A ZIP signature and a few well-known members
// are insufficient: every internal relationship target must exist, and every
// r:id/r:embed/r:link reference in an XML part must be declared by that part's
// relationship part.
func ValidateOfficeOpenXMLPackage(filePath string) error {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return fmt.Errorf("open OOXML package: %w", err)
	}
	defer reader.Close()
	return validateOPCRelationshipCompleteness(reader.File)
}

func validateOPCRelationshipCompleteness(files []*zip.File) error {
	entries := make(map[string]*zip.File, len(files))
	for _, file := range files {
		if file == nil || file.FileInfo().IsDir() {
			continue
		}
		name, err := normalizeOPCPartName(file.Name)
		if err != nil {
			return fmt.Errorf("invalid OPC part name %q: %w", file.Name, err)
		}
		if _, exists := entries[name]; exists {
			return fmt.Errorf("duplicate OPC part %q", name)
		}
		entries[name] = file
	}

	contentTypes, ok := entries["[Content_Types].xml"]
	if !ok {
		return fmt.Errorf("OPC package is missing [Content_Types].xml")
	}
	if err := validateOPCXMLPart(contentTypes); err != nil {
		return fmt.Errorf("parse [Content_Types].xml: %w", err)
	}
	if _, ok := entries["_rels/.rels"]; !ok {
		return fmt.Errorf("Office Open XML package is missing _rels/.rels")
	}

	relationshipIDs := make(map[string]map[string]struct{})
	var issues []string
	for name, file := range entries {
		if !strings.HasSuffix(strings.ToLower(name), ".rels") {
			continue
		}
		sourcePart, ok := opcRelationshipSourcePart(name)
		if !ok {
			issues = append(issues, fmt.Sprintf("relationship part %q is not in an OPC _rels directory", name))
			continue
		}
		if sourcePart != "" {
			if _, exists := entries[sourcePart]; !exists {
				issues = append(issues, fmt.Sprintf("relationship part %q has missing source part %q", name, sourcePart))
			}
		}

		var relationships opcRelationships
		if err := decodeOPCXMLPart(file, &relationships); err != nil {
			issues = append(issues, fmt.Sprintf("relationship part %q is not valid XML: %v", name, err))
			continue
		}
		ids := make(map[string]struct{}, len(relationships.Items))
		for _, relationship := range relationships.Items {
			id := strings.TrimSpace(relationship.ID)
			if id == "" {
				issues = append(issues, fmt.Sprintf("relationship part %q contains an empty Id", name))
				continue
			}
			if _, duplicate := ids[id]; duplicate {
				issues = append(issues, fmt.Sprintf("relationship part %q contains duplicate Id %q", name, id))
				continue
			}
			ids[id] = struct{}{}
			if strings.EqualFold(strings.TrimSpace(relationship.TargetMode), "External") {
				continue
			}
			target, err := resolveOPCInternalTarget(sourcePart, relationship.Target)
			if err != nil {
				issues = append(issues, fmt.Sprintf("relationship %q in %q has invalid target: %v", id, name, err))
				continue
			}
			if _, exists := entries[target]; !exists {
				issues = append(issues, fmt.Sprintf("relationship %q in %q targets missing part %q", id, name, target))
			}
		}
		relationshipIDs[sourcePart] = ids
	}

	for name, file := range entries {
		if !isOPCRelationshipBearingXMLPart(name) {
			continue
		}
		references, err := opcRelationshipReferences(file)
		if err != nil {
			issues = append(issues, fmt.Sprintf("XML part %q is not well formed: %v", name, err))
			continue
		}
		ids := relationshipIDs[name]
		for _, reference := range references {
			if _, declared := ids[reference]; !declared {
				issues = append(issues, fmt.Sprintf("XML part %q references undeclared relationship %q", name, reference))
			}
		}
	}

	if len(issues) == 0 {
		return nil
	}
	sort.Strings(issues)
	if len(issues) > 20 {
		issues = append(issues[:20], fmt.Sprintf("... and %d more relationship issue(s)", len(issues)-20))
	}
	return fmt.Errorf("OPC relationship validation failed: %s", strings.Join(issues, "; "))
}

func normalizeOPCPartName(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "", fmt.Errorf("empty part name")
	}
	clean := pathpkg.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("part escapes package root")
	}
	return clean, nil
}

func opcRelationshipSourcePart(relationshipPart string) (string, bool) {
	if relationshipPart == "_rels/.rels" {
		return "", true
	}
	directory, base := pathpkg.Split(relationshipPart)
	if !strings.HasSuffix(directory, "_rels/") || !strings.HasSuffix(strings.ToLower(base), ".rels") {
		return "", false
	}
	parent := strings.TrimSuffix(directory, "_rels/")
	sourceBase := base[:len(base)-len(".rels")]
	source, err := normalizeOPCPartName(pathpkg.Join(parent, sourceBase))
	return source, err == nil
}

func resolveOPCInternalTarget(sourcePart, rawTarget string) (string, error) {
	rawTarget = strings.TrimSpace(rawTarget)
	if rawTarget == "" {
		return "", fmt.Errorf("empty target")
	}
	parsed, err := url.Parse(rawTarget)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() || parsed.Host != "" {
		return "", fmt.Errorf("internal relationship uses an absolute URI")
	}
	targetPath := parsed.Path
	if targetPath == "" {
		return "", fmt.Errorf("target has no package path")
	}
	if decoded, err := url.PathUnescape(targetPath); err == nil {
		targetPath = decoded
	}
	targetPath = strings.ReplaceAll(targetPath, "\\", "/")
	if strings.HasPrefix(targetPath, "/") {
		targetPath = strings.TrimPrefix(targetPath, "/")
	} else if sourcePart != "" {
		targetPath = pathpkg.Join(pathpkg.Dir(sourcePart), targetPath)
	}
	return normalizeOPCPartName(targetPath)
}

func isOPCRelationshipBearingXMLPart(name string) bool {
	lower := strings.ToLower(name)
	if lower == "[content_types].xml" || strings.HasSuffix(lower, ".rels") {
		return false
	}
	return strings.HasSuffix(lower, ".xml") || strings.HasSuffix(lower, ".vml")
}

func opcRelationshipReferences(file *zip.File) ([]string, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	decoder := xml.NewDecoder(io.LimitReader(reader, maxOPCXMLPartBytes+1))
	references := make(map[string]struct{})
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		for _, attribute := range start.Attr {
			if attribute.Name.Space != opcOfficeRelationshipsNamespace &&
				attribute.Name.Space != opcStrictOfficeRelationshipsNamespace {
				continue
			}
			value := strings.TrimSpace(attribute.Value)
			if value != "" {
				references[value] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(references))
	for reference := range references {
		result = append(result, reference)
	}
	sort.Strings(result)
	return result, nil
}

func validateOPCXMLPart(file *zip.File) error {
	return decodeOPCXMLPart(file, nil)
}

func decodeOPCXMLPart(file *zip.File, destination interface{}) error {
	if file.UncompressedSize64 > maxOPCXMLPartBytes {
		return fmt.Errorf("XML part exceeds %d bytes", maxOPCXMLPartBytes)
	}
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	decoder := xml.NewDecoder(io.LimitReader(reader, maxOPCXMLPartBytes+1))
	if destination == nil {
		for {
			if _, err := decoder.Token(); err == io.EOF {
				return nil
			} else if err != nil {
				return err
			}
		}
	}
	return decoder.Decode(destination)
}
