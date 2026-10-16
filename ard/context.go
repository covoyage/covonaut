package ard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

const baseContextJSON = `{"@context":{"@version":1.1,"@vocab":"https://agenticresourcediscovery.org/ns#","ard":"https://agenticresourcediscovery.org/ns#","id":"@id","identifier":"ard:identifier","displayName":"ard:displayName","type":"ard:mediaType","url":{"@id":"ard:url","@type":"@id"},"data":{"@id":"ard:data","@type":"@json"},"representativeQueries":{"@id":"ard:representativeQueries","@container":"@set"},"capabilities":{"@id":"ard:capabilities","@container":"@set"},"tags":{"@id":"ard:tags","@container":"@set"},"description":"ard:description","version":"ard:version","updatedAt":{"@id":"ard:updatedAt","@type":"http://www.w3.org/2001/XMLSchema#dateTime"},"metadata":{"@id":"ard:metadata","@type":"@json"},"trustManifest":{"@id":"ard:trustManifest","@type":"@json"}}}`

type ContextProcessor struct {
	Client            *Client
	MaxRemoteContexts int
	BaseURL           string
	VerifyIdentity    func(context.Context, string, string) error
}

type JSONLDError struct {
	Code string
	Err  error
}

func (err *JSONLDError) Error() string {
	if err.Err == nil {
		return "ard: " + err.Code
	}
	return err.Err.Error()
}

func (err *JSONLDError) Unwrap() error { return err.Err }

func newJSONLDError(code, format string, arguments ...any) error {
	return &JSONLDError{Code: code, Err: fmt.Errorf("ard: "+format, arguments...)}
}

func (processor ContextProcessor) Expand(ctx context.Context, entry Entry) ([]any, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	return processor.ExpandDocument(ctx, data)
}

func (processor ContextProcessor) ExpandDocument(ctx context.Context, data json.RawMessage) ([]any, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("ard: invalid JSON-LD document")
	}
	if processor.Client != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, processor.Client.config.Timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	limit := processor.MaxRemoteContexts
	if limit <= 0 {
		limit = 16
	}
	expansion := &contextExpansion{ctx: ctx, client: processor.Client, remaining: limit, loading: map[string]bool{}}
	active := entryContext{terms: map[string]contextTerm{}}
	base, err := decodeContextJSON([]byte(baseContextJSON))
	if err != nil {
		return nil, err
	}
	active, err = expansion.apply(active, base.(map[string]any)["@context"], "", 0)
	if err != nil {
		return nil, err
	}
	active.base = processor.BaseURL
	expansion.initial = active.clone()
	return expansion.document(document, active, 0)
}

func (processor ContextProcessor) Core(ctx context.Context, entry Entry) (Entry, error) {
	expanded, err := processor.Expand(ctx, entry)
	if err != nil {
		return Entry{}, err
	}
	if len(expanded) != 1 {
		return Entry{}, fmt.Errorf("ard: expected one resource node")
	}
	node := expanded[0].(map[string]any)
	return coreNode(node)
}

func coreNode(node map[string]any) (Entry, error) {
	fields := map[string]any{}
	coreNames := map[string]string{
		"identifier": "identifier", "displayName": "displayName", "mediaType": "type",
		"url": "url", "data": "data", "description": "description", "representativeQueries": "representativeQueries",
		"capabilities": "capabilities", "tags": "tags", "version": "version", "updatedAt": "updatedAt",
		"metadata": "metadata", "trustManifest": "trustManifest",
	}
	for iri, raw := range node {
		if iri == "@id" {
			fields["@id"] = raw
			continue
		}
		name := coreNames[strings.TrimPrefix(iri, Namespace)]
		if !strings.HasPrefix(iri, Namespace) || name == "" {
			continue
		}
		values, ok := raw.([]any)
		if !ok {
			return Entry{}, fmt.Errorf("ard: invalid expanded term %q", iri)
		}
		if name == "representativeQueries" || name == "capabilities" || name == "tags" {
			items := make([]string, 0, len(values))
			for _, value := range values {
				text, err := expandedString(value, false)
				if err != nil {
					return Entry{}, fmt.Errorf("ard: %s: %w", name, err)
				}
				items = append(items, text)
			}
			fields[name] = items
			continue
		}
		if len(values) != 1 {
			return Entry{}, fmt.Errorf("ard: %s requires one value", name)
		}
		if name == "data" || name == "metadata" || name == "trustManifest" {
			value, ok := values[0].(map[string]any)
			if !ok || value["@type"] != "@json" {
				return Entry{}, fmt.Errorf("ard: %s must remain opaque JSON", name)
			}
			fields[name] = value["@value"]
		} else {
			text, err := expandedString(values[0], name == "url")
			if err != nil {
				return Entry{}, fmt.Errorf("ard: %s: %w", name, err)
			}
			fields[name] = text
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return Entry{}, err
	}
	var core Entry
	if err := json.Unmarshal(encoded, &core); err != nil {
		return Entry{}, err
	}
	for iri, value := range node {
		if iri == "@id" || (strings.HasPrefix(iri, Namespace) && coreNames[strings.TrimPrefix(iri, Namespace)] != "") {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return Entry{}, err
		}
		core.Extra[iri] = encoded
	}
	return core, nil
}

func (processor ContextProcessor) Validate(ctx context.Context, entry Entry) ([]string, error) {
	if processor.Client != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, processor.Client.config.Timeout)
		defer cancel()
	}
	core, err := processor.Core(ctx, entry)
	if err != nil {
		return nil, err
	}
	return processor.validateCore(ctx, core)
}

func (processor ContextProcessor) validateCore(ctx context.Context, core Entry) ([]string, error) {
	warnings, err := ValidateEntry(core)
	if err != nil {
		return nil, err
	}
	return processor.verifyIdentity(ctx, core, warnings)
}

