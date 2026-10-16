package security

import (
	"fmt"
	"regexp"
	"strings"
)

// ProfessionalAgentsConfig contains host-owned configuration for isolated
// professional agents. A manifest can declare named credential bindings, but
// only the host selects which model_cfg profile is attached to each binding.
// This keeps profile names and credentials out of agent-controlled manifests.
type ProfessionalAgentsConfig struct {
	ModelBindings map[string]map[string]string `json:"model_bindings,omitempty" yaml:"model_bindings,omitempty"`
}

// ProfessionalAgentModelCredential declares one named model credential slot
// in a professional-agent manifest. Environment only supports the three model
// fields below; arbitrary host configuration cannot be requested by a
// manifest. Each field may be copied to more than one agent-scoped variable so
// one model profile can serve compatible backend adapters.
type ProfessionalAgentModelCredential struct {
	Binding     string                                      `json:"binding"`
	Environment ProfessionalAgentModelCredentialEnvironment `json:"environment"`
	Required    []string                                    `json:"required,omitempty"`
}

type ProfessionalAgentModelCredentialEnvironment struct {
	APIKey    []string `json:"api_key,omitempty"`
	BaseURL   []string `json:"base_url,omitempty"`
	ModelName []string `json:"model_name,omitempty"`
}

// ProfessionalAgentModelConfig is the minimal host-owned model view needed by
// credential binding. It deliberately excludes unrelated runtime settings.
type ProfessionalAgentModelConfig struct {
	APIKey    string
	BaseURL   string
	ModelName string
}

var professionalAgentCredentialBindingPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var professionalAgentCredentialEnvPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)
var professionalAgentIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// resolveProfessionalAgentModelCredentials resolves only explicitly selected
// model fields into explicitly declared agent-scoped environment variables.
// lookupParent is injected so the resolver is deterministic in tests. A
// variable explicitly present in the parent process wins over model_cfg.
//
// No manifest declaration or no host binding is intentionally a no-op for
// backwards compatibility with agents that use the existing environment
// allowlist mechanism.
func ResolveProfessionalAgentModelCredentials(
	agentID string,
	declarations []ProfessionalAgentModelCredential,
	config ProfessionalAgentsConfig,
	models map[string]ProfessionalAgentModelConfig,
	lookupParent func(string) (string, bool),
) (map[string]string, error) {
	resolved := make(map[string]string)
	if len(declarations) == 0 {
		return resolved, nil
	}

	agentID = strings.TrimSpace(agentID)
	if !professionalAgentIDPattern.MatchString(agentID) {
		return nil, fmt.Errorf("invalid professional agent id %q", agentID)
	}
	agentBindings := config.ModelBindings[agentID]

	seenBindings := make(map[string]struct{}, len(declarations))
	seenTargets := make(map[string]string)
	for _, declaration := range declarations {
		binding := strings.TrimSpace(declaration.Binding)
		if !professionalAgentCredentialBindingPattern.MatchString(binding) {
			return nil, fmt.Errorf("professional agent %q declares invalid model credential binding %q", agentID, binding)
		}
		if _, exists := seenBindings[binding]; exists {
			return nil, fmt.Errorf("professional agent %q declares duplicate model credential binding %q", agentID, binding)
		}
		seenBindings[binding] = struct{}{}

		fieldTargets := map[string][]string{
			"api_key":    declaration.Environment.APIKey,
			"base_url":   declaration.Environment.BaseURL,
			"model_name": declaration.Environment.ModelName,
		}
		if err := validateProfessionalAgentCredentialTargets(agentID, binding, fieldTargets, seenTargets); err != nil {
			return nil, err
		}
		required, err := validateProfessionalAgentRequiredModelFields(agentID, binding, declaration.Required, fieldTargets)
		if err != nil {
			return nil, err
		}

		profileName := strings.TrimSpace(agentBindings[binding])
		if profileName == "" {
			// A declaration alone grants no credential access. This also keeps
			// existing manifests compatible until an operator opts in.
			continue
		}
		model, exists := models[profileName]
		if !exists {
			return nil, fmt.Errorf("professional agent %q model credential binding %q references unknown model profile %q", agentID, binding, profileName)
		}
		fieldValues := map[string]string{
			"api_key":    strings.TrimSpace(model.APIKey),
			"base_url":   strings.TrimSpace(model.BaseURL),
			"model_name": strings.TrimSpace(model.ModelName),
		}

		for _, field := range []string{"api_key", "base_url", "model_name"} {
			value := fieldValues[field]
			targets := fieldTargets[field]
			satisfied := len(targets) > 0
			for _, target := range targets {
				if lookupParent != nil {
					if parentValue, present := lookupParent(target); present {
						if strings.TrimSpace(parentValue) == "" {
							satisfied = false
						} else {
							// Return the final value that must be placed in the
							// child environment. This also supports manifests that
							// do not duplicate credential targets in the legacy
							// environment allowlist.
							resolved[target] = parentValue
						}
						continue
					}
				}
				if value == "" {
					satisfied = false
					continue
				}
				resolved[target] = value
			}
			if _, isRequired := required[field]; isRequired && !satisfied {
				return nil, fmt.Errorf("professional agent %q model credential binding %q profile %q is missing required field %s", agentID, binding, profileName, field)
			}
		}
	}

	return resolved, nil
}

