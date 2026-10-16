package content

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MarkdownToPDFTool 将 Markdown 转换为 PDF
type MarkdownToPDFTool struct {
	name        string
	description string
	redact      func([]byte) []byte
}

// NewMarkdownToPDFTool 创建 Markdown 转 PDF 工具
func NewMarkdownToPDFTool(redactors ...func([]byte) []byte) *MarkdownToPDFTool {
	tool := &MarkdownToPDFTool{
		name:        "markdown_to_pdf",
		description: "将 Markdown 文件转换为 PDF，自动尝试 Node.js 和 Go 两种方式",
	}
	if len(redactors) > 0 {
		tool.redact = redactors[0]
	}
	return tool
}

// Name 返回工具名称
func (t *MarkdownToPDFTool) Name() string {
	return t.name
}

// Description 返回工具描述
func (t *MarkdownToPDFTool) Description() string {
	return t.description
}

// Parameters 返回参数定义
func (t *MarkdownToPDFTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"input_path": map[string]interface{}{
				"type":        "string",
				"description": "输入的 Markdown 文件路径",
			},
			"output_path": map[string]interface{}{
				"type":        "string",
				"description": "输出的 PDF 文件路径（可选，默认与输入同目录同名）",
			},
			"install_if_missing": map[string]interface{}{
				"type":        "boolean",
				"description": "如果工具缺失是否自动安装（默认 false）",
				"default":     false,
			},
		},
		"required": []string{"input_path"},
	}
}

// Execute 执行转换
func (t *MarkdownToPDFTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	// 获取参数
	inputPath, ok := params["input_path"].(string)
	if !ok || inputPath == "" {
		return "", fmt.Errorf("参数 input_path 是必需的")
	}

	// 检查输入文件是否存在
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return "", fmt.Errorf("输入文件不存在: %s", inputPath)
	}

	// 获取输出路径
	outputPath := t.getOutputPath(params, inputPath)
	exportInputPath, cleanupExportInput, err := PrepareMarkdownPDFExportInput(inputPath, t.redact)
	if err != nil {
		return "", err
	}
	defer cleanupExportInput()

	// 是否自动安装
	installIfMissing, _ := params["install_if_missing"].(bool)

	// 记录尝试过的方法
	var attempts []string
	var lastErr error

	// 按优先级尝试所有方法（原生 Go 工具优先，Node.js 备选）
	methods := []struct {
		name        string
		checkFunc   func() bool
		execFunc    func(context.Context, string, string) error
		installFunc func() error
	}{
		{"Go (md2pdf)", t.checkGo, t.execGo, t.installGo},
		{"Node.js (md-to-pdf)", t.checkNode, t.execNode, t.installNode},
	}

	for _, method := range methods {
		// 检查工具是否可用
		if !method.checkFunc() {
			if installIfMissing {
				// 尝试安装
				fmt.Printf("正在安装 %s...\n", method.name)
				if err := method.installFunc(); err == nil {
					fmt.Printf("✅ %s 安装成功\n", method.name)
					// 等待一下确保安装完成
					time.Sleep(2 * time.Second)
					// 安装成功后重新检查
					if method.checkFunc() {
						// 尝试执行
						if err := method.execFunc(ctx, exportInputPath, outputPath); err == nil {
							return fmt.Sprintf("✅ 转换成功！\n- 输入: %s\n- 输出: %s\n- 使用工具: %s (已自动安装)", inputPath, outputPath, method.name), nil
						} else {
							lastErr = err
							attempts = append(attempts, fmt.Sprintf("%s: 执行失败 - %v", method.name, err))
							continue
						}
					}
				} else {
					attempts = append(attempts, fmt.Sprintf("%s: 安装失败 - %v", method.name, err))
					lastErr = err
					continue
				}
			} else {
				attempts = append(attempts, fmt.Sprintf("%s: 未安装", method.name))
				continue
			}
		}

		// 工具可用，尝试执行
		fmt.Printf("使用 %s 转换...\n", method.name)
		if err := method.execFunc(ctx, exportInputPath, outputPath); err == nil {
			return fmt.Sprintf("✅ 转换成功！\n- 输入: %s\n- 输出: %s\n- 使用工具: %s", inputPath, outputPath, method.name), nil
		} else {
			attempts = append(attempts, fmt.Sprintf("%s: 执行失败 - %v", method.name, err))
			lastErr = err
		}
	}

	// 所有方法都失败了，汇总错误信息
	errorMsg := fmt.Sprintf("❌ 所有转换方式均失败，尝试了 %d 种方法:\n", len(attempts))
	for _, attempt := range attempts {
		errorMsg += fmt.Sprintf("  - %s\n", attempt)
	}

	// 添加安装建议
	errorMsg += "\n💡 建议安装任一工具:\n"
	errorMsg += "  1. Node.js: npm i -g md-to-pdf\n"
	errorMsg += "  2. Go: go install github.com/mandolyte/mdtopdf/v2/cmd/md2pdf@latest\n"

	if lastErr != nil {
		errorMsg += fmt.Sprintf("\n最后错误: %v", lastErr)
	}

	return "", fmt.Errorf("%s", errorMsg)
}

