package agentcore

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateToolArguments validates tool call arguments against the tool's
// parameter schema. Returns nil if valid, or a descriptive error.
func ValidateToolArguments(tool *Tool, arguments string) error {
	if tool.Parameters == nil {
		return nil
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return fmt.Errorf("invalid JSON arguments: %w", err)
	}

	return validateObject(tool.Parameters, args, "")
}

func validateObject(schema map[string]any, value map[string]any, path string) error {
	if err := checkRequired(schema, value, path); err != nil {
		return err
	}
	if err := checkAdditionalProperties(schema, value, path); err != nil {
		return err
	}
	return checkPropertyTypes(schema, value, path)
}

func checkRequired(schema map[string]any, value map[string]any, path string) error {
	required, ok := schema["required"]
	if !ok {
		return nil
	}

	var names []string
	switch r := required.(type) {
	case []any:
		for _, v := range r {
			if s, ok := v.(string); ok {
				names = append(names, s)
			}
		}
	case []string:
		names = r
	default:
		return nil
	}

	for _, name := range names {
		if _, exists := value[name]; !exists {
			return fmt.Errorf("missing required field: %s%s", path, name)
		}
	}
	return nil
}

func checkAdditionalProperties(schema map[string]any, value map[string]any, path string) error {
	additional, ok := schema["additionalProperties"]
	if !ok {
		return nil
	}
	allowed, ok := additional.(bool)
	if !ok || allowed {
		return nil
	}

	props := getProperties(schema)
	for key := range value {
		if _, defined := props[key]; !defined {
			return fmt.Errorf("unexpected field: %s%s", path, key)
		}
	}
	return nil
}

func checkPropertyTypes(schema map[string]any, value map[string]any, path string) error {
	props := getProperties(schema)
	for name, propSchema := range props {
		val, exists := value[name]
		if !exists {
			continue
		}
		ps, ok := propSchema.(map[string]any)
		if !ok {
			continue
		}
		if err := validateValue(ps, val, path+name); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(schema map[string]any, value any, path string) error {
	if err := checkEnum(schema, value, path); err != nil {
		return err
	}

	expectedType, ok := schema["type"].(string)
	if !ok {
		return nil
	}

	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: expected string, got %T", path, value)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s: expected number, got %T", path, value)
		}
	case "integer":
		v, ok := value.(float64)
		if !ok || v != float64(int64(v)) {
			return fmt.Errorf("%s: expected integer, got %T", path, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected boolean, got %T", path, value)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("%s: expected array, got %T", path, value)
		}
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object, got %T", path, value)
		}
		return validateObject(schema, obj, path+".")
	}
	return nil
}

func checkEnum(schema map[string]any, value any, path string) error {
	enumVals, ok := schema["enum"]
	if !ok {
		return nil
	}
	enumList, ok := enumVals.([]any)
	if !ok {
		return nil
	}

	valStr := fmt.Sprintf("%v", value)
	for _, ev := range enumList {
		if fmt.Sprintf("%v", ev) == valStr {
			return nil
		}
	}
	return fmt.Errorf("%s: value %v not in enum %v", strings.TrimSuffix(path, "."), value, enumList)
}

func getProperties(schema map[string]any) map[string]any {
	props, ok := schema["properties"]
	if !ok {
		return nil
	}
	propsMap, ok := props.(map[string]any)
	if !ok {
		return nil
	}
	return propsMap
}
