package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVisionToolLocalFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a minimal valid PNG file (1x1 pixel).
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0x0F, 0x00, 0x00,
		0x01, 0x01, 0x00, 0x05, 0x18, 0xD8, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	testFile := filepath.Join(tmpDir, "test.png")
	os.WriteFile(testFile, pngData, 0644)

	tool := NewVisionTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{
		"image":  "test.png",
		"prompt": "Describe this image",
	})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "Vision analysis placeholder") {
		t.Errorf("expected placeholder response, got: %s", tr.Content)
	}
}

func TestVisionToolBase64(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewVisionTool(tmpDir, nil)

	// Minimal PNG as base64.
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0x0F, 0x00, 0x00,
		0x01, 0x01, 0x00, 0x05, 0x18, 0xD8, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	b64 := base64.StdEncoding.EncodeToString(pngData)

	args, _ := json.Marshal(map[string]string{
		"base64": b64,
		"prompt": "What's in this image?",
	})
	result, err := tool.Func(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr := result.(ToolResult)
	if !strings.Contains(tr.Content, "Vision analysis placeholder") {
		t.Errorf("expected placeholder response, got: %s", tr.Content)
	}
}

func TestVisionToolMissingImage(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewVisionTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{
		"prompt": "Describe this image",
	})
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for missing image")
	}
	if !strings.Contains(err.Error(), "either image") {
		t.Errorf("expected image required error, got: %v", err)
	}
}

func TestVisionToolInvalidBase64(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewVisionTool(tmpDir, nil)

	args, _ := json.Marshal(map[string]string{
		"base64": "not-valid-base64!!!",
		"prompt": "Describe",
	})
	_, err := tool.Func(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if !strings.Contains(err.Error(), "invalid base64") {
		t.Errorf("expected base64 error, got: %v", err)
	}
}

func TestDetectImageMIME(t *testing.T) {
	tests := []struct {
		data     []byte
		expected string
	}{
		{[]byte{0xFF, 0xD8, 0xFF}, "image/jpeg"},
		{[]byte{0x89, 0x50, 0x4E, 0x47}, "image/png"},
		{[]byte{'G', 'I', 'F', '8'}, "image/gif"},
		{[]byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}, "image/webp"},
		{[]byte{'B', 'M'}, "image/bmp"},
		{[]byte{}, "application/octet-stream"},
	}

	for _, tt := range tests {
		result := detectImageMIME(tt.data)
		if result != tt.expected {
			t.Errorf("detectImageMIME(%v) = %s, want %s", tt.data, result, tt.expected)
		}
	}
}

func TestMimeFromExt(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"test.jpg", "image/jpeg"},
		{"test.jpeg", "image/jpeg"},
		{"test.png", "image/png"},
		{"test.gif", "image/gif"},
		{"test.webp", "image/webp"},
		{"test.bmp", "image/bmp"},
		{"test.tiff", "image/tiff"},
		{"test.tif", "image/tiff"},
		{"test.svg", "image/svg+xml"},
		{"test.unknown", ""},
	}

	for _, tt := range tests {
		result := mimeFromExt(tt.path)
		if result != tt.expected {
			t.Errorf("mimeFromExt(%s) = %s, want %s", tt.path, result, tt.expected)
		}
	}
}
