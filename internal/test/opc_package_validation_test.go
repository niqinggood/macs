package app

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contenttools "mac/internal/tools/content"
)

func writeOPCTestPackage(t *testing.T, parts map[string]string) string {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "artifact.opc")
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range parts {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return filePath
}

func minimalOPCTestParts(referenceID string) map[string]string {
	return map[string]string{
		"[Content_Types].xml":     `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/></Types>`,
		"_rels/.rels":             `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="doc/main.xml"/></Relationships>`,
		"doc/main.xml":            `<?xml version="1.0"?><root xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" r:id="` + referenceID + `"/>`,
		"doc/_rels/main.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="urn:test:item" Target="item.xml"/></Relationships>`,
		"doc/item.xml":            `<item/>`,
	}
}

func TestValidateOPCRelationshipCompletenessAcceptsMinimalLegalPackage(t *testing.T) {
	filePath := writeOPCTestPackage(t, minimalOPCTestParts("rId1"))
	if err := contenttools.ValidateOfficeOpenXMLPackage(filePath); err != nil {
		t.Fatalf("minimal legal OPC package was rejected: %v", err)
	}
}

func TestValidateOPCRelationshipCompletenessRejectsUndeclaredReference(t *testing.T) {
	filePath := writeOPCTestPackage(t, minimalOPCTestParts("rId2"))
	err := contenttools.ValidateOfficeOpenXMLPackage(filePath)
	if err == nil || !strings.Contains(err.Error(), "undeclared relationship") || !strings.Contains(err.Error(), "rId2") {
		t.Fatalf("broken relationship reference was not diagnosed: %v", err)
	}
}

func TestValidateOPCRelationshipCompletenessRejectsMissingInternalTarget(t *testing.T) {
	parts := minimalOPCTestParts("rId1")
	delete(parts, "doc/item.xml")
	filePath := writeOPCTestPackage(t, parts)
	err := contenttools.ValidateOfficeOpenXMLPackage(filePath)
	if err == nil || !strings.Contains(err.Error(), "targets missing part") || !strings.Contains(err.Error(), "doc/item.xml") {
		t.Fatalf("missing relationship target was not diagnosed: %v", err)
	}
}
