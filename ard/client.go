package ard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type NetworkPolicy struct {
	AllowHTTP    bool
	AllowPrivate bool
	AllowedHosts []string
}

type ClientConfig struct {
	VerifyIdentity   func(context.Context, string, string) error
	Policy           NetworkPolicy
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxEntries       int
	HeadersByOrigin  map[string]http.Header
}

type Client struct {
	config    ClientConfig
	http      *http.Client
	transport *http.Transport
}

func NewClient(config ClientConfig) *Client {
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = 2 << 20
	}
	if config.MaxEntries <= 0 {
		config.MaxEntries = 1000
	}
	config.Policy.AllowedHosts = append([]string(nil), config.Policy.AllowedHosts...)
	headers := make(map[string]http.Header, len(config.HeadersByOrigin))
	for origin, value := range config.HeadersByOrigin {
		headers[origin] = value.Clone()
	}
	config.HeadersByOrigin = headers
	client := &Client{config: config}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = client.dial
	client.transport = transport
	client.http = &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("ard: redirect limit exceeded")
		}
		if err := client.checkURL(request.URL); err != nil {
			return err
		}
		request.Header = client.headers(request.URL)
		request.Header.Set("Accept", via[0].Header.Get("Accept"))
		if request.Body != nil {
			request.Header.Set("Content-Type", via[0].Header.Get("Content-Type"))
		}
		for _, key := range []string{"Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id"} {
			if request.URL.Scheme == via[len(via)-1].URL.Scheme && request.URL.Host == via[len(via)-1].URL.Host {
				request.Header.Set(key, via[len(via)-1].Header.Get(key))
			}
		}
		return nil
	}}
	return client
}

func (client *Client) Close() error {
	client.transport.CloseIdleConnections()
	return nil
}

func (client *Client) checkURL(target *url.URL) error {
	if target.Scheme != "https" && !(target.Scheme == "http" && client.config.Policy.AllowHTTP) {
		return fmt.Errorf("ard: HTTPS is required")
	}
	if target.Hostname() == "" || target.User != nil {
		return fmt.Errorf("ard: invalid URL authority")
	}
	if len(client.config.Policy.AllowedHosts) > 0 {
		allowed := false
		for _, host := range client.config.Policy.AllowedHosts {
			if strings.EqualFold(host, target.Hostname()) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("ard: host %q is not allowed", target.Hostname())
		}
	}
	if address, err := netip.ParseAddr(target.Hostname()); err == nil && !client.allowedIP(address) {
		return fmt.Errorf("ard: non-public address is forbidden")
	}
	return nil
}

func (client *Client) allowedIP(address netip.Addr) bool {
	address = address.Unmap()
	if address.Zone() != "" {
		return false
	}
	if client.config.Policy.AllowPrivate {
		return true
	}
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	for _, raw := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(raw).Contains(address) {
			return false
		}
	}
	return true
}

func (client *Client) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range addresses {
		if !client.allowedIP(ip) {
			return nil, fmt.Errorf("ard: DNS resolved to a forbidden address")
		}
	}
	var lastErr error
	for _, ip := range addresses {
		connection, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("ard: host has no addresses")
	}
	return nil, lastErr
}

func (client *Client) headers(target *url.URL) http.Header {
	value := client.config.HeadersByOrigin[target.Scheme+"://"+target.Host].Clone()
	if value == nil {
		value = make(http.Header)
	}
	return value
}

func (client *Client) request(ctx context.Context, method, target, accept string, body []byte) ([]byte, string, string, error) {
	return client.requestWithHTTP(ctx, method, target, accept, body, client.http)
}

func (client *Client) requestWithHTTP(ctx context.Context, method, target, accept string, body []byte, httpClient *http.Client) ([]byte, string, string, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, "", "", err
	}
	if err := client.checkURL(parsed); err != nil {
		return nil, "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return nil, "", "", err
	}
	request.Header = client.headers(parsed)
	request.Header.Set("Accept", accept)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, "", "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, client.config.MaxResponseBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if int64(len(data)) > client.config.MaxResponseBytes {
		return nil, "", "", fmt.Errorf("ard: response exceeds size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		protocolErr := &ProtocolError{StatusCode: response.StatusCode}
		_ = json.Unmarshal(data, protocolErr)
		if protocolErr.Message == "" {
			protocolErr.Message = http.StatusText(response.StatusCode)
		}
		return nil, "", "", protocolErr
	}
	return data, response.Header.Get("Content-Type"), response.Request.URL.String(), nil
}