func (processor ContextProcessor) verifyIdentity(ctx context.Context, core Entry, warnings []string) ([]string, error) {
	verifier := processor.VerifyIdentity
	if verifier == nil && processor.Client != nil {
		verifier = processor.Client.config.VerifyIdentity
	}
	if core.ID != "" && core.ID != core.Identifier {
		if verifier == nil {
			return nil, fmt.Errorf("ard: distinct @id requires an identity equivalence verifier")
		}
		if err := verifier(ctx, core.Identifier, core.ID); err != nil {
			return nil, fmt.Errorf("ard: @id equivalence: %w", err)
		}
		filtered := warnings[:0]
		for _, warning := range warnings {
			if warning != "@id resource equivalence requires application verification" {
				filtered = append(filtered, warning)
			}
		}
		warnings = filtered
	}
	return warnings, nil
}

type contextTerm struct {
	iri       string
	typeIRI   string
	container string
	prefix    bool
	language  *string
	direction *string
	scoped    any
	hasScoped bool
	source    string
	protected bool
	reverse   bool
	index     string
}

type entryContext struct {
	baseDisabled bool
	vocab        string
	base         string
	language     string
	terms        map[string]contextTerm
	direction    string
	previous     *entryContext
}

type contextExpansion struct {
	validationContexts map[string]bool
	remoteDepth        int
	ctx                context.Context
	client             *Client
	remaining          int
	loading            map[string]bool
	initial            entryContext
}

func decodeContextJSON(data []byte) (any, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("ard: invalid context JSON")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func expandedString(value any, reference bool) (string, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", fmt.Errorf("expected a string value")
	}
	key := "@value"
	if reference {
		key = "@id"
	}
	text, ok := object[key].(string)
	if !ok {
		return "", fmt.Errorf("expected a string %s", key)
	}
	return text, nil
}

func (expansion *contextExpansion) guard(depth int) error {
	if err := expansion.ctx.Err(); err != nil {
		return err
	}
	if depth > 64 {
		return fmt.Errorf("ard: context or node nesting limit exceeded")
	}
	return nil
}

func (active entryContext) clone() entryContext {
	terms := make(map[string]contextTerm, len(active.terms))
	for name, term := range active.terms {
		terms[name] = term
	}
	active.terms = terms
	return active
}

func resolveContextIRI(value, base string) (string, error) {
	if strings.HasPrefix(value, "_:") {
		return value, nil
	}
	if strings.HasPrefix(value, ":") {
		value = "./" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		return value, nil
	}
	if base == "" {
		return "", fmt.Errorf("ard: relative IRI %q has no base", value)
	}
	origin, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return origin.ResolveReference(parsed).String(), nil
}

func (active entryContext) iri(value string, vocab bool) (string, error) {
	if strings.HasPrefix(value, "@") {
		switch value {
		case "@id", "@type", "@value", "@language", "@direction", "@list", "@set", "@json", "@vocab", "@graph", "@included", "@reverse", "@nest", "@index", "@none", "@context":
			return value, nil
		}
		if reservedContextName(value) {
			return "", nil
		}
	}
	if vocab {
		if term, exists := active.terms[value]; exists {
			return term.iri, nil
		}
	}
	if strings.HasPrefix(value, "_:") {
		return value, nil
	}
	if prefix, suffix, found := strings.Cut(value, ":"); found && prefix != "" {
		if term, exists := active.terms[prefix]; exists && term.prefix && !strings.HasPrefix(suffix, "//") {
			return term.iri + suffix, nil
		}
		return resolveContextIRI(value, active.base)
	}
	if vocab && active.vocab != "" {
		return active.vocab + value, nil
	}
	if vocab {
		return "", nil
	}
	if active.baseDisabled {
		return value, nil
	}
	return resolveContextIRI(value, active.base)
}

func reservedContextName(value string) bool {
	if len(value) < 2 || value[0] != '@' {
		return false
	}
	for _, character := range value[1:] {
		if character < 'A' || (character > 'Z' && character < 'a') || character > 'z' {
			return false
		}
	}
	return true
}

