package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
	"mac/internal/security"
)

// Run the actual model loop, durable event save, correction/profile path, and
// the next fresh agent's recall. Only the HTTP provider and database are fake.
func TestGenerateMemoryEventUsesSameIdentityForSaveProfileAndRecall(t *testing.T) {
	for _, test := range []struct {
		name, userID, userName, expectedName string
		tenant                               bool
	}{
		{name: "web_user_id_fallback", userID: "fixture-web-user", expectedName: "fixture-web-user"},
		{name: "explicit_name_has_priority", userID: "fixture-web-user", userName: "Fixture Display Name", expectedName: "Fixture Display Name"},
		{name: "sealed_tenant_partition_unchanged", userID: "fixture-web-user", expectedName: "fixture-web-user", tenant: true},
		{name: "missing_identity_stays_empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			ctx := context.WithValue(context.Background(), "session_id", "fixture-session")
			ctx = context.WithValue(ctx, "user_id", test.userID)
			ctx = context.WithValue(ctx, "user_name", test.userName)
			ctx = context.WithValue(ctx, "channel", "web")
			// Unsealed browser-style strings must not change the memory owner.
			ctx = context.WithValue(ctx, "tenant_id", "untrusted-tenant")
			expectedTenant := defaultExecutionTenantID
			if test.tenant {
				partition := security.PersonalPartitionID("fixture-tenant", "sealed-subject", defaultExecutionTenantID)
				workspace = projectctx.PersonalWorkspace(workspace, partition)
				if err := os.MkdirAll(workspace, 0700); err != nil {
					t.Fatal(err)
				}
				ctx = withExecutionIdentity(ctx, ExecutionIdentity{
					TenantID: "fixture-tenant", SubjectID: "sealed-subject", UserID: "sealed-user",
					TenantScoped: true, SubjectScoped: true,
				})
				expectedTenant = "usr_" + partition
			}
			ctx = projectctx.WithResolvedWorkspace(ctx, workspace)
			database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "fixture.db")), &gorm.Config{
				Logger: gormlogger.Default.LogMode(gormlogger.Silent),
			})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := database.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			if err := memorycore.InitMemoryDB(database); err != nil {
				t.Fatal(err)
			}
			store := memorycore.NewMemoryStore(filepath.Join(workspace, "fixture-memory"), database, "identity-fixture", 10)
			const answer = "已确认 legacy_tool 偏好。identity-recall-proof"
			var mu sync.Mutex
			var calls []string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Messages []map[string]interface{} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				serialized, _ := json.Marshal(payload.Messages)
				mu.Lock()
				calls = append(calls, string(serialized))
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"choices": []interface{}{map[string]interface{}{
						"message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop",
					}},
				})
			}))
			defer provider.Close()
			newAgent := func() *LLMAgent {
				config := &Config{Workspace: workspace}
				config.SkillsConfig.DisEnabled = true
				// Attach only the temporary stores below, never the process DB.
				config.MemoryConfig.Enabled = false
				agent := NewLLMAgent(AgentConfig{
					Name: "identity-fixture", Model: "fixture", SystemMessage: "Answer briefly.", MaxTokens: 256, MaxIterations: 2,
				}, &ModelConfig{Type: "openai", ModelName: "fixture-model", APIKey: "fixture-key", BaseURL: provider.URL + "/v1"}, nil, config, log.New(io.Discard, "", 0))
				agent.httpClient = provider.Client()
				agent.hasMemory = true
				agent.memory = store
				agent.memoryService = memorycore.NewService(database, workspace, agent.name)
				if err := agent.memoryService.SetContextStore(agent.scm); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(agent.ptcServer.Stop)
				return agent
			}
			first := newAgent()
			input := "以后不要用 legacy_tool 。确认本条长期偏好。"
			if got, err := first.Generate(ctx, input); err != nil || !strings.Contains(got, answer) {
				t.Fatalf("real Generate did not complete: %q %v", got, err)
			}
			var event memorycore.MemoryEvent
			if err := database.Where("session_id = ?", "fixture-session").First(&event).Error; err != nil {
				t.Fatal(err)
			}
			if event.UserName != test.expectedName || event.UserID != test.userID || event.TenantID != expectedTenant || event.Status != "success" {
				t.Fatalf("saved event identity/status: name=%q user=%q tenant=%q status=%q", event.UserName, event.UserID, event.TenantID, event.Status)
			}
			waitProfile := func(interactions int) memorycore.UserProfile {
				t.Helper()
				deadline := time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					var profile memorycore.UserProfile
					err := database.Where("tenant_id = ? AND user_name = ?", expectedTenant, test.expectedName).First(&profile).Error
					if err == nil && profile.TotalInteractions >= interactions {
						return profile
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("event save did not update the matching profile")
				return memorycore.UserProfile{}
			}
			profile := waitProfile(1)
			if test.expectedName != "" && !strings.Contains(string(profile.Constraints), `"legacy_tool"`) {
				t.Fatal("durable correction and event profile were saved under different names")
			}
			if test.expectedName == "" && strings.Contains(string(profile.Constraints), "legacy_tool") {
				t.Fatal("an absent identity was invented to save a correction")
			}
			var foreignProfiles int64
			if err := database.Model(&memorycore.UserProfile{}).Where("tenant_id <> ? OR user_name <> ?", expectedTenant, test.expectedName).Count(&foreignProfiles).Error; err != nil || foreignProfiles != 0 {
				t.Fatalf("save created another identity or tenant profile: %d %v", foreignProfiles, err)
			}
			// A fresh instance cannot satisfy this from in-memory chat history.
			second := newAgent()
			if _, err := second.Generate(ctx, "legacy_tool 偏好和上轮回答是什么？"); err != nil {
				t.Fatal(err)
			}
			waitProfile(2)
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != 2 || !strings.Contains(calls[1], "identity-recall-proof") {
				t.Fatalf("fresh Generate did not recall the persisted event: calls=%d", len(calls))
			}
			if test.expectedName != "" && !strings.Contains(calls[1], "避免使用: legacy_tool") {
				t.Fatal("fresh Generate did not recall the correction/profile for the same name")
			}
			if recalled := store.GetMemoryContextForTenant("foreign-tenant", test.expectedName, "fixture-session", "legacy_tool"); strings.Contains(recalled, "identity-recall-proof") {
				t.Fatal("event became visible to another tenant")
			}
			if test.expectedName != "" {
				if recalled := store.GetMemoryContextForTenant(expectedTenant, "different-user", "fixture-session", "legacy_tool"); strings.Contains(recalled, "identity-recall-proof") {
					t.Fatal("event became visible to another username")
				}
			}
		})
	}
}
