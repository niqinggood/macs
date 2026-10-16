package toolcontract

// IsReadOnlyToolName reports whether a tool is side-effect free by contract.
func IsReadOnlyToolName(name string) bool {
	switch name {
	case "read_file", "list_dir", "grep", "glob", "snapshot", "search_history",
		"knowledge_query", "list_skills", "get_stats", "tool_detail",
		"rag_search", "rag_extract", "agent_reach", "web_search", "web_targets", "web_extract",
		"plan_task", "update_plan_step",
		"vector_search", "lsp_hover", "lsp_definition", "lsp_references":
		return true
	}
	return false
}
