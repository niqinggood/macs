package app

import (
	"os"
	"strings"
	"testing"
)

func TestPrivateNetworkProtectionSettingIsWiredInWebUI(t *testing.T) {
	indexData, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read web/index.html: %v", err)
	}
	appData, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web/app.js: %v", err)
	}

	index := string(indexData)
	app := string(appData)
	for _, want := range []string{
		`id="privateNetworkProtection"`,
		"默认关闭，允许 web_fetch 访问内网和本机 URL",
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("settings UI missing %q", want)
		}
	}
	for _, want := range []string{
		"currentConfig.tools_config.private_network_protection",
		"private_network_protection: document.getElementById('privateNetworkProtection')?.checked || false",
	} {
		if !strings.Contains(app, want) {
			t.Fatalf("settings JavaScript missing %q", want)
		}
	}
}
