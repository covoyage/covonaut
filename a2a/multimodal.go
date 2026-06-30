package a2a

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// File Upload / Download helpers for multimodal content
// ---------------------------------------------------------------------------

// UploadFile reads a local file and returns a FilePart with base64-encoded content.
func UploadFile(path string) (*FilePart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	mime := http.DetectContentType(data)
	if mime == "application/octet-stream" {
		mime = detectMIMEType(path)
	}
	return &FilePart{
		Name:     filepath.Base(path),
		MIMEType: mime,
		Bytes:    base64.StdEncoding.EncodeToString(data),
	}, nil
}

// UploadFileFromReader reads from an io.Reader and returns a FilePart.
func UploadFileFromReader(r io.Reader, name, mimeType string) (*FilePart, error) {
	data, err := io.ReadAll(io.LimitReader(r, 50<<20))
	if err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	return &FilePart{
		Name:     name,
		MIMEType: mimeType,
		Bytes:    base64.StdEncoding.EncodeToString(data),
	}, nil
}

// DownloadFile decodes a base64-encoded FilePart and writes it to disk.
func DownloadFile(part *FilePart, destPath string) error {
	if part.Bytes == "" {
		return fmt.Errorf("file part has no bytes")
	}

	data, err := base64.StdEncoding.DecodeString(part.Bytes)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}

	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

var safeHTTPClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		DisableKeepAlives:   false,
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

// DownloadFileFromURI downloads a file from a URI and returns its bytes.
func DownloadFileFromURI(ctx context.Context, uri string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	resp, err := safeHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %d", uri, resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}

// detectMIMEType guesses the MIME type from file extension.
func detectMIMEType(path string) string {
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
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".html", ".htm":
		return "text/html"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".zip":
		return "application/zip"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	default:
		return "application/octet-stream"
	}
}

// NewFilePartFromPath creates a Part from a local file path.
func NewFilePartFromPath(path string) (Part, error) {
	fp, err := UploadFile(path)
	if err != nil {
		return Part{}, err
	}
	return Part{Type: PartTypeFile, File: fp}, nil
}

// NewFilePartFromReader creates a Part from an io.Reader.
func NewFilePartFromReader(r io.Reader, name, mimeType string) (Part, error) {
	fp, err := UploadFileFromReader(r, name, mimeType)
	if err != nil {
		return Part{}, err
	}
	return Part{Type: PartTypeFile, File: fp}, nil
}
