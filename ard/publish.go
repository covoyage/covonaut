package ard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
)

func NewManifestHandler(manifest Manifest) (http.Handler, error) {
	return NewManifestHandlerWithContext(context.Background(), manifest, ContextProcessor{})
}

func NewManifestHandlerWithContext(ctx context.Context, manifest Manifest, processor ContextProcessor) (http.Handler, error) {
	for _, entry := range manifest.Entries {
		if _, err := processor.Validate(ctx, entry); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("ETag", etag)
		writer.Header().Set("Cache-Control", "public, max-age=300")
		if request.Header.Get("If-None-Match") == etag {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		if request.Method == http.MethodGet {
			_, _ = writer.Write(data)
		}
	}), nil
}