func (expansion *contextExpansion) apply(active entryContext, raw any, source string, depth int) (entryContext, error) {
	if err := expansion.guard(depth); err != nil {
		return active, err
	}
	switch value := raw.(type) {
	case nil:
		for _, term := range active.terms {
			if term.protected {
				return active, newJSONLDError("invalid context nullification", "cannot clear protected context")
			}
		}
		return expansion.initial.clone(), nil
	case []any:
		var err error
		for _, item := range value {
			active, err = expansion.apply(active, item, source, depth+1)
			if err != nil {
				return active, err
			}
		}
		return active, nil
	case string:
		target, err := resolveContextIRI(value, source)
		if err != nil {
			return active, err
		}
		if target == BaseContextURL {
			base, err := decodeContextJSON([]byte(baseContextJSON))
			if err != nil {
				return active, err
			}
			return expansion.apply(active, base.(map[string]any)["@context"], target, depth+1)
		}
		if expansion.client == nil {
			return active, newJSONLDError("loading remote context failed", "remote context loading is disabled")
		}
		if expansion.loading[target] {
			if expansion.validationContexts[target] {
				return active, nil
			}
			return active, newJSONLDError("context overflow", "cyclic remote context")
		}
		expansion.remaining--
		if expansion.remaining < 0 {
			return active, newJSONLDError("context overflow", "remote context budget exceeded")
		}
		expansion.loading[target] = true
		defer delete(expansion.loading, target)
		data, _, finalURL, err := expansion.client.fetch(expansion.ctx, target, "application/ld+json, application/json")
		if err != nil {
			return active, newJSONLDError("loading remote context failed", "remote context loading: %w", err)
		}
		document, err := decodeContextJSON(data)
		if err != nil {
			return active, err
		}
		object, ok := document.(map[string]any)
		if !ok {
			return active, newJSONLDError("invalid remote context", "remote context must be an object")
		}
		remote, exists := object["@context"]
		if !exists {
			return active, newJSONLDError("invalid remote context", "remote document has no @context")
		}
		expansion.remoteDepth++
		defer func() { expansion.remoteDepth-- }()
		return expansion.apply(active, remote, finalURL, depth+1)
	case map[string]any:
		active = active.clone()
		origins := map[string]string{}
		vocabularySource := source
		if imported, exists := value["@import"]; exists {
			target, ok := imported.(string)
			if !ok {
				return active, newJSONLDError("invalid @import value", "@import must be a URL")
			}
			resolved, err := resolveContextIRI(target, source)
			if err != nil {
				return active, err
			}
			if expansion.client == nil {
				return active, newJSONLDError("loading remote context failed", "remote context loading is disabled")
			}
			expansion.remaining--
			if expansion.remaining < 0 {
				return active, newJSONLDError("context overflow", "remote context budget exceeded")
			}
			data, _, finalURL, err := expansion.client.fetch(expansion.ctx, resolved, "application/ld+json, application/json")
			if err != nil {
				return active, err
			}
			document, err := decodeContextJSON(data)
			if err != nil {
				return active, err
			}
			object, ok := document.(map[string]any)
			if !ok {
				return active, newJSONLDError("invalid remote context", "imported context must be an object")
			}
			imported, ok := object["@context"].(map[string]any)
			if !ok {
				return active, newJSONLDError("invalid remote context", "imported @context must be an object")
			}
			if _, nested := imported["@import"]; nested {
				return active, newJSONLDError("invalid context entry", "nested @import is forbidden")
			}
			merged := map[string]any{}
			for name, definition := range imported {
				if name == "@base" {
					continue
				}
				merged[name] = definition
				origins[name] = finalURL
			}
			for name, definition := range value {
				merged[name] = definition
				origins[name] = source
			}
			if origin, exists := origins["@vocab"]; exists {
				vocabularySource = origin
			}
			value = merged
		}
		defaultProtected := false
		if protected, exists := value["@protected"]; exists {
			var ok bool
			defaultProtected, ok = protected.(bool)
			if !ok {
				return active, newJSONLDError("invalid @protected value", "@protected must be a boolean")
			}
		}
		if propagate, exists := value["@propagate"]; exists {
			flag, ok := propagate.(bool)
			if !ok {
				return active, newJSONLDError("invalid @propagate value", "@propagate must be a boolean")
			}
			if !flag && active.previous == nil {
				prior := active.clone()
				active.previous = &prior
			}
		}
		definitions := map[string]any{}
		for name, definition := range value {
			if !strings.HasPrefix(name, "@") {
				definitions[name] = definition
				continue
			}
			switch name {
			case "@version":
				if definition != json.Number("1.1") {
					return active, newJSONLDError("invalid @version value", "only JSON-LD 1.1 is supported")
				}
			case "@type":
				mapping, ok := definition.(map[string]any)
				if !ok {
					return active, newJSONLDError("keyword redefinition", "invalid @type context definition")
				}
				_, hasProtection := mapping["@protected"]
				if _, hasContainer := mapping["@container"]; !hasContainer && !hasProtection {
					return active, newJSONLDError("keyword redefinition", "@type context definition requires @set")
				}
				term := contextTerm{iri: "@type", protected: defaultProtected}
				for key, value := range mapping {
					switch key {
					case "@container":
						if value != "@set" {
							return active, newJSONLDError("keyword redefinition", "@type container must be @set")
						}
						term.container = "@set"
					case "@protected":
						var ok bool
						term.protected, ok = value.(bool)
						if !ok {
							return active, newJSONLDError("invalid @protected value", "@protected must be a boolean")
						}
					default:
						return active, newJSONLDError("keyword redefinition", "invalid @type context definition")
					}
				}
				if previous := active.terms["@type"]; previous.protected {
					if previous.container != term.container {
						return active, newJSONLDError("protected term redefinition", "protected term @type cannot be redefined")
					}
					term.protected = true
				}
				active.terms["@type"] = term
			case "@import", "@protected", "@propagate":
			case "@vocab", "@base", "@language", "@direction":
				if definition != nil {
					if _, ok := definition.(string); !ok {
						code := map[string]string{"@vocab": "invalid vocab mapping", "@base": "invalid base IRI", "@language": "invalid default language", "@direction": "invalid base direction"}[name]
						return active, newJSONLDError(code, "invalid %s", name)
					}
				}
			default:
				if reservedContextName(name) {
					iri, err := active.iri(name, true)
					if err != nil {
						return active, err
					}
					if iri != "" {
						return active, newJSONLDError("keyword redefinition", "keyword redefinition")
					}
				}
				if !reservedContextName(name) {
					definitions[name] = definition
				}
			}
		}
		if rawBase, exists := value["@base"]; exists && expansion.remoteDepth == 0 {
			if rawBase == nil {
				active.base = ""
				active.baseDisabled = true
			} else {
				resolved, err := resolveContextIRI(rawBase.(string), active.base)
				if err != nil {
					return active, err
				}
				active.base = resolved
				active.baseDisabled = false
			}
		}
		if rawVocab, exists := value["@vocab"]; exists {
			if rawVocab == nil {
				active.vocab = ""
			} else {
				resolved, err := active.iri(rawVocab.(string), true)
				if err != nil {
					return active, err
				}
				if resolved == "" {
					base := active.base
					if base == "" {
						base = vocabularySource
					}
					resolved, err = resolveContextIRI(rawVocab.(string), base)
					if err != nil {
						return active, err
					}
				}
				active.vocab = resolved
			}
		}
		if language, exists := value["@language"]; exists {
			active.language = ""
			if language != nil {
				active.language = strings.ToLower(language.(string))
			}
		}
		if direction, exists := value["@direction"]; exists {
			active.direction = ""
			if direction != nil {
				active.direction = direction.(string)
				if active.direction != "ltr" && active.direction != "rtl" {
					return active, newJSONLDError("invalid base direction", "invalid @direction")
				}
			}
		}
		state := map[string]int{}
		definitionDepth := 0
		var define func(string) error
		define = func(name string) error {
			if name == "" {
				return newJSONLDError("invalid term definition", "empty term definition")
			}
			definitionDepth++
			defer func() { definitionDepth-- }()
			if err := expansion.guard(depth + definitionDepth); err != nil {
				return err
			}
			if state[name] == 2 {
				return nil
			}
			if state[name] == 1 {
				return newJSONLDError("cyclic IRI mapping", "cyclic term definition %q", name)
			}
			state[name] = 1
			raw := definitions[name]
			termSource := source
			if origin, exists := origins[name]; exists {
				termSource = origin
			}
			term := contextTerm{protected: defaultProtected, source: termSource}
			assign := func() error {
				if previous, exists := active.terms[name]; exists && previous.protected {
					candidate, old := term, previous
					candidate.protected, old.protected = false, false
					candidate.source, old.source = "", ""
					if !reflect.DeepEqual(candidate, old) {
						return newJSONLDError("protected term redefinition", "protected term %q cannot be redefined", name)
					}
					term.protected = true
				}
				active.terms[name] = term
				state[name] = 2
				return nil
			}
			if raw == nil {
				return assign()
			}
			mapping := ""
			explicitPrefix := false
			switch definition := raw.(type) {
			case string:
				mapping = definition
			case map[string]any:
				for key := range definition {
					switch key {
					case "@id", "@type", "@prefix", "@container", "@language", "@direction", "@context", "@protected", "@reverse", "@index", "@nest":
					default:
						return newJSONLDError("invalid term definition", "unsupported term definition %q", key)
					}
				}
				if identifier, exists := definition["@id"]; exists {
					if identifier == nil {
						return assign()
					}
					var ok bool
					mapping, ok = identifier.(string)
					if !ok {
						return newJSONLDError("invalid IRI mapping", "term @id must be a string")
					}
				} else {
					mapping = name
				}
				if reverse, exists := definition["@reverse"]; exists {
					if _, duplicate := definition["@id"]; duplicate {
						return newJSONLDError("invalid reverse property", "@reverse and @id are mutually exclusive")
					}
					var ok bool
					mapping, ok = reverse.(string)
					if !ok {
						return newJSONLDError("invalid IRI mapping", "@reverse must be an IRI")
					}
					term.reverse = true
					if _, nested := definition["@nest"]; nested {
						return newJSONLDError("invalid reverse property", "reverse term cannot define @nest")
					}
				}
				if protected, exists := definition["@protected"]; exists {
					var ok bool
					term.protected, ok = protected.(bool)
					if !ok {
						return newJSONLDError("invalid @protected value", "@protected must be a boolean")
					}
				}
				if scoped, exists := definition["@context"]; exists {
					term.scoped = scoped
					term.hasScoped = true
				}
				if index, exists := definition["@index"]; exists {
					var ok bool
					term.index, ok = index.(string)
					if !ok {
						return newJSONLDError("invalid term definition", "@index must be a string")
					}
				}
				if nest, exists := definition["@nest"]; exists {
					name, ok := nest.(string)
					if !ok {
						return newJSONLDError("invalid @nest value", "@nest must be a string")
					}
					iri, err := active.iri(name, true)
					if err != nil || iri != "@nest" {
						return newJSONLDError("invalid @nest value", "invalid @nest alias")
					}
				}
				if typeValue, exists := definition["@type"]; exists {
					var ok bool
					term.typeIRI, ok = typeValue.(string)
					if !ok || term.typeIRI == "" {
						return newJSONLDError("invalid type mapping", "term @type must be a string")
					}
				}
				if prefix, exists := definition["@prefix"]; exists {
					var ok bool
					term.prefix, ok = prefix.(bool)
					if !ok {
						return newJSONLDError("invalid @prefix value", "@prefix must be a boolean")
					}
					explicitPrefix = true
				}
				if container, exists := definition["@container"]; exists {
					if container == nil {
						container = []any{}
					}
					containers, ok := container.([]any)
					if !ok {
						containers = []any{container}
					}
					var names []string
					for _, raw := range containers {
						name, ok := raw.(string)
						if !ok || !strings.Contains("|@set|@list|@language|@index|@id|@type|@graph|", "|"+name+"|") {
							return newJSONLDError("invalid container mapping", "invalid @container")
						}
						names = append(names, name)
					}
					sort.Strings(names)
					term.container = strings.Join(names, " ")
					if err := validateContainers(term); err != nil {
						return err
					}
				}
				if language, exists := definition["@language"]; exists {
					text := ""
					if language != nil {
						var ok bool
						text, ok = language.(string)
						if !ok {
							return newJSONLDError("invalid language mapping", "term @language must be a string")
						}
					}
					text = strings.ToLower(text)
					term.language = &text
				}
				if direction, exists := definition["@direction"]; exists {
					text := ""
					if direction != nil {
						var ok bool
						text, ok = direction.(string)
						if !ok || (text != "ltr" && text != "rtl") {
							return newJSONLDError("invalid base direction", "invalid @direction")
						}
					}
					term.direction = &text
				}
			default:
				return newJSONLDError("invalid term definition", "invalid term definition %q", name)
			}
			expandDefinition := func(text string) (string, error) {
				if text == name && !strings.Contains(text, ":") {
					if active.vocab == "" {
						return "", newJSONLDError("invalid IRI mapping", "term %q has no vocabulary", name)
					}
					return active.vocab + name, nil
				}
				dependency := text
				if prefix, suffix, found := strings.Cut(text, ":"); found {
					if strings.HasPrefix(suffix, "//") || prefix == "_" {
						return text, nil
					}
					dependency = prefix
				}
				if _, exists := definitions[dependency]; exists {
					if err := define(dependency); err != nil {
						return "", err
					}
				}
				return active.iri(text, true)
			}
			var err error
			if reservedContextName(mapping) {
				expanded, err := active.iri(mapping, true)
				if err != nil {
					return err
				}
				if expanded == "" {
					state[name] = 2
					return nil
				}
			}
			term.iri, err = expandDefinition(mapping)
			if err != nil {
				return err
			}
			if term.iri == "" {
				return newJSONLDError("invalid IRI mapping", "invalid IRI mapping")
			}
			if term.iri == "@context" {
				return newJSONLDError("invalid keyword alias", "invalid keyword alias")
			}
			if term.prefix && strings.ContainsAny(name, ":/") {
				return newJSONLDError("invalid term definition", "invalid prefix definition")
			}
			if strings.ContainsAny(name, ":/") && !strings.HasPrefix(name, ":") {
				expected, err := active.iri(name, false)
				if err != nil || expected != term.iri {
					return newJSONLDError("invalid IRI mapping", "inconsistent IRI term mapping")
				}
			}
			if !explicitPrefix && !strings.ContainsAny(name, ":/") && !strings.HasPrefix(term.iri, "@") {
				term.prefix = strings.HasPrefix(term.iri, "_:") || strings.ContainsAny(term.iri[len(term.iri)-1:], "#/:?[]@")
			}
			if term.typeIRI != "" {
				term.typeIRI, err = expandDefinition(term.typeIRI)
				if err != nil {
					return err
				}
				if term.typeIRI == "" || strings.HasPrefix(term.typeIRI, "_:") {
					return newJSONLDError("invalid type mapping", "invalid type mapping")
				}
				if strings.HasPrefix(term.typeIRI, "@") && term.typeIRI != "@id" && term.typeIRI != "@vocab" && term.typeIRI != "@json" && term.typeIRI != "@none" {
					return newJSONLDError("invalid type mapping", "unsupported type coercion")
				}
			}
			if term.reverse && ((term.typeIRI != "" && term.typeIRI != "@id" && term.typeIRI != "@vocab") || term.language != nil || term.direction != nil) {
				return newJSONLDError("invalid reverse property", "invalid reverse term coercion")
			}
			if term.reverse {
				for _, container := range strings.Fields(term.container) {
					if container != "@set" && container != "@index" {
						return newJSONLDError("invalid reverse property", "invalid reverse container")
					}
				}
			}
			if term.prefix && (strings.ContainsAny(name, ":/") || strings.HasPrefix(term.iri, "@")) {
				return newJSONLDError("invalid term definition", "invalid prefix definition")
			}
			if term.index != "" && !hasContainer(term, "@index") {
				return newJSONLDError("invalid term definition", "custom @index requires an index container")
			}
			if term.index != "" && strings.HasPrefix(term.index, "@") {
				return newJSONLDError("invalid term definition", "custom @index cannot be a keyword")
			}
			if hasContainer(term, "@type") {
				if term.typeIRI == "" {
					term.typeIRI = "@id"
				}
				if term.typeIRI != "@id" && term.typeIRI != "@vocab" {
					return newJSONLDError("invalid type mapping", "type map requires IRI coercion")
				}
			}
			if err := assign(); err != nil {
				return err
			}
			if term.hasScoped {
				if remote, ok := term.scoped.(string); ok {
					base := term.source
					if base == "" {
						base = active.base
					}
					target, err := resolveContextIRI(remote, base)
					if err != nil {
						return err
					}
					if expansion.loading[target] {
						return nil
					}
				}
				validationContext := active.clone()
				for name, definition := range validationContext.terms {
					definition.protected = false
					validationContext.terms[name] = definition
				}
				previousValidation := expansion.validationContexts
				expansion.validationContexts = make(map[string]bool, len(expansion.loading))
				for target := range expansion.loading {
					expansion.validationContexts[target] = true
				}
				_, err := expansion.apply(validationContext, term.scoped, term.source, depth+1)
				expansion.validationContexts = previousValidation
				if err != nil {
					return newJSONLDError("invalid scoped context", "invalid scoped context: %w", err)
				}
			}
			return nil
		}
		if vocabulary, ok := value["@vocab"].(string); ok {
			if _, exists := definitions[vocabulary]; exists {
				if err := define(vocabulary); err != nil {
					return active, err
				}
			}
			if _, exists := active.terms[vocabulary]; exists {
				active.vocab = active.terms[vocabulary].iri
			}
			if prefix, _, found := strings.Cut(vocabulary, ":"); found {
				if _, exists := definitions[prefix]; exists {
					if err := define(prefix); err != nil {
						return active, err
					}
				}
				var err error
				active.vocab, err = active.iri(vocabulary, true)
				if err != nil {
					return active, err
				}
			}
		}
		for _, name := range sortedContextKeys(definitions) {
			if err := define(name); err != nil {
				return active, err
			}
		}
		return active, nil
	default:
		return active, newJSONLDError("invalid local context", "invalid @context")
	}
}

