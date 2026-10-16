package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type leadEvidenceRecordingTool struct {
	name   string
	params map[string]interface{}
	result string
}

func (t *leadEvidenceRecordingTool) Name() string { return t.name }
func (t *leadEvidenceRecordingTool) Description() string {
	return "recording tool"
}
func (t *leadEvidenceRecordingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t *leadEvidenceRecordingTool) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	t.params = params
	if t.result != "" {
		return t.result, nil
	}
	return "ok", nil
}

func leadEvidenceTestExecution() *PipelineExecution {
	return &PipelineExecution{
		PipelineName: "lead-research",
		RuntimePipeline: &Pipeline{
			Name: "lead-research",
			Steps: []PipelineStep{
				{Name: aiEmployeeIntakeStepName, NoTools: true},
				{Name: "产品画像与SWOT", Tools: []string{"read_file", "list_dir", "glob"}},
			},
		},
	}
}

func TestLeadResearchEvidenceGuardBlocksRuntimeAndHistoricalArtifacts(t *testing.T) {
	exec := leadEvidenceTestExecution()
	for _, tt := range []struct {
		tool   string
		params map[string]interface{}
	}{
		{tool: "read_file", params: map[string]interface{}{"path": `.openkitty/storage/old_report.md`}},
		{tool: "list_dir", params: map[string]interface{}{"path": `E:\demo\.openkitty\storage`}},
		{tool: "glob", params: map[string]interface{}{"pattern": `.openkitty/**/*.json`}},
		{tool: "read_file", params: map[string]interface{}{"path": `openKitty.exe`}},
		{tool: "read_file", params: map[string]interface{}{"path": `find_customer.db-wal`}},
	} {
		t.Run(tt.tool+"_"+strings.ReplaceAll(tt.params[firstMapKey(tt.params)].(string), `\`, "_"), func(t *testing.T) {
			inner := &leadEvidenceRecordingTool{name: tt.tool}
			guarded := guardLeadResearchEvidenceTool(inner, exec, 1)
			if _, err := guarded.Execute(context.Background(), tt.params); err == nil {
				t.Fatalf("%s unexpectedly allowed %#v", tt.tool, tt.params)
			}
			if inner.params != nil {
				t.Fatalf("%s reached inner tool despite guard", tt.tool)
			}
		})
	}
}

func TestLeadResearchEvidenceGuardAllowsOnlyCurrentCompletedUpstreamOutput(t *testing.T) {
	exec := leadEvidenceTestExecution()
	exec.ID = "pipe_lead-research_current"
	exec.Steps = []StepResult{
		{Name: aiEmployeeIntakeStepName, Status: "completed", Output: "accepted"},
		{Name: "产品画像与SWOT", Status: "running"},
	}
	expected := buildStepOutputPath(exec, 0, aiEmployeeIntakeStepName)
	inner := &leadEvidenceRecordingTool{name: "read_file", result: "current intake"}
	guarded := guardLeadResearchEvidenceTool(inner, exec, 1)
	got, err := guarded.Execute(context.Background(), map[string]interface{}{"path": expected})
	if err != nil || got != "current intake" {
		t.Fatalf("current upstream output was not allowed: got=%q err=%v", got, err)
	}

	for _, blocked := range []string{
		`.openkitty/storage/pipe_lead-research_other_step0_任务受理与意图理解.txt`,
		`.openkitty/storage/old_report.md`,
	} {
		inner := &leadEvidenceRecordingTool{name: "read_file", result: "must not run"}
		guarded := guardLeadResearchEvidenceTool(inner, exec, 1)
		if _, err := guarded.Execute(context.Background(), map[string]interface{}{"path": blocked}); err == nil {
			t.Fatalf("unrelated runtime artifact was allowed: %s", blocked)
		}
		if inner.params != nil {
			t.Fatalf("blocked path reached inner tool: %s", blocked)
		}
	}
}

func TestLeadResearchEvidenceGuardHidesRuntimeDirectory(t *testing.T) {
	inner := &leadEvidenceRecordingTool{name: "list_dir", result: `├── 📁 web/
├── 📁 .openkitty/
├── 📄 openKitty.exe
├── 📄 find_customer.db
├── 📄 release.pre-fix.bak
├── 📄 ptc_client.cpython-311.pyc
├── 📄 product.md
📄 总文件数: 5
💾 总大小: 100 MB`}
	guarded := guardLeadResearchEvidenceTool(inner, leadEvidenceTestExecution(), 1)
	got, err := guarded.Execute(context.Background(), map[string]interface{}{
		"path": "web", "show_hidden": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if show, _ := inner.params["show_hidden"].(bool); show {
		t.Fatalf("lead evidence list_dir retained show_hidden=true: %#v", inner.params)
	}
	if !strings.Contains(got, "product.md") || !strings.Contains(got, "web/") {
		t.Fatalf("product evidence was lost: %s", got)
	}
	for _, forbidden := range []string{".openkitty", ".exe", ".db", ".bak", ".pyc", "总文件数", "总大小"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("directory evidence leaked %q: %s", forbidden, got)
		}
	}
}

func TestLeadResearchEvidenceGuardHidesRuntimeGlobResults(t *testing.T) {
	inner := &leadEvidenceRecordingTool{name: "glob", result: `Found 5 files matching '**/*':
openKitty-1.2.8-win.exe
find_customer.db-wal
find-customer.exe
docs/product.md
.openkitty/storage/old_report.md`}
	guarded := guardLeadResearchEvidenceTool(inner, leadEvidenceTestExecution(), 1)
	got, err := guarded.Execute(context.Background(), map[string]interface{}{
		"path": "demo", "pattern": "**/*",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "docs/product.md") {
		t.Fatalf("product glob evidence was lost: %s", got)
	}
	for _, forbidden := range []string{".openkitty", ".exe", ".db-wal", "Found 5 files"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("glob evidence leaked %q: %s", forbidden, got)
		}
	}
}

func TestReadFileRejectsSensitiveCredentialPaths(t *testing.T) {
	workspace := t.TempDir()
	secretPath := filepath.Join(workspace, "chatroom_key.txt")
	if err := os.WriteFile(secretPath, []byte("synthetic-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewReadFileTool(workspace).Execute(context.Background(), map[string]interface{}{"path": secretPath})
	if err == nil || !strings.Contains(err.Error(), "sensitive credential path") {
		t.Fatalf("sensitive read was not blocked: %v", err)
	}
}

func TestSensitiveReadPathUsesCredentialTokensNotSubstrings(t *testing.T) {
	for _, allowed := range []string{
		"secretary-20260731.md",
		"secretariat-notes.txt",
		"credentialing-policy.md",
	} {
		if sensitiveReadPath(allowed) {
			t.Fatalf("ordinary filename was treated as a credential: %s", allowed)
		}
	}
	for _, blocked := range []string{
		"client-secret.txt",
		"clientSecret.json",
		"oauth_credentials.json",
		"access-token.txt",
		"refresh_token.backup",
		"private-key.pem",
	} {
		if !sensitiveReadPath(blocked) {
			t.Fatalf("credential filename was not blocked: %s", blocked)
		}
	}
}

func TestGlobSkipsOpenKittyRuntimeDirectory(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".openkitty", "storage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".openkitty", "storage", "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "docs", "product.txt"), []byte("product"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := NewGlobTool(workspace).Execute(context.Background(), map[string]interface{}{"pattern": "**/*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "product.txt") || strings.Contains(got, "old.txt") {
		t.Fatalf("glob runtime exclusion failed: %s", got)
	}
}

func TestPipelineAuditToolPreservesCredentialShapedResults(t *testing.T) {
	secret := "sk-openkitt-1234567890abcdef1234567890abcdef"
	inner := &leadEvidenceRecordingTool{name: "read_file", result: secret}
	exec := &PipelineExecution{
		ID:    "pipe_redaction_test",
		Steps: []StepResult{{Name: "evidence"}},
	}
	got, err := (&auditTool{inner: inner, exec: exec, stepIdx: 0}).Execute(context.Background(), map[string]interface{}{"path": "synthetic.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, secret) {
		t.Fatalf("pipeline model result lost account information: %q", got)
	}
	if len(exec.Steps[0].ToolCalls) != 1 || !strings.Contains(exec.Steps[0].ToolCalls[0].Result, secret) {
		t.Fatalf("pipeline audit lost account information: %#v", exec.Steps[0].ToolCalls)
	}
}

func firstMapKey(values map[string]interface{}) string {
	for key := range values {
		return key
	}
	return ""
}
