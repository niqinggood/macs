package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	gatewayhttp "mac/internal/gateway/httpx"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUILanguagePreferencePersistsInDatabase(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&gatewayhttp.UISetting{}); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	gatewayhttp.HandleUILanguage(get, httptest.NewRequest("GET", "/api/ui/language", nil), database)
	var initial map[string]interface{}
	if err := json.Unmarshal(get.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial["language"] != "zh" || initial["english_enabled"] != false {
		t.Fatalf("unexpected default language response: %s", get.Body.String())
	}
	var settingCount int64
	if err := database.Model(&gatewayhttp.UISetting{}).Where("id = ?", gatewayhttp.PrimaryUISettingID).Count(&settingCount).Error; err != nil {
		t.Fatal(err)
	}
	if settingCount != 1 {
		t.Fatalf("default language flag must be persisted on first read, rows=%d", settingCount)
	}

	set := httptest.NewRecorder()
	gatewayhttp.HandleUILanguage(set, httptest.NewRequest("POST", "/api/ui/language", strings.NewReader(`{"language":"en"}`)), database)
	if set.Code != 200 {
		t.Fatalf("save language status=%d body=%s", set.Code, set.Body.String())
	}

	afterRestart := httptest.NewRecorder()
	gatewayhttp.HandleUILanguage(afterRestart, httptest.NewRequest("GET", "/api/ui/language", nil), database)
	var persisted map[string]interface{}
	if err := json.Unmarshal(afterRestart.Body.Bytes(), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["language"] != "en" || persisted["english_enabled"] != true {
		t.Fatalf("database language flag did not survive gateway recreation: %s", afterRestart.Body.String())
	}
}

func TestWebLanguageToggleAssetsAreWired(t *testing.T) {
	html, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	style, err := os.ReadFile("web/style.css")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("web/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"id=\"languageToggle\"", "/web/i18n.js", "data-i18n=\"newChat\"", "data-i18n-placeholder=\"messageSingle\"", "data-i18n=\"settingsTitle\">系统设置", "data-i18n=\"toolsConfigTab\">工具配置", "data-i18n=\"gatewayConfigTab\">网关配置"} {
		if !strings.Contains(string(html), marker) {
			t.Fatalf("web language UI missing %q", marker)
		}
	}
	menuIndex := strings.Index(string(html), `id="toolMenu"`)
	languageIndex := strings.Index(string(html), `id="languageToggle"`)
	themeIndex := strings.Index(string(html), `id="themeToggle"`)
	shutdownIndex := strings.Index(string(html), `onclick="shutdownServer()"`)
	if menuIndex < 0 || languageIndex < menuIndex || themeIndex < menuIndex || shutdownIndex < languageIndex || shutdownIndex < themeIndex {
		t.Fatal("language and theme switches must stay inside the workspace menu")
	}
	for _, marker := range []string{".workbench-utility-row", ".workbench-utility-btn", "height: 26px", "font-size: 11px"} {
		if !strings.Contains(string(style), marker) {
			t.Fatalf("compact workspace switch styling missing %q", marker)
		}
	}
	if strings.Contains(string(style), "@keyframes settingsPulse") {
		t.Fatal("secondary settings button must not keep the attention-grabbing pulse animation")
	}
	for _, marker := range []string{"/api/ui/language", "toggleLanguage", "openKittyT", "english_enabled"} {
		if !strings.Contains(string(script), marker) {
			t.Fatalf("language script missing %q", marker)
		}
	}
}
