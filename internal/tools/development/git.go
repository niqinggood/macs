package development

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type GitTool struct {
	repoPath   string
	gitPath    string // git 可执行文件路径
	workspace  string
	timeout    time.Duration
	sshKeyPath string // SSH密钥路径（用于私有仓库）
	cleanBytes func([]byte) []byte
}

func NewGitTool(workspace string) *GitTool {
	return NewGitToolWithCleaner(workspace, nil)
}

func NewGitToolWithCleaner(workspace string, cleaner func([]byte) []byte) *GitTool {
	// 查找git命令路径
	gitPath, _ := exec.LookPath("git")
	if gitPath == "" {
		gitPath = "git" // 让系统在PATH中查找
	}

	return &GitTool{
		workspace:  workspace,
		gitPath:    gitPath,
		timeout:    5 * time.Minute,
		cleanBytes: cleaner,
	}
}

func (t *GitTool) Name() string { return "git" }

func (t *GitTool) Description() string {
	return `强大的Git版本控制工具，支持完整的Git操作。
可以克隆仓库、提交代码、管理分支、查看历史等。
支持HTTP/HTTPS和SSH协议。`
}

func (t *GitTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{
					// 仓库操作
					"clone",  // 克隆仓库
					"init",   // 初始化仓库
					"status", // 查看状态

					// 提交操作
					"add",    // 添加文件
					"commit", // 提交
					"log",    // 查看日志
					"diff",   // 查看差异
					"show",   // 显示提交详情

					// 分支操作
					"branch",   // 分支管理
					"checkout", // 切换分支
					"merge",    // 合并分支
					"rebase",   // 变基

					// 远程操作
					"push",   // 推送
					"pull",   // 拉取
					"fetch",  // 获取
					"remote", // 远程仓库管理

					// 标签操作
					"tag", // 标签管理

					// 撤销操作
					"reset",  // 重置
					"revert", // 撤销提交
					"clean",  // 清理未跟踪文件

					// 高级操作
					"stash",       // 暂存
					"cherry-pick", // 挑选提交
					"bisect",      // 二分查找
					"blame",       // 查看每行谁修改的

					// 工作区隔离（借鉴Claude Code）
					"worktree_add",    // 创建隔离工作区
					"worktree_remove", // 删除工作区
					"worktree_list",   // 列出所有工作区

					// 统计分析
					"stats", // 统计信息
					"graph", // 提交图
				},
				"description": "Git操作类型",
			},

			// 仓库相关参数
			"repository": map[string]interface{}{
				"type":        "string",
				"description": "仓库URL（用于clone）",
			},
			"branch": map[string]interface{}{
				"type":        "string",
				"description": "分支名",
			},
			"remote": map[string]interface{}{
				"type":        "string",
				"description": "远程仓库名（默认origin）",
			},

			// 文件相关参数
			"files": map[string]interface{}{
				"type":        "array",
				"description": "文件列表",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"file": map[string]interface{}{
				"type":        "string",
				"description": "单个文件路径",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "路径",
			},

			// 提交相关参数
			"message": map[string]interface{}{
				"type":        "string",
				"description": "提交信息",
			},
			"commit": map[string]interface{}{
				"type":        "string",
				"description": "提交哈希",
			},
			"commits": map[string]interface{}{
				"type":        "array",
				"description": "多个提交哈希",
			},

			// 其他参数
			"tag_name": map[string]interface{}{
				"type":        "string",
				"description": "标签名",
			},
			"tag_message": map[string]interface{}{
				"type":        "string",
				"description": "标签说明",
			},
			"author": map[string]interface{}{
				"type":        "string",
				"description": "作者",
			},
			"since": map[string]interface{}{
				"type":        "string",
				"description": "起始时间 (YYYY-MM-DD)",
			},
			"until": map[string]interface{}{
				"type":        "string",
				"description": "结束时间",
			},
			"max_count": map[string]interface{}{
				"type":        "integer",
				"description": "最大显示条数",
			},
			"format": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"oneline", "short", "medium", "full", "fuller", "json"},
				"description": "输出格式",
			},
			"depth": map[string]interface{}{
				"type":        "integer",
				"description": "克隆深度",
			},
			"recursive": map[string]interface{}{
				"type":        "boolean",
				"description": "是否递归克隆子模块",
			},
		},
		"required": []string{"action"},
	}
}

