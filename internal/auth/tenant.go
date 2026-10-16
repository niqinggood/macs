package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// TenantUser 租户用户
type TenantUser struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	TenantID     string    `gorm:"index;uniqueIndex:idx_tenant_users_identity;size:64;not null" json:"tenant_id"`
	Username     string    `gorm:"index;uniqueIndex:idx_tenant_users_identity;size:100;not null" json:"username"`
	PasswordHash string    `gorm:"size:128;not null" json:"-"`
	DisplayName  string    `gorm:"size:200" json:"display_name"`
	Role         string    `gorm:"size:20;default:'user'" json:"role"` // admin, user
	CreatedAt    time.Time `json:"created_at"`
}

// tenantSession 租户用户会话
type TenantSession struct {
	Token     string
	TenantID  string
	SubjectID string
	Username  string
	Expires   time.Time
}

var (
	tenantSessions   = map[string]*TenantSession{}
	tenantSessionsMu sync.RWMutex
)

func hashPassword(pw string) string {
	h := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(h[:])
}

func InitTenantUsers(db *gorm.DB) {
	db.AutoMigrate(&TenantUser{})
	// 为每个租户创建默认用户（如果不存在）
	tenantDirs, _ := os.ReadDir(filepath.Join(".openkitty", "tenants"))
	for _, d := range tenantDirs {
		if !d.IsDir() {
			continue
		}
		tid := d.Name()
		var count int64
		db.Model(&TenantUser{}).Where("tenant_id = ?", tid).Count(&count)
		if count == 0 {
			db.Create(&TenantUser{
				TenantID:     tid,
				Username:     "admin",
				PasswordHash: hashPassword("admin"),
				DisplayName:  "管理员",
				Role:         "admin",
			})
			db.Create(&TenantUser{
				TenantID:     tid,
				Username:     "user",
				PasswordHash: hashPassword("user"),
				DisplayName:  "普通用户",
				Role:         "user",
			})
		}
	}
}

// ===== API Handlers =====

type TenantHandler struct {
	db         *gorm.DB
	isLicensed func(string) bool
}

func NewTenantHandler(db *gorm.DB, isLicensed func(string) bool) *TenantHandler {
	return &TenantHandler{db: db, isLicensed: isLicensed}
}

func (h *TenantHandler) licensed(tenantID string) bool {
	return h.isLicensed == nil || h.isLicensed(tenantID)
}

func (h *TenantHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var body struct {
		TenantID string `json:"tenant_id"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	var user TenantUser
	err := h.db.Where("tenant_id = ? AND username = ?", body.TenantID, body.Username).First(&user).Error
	if err != nil || user.PasswordHash != hashPassword(body.Password) {
		http.Error(w, `{"ok":false,"error":"租户、用户名或密码错误"}`, 401)
		return
	}

	token := fmt.Sprintf("tks_%s_%d", user.TenantID, time.Now().UnixNano())
	tenantSessionsMu.Lock()
	tenantSessions[token] = &TenantSession{
		Token: token, TenantID: user.TenantID,
		SubjectID: fmt.Sprintf("user:%d", user.ID),
		Username:  user.Username, Expires: time.Now().Add(24 * time.Hour),
	}
	tenantSessionsMu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "token": token,
		"tenant_id":    user.TenantID,
		"user_id":      fmt.Sprintf("user:%d", user.ID),
		"username":     user.Username,
		"display_name": user.DisplayName,
		"role":         user.Role,
	})
}

func (h *TenantHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Auth-Token")
	tenantSessionsMu.Lock()
	delete(tenantSessions, token)
	tenantSessionsMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (h *TenantHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Auth-Token")
	sess := GetTenantSession(token)
	if sess == nil {
		http.Error(w, `{"ok":false}`, 401)
		return
	}
	var user TenantUser
	h.db.Where("tenant_id = ? AND username = ?", sess.TenantID, sess.Username).First(&user)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true, "tenant_id": sess.TenantID, "username": sess.Username,
		"user_id": sess.SubjectID, "display_name": user.DisplayName, "role": user.Role,
	})
}

func (h *TenantHandler) HandleUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	token := r.Header.Get("X-Auth-Token")
	sess := GetTenantSession(token)
	if sess == nil {
		http.Error(w, `{"ok":false}`, 401)
		return
	}

	if r.Method == "POST" {
		// 商用授权检查
		if !h.licensed(sess.TenantID) {
			http.Error(w, `{"error":"免费版不支持创建用户。请联系获取商用授权文件。"}`, 402)
			return
		}
		var body struct {
			Username    string `json:"username"`
			Password    string `json:"password"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Username == "" || body.Password == "" {
			http.Error(w, `{"error":"username and password required"}`, 400)
			return
		}
		var existing TenantUser
		if h.db.Where("tenant_id=? AND username=?", sess.TenantID, body.Username).First(&existing).Error == nil {
			http.Error(w, `{"error":"用户名已存在"}`, 400)
			return
		}
		if body.Role == "" {
			body.Role = "user"
		}
		h.db.Create(&TenantUser{
			TenantID:     sess.TenantID,
			Username:     body.Username,
			PasswordHash: hashPassword(body.Password),
			DisplayName:  body.DisplayName,
			Role:         body.Role,
		})
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		return
	}

	var users []TenantUser
	h.db.Where("tenant_id = ?", sess.TenantID).Find(&users)
	json.NewEncoder(w).Encode(map[string]interface{}{"users": users})
}

func GetTenantSession(token string) *TenantSession {
	tenantSessionsMu.RLock()
	defer tenantSessionsMu.RUnlock()
	sess, ok := tenantSessions[token]
	if !ok || time.Now().After(sess.Expires) {
		return nil
	}
	return sess
}

// tenantSessionMiddleware extracts session from token and injects into context
func TenantSessionMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract tenant_id from session and set as X-Tenant-ID header
		if token := r.Header.Get("X-Auth-Token"); token != "" {
			sess := GetTenantSession(token)
			if sess != nil {
				r.Header.Set("X-Tenant-ID", sess.TenantID)
				r.Header.Set("X-Username", sess.Username)
			}
		}
		next(w, r)
	}
}

// listTenants returns available tenant IDs from the tenants/ directory
func ListTenants() []string {
	var tenants []string
	entries, _ := os.ReadDir(filepath.Join(".openkitty", "tenants"))
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			tenants = append(tenants, e.Name())
		}
	}
	if len(tenants) == 0 {
		tenants = []string{"default"}
	}
	return tenants
}

func (h *TenantHandler) HandlePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	token := r.Header.Get("X-Auth-Token")
	sess := GetTenantSession(token)
	if sess == nil {
		http.Error(w, `{"ok":false}`, 401)
		return
	}
	if !h.licensed(sess.TenantID) {
		http.Error(w, `{"error":"免费版不支持修改密码。请联系获取商用授权文件。"}`, 402)
		return
	}
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.NewPassword == "" {
		http.Error(w, `{"error":"new_password required"}`, 400)
		return
	}
	var user TenantUser
	if h.db.Where("tenant_id=? AND username=?", sess.TenantID, sess.Username).First(&user).Error != nil {
		http.Error(w, `{"error":"user not found"}`, 404)
		return
	}
	if user.PasswordHash != hashPassword(body.OldPassword) {
		http.Error(w, `{"error":"原密码错误"}`, 403)
		return
	}
	h.db.Model(&user).Update("password_hash", hashPassword(body.NewPassword))
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}
