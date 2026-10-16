package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ListDirTool struct {
	workspace string
	maxDepth  int
	deps      FileToolDependencies // 默认最大深度
}

func NewListDirTool(workspace string, dependencies ...FileToolDependencies) *ListDirTool {
	deps := defaultFileToolDependencies()
	if len(dependencies) > 0 {
		deps = mergeFileToolDependencies(deps, dependencies[0])
	}
	return &ListDirTool{
		workspace: workspace,
		maxDepth:  4,
		deps:      deps, // 默认递归4层
	}
}

func (t *ListDirTool) WithMaxDepth(depth int) *ListDirTool {
	t.maxDepth = depth
	return t
}

func (t *ListDirTool) Name() string { return "list_dir" }
func (t *ListDirTool) Description() string {
	return "List a directory, optionally recursively. Paths may be absolute or relative to the workspace. When the user explicitly provides an absolute directory, pass it here unchanged; the tool result, not workspace location alone, determines whether access is allowed."
}

func (t *ListDirTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Directory to list. May be an absolute path outside the workspace when the active host policy allows it. Defaults to the workspace (.).",
			},
			"depth": map[string]interface{}{
				"type":        "integer",
				"description": "递归深度，1=只当前目录，2=包含子目录，3=包含子目录的子目录，默认为1",
				"minimum":     1,
				"maximum":     5,
			},
			"detail": map[string]interface{}{
				"type":        "boolean",
				"description": "是否显示详细信息（文件大小、修改时间等），默认为false",
			},
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "文件模式过滤，如 '*.go'、'*.md'，支持通配符",
			},
			"show_hidden": map[string]interface{}{
				"type":        "boolean",
				"description": "是否显示隐藏文件（以.开头的文件），默认为false",
			},
			"sort_by": map[string]interface{}{
				"type":        "string",
				"description": "排序方式：name, size, time，默认为name",
				"enum":        []string{"name", "size", "time"},
			},
		},
	}
}
func (t *ListDirTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	// 解析参数
	path, _ := params["path"].(string)
	if path == "" {
		path, _ = params["file"].(string)
	}
	if path == "" {
		path = "."
	}
	if err := t.deps.EnforcePath(ctx, t.workspace, path); err != nil {
		return "", err
	}

	depth := 1
	if d, ok := params["depth"].(float64); ok {
		depth = int(d)
		if depth < 1 {
			depth = 1
		}
		if depth > 5 {
			depth = 5
		}
	}

	detail := false
	if d, ok := params["detail"].(bool); ok {
		detail = d
	}

	pattern, _ := params["pattern"].(string)

	showHidden := false
	if sh, ok := params["show_hidden"].(bool); ok {
		showHidden = sh
	}

	sortBy := "name"
	if sb, ok := params["sort_by"].(string); ok {
		sortBy = sb
	}

	// 处理路径
	fullPath, err := t.resolvePath(path)
	if err != nil {
		return fmt.Sprintf("路径解析失败: %v", err), nil
	}

	// 检查路径是否存在
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("目录不存在: %s", path), nil
		}
		return fmt.Sprintf("访问目录失败: %v", err), nil
	}

	if !info.IsDir() {
		return fmt.Sprintf("路径不是目录: %s", path), nil
	}

	// 构建目录树
	var result strings.Builder
	result.WriteString(fmt.Sprintf(" Directory: %s\n", path))
	result.WriteString(fmt.Sprintf("📊 recursion depth: %d\n", depth))
	result.WriteString(strings.Repeat("─", 60) + "\n\n")

	// 递归遍历目录
	err = t.buildTree(&result, fullPath, "", 0, depth, detail, pattern, showHidden, sortBy)
	if err != nil {
		return fmt.Sprintf("遍历目录失败: %v", err), nil
	}

	// 添加统计信息
	stats := t.getDirectoryStats(fullPath, depth, pattern, showHidden)
	result.WriteString("\n" + strings.Repeat("─", 60) + "\n")
	result.WriteString(fmt.Sprintf("📈 统计信息:\n"))
	result.WriteString(fmt.Sprintf("   📁 总目录数: %d\n", stats["dirs"]))
	result.WriteString(fmt.Sprintf("   📄 总文件数: %d\n", stats["files"]))
	result.WriteString(fmt.Sprintf("   💾 总大小: %s\n", formatSize(stats["total_size"].(int64))))
	result.WriteString(fmt.Sprintf("\n绝对路径: %s\n", fullPath))

	return result.String(), nil
}

func (t *ListDirTool) resolvePath(path string) (string, error) {
	// 处理空路径
	if path == "" {
		path = "."
	}

	// 清理路径（去除多余的 . 和 ..）
	path = filepath.Clean(path)

	// 获取工作空间的绝对路径
	workspaceAbs, err := filepath.Abs(t.workspace)
	if err != nil {
		return "", fmt.Errorf("无法获取工作空间绝对路径: %v", err)
	}

	// 如果是绝对路径
	if filepath.IsAbs(path) {
		// 检查是否在工作空间内（安全考虑）
		if strings.HasPrefix(path, workspaceAbs) {
			return path, nil
		}
		// 不在工作空间内，但允许访问（可以根据需求决定是否允许）
		return path, nil
	}

	// 处理 "." 的情况
	if path == "." {
		return workspaceAbs, nil
	}

	// 构建工作空间内的完整路径
	fullPath := filepath.Join(workspaceAbs, path)

	// 清理路径，确保没有 .. 逃逸出工作空间
	fullPath = filepath.Clean(fullPath)

	// 安全检查：防止路径遍历攻击（如 ../../../etc/passwd）
	if !strings.HasPrefix(fullPath, workspaceAbs) {
		// 尝试从当前目录解析
		if currentAbs, err := filepath.Abs(path); err == nil {
			return currentAbs, nil
		}
		return "", fmt.Errorf("路径 '%s' 尝试逃逸出工作空间", path)
	}

	return fullPath, nil
}