func validateProfessionalAgentRequiredModelFields(
	agentID, binding string,
	requiredFields []string,
	fieldTargets map[string][]string,
) (map[string]struct{}, error) {
	required := make(map[string]struct{}, len(requiredFields))
	for _, rawField := range requiredFields {
		field := strings.TrimSpace(rawField)
		switch field {
		case "api_key", "base_url", "model_name":
		default:
			return nil, fmt.Errorf("professional agent %q model credential binding %q declares unsupported required field %q", agentID, binding, field)
		}
		if _, duplicate := required[field]; duplicate {
			return nil, fmt.Errorf("professional agent %q model credential binding %q declares duplicate required field %q", agentID, binding, field)
		}
		if len(fieldTargets[field]) == 0 {
			return nil, fmt.Errorf("professional agent %q model credential binding %q requires field %s without an environment target", agentID, binding, field)
		}
		required[field] = struct{}{}
	}
	return required, nil
}

func validateProfessionalAgentCredentialTargets(
	agentID, binding string,
	fieldTargets map[string][]string,
	seenTargets map[string]string,
) error {
	scopePrefix := strings.ToUpper(strings.ReplaceAll(agentID, "-", "_")) + "_"
	fields := []string{"api_key", "base_url", "model_name"}
	for _, field := range fields {
		for _, rawTarget := range fieldTargets[field] {
			target := strings.TrimSpace(rawTarget)
			if !professionalAgentCredentialEnvPattern.MatchString(target) {
				return fmt.Errorf("professional agent %q model credential binding %q declares invalid environment target %q", agentID, binding, target)
			}
			if !strings.HasPrefix(target, scopePrefix) {
				return fmt.Errorf("professional agent %q model credential binding %q environment target %q is outside agent scope %q", agentID, binding, target, scopePrefix)
			}
			if professionalAgentCredentialEnvIsDangerous(target) {
				return fmt.Errorf("professional agent %q model credential binding %q declares forbidden environment target %q", agentID, binding, target)
			}
			if previous, duplicate := seenTargets[target]; duplicate {
				return fmt.Errorf("professional agent %q model credential environment target %q is declared more than once (%s and %s)", agentID, target, previous, field)
			}
			seenTargets[target] = field
		}
	}
	return nil
}

func professionalAgentCredentialEnvIsDangerous(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if strings.HasPrefix(upper, "PYTHON") || strings.HasPrefix(upper, "OPENKITTY_") ||
		strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "DYLD_") {
		return true
	}
	switch upper {
	case "PATH", "PATHEXT", "COMSPEC", "SYSTEMROOT", "WINDIR", "HOME", "USERPROFILE",
		"TMP", "TEMP", "SHELL", "NODE_OPTIONS", "GODEBUG", "GOTRACEBACK", "GOMAXPROCS":
		return true
	default:
		return false
	}
}
