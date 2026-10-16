package httpx

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

const PrimaryUISettingID uint = 1

// UISetting is intentionally a single-row persisted flag. The boolean keeps
// the language signal simple while the API exposes stable zh/en values.
type UISetting struct {
	ID             uint      `gorm:"primaryKey" json:"-"`
	EnglishEnabled bool      `gorm:"not null;default:false" json:"english_enabled"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func UILanguageResponse(setting UISetting) map[string]interface{} {
	language := "zh"
	if setting.EnglishEnabled {
		language = "en"
	}
	return map[string]interface{}{
		"ok":              true,
		"language":        language,
		"english_enabled": setting.EnglishEnabled,
		"updated_at":      setting.UpdatedAt,
	}
}

func LoadUISetting(database *gorm.DB) (UISetting, error) {
	setting := UISetting{ID: PrimaryUISettingID}
	err := database.Where("id = ?", PrimaryUISettingID).FirstOrCreate(&setting).Error
	return setting, err
}

func SaveUISetting(database *gorm.DB, englishEnabled bool) (UISetting, error) {
	setting := UISetting{ID: PrimaryUISettingID, EnglishEnabled: englishEnabled}
	if err := database.Save(&setting).Error; err != nil {
		return UISetting{}, err
	}
	return setting, nil
}

func HandleUILanguage(w http.ResponseWriter, r *http.Request, database *gorm.DB) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if database == nil {
		http.Error(w, `{"ok":false,"error":"database unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		setting, err := LoadUISetting(database)
		if err != nil {
			http.Error(w, `{"ok":false,"error":"failed to load language preference"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(UILanguageResponse(setting))

	case http.MethodPost:
		var request struct {
			Language       string `json:"language"`
			EnglishEnabled *bool  `json:"english_enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
			http.Error(w, `{"ok":false,"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}

		var enabled bool
		switch strings.ToLower(strings.TrimSpace(request.Language)) {
		case "en":
			enabled = true
		case "zh", "zh-cn":
			enabled = false
		case "":
			if request.EnglishEnabled == nil {
				http.Error(w, `{"ok":false,"error":"language must be zh or en"}`, http.StatusBadRequest)
				return
			}
			enabled = *request.EnglishEnabled
		default:
			http.Error(w, `{"ok":false,"error":"language must be zh or en"}`, http.StatusBadRequest)
			return
		}

		setting, err := SaveUISetting(database, enabled)
		if err != nil {
			http.Error(w, `{"ok":false,"error":"failed to save language preference"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(UILanguageResponse(setting))

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, `{"ok":false,"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
