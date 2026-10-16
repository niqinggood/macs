package auth

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// A default desktop installation must discover models on the same published
// service it uses for login. Explicit deployment overrides remain authoritative.
func TestPlatformAccountEndpointsUsePublishedServiceAndRespectOverrides(t *testing.T) {
	t.Setenv("OPENKITTY_ACCOUNT_REGISTER_URL", "")
	t.Setenv("OPENKITTY_ACCOUNT_CENTER_URL", "")
	for _, test := range []struct{ name, configured, environment, want string }{
		{"default", "", "", "https://www.sicurrent.com"},
		{"configured", "http://127.0.0.1:18080/nova/login", "", "http://127.0.0.1:18080"},
		{"environment", "https://configured.example", "https://environment.example", "https://environment.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OPENKITTY_ACCOUNT_SERVICE_URL", test.environment)
			h := NewAccountHandler(AccountConfig{ServiceURL: test.configured})
			w := httptest.NewRecorder()
			h.HandleConfig(w, httptest.NewRequest("GET", "http://localhost/api/account/config", nil))
			var reply map[string]interface{}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
				t.Fatal("invalid account configuration response")
			}
			for key, path := range map[string]string{"login_url": "/nova/login", "check_login_url": "/nova/check-login", "logout_url": "/nova/logout", "account_center_url": "/login/", "platform_model_base_url": "/api/macs/v1"} {
				if reply[key] != test.want+path {
					t.Errorf("%s = %v, want %s", key, reply[key], test.want+path)
				}
			}
		})
	}
}
