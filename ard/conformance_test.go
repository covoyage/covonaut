package ard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestOfficialARDBaseContext(t *testing.T) {
	root := os.Getenv("ARD_SPEC_DIR")
	if root == "" {
		t.Skip("set ARD_SPEC_DIR to the official ard-spec checkout")
	}
	data, err := os.ReadFile(filepath.Join(root, "spec", "schemas", "ard.context.jsonld"))
	if err != nil {
		t.Fatal(err)
	}
	var official, pinned any
	if err := json.Unmarshal(data, &official); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(baseContextJSON), &pinned); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(official, pinned) {
		t.Fatal("pinned ARD base context differs from the official artifact")
	}
}

func TestOfficialARDSchemaConstraints(t *testing.T) {
	root, binary := os.Getenv("ARD_SPEC_DIR"), os.Getenv("ARD_SCHEMA_PYTHON")
	if root == "" || binary == "" {
		t.Skip("set ARD_SPEC_DIR and ARD_SCHEMA_PYTHON with jsonschema installed")
	}
	inline := map[string]any{"identifier": "urn:air:example.com:demo", "displayName": "Demo", "type": "application/json", "data": map[string]any{}}
	for _, test := range []struct {
		name       string
		definition string
		change     func(map[string]any)
		valid      bool
	}{
		{"inline", "ArdEntry", nil, true},
		{"reference", "ArdEntry", func(entry map[string]any) { delete(entry, "data"); entry["url"] = "https://example.com/card" }, true},
		{"open-namespace", "ArdEntry", func(entry map[string]any) {
			entry["@context"] = map[string]any{"ex": "https://example.com/ns#"}
			entry["ex:custom"] = map[string]any{"nested": true}
		}, true},
		{"queries-are-recommended", "ArdEntry", func(entry map[string]any) { entry["representativeQueries"] = []string{"one example"} }, true},
		{"empty-optional-arrays", "ArdEntry", func(entry map[string]any) { entry["tags"] = []string{}; entry["capabilities"] = []string{} }, true},
		{"missing-identifier", "ArdEntry", func(entry map[string]any) { delete(entry, "identifier") }, false},
		{"missing-name", "ArdEntry", func(entry map[string]any) { delete(entry, "displayName") }, false},
		{"missing-type", "ArdEntry", func(entry map[string]any) { delete(entry, "type") }, false},
		{"missing-delivery", "ArdEntry", func(entry map[string]any) { delete(entry, "data") }, false},
		{"both-deliveries", "ArdEntry", func(entry map[string]any) { entry["url"] = "https://example.com/card" }, false},
		{"null-reference", "ArdEntry", func(entry map[string]any) { delete(entry, "data"); entry["url"] = nil }, false},
		{"null-data", "ArdEntry", func(entry map[string]any) { entry["data"] = nil }, false},
		{"array-data", "ArdEntry", func(entry map[string]any) { entry["data"] = []any{} }, false},
		{"invalid-identifier", "ArdEntry", func(entry map[string]any) { entry["identifier"] = "not-an-air-urn" }, false},
		{"non-string-query", "ArdEntry", func(entry map[string]any) { entry["representativeQueries"] = []any{42} }, false},
		{"manifest", "ArdManifest", func(entry map[string]any) {
			value := make(map[string]any, len(entry))
			for key, child := range entry {
				value[key] = child
				delete(entry, key)
			}
			entry["entries"] = []any{value}
			entry["transport-extension"] = true
		}, true},
		{"empty-manifest", "ArdManifest", func(entry map[string]any) { clear(entry); entry["entries"] = []any{} }, true},
		{"missing-entries", "ArdManifest", func(entry map[string]any) { clear(entry) }, false},
		{"schema-lowercase-trust-envelope-is-unchecked", "ArdEntry", func(entry map[string]any) { entry["trustManifest"] = map[string]any{} }, true},
		{"schema-capitalized-trust-envelope-is-checked", "ArdEntry", func(entry map[string]any) { entry["TrustManifest"] = map[string]any{} }, false},
		{"schema-nested-metadata-is-rejected", "ArdEntry", func(entry map[string]any) {
			entry["metadata"] = map[string]any{"nested": map[string]any{"value": true}}
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := make(map[string]any, len(inline))
			for key, value := range inline {
				entry[key] = value
			}
			if test.change != nil {
				test.change(entry)
			}
			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, binary, "-c", `import json, sys
import jsonschema
schema = json.load(open(sys.argv[1]))
schema["$ref"] = "#/$defs/" + sys.argv[2]
jsonschema.Draft202012Validator.check_schema(schema)
validator = jsonschema.Draft202012Validator(schema)
try:
    validator.validate(json.loads(sys.argv[3]))
except jsonschema.ValidationError as error:
    print("invalid: " + error.message)
    sys.exit(1)
print("valid")
`, filepath.Join(root, "spec", "schemas", "ard-entry.schema.json"), test.definition, string(data)).CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			accepted := err == nil && strings.TrimSpace(string(output)) == "valid"
			if !accepted && !bytes.HasPrefix(output, []byte("invalid: ")) {
				t.Fatalf("schema validator did not run: %v\n%s", err, output)
			}
			if accepted != test.valid {
				t.Fatalf("schema validation expected valid=%v, error=%v\n%s", test.valid, err, output)
			}
		})
	}
}

