package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	memorycore "mac/internal/memory"
	"mac/internal/projectctx"
)

type personalMemorySession struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AgentName string    `json:"agent_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (g *GatewayService) personalMemorySessionQuery(r *http.Request, agent string) *gorm.DB {
	query := scopeRowsToPersistedOwner(g.db.WithContext(r.Context()).Model(&ChatSession{}), defaultExecutionTenantID, "").
		Where("agent_name = ? AND project_id = ?", agent, g.project.ProjectID).
		Where("media_owner = '' OR media_owner IS NULL OR media_owner = ?", g.personalMemoryMediaOwner(r))
	if audience := webMemoryAudienceFromContext(r.Context()); audience != nil {
		if audience.gateway != g || audience.projectID != g.project.ProjectID || !projectctx.SameWorkspace(audience.workspace, g.currentProjectWorkspace()) {
			query = query.Where("1 = 0")
		} else {
			query = query.Where("user_id = ?", audience.userID)
		}
	}
	if g.db.Migrator().HasTable(&GatewayTaskRecord{}) {
		agentOSSessions := g.db.Model(&GatewayTaskRecord{}).Select("session_id").Where("queue_name = ? AND session_id <> ''", agentOSQueueName)
		query = query.Where("id NOT IN (?)", agentOSSessions)
	}
	return query
}

func (g *GatewayService) personalMemorySessions(r *http.Request, agent string) ([]personalMemorySession, error) {
	result := make([]personalMemorySession, 0)
	if !g.db.Migrator().HasTable(&ChatSession{}) {
		return result, nil
	}
	var sessions []ChatSession
	if err := g.personalMemorySessionQuery(r, agent).Order("updated_at DESC, id ASC").Limit(200).Find(&sessions).Error; err != nil {
		return nil, err
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.Workspace) != "" && projectctx.SameWorkspace(session.Workspace, g.currentProjectWorkspace()) {
			result = append(result, personalMemorySession{ID: session.ID, Name: session.Name, AgentName: session.AgentName, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt})
		}
	}
	return result, nil
}

func (g *GatewayService) personalMemoryScope(r *http.Request, service *memorycore.Service, owner memorycore.Owner, agent string, scope memorycore.MemoryScope, sessionID string) (*memorycore.Service, memorycore.Owner, error) {
	if scope == memorycore.MemoryGlobal {
		if sessionID != "" {
			return nil, owner, errors.New("global memory does not accept a session")
		}
		return memorycore.NewGlobalService(g.db, g.currentProjectWorkspace()), owner, nil
	}
	if scope != memorycore.MemorySession {
		if sessionID != "" {
			return nil, owner, errors.New("session requires session scope")
		}
		return service, owner, nil
	}
	if strings.TrimSpace(sessionID) == "" || len(sessionID) > 64 || !g.db.Migrator().HasTable(&ChatSession{}) {
		return nil, owner, errors.New("select a valid Web conversation")
	}
	var session ChatSession
	if err := g.personalMemorySessionQuery(r, agent).Where("id = ?", sessionID).First(&session).Error; err != nil {
		return nil, owner, errors.New("conversation is unavailable in this workspace and assistant")
	}
	if strings.TrimSpace(session.Workspace) == "" || !projectctx.SameWorkspace(session.Workspace, g.currentProjectWorkspace()) {
		return nil, owner, errors.New("conversation belongs to another workspace")
	}
	owner.SessionID = session.ID
	return service, owner, nil
}
