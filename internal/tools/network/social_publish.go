package network

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SocialPublishTool creates and publishes social media content.
type SocialPublishTool struct {
	workspace string
}

func NewSocialPublishTool(workspace string) *SocialPublishTool {
	os.MkdirAll(filepath.Join(workspace, ".openkitty", "storage", "social_posts"), 0755)
	return &SocialPublishTool{workspace: workspace}
}

func (t *SocialPublishTool) Name() string { return "social_publish" }

func (t *SocialPublishTool) Description() string {
	return "Create and publish social media content to multiple platforms. " +
		"Platforms: xiaohongshu (小红书), zhihu (知乎), weibo (微博), douyin (抖音), linkedin, twitter. " +
		"Generates platform-optimized content, saves drafts, and provides posting instructions. " +
		"For automated posting, use with CDP (Chrome DevTools Protocol) browser automation."
}

func (t *SocialPublishTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action: draft (create draft), publish (post now via CDP), schedule (save for later), list (view drafts)",
				"enum":        []string{"draft", "publish", "schedule", "list"},
			},
			"platform": map[string]interface{}{
				"type":        "string",
				"description": "Target platform: xiaohongshu, zhihu, weibo, douyin, linkedin, twitter",
				"enum":        []string{"xiaohongshu", "zhihu", "weibo", "douyin", "linkedin", "twitter"},
			},
			"title": map[string]interface{}{
				"type": "string", "description": "Post title (for zhihu, linkedin, weibo)",
			},
			"content": map[string]interface{}{
				"type": "string", "description": "Post content/body",
			},
			"tags": map[string]interface{}{
				"type": "string", "description": "Comma-separated tags/hashtags",
			},
			"images": map[string]interface{}{
				"type": "string", "description": "Comma-separated image file paths to attach",
			},
		},
		"required": []string{"action", "platform"},
	}
}

func (t *SocialPublishTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := getString(params, "action", "draft")
	platform := getString(params, "platform", "")
	title := getString(params, "title", "")
	content := getString(params, "content", "")
	tags := getString(params, "tags", "")

	if platform == "" {
		return "Error: platform is required", nil
	}

	switch action {
	case "draft":
		return t.createDraft(platform, title, content, tags)
	case "publish":
		return t.publishPost(platform, title, content, tags)
	case "schedule":
		return t.schedulePost(platform, title, content, tags)
	case "list":
		return t.listDrafts(platform)
	}
	return fmt.Sprintf("Unknown action: %s", action), nil
}

func (t *SocialPublishTool) createDraft(platform, title, content, tags string) (string, error) {
	if content == "" {
		return "Error: content is required for draft", nil
	}

	formatted := t.formatForPlatform(platform, title, content, tags)
	draftPath := filepath.Join(t.workspace, ".openkitty", "storage", "social_posts",
		fmt.Sprintf("%s_%s.md", platform, time.Now().Format("20060102_150405")))

	os.WriteFile(draftPath, []byte(formatted), 0644)

	return fmt.Sprintf("Draft created for %s:\n%s\n\nSaved to: %s\n\nTo publish:\n- Xiaohongshu: Open https://creator.xiaohongshu.com and paste content\n- Zhihu: Open https://zhuanlan.zhihu.com/write and paste content\n- Weibo: Open https://weibo.com and paste content",
		platform, truncate(formatted, 500), draftPath), nil
}

func (t *SocialPublishTool) publishPost(platform, title, content, tags string) (string, error) {
	// Use CDP browser automation if available
	formatted := t.formatForPlatform(platform, title, content, tags)

	// For now, save draft + provide CDP instructions
	draftPath := filepath.Join(t.workspace, ".openkitty", "storage", "social_posts",
		fmt.Sprintf("%s_%s_published.md", platform, time.Now().Format("20060102_150405")))
	os.WriteFile(draftPath, []byte(formatted), 0644)

	return fmt.Sprintf("Post prepared for %s. To auto-publish via browser:\n"+
		"1. Use cdp_tool to open the platform's editor URL\n"+
		"2. Fill in the content from: %s\n"+
		"3. Click the publish button\n\n"+
		"Platform URLs:\n"+
		"- Xiaohongshu: https://creator.xiaohongshu.com/publish/publish\n"+
		"- Zhihu: https://zhuanlan.zhihu.com/write\n"+
		"- Weibo: https://weibo.com\n"+
		"- LinkedIn: https://www.linkedin.com/post/new\n",
		platform, draftPath), nil
}

func (t *SocialPublishTool) schedulePost(platform, title, content, tags string) (string, error) {
	formatted := t.formatForPlatform(platform, title, content, tags)
	schedulePath := filepath.Join(t.workspace, ".openkitty", "storage", "social_posts",
		fmt.Sprintf("%s_scheduled_%s.md", platform, time.Now().Format("20060102_150405")))

	os.WriteFile(schedulePath, []byte(formatted), 0644)

	return fmt.Sprintf("Post scheduled for %s. Saved to: %s\nUse social_publish action=publish to post when ready.",
		platform, schedulePath), nil
}

