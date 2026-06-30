package browser_providers

import (
	"os"
	"testing"
)

func TestGetEnv_Default(t *testing.T) {
	val := GetEnv("NONEXISTENT_VAR_ABC", "default")
	if val != "default" {
		t.Fatalf("expected default, got %q", val)
	}
}

func TestGetEnv_FromEnv(t *testing.T) {
	os.Setenv("TEST_VAR_GETENV", "from_env")
	defer os.Unsetenv("TEST_VAR_GETENV")

	val := GetEnv("TEST_VAR_GETENV", "default")
	if val != "from_env" {
		t.Fatalf("expected from_env, got %q", val)
	}
}

func TestGetEnvBool_Default(t *testing.T) {
	val := GetEnvBool("NONEXISTENT_BOOL_ABC", false)
	if val != false {
		t.Fatalf("expected false, got %v", val)
	}

	val = GetEnvBool("NONEXISTENT_BOOL_ABC", true)
	if val != true {
		t.Fatalf("expected true, got %v", val)
	}
}

func TestGetEnvBool_TrueValues(t *testing.T) {
	for _, tv := range []string{"true", "1", "yes"} {
		os.Setenv("TEST_BOOL", tv)
		if !GetEnvBool("TEST_BOOL", false) {
			t.Fatalf("expected true for %q", tv)
		}
		os.Unsetenv("TEST_BOOL")
	}
}

func TestGetEnvBool_FalseValues(t *testing.T) {
	for _, fv := range []string{"false", "0", "no", "anything"} {
		os.Setenv("TEST_BOOL", fv)
		if GetEnvBool("TEST_BOOL", true) {
			t.Fatalf("expected false for %q", fv)
		}
		os.Unsetenv("TEST_BOOL")
	}
}

func TestInterface(t *testing.T) {
	var _ CloudBrowserProvider = (*struct {
		CloudBrowserProvider
	})(nil)
}
