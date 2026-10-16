package ard

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

const (
	SpecVersion    = "0.91"
	BaseContextURL = "https://agenticresourcediscovery.org/context/v1"
	Namespace      = "https://agenticresourcediscovery.org/ns#"
	ManifestPath   = "/.well-known/ard.json"
	TypeA2A        = "application/a2a-agent-card+json"
	TypeMCP        = "application/mcp-server-card+json"
	TypeSkill      = "application/agent-skills+md"
	TypeRegistry   = "application/ai-registry+json"
)

type Entry struct {
	Context               json.RawMessage            `json:"@context,omitempty"`
	ID                    string                     `json:"@id,omitempty"`
	Identifier            string                     `json:"identifier,omitempty"`
	DisplayName           string                     `json:"displayName,omitempty"`
	Type                  string                     `json:"type,omitempty"`
	URL                   string                     `json:"url,omitempty"`
	Data                  json.RawMessage            `json:"data,omitempty"`
	Description           string                     `json:"description,omitempty"`
	RepresentativeQueries []string                   `json:"representativeQueries,omitempty"`
	Capabilities          []string                   `json:"capabilities,omitempty"`
	Tags                  []string                   `json:"tags,omitempty"`
	Version               string                     `json:"version,omitempty"`
	UpdatedAt             string                     `json:"updatedAt,omitempty"`
	Metadata              json.RawMessage            `json:"metadata,omitempty"`
	TrustManifest         json.RawMessage            `json:"trustManifest,omitempty"`
	Extra                 map[string]json.RawMessage `json:"-"`
	emptyURL              json.RawMessage            `json:"-"`
}

type entryFields Entry

func (entry Entry) MarshalJSON() ([]byte, error) {
	data, err := marshalOpen(entryFields(entry), entry.Extra)
	if err != nil || entry.URL != "" || len(entry.emptyURL) == 0 {
		return data, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	fields["url"] = entry.emptyURL
	return json.Marshal(fields)
}

func (entry *Entry) UnmarshalJSON(data []byte) error {
	var fields entryFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	extra, err := unknownFields(data, fields)
	if err != nil {
		return err
	}
	*entry = Entry(fields)
	entry.Extra = extra
	if entry.URL == "" {
		var content struct {
			URL json.RawMessage `json:"url"`
		}
		if err := json.Unmarshal(data, &content); err != nil {
			return err
		}
		entry.emptyURL = content.URL
	}
	return nil
}

func marshalOpen(value any, extra map[string]json.RawMessage) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for key, raw := range extra {
		if fieldNames(value)[key] {
			return nil, fmt.Errorf("ard: duplicate field %q", key)
		}
		fields[key] = raw
	}
	return json.Marshal(fields)
}

func unknownFields(data []byte, value any) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("ard: expected JSON object")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &known); err != nil {
		return nil, err
	}
	for key := range known {
		delete(fields, key)
	}
	for key := range fieldNames(value) {
		delete(fields, key)
	}
	return fields, nil
}

func fieldNames(value any) map[string]bool {
	names := map[string]bool{}
	typ := reflect.TypeOf(value)
	for index := 0; index < typ.NumField(); index++ {
		name := strings.Split(typ.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			names[name] = true
		}
	}
	return names
}

type Manifest struct {
	Entries []Entry                    `json:"entries"`
	Extra   map[string]json.RawMessage `json:"-"`
}

func (manifest Manifest) MarshalJSON() ([]byte, error) {
	entries := manifest.Entries
	if entries == nil {
		entries = []Entry{}
	}
	return marshalOpen(struct {
		Entries []Entry `json:"entries"`
	}{entries}, manifest.Extra)
}