func sortedContextKeys(object map[string]any) []string {
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (expansion *contextExpansion) document(raw any, active entryContext, depth int) ([]any, error) {
	if err := expansion.guard(depth); err != nil {
		return nil, err
	}
	switch value := raw.(type) {
	case nil:
		return []any{}, nil
	case []any:
		var nodes []any
		for _, item := range value {
			expanded, err := expansion.document(item, active, depth+1)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, expanded...)
		}
		return nodes, nil
	case map[string]any:
		node, err := expansion.node(value, active, depth+1)
		if err != nil {
			return nil, err
		}
		if len(node) == 0 {
			return []any{}, nil
		}
		if _, exists := node["@value"]; exists {
			return []any{}, nil
		}
		if _, exists := node["@list"]; exists {
			return []any{}, nil
		}
		if len(node) == 1 {
			if _, exists := node["@id"]; exists {
				return []any{}, nil
			}
		}
		if set, exists := node["@set"]; exists {
			return expansion.document(set, active, depth+1)
		}
		if len(node) == 1 {
			if graph, ok := node["@graph"].([]any); ok {
				return graph, nil
			}
		}
		return []any{node}, nil
	default:
		return []any{}, nil
	}
}

func (expansion *contextExpansion) node(object map[string]any, active entryContext, depth int, propertyTerms ...contextTerm) (map[string]any, error) {
	if err := expansion.guard(depth); err != nil {
		return nil, err
	}
	propertyTerm := contextTerm{}
	if len(propertyTerms) > 0 {
		propertyTerm = propertyTerms[0]
	}
	if raw, exists := object["@context"]; exists {
		var err error
		active, err = expansion.apply(active, raw, active.base, depth+1)
		if err != nil {
			return nil, err
		}
	}
	typeContext := active.clone()
	for _, key := range sortedContextKeys(object) {
		if key == "@context" {
			continue
		}
		iri, err := active.iri(key, true)
		if err != nil {
			return nil, err
		}
		if iri != "@type" {
			continue
		}
		values, ok := object[key].([]any)
		if !ok {
			values = []any{object[key]}
		}
		var names []string
		for _, raw := range values {
			if name, ok := raw.(string); ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			term := typeContext.terms[name]
			if !term.hasScoped {
				continue
			}
			active, err = expansion.apply(active, term.scoped, term.source, depth+1)
			if err != nil {
				return nil, err
			}
			propagate := false
			if scoped, ok := term.scoped.(map[string]any); ok {
				propagate, _ = scoped["@propagate"].(bool)
			}
			if !propagate && active.previous == nil {
				prior := typeContext.clone()
				active.previous = &prior
			}
		}
	}
	node := map[string]any{}
	for _, key := range sortedContextKeys(object) {
		if key == "@context" {
			continue
		}
		iri, err := active.iri(key, true)
		if err != nil {
			return nil, err
		}
		if iri == "" {
			continue
		}
		value := object[key]
		if value == nil && iri != "@value" && iri != "@set" {
			if iri == Namespace+"url" && expansion.initial.vocab == Namespace {
				return nil, fmt.Errorf("ard: url must not be null")
			}
			if active.terms[key].typeIRI != "@json" {
				continue
			}
		}
		if iri == "@graph" || iri == "@included" {
			childContext := active
			if active.previous != nil {
				childContext = *active.previous
			}
			var expanded []any
			if iri == "@included" {
				expanded, err = expansion.values(value, childContext, contextTerm{}, depth+1)
				if err == nil {
					for _, item := range expanded {
						object := item.(map[string]any)
						if _, literal := object["@value"]; literal {
							return nil, newJSONLDError("invalid @included value", "invalid @included value")
						}
						if _, list := object["@list"]; list {
							return nil, newJSONLDError("invalid @included value", "invalid @included value")
						}
					}
				}
			} else {
				expanded, err = expansion.document(value, childContext, depth+1)
			}
			if err != nil {
				return nil, err
			}
			if previous, ok := node[iri].([]any); ok {
				expanded = append(previous, expanded...)
			}
			node[iri] = expanded
			continue
		}
		if iri == "@list" || iri == "@set" {
			expanded, err := expansion.values(value, active, propertyTerm, depth+1)
			if err != nil {
				return nil, err
			}
			node[iri] = expanded
			continue
		}
		if iri == "@nest" {
			nestContext := active
			term := active.terms[key]
			if term.hasScoped {
				nestContext, err = expansion.apply(active, term.scoped, term.source, depth+1)
				if err != nil {
					return nil, err
				}
			}
			items, ok := value.([]any)
			if !ok {
				items = []any{value}
			}
			for _, raw := range items {
				object, ok := raw.(map[string]any)
				if !ok {
					return nil, newJSONLDError("invalid @nest value", "@nest must contain objects")
				}
				nested, err := expansion.node(object, nestContext, depth+1)
				if err != nil {
					return nil, err
				}
				for key, value := range nested {
					if key == "@value" {
						return nil, newJSONLDError("invalid @nest value", "@nest cannot contain a value object")
					}
					if key == "@id" {
						if _, duplicate := node[key]; duplicate {
							return nil, newJSONLDError("colliding keywords", "duplicate @id")
						}
					}
					if values, ok := value.([]any); ok {
						if previous, ok := node[key].([]any); ok {
							value = append(previous, values...)
						}
					}
					node[key] = value
				}
			}
			continue
		}
		if iri == "@reverse" {
			object, ok := value.(map[string]any)
			if !ok {
				return nil, newJSONLDError("invalid @reverse value", "@reverse requires an object")
			}
			reversed, err := expansion.node(object, active, depth+1)
			if err != nil {
				return nil, err
			}
			for property, raw := range reversed {
				if property == "@reverse" {
					for forward, values := range raw.(map[string]any) {
						if previous, ok := node[forward].([]any); ok {
							values = append(previous, values.([]any)...)
						}
						node[forward] = values
					}
					continue
				}
				if strings.HasPrefix(property, "@") {
					return nil, newJSONLDError("invalid reverse property map", "invalid reverse property")
				}
				if err := addReverse(node, property, raw.([]any)); err != nil {
					return nil, err
				}
			}
			continue
		}
		if iri == "@value" || iri == "@language" || iri == "@direction" || iri == "@index" {
			if _, exists := node[iri]; exists {
				return nil, newJSONLDError("colliding keywords", "duplicate %s", iri)
			}
			if iri == "@language" {
				text, ok := value.(string)
				if !ok {
					return nil, newJSONLDError("invalid language-tagged string", "@language must be a string")
				}
				value = strings.ToLower(text)
			}
			if iri == "@direction" && value != "ltr" && value != "rtl" {
				return nil, newJSONLDError("invalid base direction", "invalid @direction")
			}
			if iri == "@index" {
				if _, ok := value.(string); !ok {
					return nil, newJSONLDError("invalid @index value", "@index must be a string")
				}
			}
			node[iri] = value
			continue
		}
		if iri == "@id" {
			text, ok := value.(string)
			if !ok {
				return nil, newJSONLDError("invalid @id value", "@id must be a string")
			}
			identifier, err := active.iri(text, false)
			if err != nil {
				return nil, err
			}
			if _, exists := node[iri]; exists {
				return nil, newJSONLDError("colliding keywords", "duplicate @id")
			}
			if identifier == "" {
				node[iri] = nil
			} else {
				node[iri] = identifier
			}
			continue
		}
		if iri == "@type" {
			items, ok := value.([]any)
			if !ok {
				items = []any{value}
			}
			var types []any
			for _, item := range items {
				text, ok := item.(string)
				if !ok {
					return nil, newJSONLDError("invalid type value", "@type must contain strings")
				}
				if text == "@json" {
					types = append(types, text)
					continue
				}
				resolved, err := typeContext.iri(text, true)
				if err != nil {
					return nil, err
				}
				if resolved == "" {
					resolved, err = typeContext.iri(text, false)
					if err != nil {
						return nil, err
					}
				}
				types = append(types, resolved)
			}
			if previous, ok := node[iri].([]any); ok {
				types = append(previous, types...)
			}
			node[iri] = types
			continue
		}
		if strings.HasPrefix(iri, "@") {
			return nil, fmt.Errorf("ard: unsupported node keyword %q", iri)
		}
		term := active.terms[key]
		valueContext := active
		if term.hasScoped {
			valueContext = active.clone()
			for name, definition := range valueContext.terms {
				definition.protected = false
				valueContext.terms[name] = definition
			}
			valueContext, err = expansion.apply(valueContext, term.scoped, term.source, depth+1)
			if err != nil {
				return nil, err
			}
		}
		var values []any
		if term.typeIRI == "@json" {
			values = []any{map[string]any{"@value": value, "@type": "@json"}}
		} else {
			values, err = expansion.propertyValues(value, valueContext, term, depth+1)
			if err != nil {
				return nil, err
			}
			if hasContainer(term, "@list") {
				_, array := value.([]any)
				if len(values) != 1 || array {
					values = []any{map[string]any{"@list": values}}
				} else if _, list := values[0].(map[string]any)["@list"]; !list {
					values = []any{map[string]any{"@list": values}}
				}
			}
		}
		if term.reverse {
			if err := addReverse(node, iri, values); err != nil {
				return nil, err
			}
			continue
		}
		if previous, ok := node[iri].([]any); ok {
			values = append(previous, values...)
		}
		if values == nil {
			keepEmpty := false
			if object, ok := value.(map[string]any); ok {
				for property := range object {
					resolved, err := valueContext.iri(property, true)
					if err != nil {
						return nil, err
					}
					if resolved == "@set" {
						keepEmpty = true
					}
				}
			}
			if !keepEmpty {
				continue
			}
		}
		node[iri] = values
	}
	if value, exists := node["@value"]; exists {
		for key := range node {
			if key != "@value" && key != "@type" && key != "@language" && key != "@direction" && key != "@index" {
				return nil, newJSONLDError("invalid value object", "invalid value object")
			}
		}
		if types, ok := node["@type"].([]any); ok {
			if len(types) != 1 {
				return nil, newJSONLDError("invalid typed value", "value object requires one type")
			}
			node["@type"] = types[0]
		}
		if node["@type"] != "@json" {
			if datatype, ok := node["@type"].(string); ok {
				parsed, err := url.Parse(datatype)
				if err != nil || !parsed.IsAbs() || strings.HasPrefix(datatype, "_:") || strings.ContainsAny(datatype, " \t\r\n") {
					return nil, newJSONLDError("invalid typed value", "invalid typed value")
				}
			}
			if _, direction := node["@direction"]; direction {
				if _, typed := node["@type"]; typed {
					return nil, newJSONLDError("invalid value object", "typed value cannot carry @direction")
				}
				if _, text := value.(string); !text {
					return nil, newJSONLDError("invalid language-tagged value", "directed value must be a string")
				}
			}
			switch value.(type) {
			case nil, string, bool, json.Number:
			default:
				return nil, newJSONLDError("invalid value object value", "invalid literal value")
			}
			if _, language := node["@language"]; language {
				if _, typed := node["@type"]; typed {
					return nil, newJSONLDError("invalid value object", "typed value cannot carry @language")
				}
				if _, text := value.(string); !text {
					return nil, newJSONLDError("invalid language-tagged value", "language-tagged value must be a string")
				}
			}
		}
	} else if _, exists := node["@language"]; exists && len(node) == 1 {
		return map[string]any{"@value": nil}, nil
	}
	if _, list := node["@list"]; list {
		for key := range node {
			if key != "@list" && key != "@index" {
				return nil, newJSONLDError("invalid set or list object", "invalid list object")
			}
		}
	}
	if _, set := node["@set"]; set {
		for key := range node {
			if key != "@set" && key != "@index" {
				return nil, newJSONLDError("invalid set or list object", "invalid set object")
			}
		}
	}
	return node, nil
}

