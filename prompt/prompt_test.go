package prompt

import (
	"fmt"
	"testing"
)

func TestRender(t *testing.T) {
	tmpl := New("Hello {{name}}, your score is {{score}}")

	result, err := tmpl.Render(map[string]string{
		"name":  "Alice",
		"score": "95",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "Hello Alice, your score is 95" {
		t.Errorf("got %q", result)
	}
}

func TestRenderMissingOptional(t *testing.T) {
	tmpl := New("Hello {{name}}")
	result, err := tmpl.Render(nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// missing variables are left as-is when not strict
	if result != "Hello {{name}}" {
		t.Errorf("got %q", result)
	}
}

func TestRenderStrictMissing(t *testing.T) {
	tmpl := New("Hello {{name}}").WithStrict()
	_, err := tmpl.Render(nil)
	if err == nil {
		t.Fatal("expected error for missing variable in strict mode")
	}
}

func TestRenderWithDefaults(t *testing.T) {
	tmpl := New("{{greeting}} {{name}}").WithDefaults(map[string]string{
		"greeting": "Hello",
	})
	result, err := tmpl.Render(map[string]string{"name": "Bob"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "Hello Bob" {
		t.Errorf("got %q", result)
	}
}

func TestRenderOverrideDefault(t *testing.T) {
	tmpl := New("{{greeting}} {{name}}").WithDefaults(map[string]string{
		"greeting": "Hello",
	})
	result, err := tmpl.Render(map[string]string{
		"greeting": "Hi",
		"name":     "Bob",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "Hi Bob" {
		t.Errorf("got %q", result)
	}
}

func TestRenderMultiplePlaceholders(t *testing.T) {
	tmpl := New("{{a}}{{b}}{{a}}")
	result, err := tmpl.Render(map[string]string{"a": "X", "b": "Y"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "XYX" {
		t.Errorf("got %q", result)
	}
}

func TestRenderNoPlaceholders(t *testing.T) {
	tmpl := New("static text")
	result, err := tmpl.Render(nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "static text" {
		t.Errorf("got %q", result)
	}
}

func Example() {
	tmpl := New("Hello {{name}}!")
	result, _ := tmpl.Render(map[string]string{"name": "World"})
	fmt.Println(result)
	// Output: Hello World!
}

func Example_withDefaults() {
	tmpl := New("{{greeting}} {{name}}").WithDefaults(map[string]string{"greeting": "Hello"})
	result, _ := tmpl.Render(map[string]string{"name": "Alice"})
	fmt.Println(result)
	// Output: Hello Alice
}

func Example_strict() {
	tmpl := New("Hello {{name}}").WithStrict()
	_, err := tmpl.Render(nil)
	fmt.Println(err)
	// Output: prompt template: undefined variable "name"
}

func Example_format() {
	result := Format("{{a}} + {{b}} = {{c}}", map[string]string{
		"a": "1", "b": "2", "c": "3",
	})
	fmt.Println(result)
	// Output: 1 + 2 = 3
}

func TestRenderMessages(t *testing.T) {
	tmpl := New("system: {{role}}")
	msgs, err := tmpl.RenderMessages(map[string]string{"role": "admin"})
	if err != nil {
		t.Fatalf("RenderMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("expected system role, got %q", msgs[0].Role)
	}
	if msgs[0].Content != "system: admin" {
		t.Errorf("got content %q", msgs[0].Content)
	}
}