// prepareMarkdownPDFExportInput creates a sanitized export-only copy beside
// the source so relative Markdown assets keep resolving. The operational source
// remains unchanged and is never replaced with the sanitized bytes.
func PrepareMarkdownPDFExportInput(inputPath string, redactors ...func([]byte) []byte) (string, func(), error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return "", func() {}, fmt.Errorf("读取输入文件失败: %v", err)
	}
	exportData := append([]byte(nil), data...)
	if len(redactors) > 0 && redactors[0] != nil {
		exportData = redactors[0](exportData)
	}
	temp, err := os.CreateTemp(filepath.Dir(inputPath), ".openkitty-pdf-export-*.md")
	if err != nil {
		return "", func() {}, fmt.Errorf("创建 PDF 导出副本失败: %v", err)
	}
	tempPath := temp.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	if _, err := temp.Write(exportData); err != nil {
		_ = temp.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("写入 PDF 导出副本失败: %v", err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("关闭 PDF 导出副本失败: %v", err)
	}
	return tempPath, cleanup, nil
}

// getOutputPath 获取输出路径
func (t *MarkdownToPDFTool) getOutputPath(params map[string]interface{}, inputPath string) string {
	if outputPath, ok := params["output_path"].(string); ok && outputPath != "" {
		return outputPath
	}
	ext := filepath.Ext(inputPath)
	return inputPath[0:len(inputPath)-len(ext)] + ".pdf"
}

// checkNode 检查 Node.js 工具是否可用
func (t *MarkdownToPDFTool) checkNode() bool {
	// 检查 md-to-pdf 命令是否存在
	if _, err := exec.LookPath("md-to-pdf"); err == nil {
		return true
	}
	return false
}

// execNode 执行 Node.js 转换（修正版）
func (t *MarkdownToPDFTool) execNode(ctx context.Context, inputPath, outputPath string) error {
	// 设置超时
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// 读取输入文件内容
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("读取输入文件失败: %v", err)
	}

	// 创建输出文件
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %v", err)
	}
	defer outFile.Close()

	// 执行 md-to-pdf 命令，通过 stdin 传递内容，stdout 接收 PDF 输出
	cmd := exec.CommandContext(ctx, "md-to-pdf")
	cmd.Stdin = strings.NewReader(string(content))
	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr

	// 执行命令
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("执行 md-to-pdf 失败: %v", err)
	}

	// 验证输出文件
	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("无法验证输出文件: %v", err)
	}
	if fileInfo.Size() == 0 {
		return fmt.Errorf("生成的 PDF 文件为空")
	}

	return nil
}

// installNode 安装 Node.js 工具
func (t *MarkdownToPDFTool) installNode() error {
	// 检查 npm 是否存在
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("npm 未安装，请先安装 Node.js")
	}

	fmt.Println("正在安装 md-to-pdf...")
	cmd := exec.Command("npm", "install", "-g", "md-to-pdf")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装失败: %v, 输出: %s", err, string(output))
	}

	fmt.Println("md-to-pdf 安装成功")
	return nil
}

// checkGo 检查 Go 工具是否可用
func (t *MarkdownToPDFTool) checkGo() bool {
	if _, err := exec.LookPath("md2pdf"); err == nil {
		// 尝试执行帮助命令验证
		cmd := exec.Command("md2pdf", "-h")
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}

// execGo 执行 Go 转换
func (t *MarkdownToPDFTool) execGo(ctx context.Context, inputPath, outputPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// md2pdf 的参数格式：-i 输入文件 -o 输出文件
	cmd := exec.CommandContext(ctx, "md2pdf", "-i", inputPath, "-o", outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v, 输出: %s", err, string(output))
	}

	// 验证输出文件
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("转换完成但未找到输出文件")
	}

	return nil
}

// installGo 安装 Go 工具
func (t *MarkdownToPDFTool) installGo() error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go 未安装，请先安装 Go")
	}

	fmt.Println("正在安装 md2pdf...")
	cmd := exec.Command("go", "install", "github.com/mandolyte/mdtopdf/v2/cmd/md2pdf@latest")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装失败: %v, 输出: %s", err, string(output))
	}

	fmt.Println("md2pdf 安装成功")
	return nil
}

// registerMarkdownToPDFTool 注册工具
