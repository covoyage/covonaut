package component

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestSelectListNavigation(t *testing.T) {
	items := []SelectItem{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Beta"},
		{Value: "c", Label: "Gamma"},
	}
	sl := NewSelectList(items)
	sl.SetFocused(true)

	var selected SelectItem
	sl.OnSelect(func(it SelectItem) { selected = it })

	// Down twice, confirm -> Gamma
	sl.Update(core.KeyMsg{Data: "\x1b[B"})
	sl.Update(core.KeyMsg{Data: "\x1b[B"})
	sl.Update(core.KeyMsg{Data: "\r"})

	if selected.Value != "c" {
		t.Fatalf("expected c, got %q", selected.Value)
	}
}

func TestSelectListFilter(t *testing.T) {
	items := []SelectItem{
		{Value: "alpha", Label: "Alpha"},
		{Value: "beta", Label: "Beta"},
		{Value: "gamma", Label: "Gamma"},
		{Value: "banana", Label: "Banana"},
	}
	sl := NewSelectList(items)
	sl.SetFilter("ba")
	// "Banana" should match strongly ahead of "Beta"; alpha/gamma excluded.
	cur, ok := sl.CurrentItem()
	if !ok {
		t.Fatalf("expected at least one match")
	}
	if cur.Label != "Banana" && cur.Label != "Beta" {
		t.Fatalf("unexpected top match %q", cur.Label)
	}
}
