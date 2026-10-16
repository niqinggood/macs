package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"mac/internal/textutil"
)

// 交互式测试工具
func InteractiveTest() {
	fmt.Println("=== WebFetchTool 交互式测试工具 ===")
	fmt.Println("输入 help 查看命令，输入 exit 退出")

	tool := NewWebFetchTool(10, "", false, nil, false)
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("\n> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "exit" {
			break
		}

		if input == "help" {
			printHelp()
			continue
		}

		// 解析命令
		parts := strings.SplitN(input, " ", 2)
		command := parts[0]

		switch command {
		case "search":
			if len(parts) < 2 {
				fmt.Println("用法: search <引擎> <查询词>")
				fmt.Println("引擎: baidu, 360, sogou, bing, google, duckduckgo")
				continue
			}

			args := strings.SplitN(parts[1], " ", 2)
			if len(args) < 2 {
				fmt.Println("请指定引擎和查询词")
				continue
			}

			engine := args[0]
			query := args[1]

			doSearch(tool, engine, query)

		case "url":
			if len(parts) < 2 {
				fmt.Println("用法: url <URL>")
				continue
			}

			doFetchURL(tool, parts[1])

		default:
			fmt.Printf("未知命令: %s，输入 help 查看帮助\n", command)
		}
	}
}

func printHelp() {
	fmt.Println(`
可用命令:
  search <引擎> <查询词>  - 使用指定引擎搜索
    引擎: baidu, 360, sogou, bing, google, duckduckgo
    示例: search baidu 上海浦东创业园
  
  url <URL>              - 抓取指定URL内容
    示例: url https://www.so.com/s?q=test
  
  exit                   - 退出程序
  help                   - 显示此帮助`)
}

func doSearch(tool *WebFetchTool, engine, query string) {
	var searchURL string

	switch engine {
	case "baidu":
		searchURL = "https://www.baidu.com/s?wd=" + url.QueryEscape(query)
	case "360":
		searchURL = "https://www.so.com/s?q=" + url.QueryEscape(query)
	case "sogou":
		searchURL = "https://www.sogou.com/web?query=" + url.QueryEscape(query)
	case "bing":
		searchURL = "https://www.bing.com/search?q=" + url.QueryEscape(query)
	case "google":
		searchURL = "https://www.google.com/search?q=" + url.QueryEscape(query)
	case "duckduckgo":
		searchURL = "https://duckduckgo.com/html/?q=" + url.QueryEscape(query)
	default:
		fmt.Printf("不支持的引擎: %s\n", engine)
		return
	}

	fmt.Printf("\n使用 %s 搜索: %s\n", engine, query)
	fmt.Printf("URL: %s\n", searchURL)

	params := map[string]interface{}{
		"url":        searchURL,
		"engine":     engine,
		"maxResults": 10,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	result, err := tool.Execute(ctx, params)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Printf("\n❌ 错误: %v (耗时: %v)\n", err, elapsed)
		return
	}

	fmt.Printf("\n✅ 成功 (耗时: %v)\n", elapsed)
	fmt.Println("\n结果:")
	fmt.Println(result)
}

func doFetchURL(tool *WebFetchTool, urlStr string) {
	fmt.Printf("\n抓取URL: %s\n", urlStr)

	params := map[string]interface{}{
		"url": urlStr,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	result, err := tool.Execute(ctx, params)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Printf("\n❌ 错误: %v (耗时: %v)\n", err, elapsed)
		return
	}

	fmt.Printf("\n✅ 成功 (耗时: %v)\n", elapsed)
	fmt.Println("\n结果:")
	fmt.Println(result)
}

// 运行测试并保存结果
func RunTestWithReport() {
	results := make(map[string]interface{})

	tool := NewWebFetchTool(10, "", false, nil, false)

	testCases := map[string]string{
		"360搜索": "https://www.so.com/s?q=" + url.QueryEscape("上海浦东新区创业园"),
		"搜狗搜索":  "https://www.sogou.com/web?query=" + url.QueryEscape("创业园租金"),
		"百度搜索":  "https://www.baidu.com/s?wd=" + url.QueryEscape("浦东注册公司"),
	}

	for name, url := range testCases {
		fmt.Printf("\n测试: %s\n", name)

		params := map[string]interface{}{
			"url":        url,
			"maxResults": 5,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		start := time.Now()
		result, err := tool.Execute(ctx, params)
		elapsed := time.Since(start)
		cancel()

		if err != nil {
			results[name] = map[string]interface{}{
				"error": err.Error(),
				"time":  elapsed.String(),
			}
		} else {
			results[name] = map[string]interface{}{
				"success": true,
				"time":    elapsed.String(),
				"preview": textutil.TruncateUTF8Bytes(result, 200),
				"full":    result,
			}
		}
	}

	// 保存结果
	data, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println("\n=== 测试报告 ===")
	fmt.Println(string(data))
}
