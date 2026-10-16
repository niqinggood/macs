package configvalue

import "testing"

func TestOptionalModelKeyDoesNotSkipEndpointOrModelValidation(t *testing.T) {
	base := Model{APIKeyOptional: true, BaseURL: "http://localhost:11434/v1", ModelName: "local-model"}
	if err := ValidateModel("local", &base); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "local-token", "ollama"} {
		model := base
		model.APIKey = key
		if err := ValidateModel("local", &model); err != nil {
			t.Errorf("local credential rejected: %v", err)
		}
	}
	for _, field := range []string{"base_url", "model_name"} {
		model := base
		if field == "base_url" {
			model.BaseURL = ""
		} else {
			model.ModelName = ""
		}
		if err := ValidateModel("local", &model); err == nil {
			t.Errorf("missing %s accepted because the key was optional", field)
		}
	}
	base.APIKeyOptional = false
	if err := ValidateModel("cloud", &base); err == nil {
		t.Fatal("required key validation was lost")
	}
}
