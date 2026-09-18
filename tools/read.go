package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// ReadOperations defines pluggable filesystem operations for the read tool.
type ReadOperations interface {
	ReadFile(path string) ([]byte, error)
	Stat(path string) (os.FileInfo, error)
}

// DefaultReadOperations uses the local filesystem.
type DefaultReadOperations struct{}

func (d DefaultReadOperations) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (d DefaultReadOperations) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

// ReadMediaKind is the non-text payload a unified read call can dispatch.
type ReadMediaKind string

const (
	ReadMediaImage    ReadMediaKind = "image"
	ReadMediaPDF      ReadMediaKind = "pdf"
	ReadMediaNotebook ReadMediaKind = "notebook"
	ReadMediaVideo    ReadMediaKind = "video"
)

// ReadMediaHandler loads images, PDFs, or videos for the read tool.
// paths is one or more resolved local paths or http(s) URLs.
// Notebook rendering stays in this package. A nil handler keeps the
// previous text-only behavior (images error; other binaries are rejected).
type ReadMediaHandler func(ctx context.Context, kind ReadMediaKind, paths []string, input ReadToolInput) (any, error)

// ReadToolConfig configures the read tool.
type ReadToolConfig struct {
	Operations   ReadOperations
	MaxBytes     int64
	MaxLines     int64
	MediaHandler ReadMediaHandler
}

func (c *ReadToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = DefaultReadOperations{}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024
	}
	if c.MaxLines <= 0 {
		c.MaxLines = 2000
	}
}

// ReadToolInput is the JSON arguments for the read tool.
type ReadToolInput struct {
	Path         string   `json:"path"`
	Paths        []string `json:"paths,omitempty"`
	Offset       *int     `json:"offset,omitempty"`
	Limit        *int     `json:"limit,omitempty"`
	Pages        string   `json:"pages,omitempty"`
	MaxPages     int      `json:"max_pages,omitempty"`
	Info         bool     `json:"info,omitempty"`
	Prompt       string   `json:"prompt,omitempty"`
	Frames       int      `json:"frames,omitempty"`
	StartSeconds *float64 `json:"start_seconds,omitempty"`
	EndSeconds   *float64 `json:"end_seconds,omitempty"`
}

const readMaxMediaPaths = 8

// ReadToolDetails carries truncation metadata.
type ReadToolDetails struct {
	Truncation *TruncationResult `json:"truncation,omitempty"`
}