func (manifest *Manifest) UnmarshalJSON(data []byte) error {
	var fields struct {
		Entries *[]Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields.Entries == nil {
		return fmt.Errorf("ard: manifest requires an entries array")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	delete(raw, "entries")
	manifest.Entries, manifest.Extra = *fields.Entries, raw
	return nil
}

type Federation string

const (
	FederationAuto      Federation = "auto"
	FederationReferrals Federation = "referrals"
	FederationNone      Federation = "none"
)

type Query struct {
	Context json.RawMessage            `json:"@context,omitempty"`
	Text    string                     `json:"text,omitempty"`
	Filter  map[string]json.RawMessage `json:"filter,omitempty"`
}

type SearchRequest struct {
	Query      Query      `json:"query"`
	Federation Federation `json:"federation,omitempty"`
	PageSize   int        `json:"pageSize,omitempty"`
	PageToken  string     `json:"pageToken,omitempty"`
}

type SearchResult struct {
	Entry
	Score  *int
	Source string
}

func (result SearchResult) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(result.Entry)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if result.Score != nil {
		fields["score"], _ = json.Marshal(*result.Score)
	}
	if result.Source != "" {
		fields["source"], _ = json.Marshal(result.Source)
	}
	return json.Marshal(fields)
}

func (result *SearchResult) UnmarshalJSON(data []byte) error {
	var fields struct {
		Score  *int   `json:"score"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return err
	}
	delete(entry.Extra, "score")
	delete(entry.Extra, "source")
	*result = SearchResult{Entry: entry, Score: fields.Score, Source: fields.Source}
	return nil
}

type RegistryReferral struct {
	Identifier  string `json:"identifier"`
	DisplayName string `json:"displayName"`
	Type        string `json:"type"`
	URL         string `json:"url"`
}

type SearchResponse struct {
	Results   []SearchResult     `json:"results"`
	Referrals []RegistryReferral `json:"referrals,omitempty"`
	PageToken string             `json:"pageToken,omitempty"`
}

type ProtocolError struct {
	StatusCode int    `json:"-"`
	ErrorCode  string `json:"errorCode"`
	Message    string `json:"message"`
}

func (err *ProtocolError) Error() string {
	return fmt.Sprintf("ard: HTTP %d %s: %s", err.StatusCode, err.ErrorCode, err.Message)
}

var identifierPattern = regexp.MustCompile(`^urn:air:([a-zA-Z0-9.-]+)(:[a-zA-Z0-9._-]+)+$`)

func Publisher(identifier string) (string, error) {
	match := identifierPattern.FindStringSubmatch(identifier)
	if match == nil {
		return "", fmt.Errorf("ard: invalid identifier %q", identifier)
	}
	domain := strings.ToLower(match[1])
	if len(domain) > 253 || !strings.Contains(domain, ".") {
		return "", fmt.Errorf("ard: publisher must be a domain")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("ard: invalid publisher domain")
		}
	}
	return domain, nil
}

func validateNodeIdentity(entry Entry) error {
	if entry.ID != "" {
		identifier, err := url.Parse(entry.ID)
		if err != nil || !identifier.IsAbs() {
			return fmt.Errorf("ard: @id must be an absolute IRI")
		}
		if strings.HasPrefix(strings.ToLower(entry.ID), "urn:air:") && entry.ID != entry.Identifier {
			return fmt.Errorf("ard: @id and identifier denote different discovery handles")
		}
	}
	return nil
}

var mediaTypeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]{0,126}$`)

func ValidateEntry(entry Entry) ([]string, error) {
	if _, err := Publisher(entry.Identifier); err != nil {
		return nil, err
	}
	if err := validateNodeIdentity(entry); err != nil {
		return nil, err
	}
	if strings.TrimSpace(entry.DisplayName) == "" {
		return nil, fmt.Errorf("ard: displayName is required")
	}
	mediaType, _, err := mime.ParseMediaType(entry.Type)
	primaryType, subtype, hasSubtype := strings.Cut(mediaType, "/")
	if err != nil || !hasSubtype || !mediaTypeNamePattern.MatchString(primaryType) || !mediaTypeNamePattern.MatchString(subtype) {
		return nil, fmt.Errorf("ard: invalid resource media type")
	}
	hasData := len(entry.Data) > 0
	hasURL := entry.URL != "" || len(entry.emptyURL) > 0
	if hasURL == hasData {
		return nil, fmt.Errorf("ard: exactly one of url or data is required")
	}
	if hasData {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(entry.Data, &object); err != nil || object == nil {
			return nil, fmt.Errorf("ard: data must be an object")
		}
	} else {
		parsed, err := url.Parse(entry.URL)
		if err != nil || parsed.Scheme == "" {
			return nil, fmt.Errorf("ard: url must be absolute")
		}
	}
	var warnings []string
	if entry.ID != "" && entry.ID != entry.Identifier {
		warnings = append(warnings, "@id resource equivalence requires application verification")
	}
	if len(entry.TrustManifest) > 0 {
		var trust struct {
			Identity string `json:"identity"`
		}
		if err := json.Unmarshal(entry.TrustManifest, &trust); err != nil || trust.Identity == "" {
			return nil, fmt.Errorf("ard: trustManifest requires identity")
		}
		identity, err := url.Parse(trust.Identity)
		if err != nil || identity.Scheme == "" {
			return nil, fmt.Errorf("ard: invalid trust identity")
		}
		if identity.Scheme == "spiffe" || identity.Scheme == "https" {
			publisher, _ := Publisher(entry.Identifier)
			if !strings.EqualFold(identity.Hostname(), publisher) {
				return nil, fmt.Errorf("ard: trust identity does not match publisher")
			}
		}
	}
	if len(entry.RepresentativeQueries) < 2 || len(entry.RepresentativeQueries) > 5 {
		warnings = append(warnings, "representativeQueries should contain 2–5 examples")
	}
	return warnings, nil
}
