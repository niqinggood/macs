package toolregistry

// CoreTools always have their full schema sent to the model.
var CoreTools = map[string]bool{
	"read_file": true, "write_file": true, "edit_file": true, "json_patch": true, "list_dir": true,
	"grep": true, "glob": true, "project_map": true, "snapshot": true,
	"code_check": true, "cli": true, "persistent_shell": true, "fix_and_retry": true,
	"shell_analyze": true, "shell_sessions": true, "web_fetch": true, "agent_reach": true,
	"social_trending": true, "weather_forecast": true, "fuel_price": true, "stock_facts": true,
	"source_acquire": true, "nova_chatroom_search": true, "plc_modbus": true, "uart": true,
	"ros2": true, "robot_status": true, "robot_stop": true, "robot_move": true,
	"robot_command": true, "web_targets": true, "web_navigate": true, "web_page": true,
	"web_close": true, "browser_task": true, "desktop_task": true, "computer": true, "assemble_team": true,
	"notepad": true, "ptc_execute": true, "search_history": true, "knowledge_query": true,
	"speak": true, "listen": true, "watch": true, "camera": true, "plan_task": true,
	"update_plan_step": true, "tool_detail": true, "capabilities": true, "list_skills": true,
	"use_skill": true, "convert": true, "external_agent": true, "verify_work": true, "git": true,
}

// PromptHiddenTools remain discoverable but are omitted from the system prompt.
var PromptHiddenTools = map[string]bool{
	"audio": true, "image_process": true, "extract": true, "sys": true, "screenshot": true,
	"remote": true, "native_env": true, "ask_clarification": true, "web_eval": true,
	"web_click": true, "web_scroll": true, "web_screenshot": true, "web_extract": true,
	"web_click_index": true, "web_input": true, "web_replay": true,
	"workflow_learner": true,
}
