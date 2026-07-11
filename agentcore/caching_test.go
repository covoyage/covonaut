package agentcore

import (
	"context"
	"testing"
)

func TestPromptCaching_SystemPromptGetsMarker(t *testing.T) {
	ext := NewPromptCachingExtension()
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
		{Role: RoleUser, Content: "q"},
		{Role: RoleAssistant, Content: "a"},
		{Role: RoleUser, Content: "bye"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	if out[0].CacheControl == nil {
		t.Fatal("system prompt missing cache marker")
	}
	if out[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("got type %q want ephemeral", out[0].CacheControl.Type)
	}
}

func TestPromptCaching_TailBreakpoint(t *testing.T) {
	ext := NewPromptCachingExtension() // CacheLastN=3
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "m1"},
		{Role: RoleAssistant, Content: "m2"},
		{Role: RoleUser, Content: "m3"},
		{Role: RoleAssistant, Content: "m4"},
		{Role: RoleUser, Content: "m5"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	// len=6, CacheLastN=3 → idx=3 (the second user msg "m3")
	if out[3].CacheControl == nil {
		t.Fatal("tail breakpoint missing at idx 3")
	}
}

func TestPromptCaching_DoesNotMutateInput(t *testing.T) {
	ext := NewPromptCachingExtension()
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
		{Role: RoleUser, Content: "bye"},
	}
	_ = ext.TransformContext(context.Background(), msgs)
	if msgs[0].CacheControl != nil {
		t.Fatal("input slice was mutated")
	}
}

func TestPromptCaching_PreservesExistingMarker(t *testing.T) {
	ext := NewPromptCachingExtension()
	existing := &CacheControlMarker{Type: "ephemeral", TTL: "5m"}
	msgs := []Message{
		{Role: RoleSystem, Content: "sys", CacheControl: existing},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
		{Role: RoleUser, Content: "bye"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	if out[0].CacheControl != existing {
		t.Fatal("existing marker was overwritten")
	}
}

func TestPromptCaching_NoSystemMessage(t *testing.T) {
	ext := NewPromptCachingExtension()
	msgs := []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
		{Role: RoleUser, Content: "bye"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	// No system msg → only tail breakpoint. len=3, CacheLastN=3 → idx=0.
	if out[0].CacheControl == nil {
		t.Fatal("tail breakpoint missing at idx 0")
	}
}

func TestPromptCaching_TooFewMessages(t *testing.T) {
	ext := NewPromptCachingExtension(WithCacheLastN(10))
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	// len=2, CacheLastN=10 → idx=-8 <0, no tail marker. Only system.
	if out[0].CacheControl == nil {
		t.Fatal("system marker missing")
	}
	// idx 1 is user, should NOT have a marker (tail idx negative)
	if out[1].CacheControl != nil {
		t.Fatal("unexpected marker on idx 1")
	}
}

func TestPromptCaching_RespectsMaxBreakpoints(t *testing.T) {
	ext := NewPromptCachingExtension(WithMaxBreakpoints(1))
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "m1"},
		{Role: RoleAssistant, Content: "m2"},
		{Role: RoleUser, Content: "m3"},
		{Role: RoleAssistant, Content: "m4"},
		{Role: RoleUser, Content: "m5"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	// MaxBreakpoints=1 → system gets one, tail must NOT get one.
	count := 0
	for _, m := range out {
		if m.CacheControl != nil {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("got %d markers, want 1 (max enforced)", count)
	}
}

func TestPromptCaching_MaxBreakpointsZeroDisables(t *testing.T) {
	ext := NewPromptCachingExtension(WithMaxBreakpoints(0))
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
		{Role: RoleUser, Content: "bye"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	for i, m := range out {
		if m.CacheControl != nil {
			t.Fatalf("idx %d got marker, want none (disabled)", i)
		}
	}
}

func TestPromptCaching_ZeroValueDisabled(t *testing.T) {
	ext := &PromptCachingExtension{}
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	for i, m := range out {
		if m.CacheControl != nil {
			t.Fatalf("idx %d got marker, want none (zero-value disabled)", i)
		}
	}
}

func TestPromptCaching_DisableSystemPrompt(t *testing.T) {
	ext := NewPromptCachingExtension(WithCacheSystemPrompt(false))
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "m1"},
		{Role: RoleAssistant, Content: "m2"},
		{Role: RoleUser, Content: "m3"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	if out[0].CacheControl != nil {
		t.Fatal("system prompt got marker despite disabled")
	}
}

func TestPromptCaching_DisableTail(t *testing.T) {
	ext := NewPromptCachingExtension(WithCacheLastN(0))
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "m1"},
		{Role: RoleAssistant, Content: "m2"},
		{Role: RoleUser, Content: "m3"},
	}
	out := ext.TransformContext(context.Background(), msgs)
	if out[0].CacheControl == nil {
		t.Fatal("system marker missing")
	}
	for i := 1; i < len(out); i++ {
		if out[i].CacheControl != nil {
			t.Fatalf("idx %d got unexpected marker (tail disabled)", i)
		}
	}
}

func TestPromptCaching_Defaults(t *testing.T) {
	ext := NewPromptCachingExtension()
	if ext.MaxBreakpoints != 4 {
		t.Fatalf("MaxBreakpoints=%d want 4", ext.MaxBreakpoints)
	}
	if !ext.CacheSystemPrompt {
		t.Fatal("CacheSystemPrompt default should be true")
	}
	if ext.CacheLastN != 3 {
		t.Fatalf("CacheLastN=%d want 3", ext.CacheLastN)
	}
}

func TestPromptCaching_ImplementsExtensionInterfaces(t *testing.T) {
	ext := NewPromptCachingExtension()
	var _ Extension = ext
	var _ TransformContextProvider = ext
}
