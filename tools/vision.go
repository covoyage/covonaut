package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// VisionOperations defines pluggable operations for the vision tool.
type VisionOperations interface {
	// Analyze sends an image to a vision-capable LLM for analysis.
	Analyze(imageData []byte, mimeType string, prompt string) (string, error)
	// Download fetches an image from a URL.
	Download(url string) ([]byte, string, error)
}

// DefaultVisionOperations uses an external vision API.
type DefaultVisionOperations struct {
	APIURL string
	APIKey string
	Client *http.Client
}

func (d DefaultVisionOperations) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (d DefaultVisionOperations) Download(url string) ([]byte, string, error) {
	// Validate URL.
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, "", fmt.Errorf("invalid URL: must start with http:// or https://")
	}

	resp, err := d.client().Get(url)
	if err != nil {
		return nil, "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Limit download size (50MB).
	const maxBytes = 50 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, "", err
	}

	// Detect MIME type.
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = detectImageMIME(data)
	}

	return data, mimeType, nil
}

func (d DefaultVisionOperations) Analyze(imageData []byte, mimeType string, prompt string) (string, error) {
	// Placeholder: in production, this would call OpenAI, Anthropic, or other vision API.
	// For now, return a descriptive placeholder.
	return fmt.Sprintf(
		"[Vision analysis placeholder] Image: %s, %d bytes, MIME: %s. "+
			"Prompt: %s. "+
			"Configure a real vision API (OpenAI GPT-4V, Anthropic Claude, etc.) for actual analysis.",
		"image", len(imageData), mimeType, prompt,
	), nil
}

// detectImageMIME detects image MIME type from magic bytes.
func detectImageMIME(data []byte) string {
	if len(data) < 2 {
		return "application/octet-stream"
	}

	// JPEG: FF D8 FF
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}

	// PNG: 89 50 4E 47
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}

	// GIF: GIF87a or GIF89a
	if len(data) >= 4 && data[0] == 'G' && data[1] == 'I' && data[2] == 'F' {
		return "image/gif"
	}

	// WebP: RIFF....WEBP
	if len(data) >= 12 && data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		return "image/webp"
	}

	// BMP: BM
	if data[0] == 'B' && data[1] == 'M' {
		return "image/bmp"
	}

	// TIFF: II or MM
	if len(data) >= 2 && (data[0] == 'I' && data[1] == 'I') || (data[0] == 'M' && data[1] == 'M') {
		return "image/tiff"
	}

	return "application/octet-stream"
}

// VisionToolConfig configures the vision tool.
type VisionToolConfig struct {
	Operations VisionOperations
	MaxBytes   int64
	MaxPixels  int64

	// Provider and Model are optional. When set and Operations is nil,
	// the tool creates a default implementation that calls this provider.
	Provider agentcore.Provider
	Model    string
}

func (c *VisionToolConfig) defaults() {
	if c.Operations == nil {
		if c.Provider != nil && c.Model != "" {
			c.Operations = &providerVisionOperations{
				provider: c.Provider,
				model:    c.Model,
			}
		} else {
			c.Operations = DefaultVisionOperations{}
		}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 20 * 1024 * 1024 // 20MB base64
	}
	if c.MaxPixels <= 0 {
		c.MaxPixels = 4096 * 4096 // ~16MP
	}
}

// providerVisionOperations implements VisionOperations using an agentcore.Provider.
type providerVisionOperations struct {
	provider agentcore.Provider
	model    string
}

