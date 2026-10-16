package toolcontract

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// unwrapToolParameterEnvelope repairs the common provider response shape
// {"parameters": {<real tool arguments>}}. Tool schemas themselves may
// legitimately define a parameter named "parameters", so the repair is made
// only when that name is not advertised and the fully merged candidate is
// schema-valid.
// SchemaLookup resolves a tool's parameter schema without coupling argument
// normalization to a concrete registry implementation.
type SchemaLookup func(name string) (map[string]interface{}, bool)

func UnwrapParameterEnvelope(call Call, lookup SchemaLookup) (Call, bool) {
	if lookup == nil || len(call.Arguments) == 0 {
		return call, false
	}
	schema, ok := lookup(strings.TrimSpace(call.Name))
	if !ok {
		return call, false
	}
	properties, _ := schema["properties"].(map[string]interface{})
	nested, ok := toolArgumentObject(call.Arguments["parameters"])
	if !ok || len(nested) == 0 {
		return call, false
	}
	if _, advertised := properties["parameters"]; advertised {
		return liftRequiredToolArgumentsFromParameters(call, schema, properties, nested)
	}
	for name := range nested {
		if _, advertised := properties[name]; !advertised {
			return call, false
		}
	}

	candidate := make(map[string]interface{}, len(call.Arguments)+len(nested)-1)
	for name, value := range call.Arguments {
		if name != "parameters" {
			candidate[name] = value
		}
	}
	for name, value := range nested {
		if existing, present := candidate[name]; present {
			if !jsonValuesEqual(existing, value) {
				return call, false
			}
			continue
		}
		candidate[name] = value
	}
	if err := ValidateArguments(schema, candidate); err != nil {
		return call, false
	}

	repaired := call
	repaired.Arguments = candidate
	return repaired, true
}

// normalizeKnownToolArgumentAliases repairs common, unambiguous argument-name
// drift between closely related file tools. read_file and write_file advertise
// `path`, so providers frequently reuse that spelling for edit_file even though
// its canonical schema uses `file_path`. Keep file_path canonical for backwards
// compatibility, but accept the sibling-tool spelling before schema validation.
func NormalizeKnownArgumentAliases(call Call, lookup SchemaLookup) (Call, bool) {
	if lookup == nil || !strings.EqualFold(strings.TrimSpace(call.Name), "edit_file") {
		return call, false
	}
	schema, ok := lookup(call.Name)
	if !ok {
		return call, false
	}
	if _, alreadyCanonical := call.Arguments["file_path"]; alreadyCanonical {
		return call, false
	}
	path, hasAlias := call.Arguments["path"]
	if !hasAlias {
		return call, false
	}

	candidate := make(map[string]interface{}, len(call.Arguments))
	for name, value := range call.Arguments {
		if name != "path" {
			candidate[name] = value
		}
	}
	candidate["file_path"] = path
	if err := ValidateArguments(schema, candidate); err != nil {
		return call, false
	}

	repaired := call
	repaired.Arguments = candidate
	return repaired, true
}

// normalizeImageInspectionToolAlias repairs a model's unambiguous attempt to
// call watch(file=<rendered image>) as though watch were the image-analysis
// tool. watch has no file/path input; vision does. This recovery is deliberately
// limited to a concrete local image extension and still validates the canonical
// vision schema before execution, so ordinary screen capture/monitor calls are
// untouched.
func NormalizeImageInspectionAlias(call Call, lookup SchemaLookup) (Call, bool) {
	if lookup == nil || !strings.EqualFold(strings.TrimSpace(call.Name), "watch") {
		return call, false
	}
	visionSchema, ok := lookup("vision")
	if !ok {
		return call, false
	}
	pathValue, hasFile := call.Arguments["file"].(string)
	if !hasFile || strings.TrimSpace(pathValue) == "" {
		pathValue, hasFile = call.Arguments["path"].(string)
	}
	if !hasFile || !isSupportedVisionImagePath(pathValue) {
		return call, false
	}

	candidate := map[string]interface{}{"path": strings.TrimSpace(pathValue)}
	for _, name := range []string{"question", "prompt"} {
		if question, present := call.Arguments[name].(string); present && strings.TrimSpace(question) != "" {
			candidate["question"] = strings.TrimSpace(question)
			break
		}
	}
	if err := ValidateArguments(visionSchema, candidate); err != nil {
		return call, false
	}

	repaired := call
	repaired.Name = "vision"
	repaired.Arguments = candidate
	return repaired, true
}

func isSupportedVisionImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

// liftRequiredToolArgumentsFromParameters handles tools such as use_skill that
// legitimately advertise a free-form `parameters` object. Some providers put
// required top-level fields (for example skill_name) inside that object. Lift
// only advertised required fields and preserve all remaining skill parameters.
func liftRequiredToolArgumentsFromParameters(call Call, schema, properties, nested map[string]interface{}) (Call, bool) {
	candidate := make(map[string]interface{}, len(call.Arguments)+1)
	for name, value := range call.Arguments {
		candidate[name] = value
	}
	nestedCopy := make(map[string]interface{}, len(nested))
	for name, value := range nested {
		nestedCopy[name] = value
	}
	lifted := false
	for _, name := range schemaStringList(schema["required"]) {
		if _, present := candidate[name]; present {
			continue
		}
		if _, advertised := properties[name]; !advertised {
			continue
		}
		value, present := nestedCopy[name]
		if !present {
			continue
		}
		candidate[name] = value
		delete(nestedCopy, name)
		lifted = true
	}
	if !lifted {
		return call, false
	}
	candidate["parameters"] = nestedCopy
	if err := ValidateArguments(schema, candidate); err != nil {
		return call, false
	}
	repaired := call
	repaired.Arguments = candidate
	return repaired, true
}

func toolArgumentObject(value interface{}) (map[string]interface{}, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		return typed, true
	case string:
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(typed)), &decoded); err != nil {
			return nil, false
		}
		return decoded, decoded != nil
	default:
		return nil, false
	}
}

func jsonValuesEqual(left, right interface{}) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