func (client *Client) fetch(ctx context.Context, target, accept string) ([]byte, string, string, error) {
	return client.request(ctx, http.MethodGet, target, accept, nil)
}

func (client *Client) FetchManifest(ctx context.Context, target string) (*Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	data, _, _, err := client.fetch(ctx, target, "application/json, application/ld+json")
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if len(manifest.Entries) > client.config.MaxEntries {
		return nil, fmt.Errorf("ard: too many entries")
	}
	for _, entry := range manifest.Entries {
		if _, err := (ContextProcessor{Client: client}).Validate(ctx, entry); err != nil {
			return nil, err
		}
	}
	return &manifest, nil
}

func (client *Client) Discover(ctx context.Context, origin string) (*Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	base, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	base.Path, base.RawPath, base.RawQuery, base.Fragment = "/", "", "", ""
	if err := client.checkURL(base); err != nil {
		return nil, err
	}
	manifestURL := base.ResolveReference(&url.URL{Path: ManifestPath}).String()
	manifest, manifestErr := client.FetchManifest(ctx, manifestURL)
	if manifestErr != nil {
		protocolErr, ok := manifestErr.(*ProtocolError)
		if !ok || protocolErr.StatusCode != http.StatusNotFound {
			return nil, manifestErr
		}
	}
	data, contentType, finalURL, pageErr := client.fetch(ctx, base.String(), "text/html")
	if pageErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if manifest != nil {
			return manifest, nil
		}
		return nil, pageErr
	}
	if !strings.Contains(contentType, "text/html") {
		if manifest != nil {
			return manifest, nil
		}
		return nil, fmt.Errorf("ard: no manifest or HTML discovery page")
	}
	links, inline, err := parseHTML(data, finalURL)
	if err != nil {
		return nil, err
	}
	if manifest == nil {
		manifest = &Manifest{Entries: []Entry{}}
	}
	seen := map[string]bool{manifestURL: true}
	for _, link := range links {
		if seen[link] {
			continue
		}
		seen[link] = true
		if len(seen) > 16 {
			return nil, fmt.Errorf("ard: discovery link limit exceeded")
		}
		linked, err := client.FetchManifest(ctx, link)
		if err != nil {
			return nil, err
		}
		manifest.Entries = append(manifest.Entries, linked.Entries...)
	}
	inlineEntries := 0
	for _, script := range inline {
		processor := ContextProcessor{Client: client, BaseURL: finalURL}
		nodes, err := processor.ExpandDocument(ctx, script)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if bytes.Contains(script, []byte(Namespace)) || bytes.Contains(script, []byte(BaseContextURL)) || bytes.Contains(script, []byte("urn:air:")) {
				return nil, err
			}
			continue
		}
		var collect func(any, int) error
		collect = func(raw any, depth int) error {
			if depth > 64 {
				return fmt.Errorf("ard: in-page node nesting limit exceeded")
			}
			switch node := raw.(type) {
			case []any:
				for _, value := range node {
					if err := collect(value, depth+1); err != nil {
						return err
					}
				}
			case map[string]any:
				if _, opaque := node["@value"]; opaque {
					return nil
				}
				if _, resource := node[Namespace+"identifier"]; resource {
					entry, err := coreNode(node)
					if err != nil {
						return err
					}
					if _, err := processor.Validate(ctx, entry); err != nil {
						return err
					}
					manifest.Entries = append(manifest.Entries, entry)
					inlineEntries++
					if len(manifest.Entries) > client.config.MaxEntries {
						return fmt.Errorf("ard: too many entries")
					}
				}
				for _, key := range sortedContextKeys(node) {
					if err := collect(node[key], depth+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := collect(nodes, 0); err != nil {
			return nil, err
		}
	}
	if len(manifest.Entries) > client.config.MaxEntries {
		return nil, fmt.Errorf("ard: too many entries")
	}
	if manifestErr != nil && len(links) == 0 && inlineEntries == 0 {
		return nil, manifestErr
	}
	unique := make([]Entry, 0, len(manifest.Entries))
	identifiers := map[string]bool{}
	for _, entry := range manifest.Entries {
		core, err := (ContextProcessor{Client: client}).Core(ctx, entry)
		if err != nil {
			return nil, err
		}
		if !identifiers[core.Identifier] {
			unique = append(unique, entry)
			identifiers[core.Identifier] = true
		}
	}
	manifest.Entries = unique
	return manifest, nil
}

func parseHTML(data []byte, baseURL string) ([]string, []json.RawMessage, error) {
	document, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, nil, err
	}
	foundBase := false
	var findBase func(*html.Node) error
	findBase = func(node *html.Node) error {
		if node.Type == html.ElementNode && node.Data == "base" && !foundBase {
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					parsed, err := url.Parse(attr.Val)
					if err != nil {
						return err
					}
					base = base.ResolveReference(parsed)
					foundBase = true
					break
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := findBase(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := findBase(document); err != nil {
		return nil, nil, err
	}
	var links []string
	var entries []json.RawMessage
	var visit func(*html.Node) error
	visit = func(node *html.Node) error {
		if node.Type == html.ElementNode {
			attrs := map[string]string{}
			for _, attr := range node.Attr {
				attrs[attr.Key] = attr.Val
			}
			if node.Data == "link" {
				for _, rel := range strings.Fields(attrs["rel"]) {
					if strings.EqualFold(rel, "ard") {
						parsed, err := url.Parse(attrs["href"])
						if err != nil || attrs["href"] == "" {
							return fmt.Errorf("ard: invalid discovery link")
						}
						links = append(links, base.ResolveReference(parsed).String())
					}
				}
			}
			if node.Data == "script" && strings.EqualFold(attrs["type"], "application/ld+json") {
				var text strings.Builder
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if child.Type == html.TextNode {
						text.WriteString(child.Data)
					}
				}
				raw := bytes.TrimSpace([]byte(text.String()))
				if json.Valid(raw) {
					entries = append(entries, append(json.RawMessage(nil), raw...))
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(document); err != nil {
		return nil, nil, err
	}
	return links, entries, nil
}

func (client *Client) Search(ctx context.Context, registryBase string, request SearchRequest) (*SearchResponse, error) {
	base, err := url.Parse(registryBase)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/search"
	base.RawPath, base.RawQuery, base.Fragment = "", "", ""
	return client.SearchEndpoint(ctx, base.String(), request)
}

func (client *Client) SearchEndpoint(ctx context.Context, endpoint string, request SearchRequest) (*SearchResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	if strings.TrimSpace(request.Query.Text) == "" {
		return nil, fmt.Errorf("ard: search text is required")
	}
	if request.PageSize < 0 || request.PageSize > 100 {
		return nil, fmt.Errorf("ard: pageSize must be 1–100 or omitted")
	}
	if request.Federation != "" && request.Federation != FederationAuto && request.Federation != FederationNone && request.Federation != FederationReferrals {
		return nil, fmt.Errorf("ard: invalid federation mode")
	}
	for _, value := range request.Query.Filter {
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, err
		}
		switch values := decoded.(type) {
		case string:
		case []any:
			for _, item := range values {
				if _, ok := item.(string); !ok {
					return nil, fmt.Errorf("ard: filter arrays must contain only strings")
				}
			}
		default:
			return nil, fmt.Errorf("ard: filter values must be strings or string arrays")
		}
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	data, _, _, err = client.request(ctx, http.MethodPost, endpoint, "application/json", data)
	if err != nil {
		return nil, err
	}
	var response SearchResponse
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if len(envelope["results"]) == 0 || string(envelope["results"]) == "null" {
		return nil, fmt.Errorf("ard: search response requires results array")
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	if len(response.Results) > client.config.MaxEntries || len(response.Referrals) > client.config.MaxEntries {
		return nil, fmt.Errorf("ard: too many search results or referrals")
	}
	for _, result := range response.Results {
		core, err := (ContextProcessor{Client: client}).Core(ctx, result.Entry)
		if err != nil {
			return nil, err
		}
		if _, err := Publisher(core.Identifier); err != nil {
			return nil, err
		}
		if err := validateNodeIdentity(core); err != nil {
			return nil, err
		}
		if _, err := (ContextProcessor{Client: client}).verifyIdentity(ctx, core, nil); err != nil {
			return nil, err
		}
		if result.Score != nil && (*result.Score < 0 || *result.Score > 100) {
			return nil, fmt.Errorf("ard: score must be 0–100")
		}
	}
	for _, referral := range response.Referrals {
		if _, err := Publisher(referral.Identifier); err != nil {
			return nil, err
		}
		if referral.DisplayName == "" || (referral.Type != TypeRegistry && referral.Type != "application/ai-registry") {
			return nil, fmt.Errorf("ard: invalid referral")
		}
		parsed, err := url.Parse(referral.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("ard: invalid referral URL")
		}
	}
	return &response, nil
}