func addReverse(node map[string]any, iri string, values []any) error {
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return newJSONLDError("invalid reverse property value", "reverse values must be nodes")
		}
		if _, literal := object["@value"]; literal {
			return newJSONLDError("invalid reverse property value", "reverse values cannot be literals")
		}
		if _, list := object["@list"]; list {
			return newJSONLDError("invalid reverse property value", "reverse values cannot be lists")
		}
	}
	reversed, ok := node["@reverse"].(map[string]any)
	if !ok {
		reversed = map[string]any{}
		node["@reverse"] = reversed
	}
	if previous, ok := reversed[iri].([]any); ok {
		values = append(previous, values...)
	}
	reversed[iri] = values
	return nil
}

func hasContainer(term contextTerm, name string) bool {
	for _, container := range strings.Fields(term.container) {
		if container == name {
			return true
		}
	}
	return false
}

func validateContainers(term contextTerm) error {
	names := strings.Fields(term.container)
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return newJSONLDError("invalid container mapping", "duplicate @container")
		}
		seen[name] = true
	}
	if seen["@list"] && len(names) != 1 {
		return newJSONLDError("invalid container mapping", "@list cannot be combined with other containers")
	}
	if seen["@language"] {
		if len(names) > 2 || (len(names) == 2 && !seen["@set"]) {
			return newJSONLDError("invalid container mapping", "invalid language container combination")
		}
		return nil
	}
	maps := 0
	for _, name := range []string{"@index", "@id", "@type"} {
		if seen[name] {
			maps++
		}
	}
	if maps > 1 || (seen["@graph"] && seen["@type"]) {
		return newJSONLDError("invalid container mapping", "invalid map container combination")
	}
	return nil
}

