package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/covoyage/covonaut/ard"
)

func main() {
	entry := ard.Entry{
		Identifier:            "urn:air:example.com:agent:demo",
		DisplayName:           "Demo Agent",
		Type:                  ard.TypeA2A,
		Data:                  json.RawMessage(`{"name":"Demo Agent"}`),
		Description:           "An offline discovery fixture.",
		RepresentativeQueries: []string{"find a demo agent", "show an agent descriptor"},
	}
	manifestHandler, err := ard.NewManifestHandler(ard.Manifest{Entries: []ard.Entry{entry}})
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(ard.ManifestPath, manifestHandler)
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<link rel="ard" href="/.well-known/ard.json">`))
	})
	mux.HandleFunc("/search", func(writer http.ResponseWriter, request *http.Request) {
		var input ard.SearchRequest
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		score := 100
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(ard.SearchResponse{Results: []ard.SearchResult{{Entry: entry, Score: &score, Source: "offline-fixture"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := ard.NewClient(ard.ClientConfig{Policy: ard.NetworkPolicy{AllowHTTP: true, AllowPrivate: true, AllowedHosts: []string{"127.0.0.1"}}})
	defer client.Close()
	manifest, err := client.Discover(context.Background(), server.URL)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Discovered: %s\n", manifest.Entries[0].Identifier)
	extension, err := ard.NewDiscoveryExtension(ard.ExtensionConfig{Client: client, Registries: map[string]string{"demo": server.URL}})
	if err != nil {
		log.Fatal(err)
	}
	result, err := extension.Tools()[0].Func(context.Background(), json.RawMessage(`{"query":"find a demo agent"}`))
	if err != nil {
		log.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Search: %s\n", encoded)
}