func (t *SocialPublishTool) listDrafts(platform string) (string, error) {
	dir := filepath.Join(t.workspace, ".openkitty", "storage", "social_posts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "No drafts found", nil
	}

	var sb strings.Builder
	sb.WriteString("Social Media Drafts:\n")
	count := 0
	for _, e := range entries {
		name := e.Name()
		if platform != "" && !strings.HasPrefix(name, platform+"_") {
			continue
		}
		info, _ := e.Info()
		sb.WriteString(fmt.Sprintf("- %s (%d bytes, %s)\n", name, info.Size(), info.ModTime().Format("01-02 15:04")))
		count++
	}
	if count == 0 {
		return fmt.Sprintf("No drafts found for %s", platform), nil
	}
	return sb.String(), nil
}

// formatForPlatform applies platform-specific formatting rules.
func (t *SocialPublishTool) formatForPlatform(platform, title, content, tags string) string {
	var sb strings.Builder

	switch platform {
	case "xiaohongshu":
		sb.WriteString("# 小红书笔记\n\n")
		if title != "" {
			sb.WriteString(fmt.Sprintf("**标题**: %s\n\n", title))
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
		if tags != "" {
			for _, tag := range strings.Split(tags, ",") {
				sb.WriteString(fmt.Sprintf("#%s ", strings.TrimSpace(tag)))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n---\n📌 发布提示:\n")
		sb.WriteString("- 封面图建议 3:4 竖版 (1080x1440)\n")
		sb.WriteString("- 正文适当使用emoji分段, 每段2-3句\n")
		sb.WriteString("- 话题标签控制在5-10个\n")

	case "zhihu":
		sb.WriteString("# 知乎文章\n\n")
		if title != "" {
			sb.WriteString(fmt.Sprintf("# %s\n\n", title))
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
		if tags != "" {
			sb.WriteString("话题: ")
			for _, tag := range strings.Split(tags, ",") {
				sb.WriteString(fmt.Sprintf("#%s ", strings.TrimSpace(tag)))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n---\n📌 发布提示:\n")
		sb.WriteString("- 知乎支持 Markdown, 可使用代码块和公式\n")
		sb.WriteString("- 开头100字是摘要, 要抓眼球\n")
		sb.WriteString("- 选1-3个相关话题\n")

	case "weibo":
		sb.WriteString("# 微博\n\n")
		if title != "" {
			sb.WriteString(fmt.Sprintf("**%s**\n\n", title))
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
		if tags != "" {
			for _, tag := range strings.Split(tags, ",") {
				sb.WriteString(fmt.Sprintf("#%s# ", strings.TrimSpace(tag)))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n---\n📌 发布提示:\n")
		sb.WriteString("- 微博限140字, 长文可用头条文章\n")
		sb.WriteString("- 配图建议9宫格\n")

	case "douyin":
		sb.WriteString("# 抖音文案\n\n")
		sb.WriteString(content)
		sb.WriteString("\n\n")
		if tags != "" {
			for _, tag := range strings.Split(tags, ",") {
				sb.WriteString(fmt.Sprintf("#%s ", strings.TrimSpace(tag)))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n---\n📌 发布提示:\n")
		sb.WriteString("- 文案控制在50字以内\n")
		sb.WriteString("- 视频建议 9:16 (1080x1920)\n")
		sb.WriteString("- 话题标签3-5个\n")

	case "linkedin":
		sb.WriteString("# LinkedIn Post\n\n")
		if title != "" {
			sb.WriteString(fmt.Sprintf("## %s\n\n", title))
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
		if tags != "" {
			for _, tag := range strings.Split(tags, ",") {
				sb.WriteString(fmt.Sprintf("#%s ", strings.TrimSpace(tag)))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n---\n📌 Publishing Tips:\n")
		sb.WriteString("- Keep paragraphs short (1-2 sentences)\n")
		sb.WriteString("- Use line breaks for readability\n")
		sb.WriteString("- 3-5 relevant hashtags\n")

	case "twitter":
		sb.WriteString("# Tweet\n\n")
		sb.WriteString(content)
		sb.WriteString("\n\n")
		if tags != "" {
			for _, tag := range strings.Split(tags, ",") {
				sb.WriteString(fmt.Sprintf("#%s ", strings.TrimSpace(tag)))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n---\n📌 Publishing Tips:\n")
		sb.WriteString("- Max 280 characters\n")
		sb.WriteString("- Thread for longer content (1/5, 2/5...)\n")

	default:
		sb.WriteString(fmt.Sprintf("# %s Post\n\n", platform))
		if title != "" {
			sb.WriteString(fmt.Sprintf("## %s\n\n", title))
		}
		sb.WriteString(content)
	}
	return sb.String()
}