func (expansion *contextExpansion) propertyValues(raw any, active entryContext, term contextTerm, depth int) ([]any, error) {
	if err := expansion.guard(depth); err != nil {
		return nil, err
	}
	object, mapped := raw.(map[string]any)
	if mapped && (hasContainer(term, "@language") || hasContainer(term, "@index") || hasContainer(term, "@id") || hasContainer(term, "@type")) {
		var result []any
		for _, key := range sortedContextKeys(object) {
			var values []any
			if hasContainer(term, "@language") {
				items, ok := object[key].([]any)
				if !ok {
					items = []any{object[key]}
				}
				for _, item := range items {
					if item == nil {
						continue
					}
					text, ok := item.(string)
					if !ok {
						return nil, newJSONLDError("invalid language map value", "language map values must be strings")
					}
					value := map[string]any{"@value": text}
					resolved, err := active.iri(key, true)
					if err != nil {
						return nil, err
					}
					if resolved != "@none" {
						value["@language"] = strings.ToLower(key)
					}
					direction := active.direction
					if term.direction != nil {
						direction = *term.direction
					}
					if direction != "" {
						value["@direction"] = direction
					}
					values = append(values, value)
				}
			} else {
				mapContext := active
				valueTerm := term
				if hasContainer(term, "@type") {
					if active.previous != nil {
						mapContext = *active.previous
					}
					definition := mapContext.terms[key]
					if definition.hasScoped {
						var err error
						mapContext, err = expansion.apply(mapContext, definition.scoped, definition.source, depth+1)
						if err != nil {
							return nil, err
						}
						valueTerm.hasScoped = true
					}
				}
				var err error
				values, err = expansion.values(object[key], mapContext, valueTerm, depth+1)
				if err != nil {
					return nil, err
				}
				if hasContainer(term, "@graph") {
					values = graphContainerValues(values, true)
				}
				resolved, err := active.iri(key, true)
				if err != nil {
					return nil, err
				}
				if resolved != "@none" {
					for _, raw := range values {
						value := raw.(map[string]any)
						switch {
						case hasContainer(term, "@id"):
							if _, exists := value["@id"]; !exists {
								identifier, err := active.iri(key, false)
								if err != nil {
									return nil, err
								}
								value["@id"] = identifier
							}
						case hasContainer(term, "@type"):
							types, _ := value["@type"].([]any)
							value["@type"] = append([]any{resolved}, types...)
						case term.index != "":
							if _, literal := value["@value"]; literal {
								return nil, newJSONLDError("invalid value object", "custom index cannot add properties to a value object")
							}
							iri, err := active.iri(term.index, true)
							if err != nil {
								return nil, err
							}
							indices, err := expansion.values(key, active, active.terms[term.index], depth+1)
							if err != nil {
								return nil, err
							}
							previous, _ := value[iri].([]any)
							value[iri] = append(indices, previous...)
						default:
							if _, exists := value["@index"]; !exists {
								value["@index"] = key
							}
						}
					}
				}
			}
			result = append(result, values...)
		}
		return result, nil
	}
	values, err := expansion.values(raw, active, term, depth+1)
	if err != nil {
		return nil, err
	}
	if hasContainer(term, "@graph") {
		values = graphContainerValues(values, hasContainer(term, "@id") || hasContainer(term, "@index"))
	}
	return values, nil
}

