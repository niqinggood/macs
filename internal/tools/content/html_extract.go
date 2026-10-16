package content

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// ExtractHTML returns visible text while excluding document metadata and
// executable/style content.
func ExtractHTML(data []byte) (string, error) {
	document, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("html parse: %w", err)
	}
	var output strings.Builder
	skipTags := map[string]bool{"script": true, "style": true, "noscript": true, "head": true}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && skipTags[node.Data] {
			return
		}
		if node.Type == html.TextNode {
			if text := strings.TrimSpace(node.Data); text != "" {
				output.WriteString(text + "\n")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return strings.TrimSpace(output.String()), nil
}
