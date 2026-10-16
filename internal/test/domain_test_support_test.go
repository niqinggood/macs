package app

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	agentdomain "mac/internal/agents"
	"mac/internal/aiemployee"
	"mac/internal/channelhost"
	gatewaymigrations "mac/internal/gateway/migrations"
	middlewarecore "mac/internal/middleware"
	sessioncore "mac/internal/session"
	skilldomain "mac/internal/skills"
	tenantcore "mac/internal/tenant"
	"mac/internal/textutil"
	browsertools "mac/internal/tools/browser"

	"gorm.io/gorm"
)

type DiscoveredAgent = agentdomain.DiscoveredAgent
type SkillsLoader = skilldomain.SkillsLoader
type SkillSnapshot = skilldomain.SkillSnapshot
type RemoteSkillRegistry = skilldomain.RemoteSkillRegistry
type TenantConfig = tenantcore.TenantConfig
type DenialTrackerMiddleware = middlewarecore.DenialTrackerMiddleware
type WebPageTool = browsertools.WebPageTool
type WebTargetsTool = browsertools.WebTargetsTool
type WebCloseTool = browsertools.WebCloseTool
type WebNavigateTool = browsertools.WebNavigateTool

const toolReceiptUniqueIndexName = gatewaymigrations.ToolReceiptUniqueIndexName

var (
	NewSessionTranscript                = sessioncore.NewSessionTranscript
	RecoverSessionTranscriptFromJournal = sessioncore.RecoverSessionTranscriptFromJournal
	GetAETemplates                      = aiemployee.Templates
	isWithinWorkingHours                = aiemployee.IsWithinWorkingHours
	employeeMentionAliases              = aiemployee.MentionAliases
	promptSafeSkillDescription          = skilldomain.PromptSafeDescription
)

type registryNameMockTool struct{ name string }

func (m registryNameMockTool) Name() string        { return m.name }
func (m registryNameMockTool) Description() string { return "mock registry tool" }
func (m registryNameMockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (m registryNameMockTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "ok", nil
}

func mustWebPageAction(t *testing.T, ctx context.Context, params map[string]interface{}) string {
	t.Helper()
	started := time.Now()
	out, err := (&browsertools.WebPageTool{}).Execute(ctx, params)
	if err != nil {
		t.Fatalf("web_page action %q failed: %v", params["action"], err)
	}
	t.Logf("web_page %-8s completed in %s: %s", params["action"], time.Since(started).Round(time.Millisecond), textutil.TruncateRunes(strings.ReplaceAll(out, "\n", " | "), 240))
	return out
}

func webArenaElementIndex(t *testing.T, extracted, label string) int {
	t.Helper()
	linePattern := regexp.MustCompile(`(?m)^\s*\[(\d+)\].*` + regexp.QuoteMeta(label) + `.*$`)
	match := linePattern.FindStringSubmatch(extracted)
	if len(match) != 2 {
		t.Fatalf("could not find interactive element %q in:\n%s", label, extracted)
	}
	index, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func parseSmokeTargetID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Opened tab ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Opened tab "))
		}
	}
	return ""
}

type sqliteIndexOwner struct {
	TableName string `gorm:"column:tbl_name"`
}

type sqliteTableColumn struct {
	CID        int    `gorm:"column:cid"`
	Name       string `gorm:"column:name"`
	Type       string `gorm:"column:type"`
	PrimaryKey int    `gorm:"column:pk"`
}

func NewSkillsLoader(workspace, agentName, builtinSkills string, options ...skilldomain.LoaderOption) *skilldomain.SkillsLoader {
	return skilldomain.NewSkillsLoader(workspace, agentName, builtinSkills, options...)
}

func NewTenantManager(baseDir string, logger *log.Logger) *tenantcore.TenantManager {
	return tenantcore.NewTenantManager(baseDir, logger)
}

func NewChannelManager(gateway *GatewayService) *channelhost.Manager {
	return channelhost.NewManager(newChannelRuntimeAdapter(gateway))
}

func prepareGatewaySQLiteMigrations(db *gorm.DB) error {
	return gatewaymigrations.PrepareSQLite(db, (ToolExecutionReceipt{}).TableName(), DENotificationDB{})
}
