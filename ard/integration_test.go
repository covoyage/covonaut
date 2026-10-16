package ard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/mcp"
)

var _ agentcore.Extension = (*DiscoveryExtension)(nil)
var _ agentcore.ToolProvider = (*DiscoveryExtension)(nil)

func TestFederationBudgetsAndCycles(t *testing.T) {
	var requests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		var input SearchRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Federation != FederationReferrals {
			t.Error("did not request explicit referrals")
		}
		if request.URL.Path == "/other/search" && input.PageToken != "" {
			t.Error("copied another registry's cursor")
		}
		referral := RegistryReferral{Identifier: "urn:air:example.com:registry", DisplayName: "Other", Type: TypeRegistry, URL: server.URL + "/other/search"}
		if request.URL.Path == "/other/search" {
			referral.URL = server.URL + "/search"
		}
		_ = json.NewEncoder(writer).Encode(SearchResponse{Results: []SearchResult{}, Referrals: []RegistryReferral{referral}, PageToken: "local-cursor"})
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	input := SearchRequest{Query: Query{Text: "help"}, PageToken: "root-cursor"}
	responses, err := client.SearchFederated(context.Background(), server.URL, input, FederationConfig{MaxHops: 3, MaxRegistries: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 2 || requests.Load() != 2 {
		t.Fatalf("cycle was followed: %v %d", responses, requests.Load())
	}
	responses, err = client.SearchFederated(context.Background(), server.URL, input, FederationConfig{MaxRegistries: 1})
	if err != nil || len(responses) != 1 {
		t.Fatalf("registry budget ignored: %v %v", responses, err)
	}
}

func TestDiscoveryExtensionIsReadOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input SearchRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Federation != FederationNone {
			t.Error("extension should not auto-federate")
		}
		_ = json.NewEncoder(writer).Encode(SearchResponse{Results: []SearchResult{}})
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	registries := map[string]string{"primary": server.URL}
	extension, err := NewDiscoveryExtension(ExtensionConfig{Client: client, Registries: registries})
	if err != nil {
		t.Fatal(err)
	}
	registries["primary"] = "https://unconfigured.example.com"
	if len(extension.Tools()) != 1 {
		t.Fatal("unexpected execution tools")
	}
	tool := extension.Tools()[0]
	if _, err := tool.Func(context.Background(), json.RawMessage(`{"query":"help"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Func(context.Background(), json.RawMessage(`{"query":"help","registry":"https://evil.example.com"}`)); err == nil {
		t.Fatal("accepted arbitrary registry")
	}
	if err := extension.Dispose(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Search(context.Background(), server.URL, SearchRequest{Query: Query{Text: "help"}, Federation: FederationNone}); err != nil {
		t.Fatal("disposed caller-owned client")
	}
}

func TestA2AResolutionAndAuthorization(t *testing.T) {
	var cards, executions atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/card" {
			cards.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]any{"name": "Demo", "supportedInterfaces": []any{map[string]any{"url": server.URL + "/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}}})
			return
		}
		executions.Add(1)
		_, _ = writer.Write([]byte("123456789"))
	}))
	defer server.Close()
	entry := fixtureEntry()
	entry.Data = nil
	entry.URL = server.URL + "/card"
	client := localClient()
	defer client.Close()
	if _, err := client.ResolveResource(context.Background(), entry, ResolveConfig{}); err == nil || cards.Load() != 0 {
		t.Fatal("unauthorized card fetch")
	}
	var purposes []string
	approval := func(_ context.Context, request AccessRequest) error {
		purposes = append(purposes, request.Purpose)
		return nil
	}
	resource, err := client.ResolveResource(context.Background(), entry, ResolveConfig{Authorize: approval})
	if err != nil {
		t.Fatal(err)
	}
	if resource.Endpoint.URL != server.URL+"/rpc" || resource.TrustStatus != "unverified" || executions.Load() != 0 {
		t.Fatal("descriptor resolution executed resource or guessed endpoint")
	}
	if _, err := client.ConnectA2A(context.Background(), resource, nil); err == nil {
		t.Fatal("unauthorized connection")
	}
	if _, err := client.ConnectA2A(context.Background(), resource, approval); err != nil {
		t.Fatal(err)
	}
	if strings.Join(purposes, ",") != "describe,connect" || executions.Load() != 0 {
		t.Fatalf("unexpected actions: %v", purposes)
	}
	blocked := NewClient(ClientConfig{})
	defer blocked.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	if _, err := blocked.executionHTTPClient().Do(request); err == nil {
		t.Fatal("execution bypassed network policy")
	}
	bounded := NewClient(ClientConfig{Policy: NetworkPolicy{AllowHTTP: true, AllowPrivate: true}, MaxResponseBytes: 4})
	defer bounded.Close()
	response, err := bounded.executionHTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("execution ignored size limit")
	}
}

func TestMCPBridgeUsesResolvedEndpoint(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/mcp" {
			t.Errorf("wrong MCP endpoint: %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		var rpc struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Error(err)
		}
		if rpc.ID == nil {
			writer.WriteHeader(202)
			return
		}
		result := map[string]any{}
		if rpc.Method == "initialize" {
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "demo", "version": "1"}}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	entry := fixtureEntry()
	entry.Type = TypeMCP
	approval := func(context.Context, AccessRequest) error { return nil }
	resource, err := client.ResolveResource(context.Background(), entry, ResolveConfig{Authorize: approval, Resolver: func(context.Context, Entry, json.RawMessage) (Endpoint, error) {
		return Endpoint{URL: server.URL + "/mcp", Binding: "MCP_HTTP"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ConnectMCP(context.Background(), resource, nil, mcp.HTTPConfig{}); err == nil || calls.Load() != 0 {
		t.Fatal("unauthorized MCP initialized")
	}
	connection, err := client.ConnectMCP(context.Background(), resource, approval, mcp.HTTPConfig{Endpoint: "http://ignored.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if calls.Load() < 1 {
		t.Fatal("MCP did not initialize")
	}
}

func TestSkillDescriptionOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("---\nname: demo\n---\n# Demo\nRun nothing automatically."))
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	entry := fixtureEntry()
	entry.Type = TypeSkill
	entry.Data = nil
	entry.URL = server.URL + "/SKILL.md"
	if _, err := client.DescribeSkill(context.Background(), entry, nil); err == nil {
		t.Fatal("unauthorized skill fetch")
	}
	text, err := client.DescribeSkill(context.Background(), entry, func(context.Context, AccessRequest) error { return nil })
	if err != nil || !strings.Contains(text, "# Demo") {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestTrustAndReservedFields(t *testing.T) {
	entry := fixtureEntry()
	entry.TrustManifest = json.RawMessage(`{"identity":"spiffe://other.example.com/demo"}`)
	if _, err := ValidateEntry(entry); err == nil {
		t.Fatal("publisher identity mismatch accepted")
	}
	entry = fixtureEntry()
	entry.Extra = map[string]json.RawMessage{"url": json.RawMessage(`"https://injected.example.com"`)}
	if _, err := json.Marshal(entry); err == nil {
		t.Fatal("reserved empty field injected")
	}
	denied := errors.New("denied")
	client := localClient()
	defer client.Close()
	if _, err := client.ResolveResource(context.Background(), fixtureEntry(), ResolveConfig{Authorize: func(context.Context, AccessRequest) error { return denied }}); !errors.Is(err, denied) {
		t.Fatal("authorization error lost")
	}
}

func TestRedirectRequiresApplicationApproval(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		destinationCalls.Add(1)
		_, _ = writer.Write([]byte(`{"name":"demo"}`))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := localClient()
	defer client.Close()
	denied := errors.New("redirect not approved")
	approval := func(_ context.Context, access AccessRequest) error {
		if strings.HasPrefix(access.URL, source.URL) {
			return nil
		}
		return denied
	}
	entry := fixtureEntry()
	entry.Data = nil
	entry.URL = source.URL + "/card"
	if _, err := client.ResolveResource(context.Background(), entry, ResolveConfig{Authorize: approval}); !errors.Is(err, denied) {
		t.Fatalf("redirect authorization lost: %v", err)
	}
	request, _ := http.NewRequest(http.MethodPost, source.URL+"/rpc", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	if _, err := client.authorizedHTTPClient(entry, approval, true).Do(request); !errors.Is(err, denied) {
		t.Fatalf("execution redirect authorization lost: %v", err)
	}
	if destinationCalls.Load() != 0 {
		t.Fatal("unapproved redirect target received request")
	}
}
