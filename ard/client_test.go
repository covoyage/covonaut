package ard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func localClient() *Client {
	return NewClient(ClientConfig{Policy: NetworkPolicy{AllowHTTP: true, AllowPrivate: true}, Timeout: time.Second})
}

func fixtureEntry() Entry {
	return Entry{Identifier: "urn:air:example.com:demo", DisplayName: "Demo", Type: TypeA2A, Data: json.RawMessage(`{"name":"Demo"}`), RepresentativeQueries: []string{"help me", "answer a question"}}
}

func TestContextExpansion(t *testing.T) {
	left, right := fixtureEntry(), fixtureEntry()
	left.Context = json.RawMessage(`{"ex":"https://example.com/ns#"}`)
	left.Extra = map[string]json.RawMessage{"ex:region": json.RawMessage(`"eu"`)}
	right.Context = json.RawMessage(`{"other":"https://example.com/ns#"}`)
	right.Extra = map[string]json.RawMessage{"other:region": json.RawMessage(`"eu"`)}
	processor := ContextProcessor{}
	first, err := processor.Expand(context.Background(), left)
	if err != nil {
		t.Fatal(err)
	}
	second, err := processor.Expand(context.Background(), right)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("prefixes changed expanded terms: %v != %v", first, second)
	}
	node := first[0].(map[string]any)
	if _, ok := node[Namespace+"mediaType"]; !ok {
		t.Fatal("type did not resolve through base context")
	}
	if _, ok := node["https://example.com/ns#region"]; !ok {
		t.Fatal("extension IRI missing")
	}
	left.Context = json.RawMessage(`"https://example.com/context"`)
	if _, err := processor.Expand(context.Background(), left); err == nil {
		t.Fatal("remote context should require a client")
	}
}