func (t *GitTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)

	// 确定仓库路径
	repoPath := t.repoPath
	if repoPath == "" {
		repoPath = t.workspace
	}

	switch action {
	// 仓库操作
	case "clone":
		return t.clone(params)
	case "init":
		return t.init(params)
	case "status":
		return t.status(repoPath)

	// 提交操作
	case "add":
		return t.add(repoPath, params)
	case "commit":
		return t.commit(repoPath, params)
	case "log":
		return t.log(repoPath, params)
	case "diff":
		return t.diff(repoPath, params)
	case "show":
		return t.show(repoPath, params)

	// 分支操作
	case "branch":
		return t.branch(repoPath, params)
	case "checkout":
		return t.checkout(repoPath, params)
	case "merge":
		return t.merge(repoPath, params)
	case "rebase":
		return t.rebase(repoPath, params)

	// 远程操作
	case "push":
		return t.push(repoPath, params)
	case "pull":
		return t.pull(repoPath, params)
	case "fetch":
		return t.fetch(repoPath, params)
	case "remote":
		return t.remote(repoPath, params)

	// 标签操作
	case "tag":
		return t.tag(repoPath, params)

	// 撤销操作
	case "reset":
		return t.reset(repoPath, params)
	case "revert":
		return t.revert(repoPath, params)
	case "clean":
		return t.clean(repoPath, params)

	// 高级操作
	case "stash":
		return t.stash(repoPath, params)
	case "cherry-pick":
		return t.cherryPick(repoPath, params)
	case "bisect":
		return t.bisect(repoPath, params)

	// 工作区隔离（借鉴Claude Code worktree）
	case "worktree_add":
		return t.worktreeAdd(repoPath, params)
	case "worktree_remove":
		return t.worktreeRemove(repoPath, params)
	case "worktree_list":
		return t.worktreeList(repoPath)
	case "blame":
		return t.blame(repoPath, params)

	// 统计分析
	case "stats":
		return t.stats(repoPath, params)
	case "graph":
		return t.graph(repoPath, params)

	default:
		return "", fmt.Errorf("未知Git操作: %s", action)
	}
}

func (t *GitTool) init(params map[string]interface{}) (string, error) {
	// 如果指定了路径，则在该路径初始化
	path, _ := params["path"].(string)
	if path == "" {
		path = t.workspace
	}
	output, err := t.runGitCommand(path, "init")
	if err != nil {
		return "", err
	}
	t.repoPath = path
	return output, nil
}

// show 显示提交详情
func (t *GitTool) show(repoPath string, params map[string]interface{}) (string, error) {
	commit, _ := params["commit"].(string)
	if commit == "" {
		return "", fmt.Errorf("需要指定 commit")
	}
	args := []string{"show", commit}
	output, err := t.runGitCommand(repoPath, args...)
	return t.formatDiff(output), err
}