func TestOfficialARDCDDLConformance(t *testing.T) {
	root, binary := os.Getenv("ARD_SPEC_DIR"), os.Getenv("ARD_CDDL_BIN")
	if root == "" || binary == "" {
		t.Skip("set ARD_SPEC_DIR and ARD_CDDL_BIN for official grammar validation")
	}
	grammarPath := filepath.Join(root, "spec", "schemas", "ard.cddl")
	reference := fixtureEntry()
	reference.URL = "https://example.com/card"
	reference.Data = nil
	inline := fixtureEntry()
	valid, err := json.Marshal(Manifest{Entries: []Entry{reference, inline}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"url-and-inline", valid, true},
		{"empty", []byte(`{"entries":[]}`), true},
		{"missing-entries", []byte(`{}`), false},
		{"missing-identifier", []byte(`{"entries":[{"displayName":"Demo","type":"application/json","data":{}}]}`), false},
		{"null-reference", []byte(`{"entries":[{"identifier":"urn:air:example.com:demo","displayName":"Demo","type":"application/json","url":null}]}`), false},
		{"upstream-wildcard-allows-both-deliveries", []byte(`{"entries":[{"identifier":"urn:air:example.com:demo","displayName":"Demo","type":"application/json","url":"https://example.com/card","data":{}}]}`), true},
		{"upstream-rejects-empty-optional-arrays", []byte(`{"entries":[{"identifier":"urn:air:example.com:demo","displayName":"Demo","type":"application/json","data":{},"tags":[],"capabilities":[]}]}`), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, test.data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, binary, "validate", "--cddl", grammarPath, "--json", path).CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			accepted := err == nil && bytes.Contains(output, []byte("Root type for validation: start")) && !bytes.Contains(output, []byte("[ERROR]"))
			if accepted != test.valid {
				t.Fatalf("CDDL validation: expected valid=%v, error=%v\n%s", test.valid, err, output)
			}
			if err != nil {
				if _, ok := err.(*exec.ExitError); !ok {
					t.Fatal(err)
				}
			}
		})
	}
}

type conformanceTransport struct{ root string }

func (transport conformanceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	path := strings.TrimPrefix(request.URL.Path, "/json-ld-api/tests/")
	if !filepath.IsLocal(path) {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(filepath.Join(transport.root, filepath.FromSlash(path)))
	if err != nil {
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("not found")), Request: request}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/ld+json"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
}

func TestW3CExpansionSuite(t *testing.T) {
	root := os.Getenv("ARD_W3C_TEST_DIR")
	if root == "" {
		t.Skip("set ARD_W3C_TEST_DIR to the official json-ld-api/tests directory")
	}
	data, err := os.ReadFile(filepath.Join(root, "expand-manifest.jsonld"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		BaseIRI  string `json:"baseIri"`
		Sequence []struct {
			ID        string `json:"@id"`
			Name      string `json:"name"`
			Input     string `json:"input"`
			Expect    string `json:"expect"`
			ErrorCode string `json:"expectErrorCode"`
			Option    struct {
				Base           *string `json:"base"`
				SpecVersion    string  `json:"specVersion"`
				ProcessingMode string  `json:"processingMode"`
				ExpandContext  any     `json:"expandContext"`
			} `json:"option"`
		} `json:"sequence"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sequence) == 0 {
		t.Fatal("empty W3C manifest")
	}
	for _, test := range manifest.Sequence {
		t.Run(strings.TrimPrefix(test.ID, "#")+"/"+test.Name, func(t *testing.T) {
			if test.Option.ProcessingMode == "json-ld-1.0" || test.Option.SpecVersion == "json-ld-1.0" {
				t.Skip("official JSON-LD 1.0-only case is outside the ARD 1.1 profile")
			}
			readJSON := func(path string) any {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
				if err != nil {
					t.Fatal(err)
				}
				var value any
				decoder := json.NewDecoder(bytes.NewReader(data))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			client := NewClient(ClientConfig{Policy: NetworkPolicy{AllowHTTP: true}})
			defer client.Close()
			client.http = &http.Client{Transport: conformanceTransport{root: root}}
			expansion := contextExpansion{ctx: t.Context(), client: client, remaining: 64, loading: map[string]bool{}}
			active := entryContext{base: manifest.BaseIRI + test.Input, terms: map[string]contextTerm{}}
			if test.Option.Base != nil {
				active.base = *test.Option.Base
			}
			expansion.initial = active.clone()
			var err error
			if test.Option.ExpandContext != nil {
				contextValue := test.Option.ExpandContext
				if path, ok := contextValue.(string); ok {
					contextValue = readJSON(path).(map[string]any)["@context"]
				}
				active, err = expansion.apply(active, contextValue, "", 0)
			}
			var actual []any
			if err == nil {
				actual, err = expansion.document(readJSON(test.Input), active, 0)
			}
			if test.ErrorCode != "" {
				if err == nil {
					t.Fatalf("expected %s, expansion succeeded: %v", test.ErrorCode, actual)
				}
				var coded *JSONLDError
				if !errors.As(err, &coded) || coded.Code != test.ErrorCode {
					t.Fatalf("expected JSON-LD error code %q, got %v", test.ErrorCode, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := readJSON(test.Expect)
			if len(actual) == 0 {
				actual = []any{}
			}
			if !reflect.DeepEqual(normalizeConformance(actual, false), normalizeConformance(expected, false)) {
				t.Fatalf("expansion mismatch\ngot: %v\nwant: %v", actual, expected)
			}
		})
	}
}

func normalizeConformance(value any, ordered bool) any {
	switch item := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(item))
		for key, child := range item {
			if key == "@value" {
				result[key] = child
			} else {
				result[key] = normalizeConformance(child, key == "@list")
			}
		}
		return result
	case []any:
		result := make([]any, len(item))
		for index, child := range item {
			result[index] = normalizeConformance(child, false)
		}
		if !ordered {
			sort.SliceStable(result, func(first, second int) bool {
				left, _ := json.Marshal(result[first])
				right, _ := json.Marshal(result[second])
				return string(left) < string(right)
			})
		}
		return result
	default:
		return value
	}
}