func TestPublishAndDiscover(t *testing.T) {
	handler, err := NewManifestHandler(Manifest{Entries: []Entry{fixtureEntry()}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case ManifestPath:
			handler.ServeHTTP(writer, request)
		case "/":
			writer.Header().Set("Content-Type", "text/html")
			_, _ = writer.Write([]byte(`<link rel="alternate ard" href="/extra">`))
		case "/extra":
			_ = json.NewEncoder(writer).Encode(Manifest{Entries: []Entry{fixtureEntry()}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	manifest, err := client.Discover(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 1 {
		t.Fatalf("entries not deduplicated: %d", len(manifest.Entries))
	}
	request := httptest.NewRequest(http.MethodGet, ManifestPath, nil)
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	request.Header.Set("If-None-Match", writer.Header().Get("ETag"))
	writer = httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	if writer.Code != http.StatusNotModified || writer.Body.Len() != 0 {
		t.Fatal("conditional request failed")
	}
	writer = httptest.NewRecorder()
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodHead, ManifestPath, nil))
	if writer.Code != 200 || writer.Body.Len() != 0 {
		t.Fatal("HEAD failed")
	}
	writer = httptest.NewRecorder()
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, ManifestPath, nil))
	if writer.Code != 405 {
		t.Fatal("accepted POST")
	}
}

func TestHTMLDiscovery(t *testing.T) {
	entry := fixtureEntry()
	entry.Context = json.RawMessage(`"` + BaseContextURL + `"`)
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == ManifestPath {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<script type="application/ld+json">` + string(encoded) + `</script>`))
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	manifest, err := client.Discover(context.Background(), server.URL)
	if err != nil || len(manifest.Entries) != 1 {
		t.Fatalf("manifest=%v err=%v", manifest, err)
	}
}

func TestSearchAndReferralEndpoint(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		count.Add(1)
		if request.URL.Path != "/api/search" {
			t.Errorf("unexpected path: %s", request.URL.Path)
		}
		if request.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", request.Method)
		}
		var input SearchRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.PageToken != "opaque" {
			t.Error("page token changed")
		}
		_, _ = writer.Write([]byte(`{"results":[{"identifier":"urn:air:example.com:demo","score":0}],"pageToken":"next"}`))
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	input := SearchRequest{Query: Query{Text: "help", Filter: map[string]json.RawMessage{"type": json.RawMessage(`"application/a2a-agent-card+json"`)}}, PageToken: "opaque"}
	result, err := client.Search(context.Background(), server.URL+"/api/", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.PageToken != "next" || result.Results[0].Score == nil || *result.Results[0].Score != 0 {
		t.Fatal("search metadata lost")
	}
	if _, err := client.SearchEndpoint(context.Background(), server.URL+"/api/search", input); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 2 {
		t.Fatal("unexpected number of requests")
	}
}

func TestNetworkPolicy(t *testing.T) {
	client := NewClient(ClientConfig{})
	defer client.Close()
	for _, target := range []string{"http://example.com", "https://127.0.0.1", "https://[::ffff:127.0.0.1]", "https://user:pass@example.com"} {
		if _, err := client.FetchManifest(context.Background(), target); err == nil {
			t.Fatalf("accepted %s", target)
		}
	}
	for _, raw := range []string{"100.100.100.200", "169.254.169.254", "10.1.2.3", "::1", "64:ff9b::7f00:1"} {
		if client.allowedIP(netip.MustParseAddr(raw)) {
			t.Fatalf("accepted %s", raw)
		}
	}
	if !client.allowedIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("blocked public address")
	}
}

func TestRedirectCredentialsAndPolicy(t *testing.T) {
	var received string
	destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received = request.Header.Get("Authorization")
		_ = json.NewEncoder(writer).Encode(Manifest{Entries: []Entry{}})
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	client := NewClient(ClientConfig{Policy: NetworkPolicy{AllowHTTP: true, AllowPrivate: true}, HeadersByOrigin: map[string]http.Header{source.URL: {"Authorization": {"Bearer secret"}}}})
	defer client.Close()
	if _, err := client.FetchManifest(context.Background(), source.URL); err != nil {
		t.Fatal(err)
	}
	if received != "" {
		t.Fatal("credentials leaked to another origin")
	}
	blocked := NewClient(ClientConfig{Policy: NetworkPolicy{AllowHTTP: true, AllowPrivate: true, AllowedHosts: []string{"localhost"}}})
	defer blocked.Close()
	if _, err := blocked.FetchManifest(context.Background(), source.URL); err == nil {
		t.Fatal("host allowlist not enforced")
	}
}

func TestLimitsErrorsAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/error" {
			writer.WriteHeader(429)
			_, _ = writer.Write([]byte(`{"errorCode":"RATE_LIMIT_EXCEEDED","message":"slow down"}`))
			return
		}
		if request.URL.Path == "/invalid/search" {
			_, _ = writer.Write([]byte(`{"results":[{"identifier":"urn:air:example.com:demo","score":101}]}`))
			return
		}
		_, _ = writer.Write([]byte(strings.Repeat("x", 65)))
	}))
	defer server.Close()
	client := NewClient(ClientConfig{Policy: NetworkPolicy{AllowHTTP: true, AllowPrivate: true}, MaxResponseBytes: 64})
	defer client.Close()
	if _, err := client.FetchManifest(context.Background(), server.URL); err == nil {
		t.Fatal("size limit not enforced")
	}
	_, err := client.FetchManifest(context.Background(), server.URL+"/error")
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.StatusCode != 429 || protocolErr.ErrorCode != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("bad error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.FetchManifest(ctx, server.URL); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	largeClient := localClient()
	defer largeClient.Close()
	if _, err := largeClient.Search(context.Background(), server.URL+"/invalid", SearchRequest{Query: Query{Text: "help"}}); err == nil {
		t.Fatal("accepted invalid score")
	}
}

func TestStandardProtocolErrors(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
	}{
		{http.StatusBadRequest, "INVALID_ARGUMENT"},
		{http.StatusUnauthorized, "UNAUTHENTICATED"},
		{http.StatusNotFound, "NOT_FOUND"},
		{http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED"},
		{http.StatusInternalServerError, "INTERNAL_ERROR"},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				if err := json.NewEncoder(writer).Encode(ProtocolError{ErrorCode: test.code, Message: "registry detail"}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client := localClient()
			defer client.Close()
			_, err := client.SearchEndpoint(t.Context(), server.URL, SearchRequest{Query: Query{Text: "help"}})
			var protocolErr *ProtocolError
			if !errors.As(err, &protocolErr) || protocolErr.StatusCode != test.status || protocolErr.ErrorCode != test.code || protocolErr.Message != "registry detail" {
				t.Fatalf("protocol error fields lost: %v", err)
			}
		})
	}
}

func TestRemoteContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"@context":{"ex":"https://example.com/ns#"}}`))
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	entry := fixtureEntry()
	entry.Context, _ = json.Marshal(server.URL)
	entry.Extra = map[string]json.RawMessage{"ex:region": json.RawMessage(`"eu"`)}
	if _, err := (ContextProcessor{Client: client}).Expand(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
}

func TestCoreContextSemantics(t *testing.T) {
	entry := fixtureEntry()
	entry.Context = json.RawMessage(`{"identifier":"https://example.com/not-an-identifier"}`)
	if _, err := (ContextProcessor{}).Validate(context.Background(), entry); err == nil {
		t.Fatal("context override bypassed validation")
	}
	entry = fixtureEntry()
	entry.Identifier = ""
	entry.Context = json.RawMessage(`{"resourceID":"ard:identifier"}`)
	entry.Extra = map[string]json.RawMessage{"resourceID": json.RawMessage(`"urn:air:example.com:demo"`)}
	entry.Metadata = json.RawMessage(`{"nested":{"identifier":"opaque","value":9007199254740993}}`)
	core, err := (ContextProcessor{}).Core(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if core.Identifier != "urn:air:example.com:demo" {
		t.Fatalf("alias lost: %+v", core)
	}
	if !strings.Contains(string(core.Metadata), "9007199254740993") {
		t.Fatalf("opaque JSON precision lost: %s", core.Metadata)
	}
	if _, err := ValidateEntry(core); err != nil {
		t.Fatal(err)
	}
}

func TestSearchRejectsNullAndMixedFiltersBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	for _, value := range []string{"null", " \n null \t", `["finance",null]`, `[null]`, `["finance",42]`, `true`, `{}`} {
		_, err := client.Search(context.Background(), server.URL, SearchRequest{Query: Query{Text: "help", Filter: map[string]json.RawMessage{"tags": json.RawMessage(value)}}})
		if err == nil {
			t.Fatalf("accepted invalid filter %s", value)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid filters reached registry")
	}
	for _, value := range []string{`"finance"`, `["finance","travel"]`, `[]`} {
		if _, err := client.Search(context.Background(), server.URL, SearchRequest{Query: Query{Text: "help", Filter: map[string]json.RawMessage{"tags": json.RawMessage(value)}}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTMLDiscoveryAliasesGraphsAndOpaqueData(t *testing.T) {
	markup := `<script type="application/ld+json">{"@context":{"@vocab":"https://schema.org/"},"@type":"Organization","name":"Unrelated"}</script>
	<script type="application/ld+json">not JSON</script>
	<script type="application/ld+json">{
		"@context":["https://agenticresourcediscovery.org/context/v1",{"handle":"ard:identifier"}],
		"@id":"https://example.com/catalog", "@graph":[
			{"handle":"urn:air:example.com:first","displayName":"First","type":"application/a2a-agent-card+json","data":{"nested":{"identifier":"urn:air:example.com:not-a-resource"},"large":9007199254740993}},
			{"@included":[{"handle":"urn:air:example.com:second","displayName":"Second","type":"application/mcp-server-card+json","url":"https://example.com/card"}]}
		]
	}</script>`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == ManifestPath {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(markup))
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	manifest, err := client.Discover(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 2 || manifest.Entries[0].Identifier != "urn:air:example.com:first" || manifest.Entries[1].Identifier != "urn:air:example.com:second" {
		t.Fatalf("missing aliased graph resources: %+v", manifest)
	}
	if !strings.Contains(string(manifest.Entries[0].Data), "9007199254740993") {
		t.Fatal("opaque number rounded")
	}
}

func TestHTMLDiscoveryRespectsDocumentBase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case ManifestPath:
			http.NotFound(writer, request)
		case "/":
			writer.Header().Set("Content-Type", "text/html")
			_, _ = writer.Write([]byte(`<link rel="ard" href="manifest.json"><base href="/catalog/">`))
		case "/catalog/manifest.json":
			_ = json.NewEncoder(writer).Encode(Manifest{Entries: []Entry{fixtureEntry()}})
		default:
			t.Errorf("unexpected discovery path: %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	if _, err := client.Discover(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
}
