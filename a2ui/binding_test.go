package a2ui

import (
	"testing"

	"github.com/covoyage/covonaut/a2a"
	"github.com/covoyage/covonaut/agui"
)

func TestA2ABindingRoundTrip(t *testing.T) {
	envs := NewSurface("s", BasicCatalogID).
		Add(Column("root", "t"), Text("t", "hi")).
		Data("/x", 1).
		Build()

	msg, err := EnvelopesToMessage(string(a2a.RoleAgent), envs)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Parts) != len(envs) {
		t.Fatalf("parts = %d, want %d", len(msg.Parts), len(envs))
	}
	for _, p := range msg.Parts {
		if p.Type != a2a.PartTypeData || p.Data == nil || p.Data.MIMEType != MIMEType {
			t.Fatalf("unexpected part: %+v", p)
		}
	}

	got, err := MessageEnvelopes(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(envs) {
		t.Fatalf("decoded %d envelopes, want %d", len(got), len(envs))
	}
	if got[0].Kind() != KindCreateSurface {
		t.Fatalf("first kind = %v", got[0].Kind())
	}
	if got[1].UpdateComponents.Components[0].ID != "root" {
		t.Fatalf("component id lost: %+v", got[1].UpdateComponents.Components[0])
	}
}

func TestA2ABindingIgnoresNonA2UIParts(t *testing.T) {
	msg := a2a.Message{
		Role: string(a2a.RoleAgent),
		Parts: []a2a.Part{
			{Type: a2a.PartTypeText, Text: "hello"},
		},
	}
	got, err := MessageEnvelopes(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no envelopes, got %d", len(got))
	}
}

func TestAGUIBindingRoundTrip(t *testing.T) {
	env := NewCreateSurface("s", BasicCatalogID)
	ev := ToCustomEvent(env)
	if ev.Name != AGUIEventName || ev.Type != agui.EventCustom {
		t.Fatalf("unexpected custom event: %+v", ev)
	}

	got, ok, err := FromCustomEvent(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a2ui event to be recognized")
	}
	if got.Kind() != KindCreateSurface || got.CreateSurface.SurfaceID != "s" {
		t.Fatalf("decoded envelope wrong: %+v", got)
	}
}

func TestAGUIBindingIgnoresOtherEvents(t *testing.T) {
	ev := agui.CustomEvent{
		BaseEvent: agui.BaseEvent{Type: agui.EventCustom},
		Name:      "something-else",
	}
	if _, ok, err := FromCustomEvent(ev); err != nil || ok {
		t.Fatalf("non-a2ui event should be ignored: ok=%v err=%v", ok, err)
	}
}

func TestEnvelopesToCustomEventsBatch(t *testing.T) {
	envs := NewSurface("s", BasicCatalogID).
		Add(Column("root", "t"), Text("t", "hi")).
		Data("/x", 1).
		Build()

	events := EnvelopesToCustomEvents(envs)
	if len(events) != len(envs) {
		t.Fatalf("got %d events, want %d", len(events), len(envs))
	}
	for i, ev := range events {
		if ev.Name != AGUIEventName || ev.Type != agui.EventCustom {
			t.Fatalf("event %d: unexpected type/name: %+v", i, ev)
		}
		got, ok, err := FromCustomEvent(ev)
		if err != nil {
			t.Fatalf("event %d: decode error: %v", i, err)
		}
		if !ok {
			t.Fatalf("event %d: not recognized", i)
		}
		if got.Kind() != envs[i].Kind() {
			t.Fatalf("event %d: kind = %v, want %v", i, got.Kind(), envs[i].Kind())
		}
	}
}
