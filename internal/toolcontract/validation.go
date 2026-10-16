package toolcontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

type missingRequiredToolParameterError struct {
	Parameter string
}

func (e *missingRequiredToolParameterError) Error() string {
	return fmt.Sprintf("missing required parameter %q", e.Parameter)
}

func ValidateArguments(schema map[string]interface{}, args map[string]interface{}) error {
	if schema == nil {
		return nil
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	return validateToolObject("", schema, args)
}

func validateToolObject(prefix string, schema map[string]interface{}, args map[string]interface{}) error {
	for _, name := range schemaStringList(schema["required"]) {
		if _, ok := args[name]; !ok {
			return &missingRequiredToolParameterError{Parameter: joinToolParamPath(prefix, name)}
		}
	}

	props, _ := schema["properties"].(map[string]interface{})
	for name, value := range args {
		rawProp, ok := props[name]
		if !ok {
			if allowsAdditionalProperties(schema) {
				continue
			}
			return fmt.Errorf("unknown parameter %q; allowed parameters: [%s]", joinToolParamPath(prefix, name), strings.Join(sortedSchemaPropertyNames(props), ", "))
		}
		prop, ok := rawProp.(map[string]interface{})
		if !ok {
			continue
		}
		if err := validateToolArgSchema(joinToolParamPath(prefix, name), value, prop); err != nil {
			return err
		}
	}
	return nil
}

func BuildSchemaRepairHint(toolName string, schema map[string]interface{}, args map[string]interface{}, validationErr error) string {
	var missing *missingRequiredToolParameterError
	if !errors.As(validationErr, &missing) {
		return ""
	}

	required := requiredToolParameterSpecs(schema)
	received := sortedToolArgumentNames(args)
	if len(received) == 0 {
		received = []string{"(none)"}
	}

	var b strings.Builder
	b.WriteString("[TOOL SCHEMA REPAIR]\n")
	b.WriteString("Tool: ")
	b.WriteString(strings.TrimSpace(toolName))
	b.WriteString("\nMissing: ")
	b.WriteString(missing.Parameter)
	if len(required) > 0 {
		b.WriteString("\nRequired top-level parameters: ")
		b.WriteString(strings.Join(required, ", "))
	}
	b.WriteString("\nReceived parameters: ")
	b.WriteString(strings.Join(received, ", "))
	b.WriteString("\nRetry once with one complete call that follows the advertised schema. Emit only the corrected tool call; do not repeat this incomplete payload and do not invent missing values.")
	return b.String()
}

func requiredToolParameterSpecs(schema map[string]interface{}) []string {
	props, _ := schema["properties"].(map[string]interface{})
	names := schemaStringList(schema["required"])
	result := make([]string, 0, len(names))
	for _, name := range names {
		typ := "value"
		if prop, ok := props[name].(map[string]interface{}); ok {
			if declared, ok := prop["type"].(string); ok && strings.TrimSpace(declared) != "" {
				typ = strings.TrimSpace(declared)
			}
		}
		result = append(result, name+":"+typ)
	}
	return result
}

func sortedToolArgumentNames(args map[string]interface{}) []string {
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateToolArgSchema(name string, value interface{}, prop map[string]interface{}) error {
	if err := validateToolArgType(name, value, prop); err != nil {
		return err
	}
	if err := validateToolArgEnum(name, value, prop["enum"]); err != nil {
		return err
	}

	typ, _ := prop["type"].(string)
	switch typ {
	case "object":
		obj, ok := value.(map[string]interface{})
		if !ok || value == nil {
			return nil
		}
		if _, hasProps := prop["properties"]; hasProps || len(schemaStringList(prop["required"])) > 0 {
			if err := validateToolObject(name, prop, obj); err != nil {
				return err
			}
		}
	case "array":
		itemsSchema, _ := prop["items"].(map[string]interface{})
		if len(itemsSchema) == 0 {
			return nil
		}
		arr, ok := value.([]interface{})
		if !ok {
			return nil
		}
		for i, item := range arr {
			if err := validateToolArgSchema(fmt.Sprintf("%s[%d]", name, i), item, itemsSchema); err != nil {
				return err
			}
		}
	}
	return nil
}

func joinToolParamPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func allowsAdditionalProperties(schema map[string]interface{}) bool {
	allowed, ok := schema["additionalProperties"].(bool)
	return ok && allowed
}

func sortedSchemaPropertyNames(props map[string]interface{}) []string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateToolArgType(name string, value interface{}, prop map[string]interface{}) error {
	typ, _ := prop["type"].(string)
	if typ == "" || value == nil {
		return nil
	}
	switch typ {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("parameter %q must be string, got %T", name, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("parameter %q must be boolean, got %T", name, value)
		}
	case "integer":
		if !isJSONInteger(value) {
			return fmt.Errorf("parameter %q must be integer, got %T", name, value)
		}
	case "number":
		if !isJSONNumber(value) {
			return fmt.Errorf("parameter %q must be number, got %T", name, value)
		}
	case "array":
		if _, ok := value.([]interface{}); !ok {
			return fmt.Errorf("parameter %q must be array, got %T", name, value)
		}
	case "object":
		if _, ok := value.(map[string]interface{}); !ok {
			return fmt.Errorf("parameter %q must be object, got %T", name, value)
		}
	}
	return nil
}

func validateToolArgEnum(name string, value interface{}, enum interface{}) error {
	values := schemaStringList(enum)
	if len(values) == 0 {
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return nil
	}
	for _, allowed := range values {
		if s == allowed {
			return nil
		}
	}
	return fmt.Errorf("parameter %q must be one of [%s], got %q", name, strings.Join(values, ", "), s)
}

func schemaStringList(raw interface{}) []string {
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// SchemaStringList returns string values from the two common JSON-schema list
// representations used by callers that build schemas in Go or decode JSON.
func SchemaStringList(raw interface{}) []string {
	return schemaStringList(raw)
}

func isJSONInteger(value interface{}) bool {
	switch v := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return math.Trunc(v) == v
	case float32:
		return math.Trunc(float64(v)) == float64(v)
	case json.Number:
		_, err := v.Int64()
		return err == nil
	default:
		return false
	}
}

func isJSONNumber(value interface{}) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	case json.Number:
		return true
	default:
		return false
	}
}
