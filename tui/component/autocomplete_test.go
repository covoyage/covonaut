package component

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestAutocompleteStaticProvider(t *testing.T) {
	ac := NewAutocomplete(&StaticProvider{
		TriggerStr: "/",
		Suggestions: []core.Suggestion{
			{Label: "help", InsertText: "help"},
			{Label: "clear", InsertText: "clear"},
		},
	})

	var out string
	var cursor int64
	ac.OnApply(func(newValue string, newCursor int64, _ core.Suggestion) {
		out = newValue
		cursor = newCursor
	})

	// User types "/hel" with cursor at end.
	ac.Refresh("/hel", 4)
	if !ac.Active() {
		t.Fatalf("expected autocomplete active")
	}
	cur, ok := ac.list.CurrentItem()
	if !ok || cur.Value != "help" {
		t.Fatalf("expected help, got %v", cur)
	}
	ac.Update(core.KeyMsg{Data: "\t"})
	if out != "/help" {
		t.Fatalf("expected /help, got %q", out)
	}
	if cursor != int64(len([]rune("/help"))) {
		t.Fatalf("unexpected cursor %d", cursor)
	}
}

func TestAutocompleteNoTrigger(t *testing.T) {
	ac := NewAutocomplete(&StaticProvider{
		TriggerStr:  "/",
		Suggestions: []core.Suggestion{{Label: "help", InsertText: "help"}},
	})
	ac.Refresh("hello", 5)
	if ac.Active() {
		t.Fatalf("should not activate without trigger")
	}
}

func TestAutocompleteApplyCurrent(t *testing.T) {
	ac := NewAutocomplete(&StaticProvider{
		TriggerStr: "/",
		Suggestions: []core.Suggestion{
			{Label: "/quit", InsertText: "quit"},
		},
	})
	ac.Refresh("/q", 2)
	if !ac.Active() {
		t.Fatal("expected autocomplete active")
	}
	got, ok := ac.ApplyCurrent()
	if !ok || got != "/quit" {
		t.Fatalf("ApplyCurrent = %q, %v; want /quit, true", got, ok)
	}
	if ac.Active() {
		t.Fatal("ApplyCurrent should dismiss the popup")
	}
}
