package ard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOfficialARDManifestConformance(t *testing.T) {
	binary := os.Getenv("ARD_CONFORMANCE_BIN")
	if binary == "" {
		t.Skip("set ARD_CONFORMANCE_BIN to an existing official conformance/bin/conformance-test executable")
	}
	reference := fixtureEntry()
	reference.URL = "https://example.com/card"
	reference.Data = nil
	inline := fixtureEntry()
	inline.Identifier += "-inline"
	inline.URL = ""
	inline.Data = json.RawMessage(`{"name":"Inline"}`)
	handler, err := NewManifestHandler(Manifest{Entries: []Entry{reference, inline}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "manifest", server.URL+ManifestPath).CombinedOutput()
	if err != nil {
		t.Fatalf("official manifest validation failed: %v\n%s", err, output)
	}
	for _, required := range []string{"Manifest validates against ArdManifest.", "All 2 entries validate against ArdEntry.", "CONFORMANCE STATUS: PASS"} {
		if !strings.Contains(string(output), required) {
			t.Fatalf("official validator did not perform all required checks (%q); schema skipping is not a pass:\n%s", required, output)
		}
	}
	t.Log(string(output))
}

func TestEntryRoundTrip(t *testing.T) {
	raw := []byte(`{"identifier":"urn:air:example.com:agent:demo","displayName":"Demo","type":"application/a2a-agent-card+json","url":"https://example.com/card","@context":{"ex":"https://example.com/ns#"},"ex:region":["eu"],"trustManifest":{"identity":"spiffe://example.com/demo","signature":"original"}}`)
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateEntry(entry); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip changed entry: %s", encoded)
	}
}

func TestValidation(t *testing.T) {
	entry := Entry{Identifier: "urn:air:example.com:demo", DisplayName: "Demo", Type: TypeA2A, URL: "https://example.com/card"}
	warnings, err := ValidateEntry(entry)
	if err != nil || len(warnings) != 1 {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
	entry.Data = json.RawMessage(`{}`)
	if _, err := ValidateEntry(entry); err == nil {
		t.Fatal("accepted both url and data")
	}
	entry.URL = ""
	if _, err := ValidateEntry(entry); err != nil {
		t.Fatal(err)
	}
	entry.Data = json.RawMessage(`null`)
	if _, err := ValidateEntry(entry); err == nil {
		t.Fatal("accepted null data")
	}
	for _, identifier := range []string{"demo", "urn:air:localhost:demo", "urn:air:-example.com:demo"} {
		if _, err := Publisher(identifier); err == nil {
			t.Fatalf("accepted %s", identifier)
		}
	}
}

func TestSparseSearchResult(t *testing.T) {
	var response SearchResponse
	if err := json.Unmarshal([]byte(`{"results":[{"identifier":"urn:air:example.com:demo","score":0,"source":"https://registry.example.com","custom":true}]}`), &response); err != nil {
		t.Fatal(err)
	}
	result := response.Results[0]
	if result.Score == nil || *result.Score != 0 || result.URL != "" || string(result.Extra["custom"]) != "true" {
		t.Fatalf("bad result: %+v", result)
	}
	if _, err := ValidateEntry(result.Entry); err == nil {
		t.Fatal("sparse result should not validate as a complete entry")
	}
	if _, err := json.Marshal(response); err != nil {
		t.Fatal(err)
	}
}

func TestEntryValueOrReferencePresence(t *testing.T) {
	for _, content := range []string{`"url":"","data":{}`, `"url":null,"data":{}`} {
		t.Run(content, func(t *testing.T) {
			input := []byte(`{"identifier":"urn:air:example.com:demo","displayName":"Demo","type":"application/a2a-agent-card+json",` + content + `}`)
			var entry Entry
			if err := json.Unmarshal(input, &entry); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			if err := json.Unmarshal(input, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("empty url was lost in round trip: %s", encoded)
			}
			if _, err := ValidateEntry(entry); err == nil {
				t.Fatal("accepted both url and data because url was empty")
			}
			if _, err := (ContextProcessor{}).Validate(context.Background(), entry); err == nil {
				t.Fatal("expansion accepted both url and data because url was empty")
			}
			if _, err := NewManifestHandler(Manifest{Entries: []Entry{entry}}); err == nil {
				t.Fatal("published both url and data because url was empty")
			}
		})
	}
}

func TestEntryMediaTypeRestrictedNames(t *testing.T) {
	for _, mediaType := range []string{
		"application/*", "*/json", "*/*", "application/.json",
		"application/json*", "application/json%", "application/json'",
		strings.Repeat("a", 128) + "/json", "application/" + strings.Repeat("a", 128),
	} {
		t.Run(mediaType, func(t *testing.T) {
			entry := fixtureEntry()
			entry.Type = mediaType
			if _, err := ValidateEntry(entry); err == nil {
				t.Fatal("accepted a MIME token that is not an RFC 6838 restricted name")
			}
			if _, err := (ContextProcessor{}).Validate(context.Background(), entry); err == nil {
				t.Fatal("expansion accepted an invalid resource media type")
			}
			if _, err := NewManifestHandler(Manifest{Entries: []Entry{entry}}); err == nil {
				t.Fatal("published an invalid resource media type")
			}
		})
	}
	for _, mediaType := range []string{
		"application/vnd.acme-card+json", "APPLICATION/JSON",
		`text/markdown; profile="urn:air:agent-skills"`,
		"application/a!b#c$d&e^f_g.h+i-j", "application/" + strings.Repeat("a", 127),
	} {
		t.Run(mediaType, func(t *testing.T) {
			entry := fixtureEntry()
			entry.Type = mediaType
			if _, err := ValidateEntry(entry); err != nil {
				t.Fatalf("rejected a valid extension media type: %v", err)
			}
			if _, err := NewManifestHandler(Manifest{Entries: []Entry{entry}}); err != nil {
				t.Fatalf("could not publish a valid extension media type: %v", err)
			}
		})
	}
}

func TestManifest(t *testing.T) {
	for _, raw := range []string{`{}`, `{"entries":null}`, `{"entries":{}}`} {
		var manifest Manifest
		if err := json.Unmarshal([]byte(raw), &manifest); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(`{"entries":[],"version":"custom"}`), &manifest); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"entries":[],"version":"custom"}` {
		t.Fatalf("got %s", encoded)
	}
}

func TestEntryIdentityBinding(t *testing.T) {
	entry := fixtureEntry()
	for _, identifier := range []string{"relative", "_:blank", "urn:air:other.example:demo", "urn:air:example.com:other"} {
		entry.ID = identifier
		if _, err := ValidateEntry(entry); err == nil {
			t.Fatalf("accepted contradictory or unresolved @id %q", identifier)
		}
	}
	entry.ID = entry.Identifier
	if _, err := ValidateEntry(entry); err != nil {
		t.Fatal(err)
	}
	entry.ID = "https://example.com/resources/demo"
	warnings, err := ValidateEntry(entry)
	if err != nil || len(warnings) != 1 {
		t.Fatalf("resource IRI equivalence was silently assumed: %v %v", warnings, err)
	}
}
