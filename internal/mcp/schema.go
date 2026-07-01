package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// ValidateJSON validates raw JSON against the small JSON Schema subset used by
// LocalGate MCP execution tools. Supported keywords: type, required, properties,
// additionalProperties, items, enum, and minLength.
func ValidateJSON(raw json.RawMessage, schema any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("input is required")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("input must contain exactly one JSON value")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}

	schemaMap, err := normalizeSchema(schema)
	if err != nil {
		return err
	}
	if len(schemaMap) == 0 {
		return nil
	}
	return validateValue("$", value, schemaMap)
}

func normalizeSchema(schema any) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}
	if m, ok := schema.(map[string]any); ok {
		return m, nil
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode tool schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode tool schema: %w", err)
	}
	return m, nil
}

func validateValue(path string, value any, schema map[string]any) error {
	if allowed, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range allowed {
			if reflect.DeepEqual(value, candidate) || fmt.Sprint(value) == fmt.Sprint(candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s must be one of %v", path, allowed)
		}
	}

	if typeName, _ := schema["type"].(string); typeName != "" {
		if err := validateType(path, value, typeName); err != nil {
			return err
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		if err := validateObject(path, typed, schema); err != nil {
			return err
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range typed {
				if err := validateValue(fmt.Sprintf("%s[%d]", path, i), item, items); err != nil {
					return err
				}
			}
		}
	case string:
		if min, ok := numericInt(schema["minLength"]); ok && utf8.RuneCountInString(typed) < min {
			return fmt.Errorf("%s must contain at least %d characters", path, min)
		}
	}
	return nil
}

func validateObject(path string, value map[string]any, schema map[string]any) error {
	for _, name := range stringSlice(schema["required"]) {
		if _, ok := value[name]; !ok {
			return fmt.Errorf("%s.%s is required", path, name)
		}
	}

	properties, _ := schema["properties"].(map[string]any)
	for name, child := range value {
		childSchemaAny, known := properties[name]
		if !known {
			if allow, ok := schema["additionalProperties"].(bool); ok && !allow {
				return fmt.Errorf("%s.%s is not allowed", path, name)
			}
			continue
		}
		childSchema, err := normalizeSchema(childSchemaAny)
		if err != nil {
			return err
		}
		if err := validateValue(path+"."+name, child, childSchema); err != nil {
			return err
		}
	}
	return nil
}

func validateType(path string, value any, expected string) error {
	valid := false
	switch expected {
	case "object":
		_, valid = value.(map[string]any)
	case "array":
		_, valid = value.([]any)
	case "string":
		_, valid = value.(string)
	case "boolean":
		_, valid = value.(bool)
	case "number":
		_, valid = value.(json.Number)
	case "integer":
		if n, ok := value.(json.Number); ok {
			_, err := n.Int64()
			valid = err == nil
		}
	case "null":
		valid = value == nil
	default:
		return fmt.Errorf("unsupported schema type %q", expected)
	}
	if !valid {
		return fmt.Errorf("%s must be %s", path, expected)
	}
	return nil
}

func stringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func numericInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), v == float64(int(v))
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
