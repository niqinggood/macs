package memory

import "context"

// WorkspacePreferenceArchive selects existing explicitly named preference
// relations using the same workspace/session audience as the knowledge reader.
// Neither the object text nor an expired relationship establishes a user's
// confirmed preference. This does not change recall or create memory Entries.
func (kg *KnowledgeGraph) WorkspacePreferenceArchive(ctx context.Context, sessionIDs []string, search string, offset, limit int) ([]KnowledgeArchiveItem, int64, error) {
	return kg.workspaceKnowledgeArchive(ctx, sessionIDs, false, search, offset, limit, []string{
		"prefers", "dislikes", "prefers_report_format", "prefers_style", "prefers_tone", "prefers_tool",
		"language_preference", "testing_framework_preference", "transportation_preference",
	})
}
