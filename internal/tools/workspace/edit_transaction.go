package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PostEditDiagnostics lets the application attach language-specific diagnostics
// without coupling workspace mutation primitives to a concrete development tool.
type PostEditDiagnostics func(filePath string) (report, status string)

// finalizeEditedFile runs the common post-commit sequence for source/text
// mutations. Callers must keep the per-path mutation lock held until it
// returns so all OpenKitty tools observe one ordered transaction.
func FinalizeEditedFile(workspace, filePath, writerID string, diagnostics PostEditDiagnostics) (string, error) {
	var report strings.Builder
	if formatResult := RunAutoFormat(filePath); formatResult != "" {
		report.WriteString("\n")
		report.WriteString(formatResult)
	}

	committed, err := FingerprintFile(filePath)
	if err != nil {
		return report.String(), fmt.Errorf("fingerprint final edited file: %w", err)
	}
	GetFileStateRegistry().NoteWrite(filePath, writerID)

	if diagnosticResult := RunPostEditDiag(filePath); diagnosticResult != "" {
		report.WriteString("\n")
		report.WriteString(strings.TrimSpace(diagnosticResult))
	}

	lspStatus := "not-installed"
	if diagnostics != nil {
		diagnosticReport, status := diagnostics(filePath)
		if strings.TrimSpace(status) != "" {
			lspStatus = status
		}
		if strings.TrimSpace(diagnosticReport) != "" {
			report.WriteString("\n")
			report.WriteString(strings.TrimSpace(diagnosticReport))
		}
	}

	current, err := FingerprintFile(filePath)
	if err != nil {
		return report.String(), fmt.Errorf("recheck final edited file: %w", err)
	}
	if committed.ContentSHA256 != current.ContentSHA256 || committed.Size != current.Size {
		return report.String(), fmt.Errorf(
			"[STALE FILE] %s changed again while post-edit checks were running (committed sha256=%s, current sha256=%s). Re-read before making another edit",
			filePath, ShortContentHash(committed.ContentSHA256), ShortContentHash(current.ContentSHA256),
		)
	}

	displayPath := filePath
	if rel, relErr := filepath.Rel(workspace, filePath); relErr == nil && rel != "" && !strings.HasPrefix(rel, "..") {
		displayPath = rel
	}
	report.WriteString(fmt.Sprintf(
		"\n[Edit receipt] path=%s sha256=%s bytes=%d lsp=%s",
		displayPath, committed.ContentSHA256, committed.Size, lspStatus,
	))
	return report.String(), nil
}
