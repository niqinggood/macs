package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func DecodeJSONDocument(data []byte) (interface{}, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document interface{}
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return document, nil
}

func ParseJSONPointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("JSON Pointer must be empty for the root or start with '/'")
	}
	rawTokens := strings.Split(pointer[1:], "/")
	tokens := make([]string, len(rawTokens))
	for i, raw := range rawTokens {
		decoded, err := decodeJSONPointerToken(raw)
		if err != nil {
			return nil, err
		}
		tokens[i] = decoded
	}
	return tokens, nil
}

func decodeJSONPointerToken(token string) (string, error) {
	var builder strings.Builder
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			builder.WriteByte(token[i])
			continue
		}
		if i+1 >= len(token) {
			return "", fmt.Errorf("invalid JSON Pointer escape in %q", token)
		}
		i++
		switch token[i] {
		case '0':
			builder.WriteByte('~')
		case '1':
			builder.WriteByte('/')
		default:
			return "", fmt.Errorf("invalid JSON Pointer escape ~%c in %q", token[i], token)
		}
	}
	return builder.String(), nil
}

func ApplyJSONPatchAt(node interface{}, tokens []string, operation string, value interface{}) (interface{}, error) {
	if operation != "add" && operation != "replace" && operation != "remove" {
		return nil, fmt.Errorf("unsupported operation %q", operation)
	}
	if len(tokens) == 0 {
		switch operation {
		case "add", "replace":
			return value, nil
		case "remove":
			return nil, fmt.Errorf("removing the document root is not supported")
		}
	}

	token := tokens[0]
	switch current := node.(type) {
	case map[string]interface{}:
		if len(tokens) == 1 {
			_, exists := current[token]
			switch operation {
			case "add":
				current[token] = value
			case "replace":
				if !exists {
					return nil, fmt.Errorf("object member %q does not exist", token)
				}
				current[token] = value
			case "remove":
				if !exists {
					return nil, fmt.Errorf("object member %q does not exist", token)
				}
				delete(current, token)
			}
			return current, nil
		}
		child, exists := current[token]
		if !exists {
			return nil, fmt.Errorf("object member %q does not exist", token)
		}
		updated, err := ApplyJSONPatchAt(child, tokens[1:], operation, value)
		if err != nil {
			return nil, err
		}
		current[token] = updated
		return current, nil

	case []interface{}:
		if len(tokens) == 1 {
			if token == "-" {
				if operation != "add" {
					return nil, fmt.Errorf("array '-' is valid only for add")
				}
				return append(current, value), nil
			}
			index, err := ParseJSONArrayIndex(token)
			if err != nil {
				return nil, err
			}
			switch operation {
			case "add":
				if index > len(current) {
					return nil, fmt.Errorf("array add index %d exceeds length %d", index, len(current))
				}
				current = append(current, nil)
				copy(current[index+1:], current[index:])
				current[index] = value
			case "replace":
				if index >= len(current) {
					return nil, fmt.Errorf("array replace index %d exceeds length %d", index, len(current))
				}
				current[index] = value
			case "remove":
				if index >= len(current) {
					return nil, fmt.Errorf("array remove index %d exceeds length %d", index, len(current))
				}
				current = append(current[:index], current[index+1:]...)
			}
			return current, nil
		}
		if token == "-" {
			return nil, fmt.Errorf("array '-' cannot be used before the final path segment")
		}
		index, err := ParseJSONArrayIndex(token)
		if err != nil {
			return nil, err
		}
		if index >= len(current) {
			return nil, fmt.Errorf("array index %d exceeds length %d", index, len(current))
		}
		updated, err := ApplyJSONPatchAt(current[index], tokens[1:], operation, value)
		if err != nil {
			return nil, err
		}
		current[index] = updated
		return current, nil

	default:
		return nil, fmt.Errorf("cannot traverse %q through %T", token, node)
	}
}

func ParseJSONArrayIndex(token string) (int, error) {
	if token == "" || strings.HasPrefix(token, "+") || strings.HasPrefix(token, "-") || (len(token) > 1 && token[0] == '0') {
		return 0, fmt.Errorf("invalid array index %q", token)
	}
	index, err := strconv.Atoi(token)
	if err != nil || index < 0 {
		return 0, fmt.Errorf("invalid array index %q", token)
	}
	return index, nil
}