// checkout 切换分支或恢复文件
func (t *GitTool) checkout(repoPath string, params map[string]interface{}) (string, error) {
	branch, _ := params["branch"].(string)
	commit, _ := params["commit"].(string)
	file, _ := params["file"].(string)

	args := []string{"checkout"}
	if branch != "" {
		args = append(args, branch)
	} else if commit != "" {
		args = append(args, commit)
	} else if file != "" {
		args = append(args, "--", file)
	} else {
		return "", fmt.Errorf("需要指定 branch、commit 或 file")
	}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// merge 合并分支
func (t *GitTool) merge(repoPath string, params map[string]interface{}) (string, error) {
	branch, _ := params["branch"].(string)
	if branch == "" {
		return "", fmt.Errorf("需要指定要合并的分支")
	}
	args := []string{"merge", branch}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// rebase 变基
func (t *GitTool) rebase(repoPath string, params map[string]interface{}) (string, error) {
	branch, _ := params["branch"].(string)
	if branch == "" {
		return "", fmt.Errorf("需要指定目标分支")
	}
	args := []string{"rebase", branch}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// fetch 获取远程更新
func (t *GitTool) fetch(repoPath string, params map[string]interface{}) (string, error) {
	remote, _ := params["remote"].(string)
	branch, _ := params["branch"].(string)
	args := []string{"fetch"}
	if remote != "" {
		args = append(args, remote)
		if branch != "" {
			args = append(args, branch)
		}
	}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// remote 管理远程仓库
func (t *GitTool) remote(repoPath string, params map[string]interface{}) (string, error) {
	subAction, _ := params["sub_action"].(string) // 如 add, remove, rename
	name, _ := params["name"].(string)
	url, _ := params["url"].(string)

	args := []string{"remote"}
	switch subAction {
	case "add":
		if name == "" || url == "" {
			return "", fmt.Errorf("添加远程需要指定 name 和 url")
		}
		args = append(args, "add", name, url)
	case "remove":
		if name == "" {
			return "", fmt.Errorf("需要指定要删除的远程名称")
		}
		args = append(args, "remove", name)
	case "rename":
		if name == "" || url == "" { // 这里 url 作为新名称
			return "", fmt.Errorf("需要指定原名称和新名称")
		}
		args = append(args, "rename", name, url)
	default:
		// 默认列出远程
		args = append(args, "-v")
	}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// tag 标签管理
func (t *GitTool) tag(repoPath string, params map[string]interface{}) (string, error) {
	subAction, _ := params["sub_action"].(string) // create, delete, list
	tagName, _ := params["tag_name"].(string)
	message, _ := params["tag_message"].(string)
	commit, _ := params["commit"].(string)

	args := []string{"tag"}
	switch subAction {
	case "create":
		if tagName == "" {
			return "", fmt.Errorf("需要指定标签名")
		}
		if message != "" {
			args = append(args, "-a", tagName, "-m", message)
		} else {
			args = append(args, tagName)
		}
		if commit != "" {
			args = append(args, commit)
		}
	case "delete":
		if tagName == "" {
			return "", fmt.Errorf("需要指定要删除的标签名")
		}
		args = append(args, "-d", tagName)
	default:
		// 列出标签
		args = append(args, "-l")
	}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// reset 重置
func (t *GitTool) reset(repoPath string, params map[string]interface{}) (string, error) {
	mode, _ := params["mode"].(string) // soft, mixed, hard
	commit, _ := params["commit"].(string)

	args := []string{"reset"}
	if mode != "" {
		args = append(args, "--"+mode)
	}
	if commit != "" {
		args = append(args, commit)
	} else {
		args = append(args, "HEAD")
	}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// revert 撤销提交
func (t *GitTool) revert(repoPath string, params map[string]interface{}) (string, error) {
	commit, _ := params["commit"].(string)
	if commit == "" {
		return "", fmt.Errorf("需要指定要撤销的提交")
	}
	args := []string{"revert", "--no-edit", commit}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// clean 清理未跟踪文件
func (t *GitTool) clean(repoPath string, params map[string]interface{}) (string, error) {
	dryRun, _ := params["dry_run"].(bool)
	directories, _ := params["directories"].(bool)

	args := []string{"clean"}
	if dryRun {
		args = append(args, "-n")
	}
	if directories {
		args = append(args, "-d")
	}
	args = append(args, "-f") // 强制执行
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// stash 暂存
func (t *GitTool) stash(repoPath string, params map[string]interface{}) (string, error) {
	subAction, _ := params["sub_action"].(string) // save, pop, apply, list, drop
	message, _ := params["message"].(string)

	args := []string{"stash"}
	switch subAction {
	case "save":
		args = append(args, "save")
		if message != "" {
			args = append(args, message)
		}
	case "pop":
		args = append(args, "pop")
	case "apply":
		args = append(args, "apply")
	case "list":
		args = append(args, "list")
	case "drop":
		args = append(args, "drop")
	default:
		// 默认 list
		args = append(args, "list")
	}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// cherryPick 挑选提交
func (t *GitTool) cherryPick(repoPath string, params map[string]interface{}) (string, error) {
	commits, _ := params["commits"].([]interface{})
	if len(commits) == 0 {
		commit, _ := params["commit"].(string)
		if commit == "" {
			return "", fmt.Errorf("需要指定要挑选的提交")
		}
		commits = []interface{}{commit}
	}

	args := []string{"cherry-pick"}
	for _, c := range commits {
		args = append(args, fmt.Sprintf("%v", c))
	}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// bisect 二分查找
func (t *GitTool) bisect(repoPath string, params map[string]interface{}) (string, error) {
	subAction, _ := params["sub_action"].(string) // start, bad, good, reset
	commit, _ := params["commit"].(string)

	args := []string{"bisect"}
	switch subAction {
	case "start":
		args = append(args, "start")
	case "bad":
		args = append(args, "bad")
		if commit != "" {
			args = append(args, commit)
		}
	case "good":
		args = append(args, "good")
		if commit != "" {
			args = append(args, commit)
		}
	case "reset":
		args = append(args, "reset")
	default:
		return "", fmt.Errorf("未知 bisect 子命令")
	}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

// blame 查看每行修改
func (t *GitTool) blame(repoPath string, params map[string]interface{}) (string, error) {
	file, _ := params["file"].(string)
	if file == "" {
		return "", fmt.Errorf("需要指定文件")
	}
	args := []string{"blame", file}
	output, err := t.runGitCommand(repoPath, args...)
	return output, err
}

func (t *GitTool) clone(params map[string]interface{}) (string, error) {
	repo, _ := params["repository"].(string)
	if repo == "" {
		return "", fmt.Errorf("需要指定repository")
	}

	branch, _ := params["branch"].(string)
	depth, _ := params["depth"].(float64)
	recursive, _ := params["recursive"].(bool)

	// 解析仓库名作为目录名
	repoName := strings.TrimSuffix(filepath.Base(repo), ".git")
	targetDir := filepath.Join(t.workspace, "repos", repoName)

	args := []string{"clone"}

	if branch != "" {
		args = append(args, "-b", branch)
	}

	if depth > 0 {
		args = append(args, "--depth", fmt.Sprintf("%d", int(depth)))
	}

	if recursive {
		args = append(args, "--recursive")
	}

	args = append(args, repo, targetDir)

	output, err := t.runGitCommand("", args...)
	if err != nil {
		return "", err
	}

	t.repoPath = targetDir

	return fmt.Sprintf("✅ 成功克隆仓库到: %s\n%s", targetDir, output), nil
}

// 查看状态
func (t *GitTool) status(repoPath string) (string, error) {
	output, err := t.runGitCommand(repoPath, "status", "--short", "--branch")
	if err != nil {
		return "", err
	}

	// 解析状态
	lines := strings.Split(output, "\n")
	var staged, unstaged, untracked []string
	var branchInfo string

	for _, line := range lines {
		if strings.HasPrefix(line, "##") {
			branchInfo = line
			continue
		}
		if len(line) < 3 {
			continue
		}

		status := line[:2]
		file := strings.TrimSpace(line[3:])

		switch status {
		case "M ", "A ", "D ", "R ", "C ":
			staged = append(staged, fmt.Sprintf("  %s %s", status, file))
		case " M", " D", " R", " C":
			unstaged = append(unstaged, fmt.Sprintf("  %s %s", status, file))
		case "??":
			untracked = append(untracked, fmt.Sprintf("  %s", file))
		}
	}

	// 格式化输出
	var result strings.Builder
	result.WriteString("📊 Git 状态\n")
	result.WriteString(strings.Repeat("─", 50) + "\n")
	result.WriteString(branchInfo + "\n\n")

	if len(staged) > 0 {
		result.WriteString("📦 暂存区:\n")
		for _, s := range staged {
			result.WriteString(s + "\n")
		}
		result.WriteString("\n")
	}

	if len(unstaged) > 0 {
		result.WriteString("✏️ 未暂存:\n")
		for _, u := range unstaged {
			result.WriteString(u + "\n")
		}
		result.WriteString("\n")
	}

	if len(untracked) > 0 {
		result.WriteString("❓ 未跟踪:\n")
		for _, u := range untracked {
			result.WriteString(u + "\n")
		}
		result.WriteString("\n")
	}

	if len(staged) == 0 && len(unstaged) == 0 && len(untracked) == 0 {
		result.WriteString("✨ 工作区干净，没有改动\n")
	}

	return result.String(), nil
}

// 添加文件
func (t *GitTool) add(repoPath string, params map[string]interface{}) (string, error) {
	files, _ := params["files"].([]interface{})
	file, _ := params["file"].(string)

	args := []string{"add"}

	if len(files) > 0 {
		for _, f := range files {
			args = append(args, fmt.Sprintf("%v", f))
		}
	} else if file != "" {
		args = append(args, file)
	} else {
		// 添加所有
		args = append(args, ".")
	}

	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("✅ 已添加文件到暂存区\n%s", output), nil
}

// 提交
func (t *GitTool) commit(repoPath string, params map[string]interface{}) (string, error) {
	message, _ := params["message"].(string)
	if message == "" {
		return "", fmt.Errorf("提交信息不能为空")
	}

	args := []string{"commit", "-m", message}

	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", err
	}

	// 解析提交信息
	var commitHash string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "commit") {
			parts := strings.Fields(line)
			if len(parts) > 1 {
				commitHash = parts[1]
			}
		}
	}

	result := fmt.Sprintf("✅ 提交成功\n%s", output)
	if commitHash != "" {
		result += fmt.Sprintf("\n🔑 提交哈希: %s", commitHash)
	}

	return result, nil
}

// 查看日志
func (t *GitTool) log(repoPath string, params map[string]interface{}) (string, error) {
	args := []string{"log"}

	maxCount, _ := params["max_count"].(float64)
	if maxCount > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", int(maxCount)))
	} else {
		args = append(args, "-n", "20") // 默认显示20条
	}

	format, _ := params["format"].(string)
	if format == "json" {
		args = append(args, "--pretty=format:'%H%n%an%n%ae%n%ad%n%s%n%b%n====='")
	} else {
		// 美化输出
		args = append(args, "--pretty=format:%C(yellow)%h%Creset %C(blue)%ad%Creset %C(green)%an%Creset %s")
		args = append(args, "--date=short")
	}

	branch, _ := params["branch"].(string)
	if branch != "" {
		args = append(args, branch)
	}

	author, _ := params["author"].(string)
	if author != "" {
		args = append(args, "--author="+author)
	}

	since, _ := params["since"].(string)
	if since != "" {
		args = append(args, "--since="+since)
	}

	until, _ := params["until"].(string)
	if until != "" {
		args = append(args, "--until="+until)
	}

	file, _ := params["file"].(string)
	if file != "" {
		args = append(args, "--", file)
	}

	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", err
	}

	if format == "json" {
		return t.parseJSONLog(output)
	}

	var result strings.Builder
	result.WriteString("📜 提交日志\n")
	result.WriteString(strings.Repeat("─", 80) + "\n")
	result.WriteString(output)
	result.WriteString("\n" + strings.Repeat("─", 80))

	return result.String(), nil
}

// 分支管理
func (t *GitTool) branch(repoPath string, params map[string]interface{}) (string, error) {
	args := []string{"branch", "-v", "-a"} // 显示所有分支和最新提交

	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", err
	}

	var result strings.Builder
	result.WriteString("🌿 分支列表\n")
	result.WriteString(strings.Repeat("─", 50) + "\n")

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "->") {
			// 远程HEAD指向信息
			continue
		}

		if strings.HasPrefix(line, "*") {
			// 当前分支
			result.WriteString("⭐ " + line + "\n")
		} else {
			result.WriteString("   " + line + "\n")
		}
	}

	return result.String(), nil
}

// 推送
func (t *GitTool) push(repoPath string, params map[string]interface{}) (string, error) {
	remote, _ := params["remote"].(string)
	if remote == "" {
		remote = "origin"
	}

	branch, _ := params["branch"].(string)
	if branch == "" {
		// 获取当前分支
		currentBranch, err := t.getCurrentBranch(repoPath)
		if err != nil {
			return "", err
		}
		branch = currentBranch
	}

	args := []string{"push", remote, branch}

	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("🚀 成功推送到 %s/%s\n%s", remote, branch, output), nil
}

// 拉取
func (t *GitTool) pull(repoPath string, params map[string]interface{}) (string, error) {
	remote, _ := params["remote"].(string)
	if remote == "" {
		remote = "origin"
	}

	branch, _ := params["branch"].(string)
	if branch == "" {
		// 获取当前分支
		currentBranch, err := t.getCurrentBranch(repoPath)
		if err != nil {
			return "", err
		}
		branch = currentBranch
	}

	args := []string{"pull", remote, branch}

	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("⬇️ 成功从 %s/%s 拉取更新\n%s", remote, branch, output), nil
}

// 查看差异
func (t *GitTool) diff(repoPath string, params map[string]interface{}) (string, error) {
	args := []string{"diff", "--color=never"}

	file, _ := params["file"].(string)
	if file != "" {
		args = append(args, "--", file)
	}

	commit, _ := params["commit"].(string)
	if commit != "" {
		args = []string{"show", commit}
	}

	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", err
	}

	if output == "" {
		return "📝 没有差异", nil
	}

	return t.formatDiff(output), nil
}

// 统计信息
func (t *GitTool) stats(repoPath string, params map[string]interface{}) (string, error) {
	var result strings.Builder
	result.WriteString("📊 Git 统计信息\n")
	result.WriteString(strings.Repeat("─", 50) + "\n")

	// 提交总数
	total, _ := t.runGitCommand(repoPath, "rev-list", "--count", "HEAD")
	result.WriteString(fmt.Sprintf("📝 总提交数: %s", strings.TrimSpace(total)))

	// 贡献者
	authors, _ := t.runGitCommand(repoPath, "shortlog", "-sn")
	result.WriteString("\n👥 贡献者:\n")
	authorLines := strings.Split(authors, "\n")
	for i, line := range authorLines {
		if i >= 10 {
			result.WriteString("   ...\n")
			break
		}
		if line != "" {
			result.WriteString("  " + line + "\n")
		}
	}

	// 最近活动
	lastWeek, _ := t.runGitCommand(repoPath, "log", "--since='1 week ago'", "--oneline")
	lastWeekCount := len(strings.Split(strings.TrimSpace(lastWeek), "\n"))
	result.WriteString(fmt.Sprintf("📅 最近一周提交: %d\n", lastWeekCount))

	// 文件统计
	files, _ := t.runGitCommand(repoPath, "ls-files", "|", "wc", "-l")
	result.WriteString(fmt.Sprintf("📁 文件数量: %s", strings.TrimSpace(files)))

	// 代码行数
	lines, _ := t.runGitCommand(repoPath, "git", "ls-files", "|", "xargs", "cat", "|", "wc", "-l")
	result.WriteString(fmt.Sprintf("📏 代码行数: %s", strings.TrimSpace(lines)))

	return result.String(), nil
}

// 提交图
func (t *GitTool) graph(repoPath string, params map[string]interface{}) (string, error) {
	args := []string{"log", "--graph", "--pretty=format:'%C(yellow)%h%Creset %s %C(blue)(%an)%Creset'", "--abbrev-commit", "--date=relative"}

	maxCount, _ := params["max_count"].(float64)
	if maxCount > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", int(maxCount)))
	} else {
		args = append(args, "-n", "30")
	}

	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", err
	}

	var result strings.Builder
	result.WriteString("🌳 提交图\n")
	result.WriteString(strings.Repeat("─", 80) + "\n")
	result.WriteString(output)

	return result.String(), nil
}

func (t *GitTool) runGitCommand(repoPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, t.gitPath, args...)
	if repoPath != "" {
		cmd.Dir = repoPath
	}

	// 如果有SSH密钥，设置环境变量
	if t.sshKeyPath != "" {
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o IdentitiesOnly=yes", t.sshKeyPath))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git命令失败: %v\n%s", err, output)
	}
	cleanOutput := output
	if t.cleanBytes != nil {
		cleanOutput = t.cleanBytes(output)
	}

	return string(cleanOutput), nil
}

