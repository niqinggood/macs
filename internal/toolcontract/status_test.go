package toolcontract

import "testing"

func TestToolResultLooksFailedSharedMarkers(t *testing.T) {
	tests := []string{
		"执行失败: boom",
		"Tool call rejected [invalid_args]: bad input",
		"[FAIL] Run error:",
		"Tool Result\nstatus: failed_compacted\n",
		"FAIL: TestPaymentFlow",
		"panic: runtime error",
		"[BLOCKED by hook] denied by policy",
		"tool error: command not found",
	}
	for _, content := range tests {
		t.Run(content, func(t *testing.T) {
			if !ToolResultLooksFailed(content) {
				t.Fatalf("expected failed marker to be detected in %q", content)
			}
		})
	}
}

func TestToolResultLooksFailedAllowsSuccessText(t *testing.T) {
	if ToolResultLooksFailed("completed successfully\nall checks passed") {
		t.Fatal("success text should not look failed")
	}
}

func TestToolResultLooksFailedAllowsNonFatalEncodingWarning(t *testing.T) {
	content := "## PTC Execution\n\n[Non-fatal encoding warning] A background output reader hit UnicodeDecodeError after the subprocess completed. The command exit code was 0."
	if ToolResultLooksFailed(content) {
		t.Fatalf("non-fatal encoding warning should not look failed: %q", content)
	}
}

func TestPersistentShellIgnoresFailureWordsInEchoedCommand(t *testing.T) {
	content := "Command Execution\ncmd: python -c \"import traceback\"\n状态: 成功\noutput result: ROWS_COLS: 16 8\n"
	if ToolResultLooksFailedForTool("persistent_shell", content) {
		t.Fatal("successful shell output was failed by echoed source text")
	}
	content = "Command Execution\ncmd: python script.py\n状态: 成功\noutput result: Traceback (most recent call last): boom\n"
	if !ToolResultLooksFailedForTool("persistent_shell", content) {
		t.Fatal("actual traceback in shell output should fail")
	}
}

func TestPersistentShellRejectsPythonFailureBehindSuccessfulPipelineStatus(t *testing.T) {
	content := `Command Execution
cmd: python3 build_deck.py 2>&1 | tail -20
status: succeeded
output result:   File "build_deck.py", line 230
    add_textbox(
               ^
SyntaxError: '(' was never closed
`
	if ToolExecutionSucceededForTool("persistent_shell", content, nil) {
		t.Fatal("Python syntax failure hidden by a successful trailing pipe was treated as success")
	}
}

func TestToolExecutionSucceededRequiresCleanContentAndNilError(t *testing.T) {
	if !ToolExecutionSucceeded("completed successfully", nil) {
		t.Fatal("clean result with nil error should succeed")
	}
	if ToolExecutionSucceeded("执行失败: boom", nil) {
		t.Fatal("failed content with nil error should not succeed")
	}
}

func TestToolExecutionSucceededForReadFileAllowsSourceFailureText(t *testing.T) {
	content := `#!/usr/bin/env python3
try:
    run()
except Exception:
    print("[FAIL] no id returned")
`
	if !ToolExecutionSucceededForTool("read_file", content, nil) {
		t.Fatal("successful read_file content should not fail just because the file text contains failure markers")
	}
	if ToolExecutionSucceededForTool("read_file", "Tool Result\nstatus: failed_compacted\nsummary: output exceeded 8000 chars", nil) {
		t.Fatal("normalized failed read_file result should still fail")
	}
	if ToolExecutionSucceededForTool("read_file", "Tool call rejected [invalid_args]: missing required parameter \"path\"", nil) {
		t.Fatal("read_file tool validation errors should still fail")
	}
	if ToolExecutionSucceededForTool("cli", "FAIL: TestPaymentFlow", nil) {
		t.Fatal("non-read_file tools should still treat failure markers as failed")
	}
}

func TestToolExecutionSucceededForWebFetchUsesNormalizedStatus(t *testing.T) {
	success := "Tool Result\nstatus: compacted\nkey_output:\nOriginal engine brave failed; used Exa.\nURL: https://vendor.example/pricing"
	if !ToolExecutionSucceededForTool("web_fetch", success, nil) {
		t.Fatal("successful compacted web fetch was reclassified from failure prose")
	}
	failure := "Tool Result\nstatus: failed_compacted\nkey_output:\nURL: https://vendor.example/pricing"
	if ToolExecutionSucceededForTool("web_fetch", failure, nil) {
		t.Fatal("failed_compacted web fetch was treated as success")
	}
}

func TestMutationReceiptAllowsFailureWordsInsideSuccessfulDiff(t *testing.T) {
	content := `Replaced 1 occurrence in build_deck.py
[Edit receipt] path=build_deck.py sha256=abc123 bytes=69284 lsp=not-installed
+    except Exception as exc:
+        raise ValueError("failed source row")
`
	if !ToolExecutionSucceededForTool("edit_file", content, nil) {
		t.Fatal("successful edit receipt was reclassified by failure words in the diff")
	}
	for _, toolName := range []string{"write_file", "json_patch", "multi_edit"} {
		if !ToolExecutionSucceededForTool(toolName, content, nil) {
			t.Fatalf("successful %s receipt was reclassified by payload prose", toolName)
		}
	}
}

func TestMutationFailureEnvelopeWinsOverEmbeddedReceipt(t *testing.T) {
	content := "执行失败: stale edit context\nTool output:\n[Edit receipt] path=old.py sha256=abc"
	if ToolExecutionSucceededForTool("edit_file", content, nil) {
		t.Fatal("explicit mutation failure envelope was hidden by an embedded receipt")
	}
}
