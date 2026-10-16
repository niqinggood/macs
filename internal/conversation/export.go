package conversation

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
)

// ExportGenerationHTML renders only the saved result and its billing snapshot.
// Asset loading and account authorization remain the host's responsibility.
func ExportGenerationHTML(generation *Generation) string {
	if generation == nil {
		return ""
	}
	task := generation.Task
	var output strings.Builder
	if task.Status == "completed" && generation.Capability == "image" && exportImageSource(task.ResultURL) {
		fmt.Fprintf(&output, `<figure><img src="%s" alt="生成的图片" style="display:block;max-width:100%%;height:auto"></figure>`, html.EscapeString(task.ResultURL))
	}
	if task.Status == "completed" && (generation.Capability == "music" || generation.Capability == "video") && exportMediaSource(task.ResultURL) {
		title := task.Title
		if title == "" {
			title = generation.ModelName
		}
		kind, player := "音乐", "audio"
		if generation.Capability == "video" {
			kind, player = "视频", "video"
		}
		fmt.Fprintf(&output, `<section class="generation-media" style="padding:18px;border:1px solid #e1e3ef;border-radius:16px;background:#f6f5ff"><strong>%s · %s</strong><p>%s</p><%s controls preload="none" src="%s"></%s><p><a href="%s">播放 / 下载%s</a></p></section>`, kind, html.EscapeString(title), html.EscapeString(generation.ModelName), player, html.EscapeString(task.ResultURL), player, html.EscapeString(task.ResultURL), kind)
	}
	if task.Lyrics != "" {
		fmt.Fprintf(&output, `<details open><summary>歌词</summary><pre style="white-space:pre-wrap">%s</pre></details>`, html.EscapeString(task.Lyrics))
	}
	statuses := map[string]string{"free": "免费", "reserved": "费用已预留", "charged": "已扣费", "settled": "已扣费", "refunded": "已退款", "released": "预留已释放"}
	status := statuses[task.PaymentStatus]
	if status == "" {
		status = "待核对"
	}
	fmt.Fprintf(&output, `<p class="generation-billing">扣费状态：%s`, status)
	if task.Cost != nil {
		fmt.Fprintf(&output, " · %g 积分", *task.Cost)
	} else if task.PaymentStatus == "free" {
		output.WriteString(" · 0 积分")
	}
	output.WriteString("</p>")
	return output.String()
}

func exportMediaSource(source string) bool {
	if target, err := url.Parse(source); err == nil && target.User == nil && target.Host != "" && (target.Scheme == "https" || target.Scheme == "http") {
		return true
	}
	const musicPrefix = "/api/platform/media/music/"
	if !strings.HasPrefix(source, musicPrefix) || !strings.HasSuffix(source, "/audio") {
		return false
	}
	id, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(source, musicPrefix), "/audio"), 10, 64)
	return err == nil && id > 0
}

func exportImageSource(source string) bool {
	for _, prefix := range []string{"data:image/png;base64,", "data:image/jpeg;base64,", "data:image/webp;base64,", "data:image/gif;base64,"} {
		if strings.HasPrefix(source, prefix) {
			return len(source) > len(prefix)
		}
	}
	target, err := url.Parse(source)
	return err == nil && target.User == nil && target.Host != "" && (target.Scheme == "https" || target.Scheme == "http")
}
