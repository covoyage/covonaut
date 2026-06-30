package a2ui

import (
	"encoding/json"

	"github.com/covoyage/covonaut/agui"
)

// AGUIEventName is the name used for AG-UI custom events that carry A2UI
// envelopes. The A2UI binding rides on AG-UI's CUSTOM event channel, with the
// envelope placed in the event value.
const AGUIEventName = "a2ui"

// ToCustomEvent wraps an A2UI envelope in an AG-UI custom event so it can be
// streamed over an AG-UI transport.
func ToCustomEvent(env Envelope) agui.CustomEvent {
	if env.Version == "" {
		env.Version = Version
	}
	return agui.CustomEvent{
		BaseEvent: agui.BaseEvent{Type: agui.EventCustom},
		Name:      AGUIEventName,
		Value:     env,
	}
}

// FromCustomEvent extracts an A2UI envelope from an AG-UI custom event. It
// returns ok=false for events that are not A2UI events.
func FromCustomEvent(ev agui.CustomEvent) (env Envelope, ok bool, err error) {
	if ev.Name != AGUIEventName {
		return Envelope{}, false, nil
	}
	raw, err := json.Marshal(ev.Value)
	if err != nil {
		return Envelope{}, false, err
	}
	parsed, err := ParseEnvelope(raw)
	if err != nil {
		return Envelope{}, false, nil
	}
	return parsed, true, nil
}

// EnvelopesToCustomEvents converts a batch of envelopes into AG-UI custom
// events, preserving order.
func EnvelopesToCustomEvents(envs []Envelope) []agui.CustomEvent {
	out := make([]agui.CustomEvent, 0, len(envs))
	for _, env := range envs {
		out = append(out, ToCustomEvent(env))
	}
	return out
}