// NewReadTool creates a read file tool.
func NewReadTool(cwd string, cfg *ReadToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &ReadToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name: "read",
		Description: strings.Join([]string{
			"Read a file, image, PDF, Jupyter notebook, or video.",
			"Local paths and http(s) URLs are accepted. Use paths (max 8) to compare several images.",
			fmt.Sprintf("Text output is truncated to %d lines or %s (whichever is hit first).", cfg.MaxLines, FormatSize(cfg.MaxBytes)),
			"Text/notebook: offset and limit. PDF: pages, max_pages, info=true for metadata.",
			"Image/video: prompt focuses the description. Video: frames (1-8), start_seconds, end_seconds.",
		}, " "),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string", "description": "Local path or http(s) URL. Combined with paths if both are set."},
				"paths":         map[string]any{"type": "array", "description": "Multiple image paths or URLs (max 8).", "items": map[string]any{"type": "string"}},
				"offset":        map[string]any{"type": "integer", "description": "Start at this line (text) or cell (notebook), 1-indexed."},
				"limit":         map[string]any{"type": "integer", "description": "Maximum number of lines (text) or cells (notebook) to read."},
				"pages":         map[string]any{"type": "string", "description": "PDF page range, e.g. '1-5' or '1,3,5-7'."},
				"max_pages":     map[string]any{"type": "integer", "description": "Maximum PDF pages to extract (default 50)."},
				"info":          map[string]any{"type": "boolean", "description": "If true, return PDF metadata instead of extracted text."},
				"prompt":        map[string]any{"type": "string", "description": "Optional question or focus when reading an image or video."},
				"frames":        map[string]any{"type": "integer", "description": "Video stills to sample (1-8, default 6)."},
				"start_seconds": map[string]any{"type": "number", "description": "Optional start of the video sample window in seconds."},
				"end_seconds":   map[string]any{"type": "number", "description": "Optional end of the video sample window in seconds."},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input ReadToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			targets := collectReadTargets(input.Path, input.Paths)
			if len(targets) == 0 {
				return resultErrf("path or paths is required")
			}
			if len(targets) > readMaxMediaPaths {
				return resultErrf("at most %d paths per call", readMaxMediaPaths)
			}

			if len(targets) > 1 {
				if cfg.MediaHandler == nil {
					return resultErrf("reading multiple files requires a media-capable agent")
				}
				return cfg.MediaHandler(ctx, ReadMediaImage, targets, input)
			}

			target := targets[0]
			if isRemoteReadPath(target) {
				if cfg.MediaHandler == nil {
					return resultErrf("remote URLs require a media-capable agent")
				}
				kind := classifyReadPath(target, nil)
				if kind == "" {
					kind = ReadMediaImage
				}
				if kind == ReadMediaNotebook {
					return resultErrf("remote Jupyter notebooks are not supported; download the file first")
				}
				return cfg.MediaHandler(ctx, kind, []string{target}, input)
			}

			resolved := resolveReadPath(target, cwd)
			info, err := cfg.Operations.Stat(resolved)
			if err != nil {
				return resultErrf("file not found: %s", target)
			}
			if info.IsDir() {
				entries, err := os.ReadDir(resolved)
				if err != nil {
					return resultErrf("cannot read directory: %s", target)
				}
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("Directory listing for %s:\n", target))
				for _, entry := range entries {
					name := entry.Name()
					if entry.IsDir() {
						name += "/"
					}
					info, _ := entry.Info()
					size := ""
					if info != nil && !entry.IsDir() {
						size = fmt.Sprintf("  (%s)", FormatSize(info.Size()))
					}
					sb.WriteString(fmt.Sprintf("  %s%s\n", name, size))
				}
				return result(sb.String(), nil)
			}

			data, err := cfg.Operations.ReadFile(resolved)
			if err != nil {
				return resultErrf("failed to read file: %w", err)
			}

			kind := classifyReadPath(resolved, data)
			switch kind {
			case ReadMediaImage, ReadMediaPDF, ReadMediaVideo:
				if cfg.MediaHandler == nil {
					switch kind {
					case ReadMediaImage:
						return resultErrf("Cannot read %q as an image from this read tool. Use a media-capable agent, or pass a MediaHandler.", target)
					case ReadMediaPDF:
						return resultErrf("%s is a PDF; PDF extraction is not configured on this read tool.", target)
					default:
						return resultErrf("%s is a video; video analysis is not configured on this read tool.", target)
					}
				}
				return cfg.MediaHandler(ctx, kind, []string{resolved}, input)
			case ReadMediaNotebook:
				rendered, renderErr := renderNotebook(data, input)
				if renderErr != nil {
					return resultErrf("failed to read notebook %s: %w", target, renderErr)
				}
				return truncateReadText(rendered, cfg)
			}

			if looksBinary(data) {
				return resultErrf("%s looks like a binary file (%s). Use a dedicated tool for this format.", target, detectReadMIME(resolved, data))
			}

			content := string(data)
			if input.Offset != nil || input.Limit != nil {
				content = sliceLines(content, input.Offset, input.Limit)
			}
			return truncateReadText(content, cfg)
		},
	}
}

func truncateReadText(content string, cfg *ReadToolConfig) (any, error) {
	truncation := TruncateHead(content, TruncationOptions{
		MaxLines: int(cfg.MaxLines),
		MaxBytes: int(cfg.MaxBytes),
	})
	output := truncation.Content
	if truncation.Truncated {
		notices := []string{}
		if truncation.TruncatedBy == "lines" {
			notices = append(notices, fmt.Sprintf("%d lines limit reached", cfg.MaxLines))
		} else {
			notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(cfg.MaxBytes)))
		}
		if truncation.FirstLineExceeds {
			notices = append(notices, "first line exceeds byte limit")
		}
		output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
	}
	return result(output, ReadToolDetails{Truncation: &truncation})
}

func sliceLines(content string, offset, limit *int) string {
	lines := strings.Split(content, "\n")
	startIdx := 1
	if offset != nil && *offset > 0 {
		startIdx = *offset
	}
	n := len(lines)
	if limit != nil && *limit > 0 {
		n = *limit
	}
	start := startIdx - 1
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := start + n
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}

