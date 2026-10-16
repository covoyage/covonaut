package ard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func expandedNode(t *testing.T, entry Entry) map[string]any {
	t.Helper()
	result, err := (ContextProcessor{}).Expand(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	return result[0].(map[string]any)
}

func TestContextArraysAndForwardDefinitions(t *testing.T) {
	entry := fixtureEntry()
	entry.Context = json.RawMessage(`[
		"https://agenticresourcediscovery.org/context/v1",
		{"region":{"@id":"ext:region"},"ext":{"@id":"https://example.com/vocab#","@prefix":true}},
		{"second":"https://example.com/vocab#"}
	]`)
	entry.Extra = map[string]json.RawMessage{"region": json.RawMessage(`"eu"`), "second:region": json.RawMessage(`"us"`)}
	node := expandedNode(t, entry)
	values := node["https://example.com/vocab#region"].([]any)
	if len(values) != 2 {
		t.Fatalf("IRI-equivalent values not combined: %v", node)
	}
	if _, ok := node[Namespace+"identifier"]; !ok {
		t.Fatal("base context lost")
	}
	entry.Context = json.RawMessage(`{"@vocab":"ext:","ext":"https://example.com/vocab#","zone":{}}`)
	entry.Extra = map[string]json.RawMessage{"zone": json.RawMessage(`"eu"`)}
	if _, ok := expandedNode(t, entry)["https://example.com/vocab#zone"]; !ok {
		t.Fatal("compact vocabulary was not resolved")
	}
}

func TestContextCoercionAndOpaqueJSON(t *testing.T) {
	entry := fixtureEntry()
	entry.Context = json.RawMessage(`{
		"ex":"https://example.com/vocab#",
		"link":{"@id":"ex:link","@type":"@id"},
		"quantity":{"@id":"ex:quantity","@type":"http://www.w3.org/2001/XMLSchema#integer"},
		"payload":{"@id":"ex:payload","@type":"@json"},
		"ordered":{"@id":"ex:ordered","@container":"@list"},
		"label":{"@id":"ex:label","@language":"ZH-CN"}
	}`)
	entry.Extra = map[string]json.RawMessage{
		"link":     json.RawMessage(`"https://example.com/resource"`),
		"quantity": json.RawMessage(`9007199254740993`),
		"payload":  json.RawMessage(`{"@context":null,"type":"literal","large":9007199254740993}`),
		"ordered":  json.RawMessage(`["first","second"]`),
		"label":    json.RawMessage(`"label"`),
	}
	node := expandedNode(t, entry)
	link := node["https://example.com/vocab#link"].([]any)[0].(map[string]any)
	if link["@id"] != "https://example.com/resource" {
		t.Fatal("IRI coercion lost")
	}
	quantity := node["https://example.com/vocab#quantity"].([]any)[0].(map[string]any)
	if quantity["@value"] != json.Number("9007199254740993") {
		t.Fatal("large number rounded")
	}
	payload := node["https://example.com/vocab#payload"].([]any)[0].(map[string]any)
	if payload["@type"] != "@json" || payload["@value"].(map[string]any)["@context"] != nil {
		t.Fatal("opaque JSON interpreted as a context")
	}
	ordered := node["https://example.com/vocab#ordered"].([]any)[0].(map[string]any)
	if len(ordered["@list"].([]any)) != 2 {
		t.Fatal("list ordering lost")
	}
	label := node["https://example.com/vocab#label"].([]any)[0].(map[string]any)
	if label["@language"] != "zh-cn" {
		t.Fatal("language tag lost")
	}
}

func TestContextIDAndCanonicalRoundTrip(t *testing.T) {
	entry := fixtureEntry()
	entry.ID = entry.Identifier
	entry.Context = json.RawMessage(`{"ex":"https://example.com/vocab#"}`)
	entry.Extra = map[string]json.RawMessage{"ex:region": json.RawMessage(`"eu"`)}
	core, err := (ContextProcessor{}).Core(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if core.ID != entry.ID || len(core.Extra) != 1 {
		t.Fatalf("ID or extension lost: %+v", core)
	}
	first := expandedNode(t, entry)
	second := expandedNode(t, core)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("canonical round trip changed graph: %v != %v", first, second)
	}
	entry.Context = json.RawMessage(`{"@base":"https://example.com/"}`)
	entry.ID = "agent"
	if expandedNode(t, entry)["@id"] != "https://example.com/agent" {
		t.Fatal("@base not applied")
	}
}

func TestUnsupportedContextFeaturesFailClosed(t *testing.T) {
	for _, raw := range []string{
		`42`, `{"@version":1.0}`, `{"@import":"https://example.com/context"}`,
		`{"@protected":"yes"}`, `{"ex":{"@id":"https://example.com/ns#","@prefix":"yes"}}`,
		`{"term":{"@reverse":"https://example.com/term","@id":"https://example.com/other"}}`,
		`{"term":{"@id":"https://example.com/term","@container":"invalid"}}`,
		`{"one":"two","two":"one"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			entry := fixtureEntry()
			entry.Context = json.RawMessage(raw)
			if _, err := (ContextProcessor{}).Expand(context.Background(), entry); err == nil {
				t.Fatal("unsupported or invalid context accepted")
			}
		})
	}
}

func TestJSONLDScopedContextsAndPropagation(t *testing.T) {
	processor := ContextProcessor{}
	data := json.RawMessage(`{
		"@context":{
			"ex":"https://example.com/",
			"resource":{"@id":"ex:resource","@context":{"local":"ex:local"}},
			"Special":{"@id":"ex:Special","@context":{"label":"ex:specialLabel"}},
			"child":"ex:child"
		},
		"@type":"Special", "label":"parent", "resource":{"local":"scoped"},
		"child":{"label":"child"}
	}`)
	result, err := processor.ExpandDocument(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	node := result[0].(map[string]any)
	if _, exists := node["https://example.com/specialLabel"]; !exists {
		t.Fatal("type-scoped context not applied")
	}
	child := node["https://example.com/child"].([]any)[0].(map[string]any)
	if _, leaked := child["https://example.com/specialLabel"]; leaked {
		t.Fatal("type-scoped context leaked to descendants")
	}
	resource := node["https://example.com/resource"].([]any)[0].(map[string]any)
	if _, exists := resource["https://example.com/local"]; !exists {
		t.Fatal("property-scoped context not applied")
	}
}

func TestJSONLDLanguageIndexAndGraphContainers(t *testing.T) {
	result, err := (ContextProcessor{}).ExpandDocument(context.Background(), json.RawMessage(`{
		"@context":{
			"ex":"https://example.com/", "@direction":"rtl",
			"label":{"@id":"ex:label","@container":"@language"},
			"items":{"@id":"ex:items","@container":["@index","@set"]},
			"links":{"@id":"ex:links","@container":"@id"},
			"graphs":{"@id":"ex:graphs","@container":["@graph","@index"]}
		},
		"label":{"en":"Hello","zh":["A","B"],"@none":"No language"},
		"items":{"first":{"@id":"https://example.com/a"}},
		"links":{"https://example.com/b":{"displayName":"B"}},
		"graphs":{"section":{"@id":"https://example.com/c"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	node := result[0].(map[string]any)
	if len(node["https://example.com/label"].([]any)) != 4 {
		t.Fatal("language map expansion incomplete")
	}
	item := node["https://example.com/items"].([]any)[0].(map[string]any)
	if item["@index"] != "first" {
		t.Fatal("index map label lost")
	}
	link := node["https://example.com/links"].([]any)[0].(map[string]any)
	if link["@id"] != "https://example.com/b" {
		t.Fatal("ID map key lost")
	}
	graph := node["https://example.com/graphs"].([]any)[0].(map[string]any)
	if graph["@index"] != "section" || len(graph["@graph"].([]any)) != 1 {
		t.Fatal("graph/index container lost")
	}
}

func TestJSONLDReverseNestListAndSet(t *testing.T) {
	result, err := (ContextProcessor{}).ExpandDocument(context.Background(), json.RawMessage(`{
		"@context":{"ex":"https://example.com/","owner":{"@reverse":"ex:owns"},"group":"@nest","items":"ex:items"},
		"@id":"https://example.com/a","owner":{"@id":"https://example.com/b"},
		"group":{"displayName":"A"},"items":{"@list":["first","second"]},
		"tags":{"@set":["one","two"]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	node := result[0].(map[string]any)
	if len(node["@reverse"].(map[string]any)["https://example.com/owns"].([]any)) != 1 {
		t.Fatal("reverse term missing")
	}
	if _, exists := node[Namespace+"displayName"]; !exists {
		t.Fatal("nested properties not flattened")
	}
	list := node["https://example.com/items"].([]any)[0].(map[string]any)["@list"].([]any)
	if len(list) != 2 || len(node[Namespace+"tags"].([]any)) != 2 {
		t.Fatal("list/set semantics lost")
	}
}

func TestJSONLDProtectedTermsAndNullContext(t *testing.T) {
	processor := ContextProcessor{}
	for _, raw := range []string{
		`{"@context":[{"term":{"@id":"https://example.com/term","@protected":true}},{"term":"https://example.com/other"}],"term":"value"}`,
		`{"@context":[{"@protected":true,"term":"https://example.com/term"},null],"term":"value"}`,
	} {
		if _, err := processor.ExpandDocument(context.Background(), json.RawMessage(raw)); err == nil {
			t.Fatal("protected term was overridden")
		}
	}
	entry := fixtureEntry()
	entry.Context = json.RawMessage(`[ {"temporary":"https://example.com/temporary"}, null ]`)
	if _, err := processor.Validate(context.Background(), entry); err != nil {
		t.Fatal("null context removed ARD initial context:", err)
	}
}

func TestJSONLDImportContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"@context":{"@protected":true,"region":"https://example.com/region"}}`))
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	entry := fixtureEntry()
	entry.Context, _ = json.Marshal(map[string]any{"@import": server.URL, "region": "https://example.com/localRegion"})
	entry.Extra = map[string]json.RawMessage{"region": json.RawMessage(`"eu"`)}
	result, err := (ContextProcessor{Client: client}).Expand(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	node := result[0].(map[string]any)
	if _, exists := node["https://example.com/localRegion"]; !exists {
		t.Fatal("local terms did not override imported context")
	}
}

func TestIdentityEquivalenceVerifier(t *testing.T) {
	entry := fixtureEntry()
	entry.ID = "https://example.com/resources/demo"
	if _, err := (ContextProcessor{}).Validate(context.Background(), entry); err == nil {
		t.Fatal("unproven resource equivalence accepted")
	}
	var calls atomic.Int32
	processor := ContextProcessor{VerifyIdentity: func(_ context.Context, identifier, id string) error {
		calls.Add(1)
		if identifier != entry.Identifier || id != entry.ID {
			t.Fatal("wrong identity arguments")
		}
		return nil
	}}
	warnings, err := processor.Validate(context.Background(), entry)
	if err != nil || len(warnings) != 0 || calls.Load() != 1 {
		t.Fatalf("equivalence proof ignored: %v %v", warnings, err)
	}
	processor.VerifyIdentity = func(context.Context, string, string) error { return fmt.Errorf("not the same resource") }
	if _, err := processor.Validate(context.Background(), entry); err == nil {
		t.Fatal("failed equivalence proof accepted")
	}
}

func TestClientIdentityVerifierCoversAllConsumerPaths(t *testing.T) {
	entry := fixtureEntry()
	entry.ID = "https://example.com/resources/demo"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/search" {
			_ = json.NewEncoder(writer).Encode(SearchResponse{Results: []SearchResult{{Entry: entry}}})
			return
		}
		_ = json.NewEncoder(writer).Encode(Manifest{Entries: []Entry{entry}})
	}))
	defer server.Close()
	var calls atomic.Int32
	client := NewClient(ClientConfig{Policy: NetworkPolicy{AllowHTTP: true, AllowPrivate: true}, VerifyIdentity: func(context.Context, string, string) error { calls.Add(1); return fmt.Errorf("identity mismatch") }})
	defer client.Close()
	if _, err := client.FetchManifest(context.Background(), server.URL); err == nil {
		t.Fatal("manifest ignored failed identity proof")
	}
	if _, err := client.Search(context.Background(), server.URL, SearchRequest{Query: Query{Text: "help"}}); err == nil {
		t.Fatal("search ignored failed identity proof")
	}
	if _, err := client.ResolveResource(context.Background(), entry, ResolveConfig{Authorize: func(context.Context, AccessRequest) error { return nil }}); err == nil {
		t.Fatal("resolution ignored failed identity proof")
	}
	if calls.Load() != 3 {
		t.Fatalf("identity verifier not reached in each path: %d", calls.Load())
	}
}

func TestManifestPublishingWithVerifiedResourceIRI(t *testing.T) {
	entry := fixtureEntry()
	entry.ID = "https://example.com/resources/demo"
	if _, err := NewManifestHandler(Manifest{Entries: []Entry{entry}}); err == nil {
		t.Fatal("unproven identity published")
	}
	processor := ContextProcessor{VerifyIdentity: func(context.Context, string, string) error { return nil }}
	if _, err := NewManifestHandlerWithContext(context.Background(), Manifest{Entries: []Entry{entry}}, processor); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidContainerCombinations(t *testing.T) {
	for _, containers := range []string{`["@list","@set"]`, `["@id","@index"]`, `["@language","@graph"]`, `["@graph","@type"]`, `["@set","@set"]`} {
		entry := fixtureEntry()
		entry.Context = json.RawMessage(`{"term":{"@id":"https://example.com/term","@container":` + containers + `}}`)
		if _, err := (ContextProcessor{}).Expand(context.Background(), entry); err == nil {
			t.Fatalf("accepted invalid containers: %s", containers)
		}
	}
}

func TestJSONLDTypeMapAndCustomIndex(t *testing.T) {
	result, err := (ContextProcessor{}).ExpandDocument(context.Background(), json.RawMessage(`{
		"@context":{
			"ex":"https://example.com/","@type":{"@container":"@set"},
			"Special":{"@id":"ex:Special","@context":{"label":"ex:specialLabel"}},
			"items":{"@id":"ex:items","@container":"@type"},
			"position":{"@id":"ex:position","@type":"http://www.w3.org/2001/XMLSchema#integer"},
			"ordered":{"@id":"ex:ordered","@container":"@index","@index":"position"},
			"plain":{"@id":"ex:plain","@type":"@none"}
		},
		"items":{"Special":{"label":"special"}},"ordered":{"1":{"@id":"https://example.com/a"}},"plain":"literal"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	node := result[0].(map[string]any)
	item := node["https://example.com/items"].([]any)[0].(map[string]any)
	if _, exists := item["https://example.com/specialLabel"]; !exists {
		t.Fatal("type map scoped context missing")
	}
	ordered := node["https://example.com/ordered"].([]any)[0].(map[string]any)
	position := ordered["https://example.com/position"].([]any)[0].(map[string]any)
	if position["@type"] != "http://www.w3.org/2001/XMLSchema#integer" {
		t.Fatal("custom index coercion lost")
	}
	if _, typed := node["https://example.com/plain"].([]any)[0].(map[string]any)["@type"]; typed {
		t.Fatal("@none added a literal datatype")
	}
}

func TestJSONLDDocumentRejectsTrailingJSON(t *testing.T) {
	if _, err := (ContextProcessor{}).ExpandDocument(context.Background(), json.RawMessage(`{} {}`)); err == nil {
		t.Fatal("trailing document accepted")
	}
}

func TestBlankNodeIdentifiersBypassURLResolution(t *testing.T) {
	for _, example := range []struct{ value, base, expected string }{
		{"https://example.com/card", "", "https://example.com/card"},
		{"../card", "https://example.com/catalog/", "https://example.com/card"},
		{"urn:air:example.com:demo", "", "urn:air:example.com:demo"},
	} {
		resolved, err := resolveContextIRI(example.value, example.base)
		if err != nil || resolved != example.expected {
			t.Fatalf("IRI resolution changed: %q %v", resolved, err)
		}
	}
	for _, base := range []string{"", "https://example.com/catalog/"} {
		for _, identifier := range []string{"_:owner", "_:b0", "_:123"} {
			resolved, err := resolveContextIRI(identifier, base)
			if err != nil || resolved != identifier {
				t.Fatalf("blank node changed: identifier=%q base=%q result=%q error=%v", identifier, base, resolved, err)
			}
		}
	}
	active := entryContext{vocab: Namespace, base: "https://example.com/", terms: map[string]contextTerm{
		"_": {iri: "https://example.com/not-a-blank-node/", prefix: true},
	}}
	for _, vocab := range []bool{false, true} {
		resolved, err := active.iri("_:owner", vocab)
		if err != nil || resolved != "_:owner" {
			t.Fatalf("blank node treated as a prefix: %q %v", resolved, err)
		}
	}
}

func TestW3CBlankNodeExpansion0068(t *testing.T) {
	t.Log("Source: https://github.com/w3c/json-ld-api/tree/ae7cf968deda89c938aa184895d77842552eee5f/tests/expand (0068-in.jsonld and 0068-out.jsonld)")
	input := json.RawMessage(`{
		"@context":{"_":"http://example.com/this-prefix-would-overwrite-all-blank-node-identifiers"},
		"@id":"_:node1",
		"@type":"_:type",
		"_:property":"all these IRIs remain unchanged because they are interpreted as blank node identifiers"
	}`)
	var expected []any
	if err := json.Unmarshal([]byte(`[{
		"@id":"_:node1",
		"@type":["_:type"],
		"_:property":[{"@value":"all these IRIs remain unchanged because they are interpreted as blank node identifiers"}]
	}]`), &expected); err != nil {
		t.Fatal(err)
	}
	actual, err := (&ContextProcessor{}).ExpandDocument(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("W3C expansion 0068: got %v, want %v", actual, expected)
	}
}

func TestW3CIdentifierCoercion0011(t *testing.T) {
	t.Log("Source: https://github.com/w3c/json-ld-api/tree/f9d65f7867150a4fd0492343a64765b55fb4371b/tests/expand (0011-in.jsonld and 0011-out.jsonld)")
	input := json.RawMessage(`{
		"@context":{
			"dc11":"http://purl.org/dc/elements/1.1/",
			"ex":"http://example.org/vocab#",
			"ex:contains":{"@type":"@id"},
			"xsd":"http://www.w3.org/2001/XMLSchema#"
		},
		"@id":"http://example.org/test#book",
		"dc11:title":"Title",
		"ex:contains":"http://example.org/test#chapter"
	}`)
	var expected []any
	if err := json.Unmarshal([]byte(`[{
		"@id":"http://example.org/test#book",
		"http://example.org/vocab#contains":[{"@id":"http://example.org/test#chapter"}],
		"http://purl.org/dc/elements/1.1/title":[{"@value":"Title"}]
	}]`), &expected); err != nil {
		t.Fatal(err)
	}
	actual, err := (&ContextProcessor{}).ExpandDocument(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("W3C expansion 0011: got %v, want %v", actual, expected)
	}
}

func TestJSONLDAbsoluteIRIsDoNotUsePrefixes(t *testing.T) {
	active := entryContext{terms: map[string]contextTerm{
		"http": {iri: "https://example.com/wrong/", prefix: true},
		"ex":   {iri: "https://example.com/vocab#", prefix: true},
	}}
	for _, example := range []struct{ value, expected string }{
		{"http://example.org/resource", "http://example.org/resource"},
		{"ex://resource", "ex://resource"},
		{"ex:resource", "https://example.com/vocab#resource"},
	} {
		for _, vocab := range []bool{false, true} {
			actual, err := active.iri(example.value, vocab)
			if err != nil || actual != example.expected {
				t.Fatalf("IRI %q (vocab=%v): got %q, want %q, error=%v", example.value, vocab, actual, example.expected, err)
			}
		}
	}
}

func TestJSONLDBlankNodesInGraphsAndReferences(t *testing.T) {
	processor := ContextProcessor{BaseURL: "https://example.com/catalog/"}
	result, err := processor.ExpandDocument(context.Background(), json.RawMessage(`{
		"@context":{
			"ex":"https://example.com/vocab#",
			"owner":{"@id":"ex:owner","@type":"@id"},
			"related":"ex:related",
			"blankAlias":"_:alias"
		},
		"@graph":[
			{"@id":"_:agent","owner":"_:owner","related":{"@id":"_:owner"},"blankAlias":"preserved"},
			{"@id":"_:owner","displayName":"Owner"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected two graph nodes: %v", result)
	}
	agent := result[0].(map[string]any)
	owner := result[1].(map[string]any)
	if agent["@id"] != "_:agent" || owner["@id"] != "_:owner" {
		t.Fatal("graph blank node IDs changed")
	}
	for _, property := range []string{"https://example.com/vocab#owner", "https://example.com/vocab#related"} {
		reference := agent[property].([]any)[0].(map[string]any)
		if reference["@id"] != "_:owner" {
			t.Fatalf("blank reference changed: %v", reference)
		}
	}
	if _, exists := agent["_:alias"]; !exists {
		t.Fatal("blank-node property alias lost")
	}
	entry := fixtureEntry()
	entry.ID = "_:agent"
	if _, err := ValidateEntry(entry); err == nil {
		t.Fatal("blank node was accepted as an authoritative ARD resource identity")
	}
}

func TestRemoteContextRedirectBaseCyclesAndBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		switch request.URL.Path {
		case "/redirect":
			http.Redirect(writer, request, "/contexts/main", http.StatusFound)
		case "/contexts/main":
			_, _ = writer.Write([]byte(`{"@context":"child"}`))
		case "/contexts/child":
			_, _ = writer.Write([]byte(`{"@context":{"ex":"https://example.com/ns#"}}`))
		case "/cycle":
			_, _ = writer.Write([]byte(`{"@context":"cycle"}`))
		case "/missing":
			_, _ = writer.Write([]byte(`{"notContext":{}}`))
		case "/trailing":
			_, _ = writer.Write([]byte(`{"@context":{}} {"@context":{}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := localClient()
	defer client.Close()
	entry := fixtureEntry()
	entry.Context, _ = json.Marshal(server.URL + "/redirect")
	entry.Extra = map[string]json.RawMessage{"ex:region": json.RawMessage(`"eu"`)}
	processor := ContextProcessor{Client: client}
	result, err := processor.Expand(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result[0].(map[string]any)["https://example.com/ns#region"]; !ok {
		t.Fatal("relative remote context not resolved after redirect")
	}
	if _, err := (ContextProcessor{Client: client, MaxRemoteContexts: 1}).Expand(context.Background(), entry); err == nil {
		t.Fatal("remote context budget ignored")
	}
	for _, path := range []string{"/cycle", "/missing", "/trailing"} {
		entry.Context, _ = json.Marshal(server.URL + path)
		if _, err := processor.Expand(context.Background(), entry); err == nil {
			t.Fatalf("invalid context %s accepted", path)
		}
	}
	before := calls.Load()
	entry.Context, _ = json.Marshal(BaseContextURL)
	entry.Extra = nil
	if _, err := processor.Expand(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("pinned base context performed a network request")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := processor.Expand(ctx, entry); err == nil {
		t.Fatal("context cancellation ignored")
	}
}

func TestJSONLDErrorCodes(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		code string
	}{
		{"local-context", `{"@context":42}`, "invalid local context"},
		{"container", `{"@context":{"label":{"@container":"@bad"}}}`, "invalid container mapping"},
		{"typed-value", `{"description":{"@value":"value","@type":["https://example.com/one","https://example.com/two"]}}`, "invalid typed value"},
		{"scoped-context", `{"@context":{"label":{"@context":{"child":{"@type":42}}}}}`, "invalid scoped context"},
		{"prefix-priority", `{"@context":{"bad/name":{"@prefix":true,"@type":"@id"}}}`, "invalid term definition"},
		{"protected-type", `{"@context":[{"@type":{"@container":"@set","@protected":true}},{"@type":{"@protected":true}}]}`, "protected term redefinition"},
		{"protected-type-reset", `{"@context":[{"@type":{"@container":"@set","@protected":true}},null]}`, "invalid context nullification"},
		{"protected-type-cannot-unprotect", `{"@context":[{"@type":{"@container":"@set","@protected":true}},{"@type":{"@container":"@set","@protected":false}},null]}`, "invalid context nullification"},
		{"identical-protected-type", `{"@context":[{"@type":{"@container":"@set","@protected":true}},{"@type":{"@container":"@set","@protected":true}}],"description":"value"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := (ContextProcessor{}).ExpandDocument(t.Context(), json.RawMessage(test.data))
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var coded *JSONLDError
			if !errors.As(fmt.Errorf("caller: %w", err), &coded) || coded.Code != test.code || coded.Err == nil {
				t.Fatalf("expected code %q through wrapped public error, got %v", test.code, err)
			}
			if test.name == "scoped-context" {
				var inner *JSONLDError
				if !errors.As(errors.Unwrap(coded), &inner) || inner.Code != "invalid type mapping" {
					t.Fatalf("scoped context lost underlying error: %v", coded)
				}
			}
		})
	}
}

func TestJSONLDErrorUnwrap(t *testing.T) {
	err := newJSONLDError("loading remote context failed", "remote context loading: %w", context.DeadlineExceeded)
	if !errors.Is(fmt.Errorf("caller: %w", err), context.DeadlineExceeded) {
		t.Fatal("JSON-LD error lost the underlying deadline")
	}
	if message := (&JSONLDError{Code: "invalid type value"}).Error(); message != "ard: invalid type value" {
		t.Fatalf("code-only error: %q", message)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (ContextProcessor{}).ExpandDocument(ctx, json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("public processor lost cancellation: %v", err)
	}
}

func TestTermDefinitionDepthLimit(t *testing.T) {
	definitions := map[string]string{}
	for index := 0; index < 80; index++ {
		definitions[fmt.Sprintf("term%03d", index)] = fmt.Sprintf("term%03d", index+1)
	}
	definitions["term080"] = "https://example.com/terminal"
	entry := fixtureEntry()
	entry.Context, _ = json.Marshal(definitions)
	if _, err := (ContextProcessor{}).Expand(context.Background(), entry); err == nil || !strings.Contains(err.Error(), "nesting limit") {
		t.Fatalf("unbounded term recursion: %v", err)
	}
}