// 获取当前分支
func (t *GitTool) getCurrentBranch(repoPath string) (string, error) {
	output, err := t.runGitCommand(repoPath, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// 格式化diff输出
func (t *GitTool) formatDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	var result strings.Builder

	result.WriteString("📝 差异对比\n")
	result.WriteString(strings.Repeat("─", 80) + "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			result.WriteString("\n📄 " + line + "\n")
		} else if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			// 忽略文件标记
			continue
		} else if strings.HasPrefix(line, "@@") {
			result.WriteString("🔍 " + line + "\n")
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			result.WriteString("🟢 " + line + "\n")
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			result.WriteString("🔴 " + line + "\n")
		} else {
			result.WriteString("   " + line + "\n")
		}
	}

	return result.String()
}

// 解析JSON格式的日志
func (t *GitTool) parseJSONLog(output string) (string, error) {
	var commits []map[string]interface{}
	entries := strings.Split(output, "=====\n")

	for _, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			continue
		}

		lines := strings.Split(entry, "\n")
		if len(lines) >= 5 {
			commit := map[string]interface{}{
				"hash":    strings.TrimSpace(lines[0]),
				"author":  strings.TrimSpace(lines[1]),
				"email":   strings.TrimSpace(lines[2]),
				"date":    strings.TrimSpace(lines[3]),
				"subject": strings.TrimSpace(lines[4]),
			}

			if len(lines) > 5 {
				commit["body"] = strings.Join(lines[5:], "\n")
			}

			commits = append(commits, commit)
		}
	}

	jsonData, err := json.MarshalIndent(commits, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonData), nil
}

