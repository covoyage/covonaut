package a2ui

import (
	"encoding/json"
	"fmt"

	"github.com/covoyage/covonaut/a2a"
)

// A2AExtensionURI identifies the A2UI extension to the A2A protocol. Agents that
// emit A2UI advertise this URI in their A2A AgentCard.
const A2AExtensionURI = "https://a2ui.org/specification/v0_9_1/a2ui-extension"

// EnvelopeToDataPart encodes an A2UI envelope as an A2A data part tagged with the
// A2UI media type. Each A2UI envelope maps to exactly one A2A message part.
func EnvelopeToDataPart(env Envelope) (a2a.Part, error) {
	if env.Version == "" {
		env.Version = Version
	}
	data, err := envelopeToMap(env)
	if err != nil {
		return a2a.Part{}, err
	}
	return a2a.Part{
		Type: a2a.PartTypeData,
		Data: &a2a.DataPart{MIMEType: MIMEType, Data: data},
	}, nil
}

// DataPartToEnvelope decodes an A2A data part back into an A2UI envelope. It
// returns ok=false for parts that are not A2UI data parts.
func DataPartToEnvelope(p a2a.Part) (env Envelope, ok bool, err error) {
	if p.Type != a2a.PartTypeData || p.Data == nil {
		return Envelope{}, false, nil
	}
	if p.Data.MIMEType != "" && p.Data.MIMEType != MIMEType {
		return Envelope{}, false, nil
	}
	raw, err := json.Marshal(p.Data.Data)
	if err != nil {
		return Envelope{}, false, err
	}
	parsed, err := ParseEnvelope(raw)
	if err != nil {
		// Not an A2UI envelope; treat as non-match rather than a hard error.
		return Envelope{}, false, nil
	}
	return parsed, true, nil
}

// EnvelopesToMessage packs a sequence of A2UI envelopes into a single A2A
// message, one part per envelope, attributed to the given role
// (a2a.RoleAgent for server-to-client streams).
func EnvelopesToMessage(role string, envs []Envelope) (a2a.Message, error) {
	parts := make([]a2a.Part, 0, len(envs))
	for i, env := range envs {
		part, err := EnvelopeToDataPart(env)
		if err != nil {
			return a2a.Message{}, fmt.Errorf("a2ui: encode envelope %d: %w", i, err)
		}
		parts = append(parts, part)
	}
	return a2a.Message{Role: role, Parts: parts}, nil
}

// MessageEnvelopes extracts every A2UI envelope carried by an A2A message,
// preserving order and skipping non-A2UI parts.
func MessageEnvelopes(m a2a.Message) ([]Envelope, error) {
	var out []Envelope
	for i, p := range m.Parts {
		env, ok, err := DataPartToEnvelope(p)
		if err != nil {
			return nil, fmt.Errorf("a2ui: decode part %d: %w", i, err)
		}
		if ok {
			out = append(out, env)
		}
	}
	return out, nil
}

func envelopeToMap(env Envelope) (map[string]any, error) {
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m) // always succeeds when json.Marshal succeeds
	return m, nil
}
