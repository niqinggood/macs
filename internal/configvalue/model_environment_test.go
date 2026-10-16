package configvalue

import (
	"strings"
	"testing"
)

func TestParseModelAPIKeyEnvironmentReference(t *testing.T) {
	for _, raw := range []string{"${OPENKITTY_KEY}", "env:OPENKITTY_KEY"} {
		name, present, err := ParseModelAPIKeyEnvironmentReference(raw)
		if err != nil || !present || name != "OPENKITTY_KEY" {
			t.Fatalf("ParseModelAPIKeyEnvironmentReference(%q) = %q, %v, %v", raw, name, present, err)
		}
	}
	if _, present, err := ParseModelAPIKeyEnvironmentReference("plain-key"); err != nil || present {
		t.Fatalf("plaintext key was treated as an environment reference: present=%v err=%v", present, err)
	}
	if _, _, err := ParseModelAPIKeyEnvironmentReference("${INVALID-NAME}"); err == nil {
		t.Fatal("invalid environment name was accepted")
	}
}

func TestModelAPIKeySerializableDoesNotLeakResolvedSecret(t *testing.T) {
	got := (ModelAPIKey{APIKey: "runtime-secret", APIKeyEnv: " OPENKITTY_KEY ", Resolved: true}).Serializable()
	if got.APIKey != "${OPENKITTY_KEY}" || got.APIKeyEnv != "OPENKITTY_KEY" || got.Resolved {
		t.Fatalf("Serializable() = %#v", got)
	}
	got = (ModelAPIKey{APIKey: "runtime-secret", APIKeyEnv: "   "}).Serializable()
	if got.APIKey != "" || got.APIKeyEnv != "" {
		t.Fatalf("whitespace environment source leaked the key: %#v", got)
	}
}

func TestResolveModelAPIKey(t *testing.T) {
	resolved, err := ResolveModelAPIKey("primary", ModelAPIKey{APIKey: "env:OPENKITTY_KEY"}, func(name string) (string, bool) {
		return " runtime-secret ", name == "OPENKITTY_KEY"
	})
	if err != nil {
		t.Fatalf("ResolveModelAPIKey: %v", err)
	}
	if resolved.APIKey != "runtime-secret" || resolved.APIKeyEnv != "OPENKITTY_KEY" || !resolved.Resolved {
		t.Fatalf("resolved = %#v", resolved)
	}

	_, err = ResolveModelAPIKey("primary", ModelAPIKey{APIKeyEnv: "MISSING"}, func(string) (string, bool) {
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "MISSING") || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("missing environment error = %v", err)
	}
}

func TestMergeModelAPIKeyFieldsClearsStaleState(t *testing.T) {
	model := ModelAPIKey{APIKey: "resolved-secret", APIKeyEnv: "OLD_KEY", Resolved: true}
	model = MergeModelAPIKeyFields(model, map[string]interface{}{"api_key": "new-plaintext"})
	if model.APIKey != "new-plaintext" || model.APIKeyEnv != "" || model.Resolved {
		t.Fatalf("plaintext merge = %#v", model)
	}

	model = MergeModelAPIKeyFields(model, map[string]interface{}{"api_key_env": "NEW_KEY"})
	if model.APIKey != "new-plaintext" || model.APIKeyEnv != "NEW_KEY" || model.Resolved {
		t.Fatalf("environment merge = %#v", model)
	}
}
