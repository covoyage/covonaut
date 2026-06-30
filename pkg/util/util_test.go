package util

import (
	"errors"
	"os"
	"testing"
)

func TestErrorString(t *testing.T) {
	if s := ErrorString(nil); s != "" {
		t.Errorf("nil error: got %q", s)
	}
	if s := ErrorString(errors.New("foo")); s != "foo" {
		t.Errorf("error: got %q", s)
	}
}

func TestDefaultString(t *testing.T) {
	if s := DefaultString("", "fallback"); s != "fallback" {
		t.Errorf("empty: got %q", s)
	}
	if s := DefaultString("  ", "fallback"); s != "fallback" {
		t.Errorf("whitespace: got %q", s)
	}
	if s := DefaultString("value", "fallback"); s != "value" {
		t.Errorf("value: got %q", s)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if s := FirstNonEmpty(); s != "" {
		t.Errorf("no args: got %q", s)
	}
	if s := FirstNonEmpty("", "  ", "hello"); s != "hello" {
		t.Errorf("first non-empty: got %q", s)
	}
	if s := FirstNonEmpty("first", "second"); s != "first" {
		t.Errorf("first: got %q", s)
	}
}

func TestEnvOrDefault(t *testing.T) {
	const key = "TEST_ENV_OR_DEFAULT_UNIQUE_KEY_12345"
	// key not set
	if v := EnvOrDefault(key, "fallback"); v != "fallback" {
		t.Errorf("empty: got %q", v)
	}
	// key set
	os.Setenv(key, "env_value")
	defer os.Unsetenv(key)
	if v := EnvOrDefault(key, "fallback"); v != "env_value" {
		t.Errorf("env: got %q", v)
	}
}