func isImageFile(path string, data []byte) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".tiff", ".tif":
		return true
	}

	mimeType := mime.TypeByExtension(ext)
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}

	// Detect images that don't match their extension
	if mimeType := detectImageMIME(data); mimeType != "application/octet-stream" {
		return true
	}

	return false
}

func collectReadTargets(path string, paths []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(path)
	for _, p := range paths {
		add(p)
	}
	return out
}

func isRemoteReadPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func classifyReadPath(path string, data []byte) ReadMediaKind {
	name := path
	if i := strings.Index(path, "?"); i >= 0 {
		name = path[:i]
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".tiff", ".tif":
		return ReadMediaImage
	case ".pdf":
		return ReadMediaPDF
	case ".ipynb":
		return ReadMediaNotebook
	case ".mp4", ".mov", ".webm", ".mkv", ".avi", ".m4v":
		return ReadMediaVideo
	}
	if len(data) > 0 && isImageFile(path, data) {
		return ReadMediaImage
	}
	if detectPDF(data) {
		return ReadMediaPDF
	}
	return ""
}

func detectPDF(data []byte) bool {
	trimmed := bytes.TrimLeft(data, "\r\n\t ")
	return bytes.HasPrefix(trimmed, []byte("%PDF-"))
}

func detectReadMIME(path string, data []byte) string {
	if m := detectImageMIME(data); m != "application/octet-stream" {
		return m
	}
	if detectPDF(data) {
		return "application/pdf"
	}
	if mt := mime.TypeByExtension(filepath.Ext(path)); mt != "" {
		return mt
	}
	return "application/octet-stream"
}

func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	sample := data
	if len(sample) > 8000 {
		sample = sample[:8000]
	}
	return bytes.IndexByte(sample, 0) >= 0
}

type notebookFile struct {
	Cells []notebookCell `json:"cells"`
}

type notebookCell struct {
	CellType string           `json:"cell_type"`
	Source   notebookSource   `json:"source"`
	Outputs  []notebookOutput `json:"outputs"`
}

type notebookSource []string

func (s *notebookSource) UnmarshalJSON(data []byte) error {
	var lines []string
	if err := json.Unmarshal(data, &lines); err == nil {
		*s = lines
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err != nil {
		return err
	}
	*s = []string{one}
	return nil
}

func (s notebookSource) String() string {
	return strings.Join(s, "")
}

type notebookOutput struct {
	OutputType string          `json:"output_type"`
	Text       notebookSource  `json:"text"`
	Ename      string          `json:"ename"`
	Evalue     string          `json:"evalue"`
	Data       json.RawMessage `json:"data"`
}

func renderNotebook(data []byte, input ReadToolInput) (string, error) {
	var nb notebookFile
	if err := json.Unmarshal(data, &nb); err != nil {
		return "", fmt.Errorf("invalid notebook JSON: %w", err)
	}
	if len(nb.Cells) == 0 {
		return "(empty notebook)", nil
	}
	start, end := 0, len(nb.Cells)
	if input.Offset != nil && *input.Offset > 0 {
		start = *input.Offset - 1
		if start < 0 {
			start = 0
		}
		if start > len(nb.Cells) {
			start = len(nb.Cells)
		}
		end = len(nb.Cells)
		if input.Limit != nil && *input.Limit > 0 {
			end = start + *input.Limit
			if end > len(nb.Cells) {
				end = len(nb.Cells)
			}
		}
	} else if input.Limit != nil && *input.Limit > 0 && *input.Limit < len(nb.Cells) {
		end = *input.Limit
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Jupyter notebook (%d cells", len(nb.Cells))
	if start != 0 || end != len(nb.Cells) {
		fmt.Fprintf(&b, ", showing %d-%d", start+1, end)
	}
	b.WriteString(")\n")
	for i := start; i < end; i++ {
		cell := nb.Cells[i]
		fmt.Fprintf(&b, "\n--- cell %d (%s) ---\n", i+1, cell.CellType)
		src := strings.TrimRight(cell.Source.String(), "\n")
		if src != "" {
			b.WriteString(src)
			b.WriteByte('\n')
		}
		for _, out := range cell.Outputs {
			text := strings.TrimSpace(out.Text.String())
			if text == "" && (out.Ename != "" || out.Evalue != "") {
				text = strings.TrimSpace(out.Ename + ": " + out.Evalue)
			}
			if text == "" {
				continue
			}
			fmt.Fprintf(&b, "[%s]\n%s\n", out.OutputType, text)
		}
	}
	return b.String(), nil
}
