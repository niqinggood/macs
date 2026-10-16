package app

import (
	"strings"
	"testing"
)

func TestMeetingSecretaryDirectPromptForbidsInventedPendingDetails(t *testing.T) {
	got := aiEmployeeDirectPrompt("meeting-secretary", "整理会议纪要")
	for _, want := range []string{
		"closed, complete factual source",
		"directly traceable",
		"Never invent",
		"dependencies",
		"default responsibility",
		"Dates appearing in sequence do not establish a dependency",
		"Preserve partial dates exactly as supplied",
		"Never add a year",
		"Risks may only quote or closely paraphrase",
		"可能影响",
		"pending confirmation",
		"Do not enumerate other fields",
		"Do not add standard-template fields",
		"原文未提供",
		"逐字段封闭抽取",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("secretary grounding prompt missing %q: %s", want, got)
		}
	}
	if got := aiEmployeeDirectPrompt("data-analyst", "analyze data"); got != "analyze data" {
		t.Fatalf("unrelated employee prompt changed: %q", got)
	}
}
