package agentcore

import (
	"encoding/json"
	"fmt"
	"strconv"
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

// CoerceToolArguments coerces argument values to match the tool's parameter
// schema types. This handles the common LLM mistake of emitting numbers and
// booleans as JSON strings (e.g. {"count": "5"} instead of {"count": 5}).
// It returns the coerced arguments string, or the original if no coercion
// was needed or if the arguments are not valid JSON / the tool has no schema.
func CoerceToolArguments(tool *Tool, arguments string) string {
	if tool.Parameters == nil || arguments == "" {
		return arguments
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return arguments // not valid JSON; let validation report the error
	}

	if coerceObjectArgs(tool.Parameters, args) {
		if coerced, err := json.Marshal(args); err == nil {
			return string(coerced)
		}
	}
	return arguments
}

// coerceObjectArgs walks the schema's properties and coerces each value
// in-place. Returns true if any value was changed.
func coerceObjectArgs(schema map[string]any, args map[string]any) bool {
	props := getProperties(schema)
	if props == nil {
		return false
	}
	changed := false
	for name, propSchemaRaw := range props {
		val, exists := args[name]
		if !exists {
			continue
		}
		propSchema, ok := propSchemaRaw.(map[string]any)
		if !ok {
			continue
		}
		if coerced, ok := coerceValue(propSchema, val); ok {
			args[name] = coerced
			changed = true
		}
	}
	return changed
}

// coerceValue attempts to coerce a value to the type declared in the schema.
// Returns (coerced, true) if coercion was applied, (original, false) otherwise.
func coerceValue(schema map[string]any, value any) (any, bool) {
	expectedType, ok := schema["type"].(string)
	if !ok {
		return value, false
	}

	switch expectedType {
	case "integer":
		// string → int
		if s, ok := value.(string); ok {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return float64(n), true // JSON numbers decode as float64
			}
		}
	case "number":
		// string → float
		if s, ok := value.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, true
			}
		}
	case "boolean":
		// string → bool
		if s, ok := value.(string); ok {
			switch strings.ToLower(s) {
			case "true":
				return true, true
			case "false":
				return false, true
			}
		}
	case "array":
		// string → array (try parsing as JSON array)
		if s, ok := value.(string); ok {
			var arr []any
			if err := json.Unmarshal([]byte(s), &arr); err == nil {
				// Recursively coerce array element types if items schema exists
				if itemsSchema, ok := schema["items"].(map[string]any); ok {
					for i, item := range arr {
						if coerced, ok := coerceValue(itemsSchema, item); ok {
							arr[i] = coerced
						}
					}
				}
				return arr, true
			}
		}
		// Also coerce elements within an existing array
		if arr, ok := value.([]any); ok {
			if itemsSchema, ok := schema["items"].(map[string]any); ok {
				changed := false
				for i, item := range arr {
					if coerced, ok := coerceValue(itemsSchema, item); ok {
						arr[i] = coerced
						changed = true
					}
				}
				return arr, changed
			}
		}
	case "object":
		// string → object (try parsing as JSON object)
		if s, ok := value.(string); ok {
			var obj map[string]any
			if err := json.Unmarshal([]byte(s), &obj); err == nil {
				coerceObjectArgs(schema, obj)
				return obj, true
			}
		}
		// Also coerce properties within an existing object
		if obj, ok := value.(map[string]any); ok {
			if coerceObjectArgs(schema, obj) {
				return obj, true
			}
		}
	}
	return value, false
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
