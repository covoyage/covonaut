package ard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/covoyage/covonaut/a2a"
	"github.com/covoyage/covonaut/mcp"
)

type AccessRequest struct {
	Entry   Entry
	Purpose string
	URL     string
}

type AuthorizeFunc func(context.Context, AccessRequest) error

type Endpoint struct {
	URL     string
	Binding string
}

type EndpointResolver func(context.Context, Entry, json.RawMessage) (Endpoint, error)

type ResolveConfig struct {
	Authorize AuthorizeFunc
	Resolver  EndpointResolver
}

type ResolvedResource struct {
	Entry       Entry
	Document    json.RawMessage
	Endpoint    Endpoint
	TrustStatus string
}

func authorize(ctx context.Context, fn AuthorizeFunc, entry Entry, purpose, target string) error {
	if fn == nil {
		return fmt.Errorf("ard: application authorization is required")
	}
	return fn(ctx, AccessRequest{Entry: entry, Purpose: purpose, URL: target})
}

func (client *Client) ResolveResource(ctx context.Context, entry Entry, config ResolveConfig) (*ResolvedResource, error) {
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	if err := authorize(ctx, config.Authorize, entry, "describe", entry.URL); err != nil {
		return nil, err
	}
	core, err := (ContextProcessor{Client: client}).Core(ctx, entry)
	if err != nil {
		return nil, err
	}
	if _, err := (ContextProcessor{Client: client}).validateCore(ctx, core); err != nil {
		return nil, err
	}
	if core.Identifier != entry.Identifier || core.URL != entry.URL || core.Type != entry.Type {
		if err := authorize(ctx, config.Authorize, core, "describe", core.URL); err != nil {
			return nil, err
		}
	}
	entry = core
	document := append(json.RawMessage(nil), entry.Data...)
	if entry.URL != "" {
		data, _, _, err := client.requestWithHTTP(ctx, http.MethodGet, entry.URL, "application/json", nil, client.authorizedHTTPClient(entry, config.Authorize, false))
		if err != nil {
			return nil, err
		}
		document = data
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(document, &object); err != nil || object == nil {
		return nil, fmt.Errorf("ard: resource descriptor must be a JSON object")
	}
	resolver := config.Resolver
	if resolver == nil {
		resolver = resolveA2A
	}
	endpoint, err := resolver(ctx, entry, document)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil {
		return nil, err
	}
	if err := client.checkURL(parsed); err != nil {
		return nil, err
	}
	return &ResolvedResource{Entry: entry, Document: document, Endpoint: endpoint, TrustStatus: "unverified"}, nil
}

func resolveA2A(_ context.Context, entry Entry, document json.RawMessage) (Endpoint, error) {
	mediaType, _, err := mime.ParseMediaType(entry.Type)
	if err != nil || mediaType != TypeA2A {
		return Endpoint{}, fmt.Errorf("ard: this resource requires an application endpoint resolver")
	}
	var card a2a.V1AgentCard
	if err := json.Unmarshal(document, &card); err != nil {
		return Endpoint{}, err
	}
	for _, candidate := range card.SupportedInterfaces {
		if candidate.ProtocolVersion == "1.0" && (candidate.ProtocolBinding == "JSONRPC" || candidate.ProtocolBinding == "HTTP+JSON") && candidate.URL != "" {
			return Endpoint{URL: candidate.URL, Binding: candidate.ProtocolBinding}, nil
		}
	}
	return Endpoint{}, fmt.Errorf("ard: no supported A2A 1.0 interface")
}

func (client *Client) DescribeSkill(ctx context.Context, entry Entry, approval AuthorizeFunc) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	if err := authorize(ctx, approval, entry, "describe", entry.URL); err != nil {
		return "", err
	}
	core, err := (ContextProcessor{Client: client}).Core(ctx, entry)
	if err != nil {
		return "", err
	}
	if _, err := (ContextProcessor{Client: client}).validateCore(ctx, core); err != nil {
		return "", err
	}
	mediaType, _, _ := mime.ParseMediaType(core.Type)
	if mediaType != TypeSkill || core.URL == "" {
		return "", fmt.Errorf("ard: a URL-backed skill is required")
	}
	if core.Identifier != entry.Identifier || core.URL != entry.URL || core.Type != entry.Type {
		if err := authorize(ctx, approval, core, "describe", core.URL); err != nil {
			return "", err
		}
	}
	data, _, _, err := client.requestWithHTTP(ctx, http.MethodGet, core.URL, "text/markdown, text/plain", nil, client.authorizedHTTPClient(core, approval, false))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (client *Client) ConnectA2A(ctx context.Context, resource *ResolvedResource, approval AuthorizeFunc) (*a2a.V1Client, error) {
	if resource == nil {
		return nil, fmt.Errorf("ard: resource is required")
	}
	mediaType, _, _ := mime.ParseMediaType(resource.Entry.Type)
	if mediaType != TypeA2A || (resource.Endpoint.Binding != "JSONRPC" && resource.Endpoint.Binding != "HTTP+JSON") {
		return nil, fmt.Errorf("ard: resource is not a supported A2A interface")
	}
	if err := authorize(ctx, approval, resource.Entry, "connect", resource.Endpoint.URL); err != nil {
		return nil, err
	}
	options := []a2a.ClientOption{a2a.WithHTTPClient(client.authorizedHTTPClient(resource.Entry, approval, true))}
	if resource.Endpoint.Binding == "HTTP+JSON" {
		options = append(options, a2a.WithRESTTransport())
	}
	return a2a.NewClient(resource.Endpoint.URL, options...).V1(), nil
}

func (client *Client) ConnectMCP(ctx context.Context, resource *ResolvedResource, approval AuthorizeFunc, config mcp.HTTPConfig) (*mcp.HTTPClient, error) {
	if resource == nil {
		return nil, fmt.Errorf("ard: resource is required")
	}
	mediaType, _, _ := mime.ParseMediaType(resource.Entry.Type)
	if mediaType != TypeMCP || resource.Endpoint.Binding != "MCP_HTTP" {
		return nil, fmt.Errorf("ard: resource is not an HTTP MCP interface")
	}
	if err := authorize(ctx, approval, resource.Entry, "connect", resource.Endpoint.URL); err != nil {
		return nil, err
	}
	config.Endpoint, config.Client = resource.Endpoint.URL, client.authorizedHTTPClient(resource.Entry, approval, true)
	return mcp.NewHTTPClient(ctx, config)
}

func (client *Client) executionHTTPClient() *http.Client {
	return &http.Client{Transport: &executionTransport{client: client}, Timeout: client.config.Timeout, CheckRedirect: client.http.CheckRedirect}
}

func (client *Client) authorizedHTTPClient(entry Entry, approval AuthorizeFunc, execute bool) *http.Client {
	configured := *client.http
	purpose := "describe"
	if execute {
		configured = *client.executionHTTPClient()
		purpose = "connect"
	}
	configured.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := client.http.CheckRedirect(request, via); err != nil {
			return err
		}
		return authorize(request.Context(), approval, entry, purpose, request.URL.String())
	}
	return &configured
}

type executionTransport struct{ client *Client }

func (transport *executionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := transport.client.checkURL(request.URL); err != nil {
		return nil, err
	}
	request = request.Clone(request.Context())
	for key, values := range transport.client.headers(request.URL) {
		request.Header[key] = values
	}
	response, err := transport.client.transport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &limitedBody{ReadCloser: response.Body, remaining: transport.client.config.MaxResponseBytes}
	return response, nil
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (body *limitedBody) Read(buffer []byte) (int, error) {
	if body.remaining < 0 {
		return 0, fmt.Errorf("ard: execution response exceeds size limit")
	}
	if int64(len(buffer)) > body.remaining+1 {
		buffer = buffer[:body.remaining+1]
	}
	count, err := body.ReadCloser.Read(buffer)
	body.remaining -= int64(count)
	if body.remaining < 0 {
		return 0, fmt.Errorf("ard: execution response exceeds size limit")
	}
	return count, err
}