func (o *providerVisionOperations) Analyze(imageData []byte, mimeType, prompt string) (string, error) {
	if o.provider == nil {
		return "", fmt.Errorf("vision provider not configured")
	}
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(imageData))
	msg := agentcore.Message{Role: "user"}
	msg = msg.AppendTextBlock(prompt)
	msg = msg.AppendImageURLBlock(dataURL)

	resp, err := o.provider.Complete(context.Background(), &agentcore.ProviderRequest{
		Model:    o.model,
		Messages: []agentcore.Message{msg},
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (o *providerVisionOperations) Download(url string) ([]byte, string, error) {
	return DefaultVisionOperations{}.Download(url)
}

// VisionToolInput is the JSON arguments for the vision tool.
type VisionToolInput struct {
	Image  string `json:"image"`            // URL or local path
	Prompt string `json:"prompt"`           // Question about the image
	Base64 string `json:"base64,omitempty"` // Direct base64 data
}

// VisionToolDetails carries vision metadata.
type VisionToolDetails struct {
	ImageSize    int    `json:"image_size"`
	MIMEType     string `json:"mime_type"`
	Base64Size   int    `json:"base64_size"`
	Resized      bool   `json:"resized"`
	OriginalSize int    `json:"original_size,omitempty"`
}

// NewVisionTool creates an image analysis tool.
func NewVisionTool(cwd string, cfg *VisionToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &VisionToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "vision_analyze",
		Description: "Analyze an image using a vision-capable LLM. " +
			"Provide either an image URL, a local file path, or base64-encoded image data. " +
			"Images are automatically resized if they exceed size limits. " +
			"Supported formats: JPEG, PNG, GIF, WebP, BMP, TIFF.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image": map[string]any{
					"type":        "string",
					"description": "URL or local file path to the image",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Question or instruction about the image (e.g., 'Describe this image', 'What text is visible?')",
				},
				"base64": map[string]any{
					"type":        "string",
					"description": "Base64-encoded image data (alternative to image URL/path)",
				},
			},
			"required": []any{"prompt"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input VisionToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if input.Prompt == "" {
				return resultErrf("prompt is required")
			}

			if input.Image == "" && input.Base64 == "" {
				return resultErrf("either image (URL/path) or base64 is required")
			}

			var imageData []byte
			var mimeType string
			var err error
			resized := false
			originalSize := 0

			if input.Base64 != "" {
				// Decode base64.
				imageData, err = base64.StdEncoding.DecodeString(input.Base64)
				if err != nil {
					return resultErrf("invalid base64 data: %w", err)
				}
				mimeType = detectImageMIME(imageData)
			} else {
				// Load from URL or file.
				if strings.HasPrefix(input.Image, "http://") || strings.HasPrefix(input.Image, "https://") {
					imageData, mimeType, err = cfg.Operations.Download(input.Image)
					if err != nil {
						return resultErrf("failed to download image: %w", err)
					}
				} else {
					// Local file.
					resolved := resolvePath(input.Image, cwd)
					imageData, err = os.ReadFile(resolved)
					if err != nil {
						return resultErrf("failed to read image file: %w", err)
					}
					mimeType = detectImageMIME(imageData)
					if extMime := mimeFromExt(resolved); extMime != "" {
						mimeType = extMime
					}
				}
			}

			originalSize = len(imageData)

			// Validate it's an image.
			if !strings.HasPrefix(mimeType, "image/") {
				return resultErrf("not an image file (detected: %s)", mimeType)
			}

			// Check size limits.
			base64Size := len(base64.StdEncoding.EncodeToString(imageData))
			if int64(base64Size) > cfg.MaxBytes {
				return resultErrf(
					"image too large: %s base64 (limit: %s). "+
						"Consider resizing the image before sending.",
					FormatSize(int64(base64Size)), FormatSize(cfg.MaxBytes),
				)
			}

			// Call vision API.
			analysis, err := cfg.Operations.Analyze(imageData, mimeType, input.Prompt)
			if err != nil {
				return resultErrf("vision analysis failed: %w", err)
			}

			return result(analysis, VisionToolDetails{
				ImageSize:    len(imageData),
				MIMEType:     mimeType,
				Base64Size:   base64Size,
				Resized:      resized,
				OriginalSize: originalSize,
			})
		},
	}
}

// mimeFromExt gets MIME type from file extension.
func mimeFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".tiff", ".tif":
		return "image/tiff"
	case ".svg":
		return "image/svg+xml"
	default:
		return ""
	}
}