func graphContainerValues(values []any, preserve bool) []any {
	for index, value := range values {
		object := value.(map[string]any)
		if _, graph := object["@graph"]; !graph || !preserve {
			values[index] = map[string]any{"@graph": []any{value}}
		}
	}
	return values
}

func (expansion *contextExpansion) values(raw any, active entryContext, term contextTerm, depth int) ([]any, error) {
	if err := expansion.guard(depth); err != nil {
		return nil, err
	}
	switch value := raw.(type) {
	case nil:
		return nil, nil
	case []any:
		values := make([]any, 0, len(value))
		for _, item := range value {
			expanded, err := expansion.values(item, active, term, depth+1)
			if err != nil {
				return nil, err
			}
			if _, nested := item.([]any); nested && hasContainer(term, "@list") {
				expanded = []any{map[string]any{"@list": expanded}}
			}
			values = append(values, expanded...)
		}
		return values, nil
	case map[string]any:
		childContext := active
		if active.previous != nil && !term.hasScoped {
			preserve := false
			for key := range value {
				iri, err := active.iri(key, true)
				if err != nil {
					return nil, err
				}
				if iri == "@value" || (iri == "@id" && len(value) == 1) {
					preserve = true
				}
			}
			if !preserve {
				childContext = *active.previous
			}
		}
		node, err := expansion.node(value, childContext, depth+1, term)
		if err != nil {
			return nil, err
		}
		if set, exists := node["@set"].([]any); exists {
			return set, nil
		}
		if literal, exists := node["@value"]; exists && literal == nil && node["@type"] != "@json" {
			return nil, nil
		}
		return []any{node}, nil
	default:
		text, textValue := value.(string)
		if textValue && (term.typeIRI == "@id" || term.typeIRI == "@vocab") {
			iri, err := active.iri(text, term.typeIRI == "@vocab")
			if err != nil {
				return nil, err
			}
			if iri == "" {
				iri, err = active.iri(text, false)
				if err != nil {
					return nil, err
				}
			}
			var identifier any = iri
			if iri == "" {
				identifier = nil
			}
			return []any{map[string]any{"@id": identifier}}, nil
		}
		literal := map[string]any{"@value": value}
		if term.typeIRI != "" && term.typeIRI != "@none" && term.typeIRI != "@id" && term.typeIRI != "@vocab" {
			literal["@type"] = term.typeIRI
		} else if _, ok := value.(string); ok {
			language := active.language
			if term.language != nil {
				language = *term.language
			}
			if language != "" {
				literal["@language"] = language
			}
			direction := active.direction
			if term.direction != nil {
				direction = *term.direction
			}
			if direction != "" {
				literal["@direction"] = direction
			}
		}
		return []any{literal}, nil
	}
}
