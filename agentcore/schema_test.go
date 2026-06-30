package agentcore

import "testing"

func TestValidateToolArgumentsNilSchema(t *testing.T) {
	tool := &Tool{Name: "test", Parameters: nil}
	if err := ValidateToolArguments(tool, `{"key":"val"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsInvalidJSON(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
		},
	}
	if err := ValidateToolArguments(tool, `{bad json}`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestValidateToolArgumentsRequired(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
	}

	// Missing required
	if err := ValidateToolArguments(tool, `{}`); err == nil {
		t.Fatal("expected error for missing required field")
	}

	// Valid
	if err := ValidateToolArguments(tool, `{"name":"John"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsRequiredStringSlice(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []string{"name"},
		},
	}

	// Missing required
	if err := ValidateToolArguments(tool, `{}`); err == nil {
		t.Fatal("expected error for missing required field")
	}

	// Valid
	if err := ValidateToolArguments(tool, `{"name":"John"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsAdditionalProperties(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}

	// Unexpected field
	if err := ValidateToolArguments(tool, `{"name":"John","extra":"bad"}`); err == nil {
		t.Fatal("expected error for unexpected field")
	}

	// Valid
	if err := ValidateToolArguments(tool, `{"name":"John"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsTypeString(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}

	if err := ValidateToolArguments(tool, `{"name":123}`); err == nil {
		t.Fatal("expected type error")
	}
}

func TestValidateToolArgumentsTypeNumber(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"age": map[string]any{"type": "number"},
			},
		},
	}

	if err := ValidateToolArguments(tool, `{"age":"not a number"}`); err == nil {
		t.Fatal("expected type error")
	}
	if err := ValidateToolArguments(tool, `{"age":25}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsTypeInteger(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"count": map[string]any{"type": "integer"},
			},
		},
	}

	if err := ValidateToolArguments(tool, `{"count":12.5}`); err == nil {
		t.Fatal("expected type error for float")
	}
	if err := ValidateToolArguments(tool, `{"count":"12"}`); err == nil {
		t.Fatal("expected type error for string")
	}
	if err := ValidateToolArguments(tool, `{"count":12}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsTypeBoolean(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"enabled": map[string]any{"type": "boolean"},
			},
		},
	}

	if err := ValidateToolArguments(tool, `{"enabled":"true"}`); err == nil {
		t.Fatal("expected type error")
	}
	if err := ValidateToolArguments(tool, `{"enabled":true}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsTypeArray(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{"type": "array"},
			},
		},
	}

	if err := ValidateToolArguments(tool, `{"items":"not array"}`); err == nil {
		t.Fatal("expected type error")
	}
	if err := ValidateToolArguments(tool, `{"items":[1,2,3]}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsTypeObject(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"nested": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	if err := ValidateToolArguments(tool, `{"nested":"not object"}`); err == nil {
		t.Fatal("expected type error")
	}
	if err := ValidateToolArguments(tool, `{"nested":{"key":"val"}}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolArgumentsEnum(t *testing.T) {
	tool := &Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"color": map[string]any{
					"type": "string",
					"enum": []any{"red", "green", "blue"},
				},
			},
		},
	}

	if err := ValidateToolArguments(tool, `{"color":"yellow"}`); err == nil {
		t.Fatal("expected enum error")
	}
	if err := ValidateToolArguments(tool, `{"color":"red"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetProperties(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
		},
	}
	props := getProperties(schema)
	if props == nil {
		t.Fatal("expected non-nil props")
	}
	if _, ok := props["a"]; !ok {
		t.Fatal("expected property 'a'")
	}
}

func TestGetPropertiesNil(t *testing.T) {
	if props := getProperties(map[string]any{}); props != nil {
		t.Fatal("expected nil")
	}
	if props := getProperties(map[string]any{"properties": "not a map"}); props != nil {
		t.Fatal("expected nil for invalid properties")
	}
}

func TestCheckEnumNotEnum(t *testing.T) {
	schema := map[string]any{"type": "string"}
	if err := checkEnum(schema, "hello", "path"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckEnumNil(t *testing.T) {
	err := ValidateToolArguments(&Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"field": map[string]any{
					"type": "string",
					"enum": map[string]any{},
				},
			},
		},
	}, `{"field":"x"}`)
	if err != nil {
		t.Fatalf("expected no error for invalid enum format, got: %v", err)
	}
}

func TestValidateToolArgumentsTypeStringFromJSONNumber(t *testing.T) {
	err := ValidateToolArguments(&Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"val": map[string]any{"type": "string"},
			},
		},
	}, `{"val":42}`)
	if err == nil {
		t.Fatal("expected type error for number passed as string")
	}
}

func TestValidateToolArgumentsRequiredAny(t *testing.T) {
	err := ValidateToolArguments(&Tool{
		Name: "test",
		Parameters: map[string]any{
			"type":     "object",
			"required": "not_a_list",
		},
	}, `{}`)
	if err != nil {
		t.Fatalf("expected no error for invalid required format, got: %v", err)
	}
}

func TestValidateToolArgumentsAdditionalPropertiesAllowedByDefault(t *testing.T) {
	err := ValidateToolArguments(&Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}, `{"name":"John","extra":"ok"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckAdditionalPropertiesNoSchema(t *testing.T) {
	if err := checkAdditionalProperties(nil, map[string]any{}, ""); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestCheckTypingNoSchema(t *testing.T) {
	err := ValidateToolArguments(&Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"val": map[string]any{
					"type": 123,
				},
			},
		},
	}, `{"val":"x"}`)
	if err != nil {
		t.Fatalf("expected no error for unsupported type format, got: %v", err)
	}
}

func TestValidateToolArgumentsTypeObjectNestedMissingRequired(t *testing.T) {
	err := ValidateToolArguments(&Tool{
		Name: "test",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"addr": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []any{"city"},
				},
			},
		},
	}, `{"addr":{}}`)
	if err == nil {
		t.Fatal("expected missing required in nested object")
	}
}