// 构建目录树
func (t *ListDirTool) buildTree(w *strings.Builder, currentPath string, prefix string, currentDepth, maxDepth int,
	detail bool, pattern string, showHidden bool, sortBy string) error {

	if currentDepth >= maxDepth {
		return nil
	}

	// 读取目录
	entries, err := os.ReadDir(currentPath)
	if err != nil {
		return err
	}

	// 过滤和排序
	entries = t.filterAndSortEntries(entries, currentPath, pattern, showHidden, sortBy)

	for i, entry := range entries {
		isLast := i == len(entries)-1
		name := entry.Name()

		// 跳过隐藏文件（除非显示）
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}

		// 构建连接线
		var connector string
		if isLast {
			connector = "└── "
		} else {
			connector = "├── "
		}

		// 获取文件信息
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// 构建条目
		if entry.IsDir() {
			// 目录条目
			if detail {
				modTime := info.ModTime().Format("2006-01-02 15:04")
				fmt.Fprintf(w, "%s%s📁 %s/ (%s)\n", prefix, connector, name, modTime)
			} else {
				fmt.Fprintf(w, "%s%s📁 %s/\n", prefix, connector, name)
			}

			// 递归进入子目录
			subPath := filepath.Join(currentPath, name)
			var subPrefix string
			if isLast {
				subPrefix = prefix + "    "
			} else {
				subPrefix = prefix + "│   "
			}
			t.buildTree(w, subPath, subPrefix, currentDepth+1, maxDepth, detail, pattern, showHidden, sortBy)

		} else {
			// 文件条目
			size := info.Size()
			if detail {
				modTime := info.ModTime().Format("2006-01-02 15:04")
				fmt.Fprintf(w, "%s%s📄 %s (%s, %s)\n",
					prefix, connector, name, formatSize(size), modTime)
			} else {
				fmt.Fprintf(w, "%s%s📄 %s\n", prefix, connector, name)
			}
		}
	}

	return nil
}

// 过滤和排序条目
func (t *ListDirTool) filterAndSortEntries(entries []os.DirEntry, currentPath string,
	pattern string, showHidden bool, sortBy string) []os.DirEntry {

	// 先过滤
	filtered := make([]os.DirEntry, 0)
	for _, entry := range entries {
		name := entry.Name()

		// 隐藏文件过滤
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}

		// 模式匹配
		if pattern != "" {
			matched, err := filepath.Match(pattern, name)
			if err != nil || !matched {
				continue
			}
		}

		filtered = append(filtered, entry)
	}

	// 再排序
	sort.Slice(filtered, func(i, j int) bool {
		infoI, errI := filtered[i].Info()
		infoJ, errJ := filtered[j].Info()

		// 目录排在前面
		if filtered[i].IsDir() != filtered[j].IsDir() {
			return filtered[i].IsDir()
		}

		// 根据排序方式
		switch sortBy {
		case "size":
			if errI == nil && errJ == nil {
				return infoI.Size() > infoJ.Size()
			}
		case "time":
			if errI == nil && errJ == nil {
				return infoI.ModTime().After(infoJ.ModTime())
			}
		default: // name
			return strings.ToLower(filtered[i].Name()) < strings.ToLower(filtered[j].Name())
		}
		return false
	})

	return filtered
}

// 获取目录统计信息
func (t *ListDirTool) getDirectoryStats(root string, maxDepth int, pattern string, showHidden bool) map[string]interface{} {
	stats := make(map[string]interface{})
	var dirCount, fileCount int
	var totalSize int64

	var walk func(path string, depth int)
	walk = func(path string, depth int) {
		if depth > maxDepth {
			return
		}

		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}

		for _, entry := range entries {
			name := entry.Name()

			// 隐藏文件过滤
			if !showHidden && strings.HasPrefix(name, ".") {
				continue
			}

			// 模式匹配
			if pattern != "" && depth == 1 { // 只在根目录应用模式过滤
				matched, err := filepath.Match(pattern, name)
				if err != nil || !matched {
					continue
				}
			}

			fullPath := filepath.Join(path, name)

			if entry.IsDir() {
				dirCount++
				walk(fullPath, depth+1)
			} else {
				fileCount++
				info, err := entry.Info()
				if err == nil {
					totalSize += info.Size()
				}
			}
		}
	}

	walk(root, 1)

	stats["dirs"] = dirCount
	stats["files"] = fileCount
	stats["total_size"] = totalSize

	return stats
}

// 格式化文件大小
func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	} else if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	} else {
		return fmt.Sprintf("%.2f GB", float64(size)/(1024*1024*1024))
	}
}

// FormatSize formats a byte count for application-facing status output.
func FormatSize(size int64) string {
	return formatSize(size)
}