// 借鉴Claude Code worktree隔离: agent在独立分支工作，不影响主分支
func (t *GitTool) worktreeAdd(repoPath string, params map[string]interface{}) (string, error) {
	name, _ := params["name"].(string) // 分支名，默认 auto-{timestamp}
	path, _ := params["path"].(string) // worktree路径
	base, _ := params["base"].(string) // 基准分支，默认 HEAD

	if name == "" {
		name = fmt.Sprintf("auto-%s", time.Now().Format("20060102-150405"))
	}
	if path == "" {
		parentDir := filepath.Dir(repoPath)
		path = filepath.Join(parentDir, ".worktrees", name)
	}
	if base == "" {
		base = "HEAD"
	}

	args := []string{"worktree", "add", path, "-b", name}
	if base != "HEAD" {
		args = append(args, base)
	}
	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", fmt.Errorf("worktree add failed: %w\nOutput: %s", err, output)
	}
	return fmt.Sprintf("Worktree created:\n- Branch: %s\n- Path: %s\n- Base: %s\n\n%s", name, path, base, output), nil
}

func (t *GitTool) worktreeRemove(repoPath string, params map[string]interface{}) (string, error) {
	path, _ := params["path"].(string)
	force, _ := params["force"].(bool)

	if path == "" {
		return "", fmt.Errorf("path is required for worktree_remove")
	}

	args := []string{"worktree", "remove", path}
	if force {
		args = append(args, "--force")
	}
	output, err := t.runGitCommand(repoPath, args...)
	if err != nil {
		return "", fmt.Errorf("worktree remove failed: %w\nOutput: %s", err, output)
	}
	return "Worktree removed: " + path + "\n" + output, nil
}

func (t *GitTool) worktreeList(repoPath string) (string, error) {
	output, err := t.runGitCommand(repoPath, "worktree", "list")
	if err != nil {
		return "", fmt.Errorf("worktree list failed: %w", err)
	}
	return output, nil
}

// GitHub API 集成
