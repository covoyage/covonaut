package agentcore

import (
	"context"
	"errors"
	"testing"
)

// stubProvider is a minimal Provider implementation for testing.
type stubProvider struct{}

func (s *stubProvider) Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	return &ProviderResponse{}, nil
}
func (s *stubProvider) Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error) {
	ch := make(chan StreamDelta)
	close(ch)
	return ch, nil
}

func TestNewEngineRegistry(t *testing.T) {
	r := NewEngineRegistry()
	if r.Default() != "compressor" {
		t.Fatalf("default = %q", r.Default())
	}
	names := r.List()
	if len(names) != 2 {
		t.Fatalf("expected 2 engines, got %d: %v", len(names), names)
	}
}

func TestEngineRegistryRegister(t *testing.T) {
	r := NewEngineRegistry()
	r.Register("custom", func(cfg ContextEngineConfig) ContextEngine {
		return &CompressorEngine{}
	})
	if len(r.List()) != 3 {
		t.Fatalf("expected 3 after register, got %d", len(r.List()))
	}
}

func TestEngineRegistryCreate(t *testing.T) {
	r := NewEngineRegistry()
	engine, err := r.Create("compressor", ContextEngineConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if engine.Name() != "compressor" {
		t.Fatalf("name = %q", engine.Name())
	}
}

func TestEngineRegistryCreateNotFound(t *testing.T) {
	r := NewEngineRegistry()
	_, err := r.Create("nonexistent", ContextEngineConfig{})
	if err == nil {
		t.Fatal("expected error")
	}
	var notFound *EngineNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected EngineNotFoundError, got %T", err)
	}
}

func TestEngineRegistryDefault(t *testing.T) {
	r := NewEngineRegistry()
	if r.Default() != "compressor" {
		t.Fatalf("default = %q", r.Default())
	}
}

func TestEngineNotFoundError(t *testing.T) {
	err := &EngineNotFoundError{Name: "test"}
	if err.Error() != "context engine 'test' not found" {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestCompressorEngineLifecycle(t *testing.T) {
	engine := NewCompressorEngine(ContextEngineConfig{
		ContextWindow:        100000,
		CompressionThreshold: 0.7,
		KeepRecentTokens:     2000,
		ProtectFirstN:        3,
	})

	if engine.Name() != "compressor" {
		t.Fatalf("name = %q", engine.Name())
	}
	if engine.CompressionCount() != 0 {
		t.Fatal("expected 0 compressions")
	}

	engine.OnSessionStart(context.Background(), "test-model", 100000)
	if engine.ContextLength() != 100000 {
		t.Fatalf("context length = %d", engine.ContextLength())
	}
	if engine.ThresholdTokens() != 70000 {
		t.Fatalf("threshold tokens = %d, want 70000", engine.ThresholdTokens())
	}

	engine.OnSessionReset()
	if engine.CompressionCount() != 0 {
		t.Fatal("expected 0 compressions after reset")
	}

	engine.OnSessionEnd() // should not panic
}

func TestCompressorEngineGetToolSchemas(t *testing.T) {
	engine := NewCompressorEngine(ContextEngineConfig{})
	schemas := engine.GetToolSchemas()
	if schemas != nil {
		t.Fatalf("expected nil, got %v", schemas)
	}
}

func TestCompressorEngineUpdateFromResponse(t *testing.T) {
	engine := NewCompressorEngine(ContextEngineConfig{})
	engine.UpdateFromResponse(TokenUsage{}) // should not panic
}

func TestCompressorEngineShouldCompact(t *testing.T) {
	engine := NewCompressorEngine(ContextEngineConfig{
		ContextWindow:        1000,
		CompressionThreshold: 0.5,
	})

	// Below threshold
	if engine.ShouldCompact(nil, nil, 1000) {
		t.Fatal("should not compact with no messages")
	}

	// Above threshold (system + large message)
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: string(make([]byte, 2000))},
	}
	if !engine.ShouldCompact(msgs, nil, 1000) {
		t.Fatal("should compact with large messages")
	}
}

func TestCompressorEngineCheckFeasibility(t *testing.T) {
	// No compression model → no warning
	engine := NewCompressorEngine(ContextEngineConfig{})
	if warn := engine.CheckFeasibility(100000); warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}

	// With compression model and adequate context
	engine2 := NewCompressorEngine(ContextEngineConfig{
		CompressionModel: "gpt-4o-mini",
		CompressionProvider: &stubProvider{},
		ContextWindow: 100000,
	})
	if warn := engine2.CheckFeasibility(100000); warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}

	// Compression model too small
	engine3 := NewCompressorEngine(ContextEngineConfig{
		CompressionModel: "gpt-4o-mini",
		CompressionProvider: &stubProvider{},
		ContextWindow: 10000,
	})
	if warn := engine3.CheckFeasibility(100000); warn == "" {
		t.Fatal("expected warning for small compression context")
	}
}

func TestCompressorEngineSummaryStats(t *testing.T) {
	engine := NewCompressorEngine(ContextEngineConfig{}).(*CompressorEngine)
	stats := engine.SummaryStats()
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if stats["previous_summary"] != false {
		t.Fatal("expected no previous summary")
	}
	if stats["ineffective_compactions"] != 0 {
		t.Fatal("expected 0 ineffective compactions")
	}
}

func TestTruncateEngineLifecycle(t *testing.T) {
	engine := NewTruncateEngine(ContextEngineConfig{
		ContextWindow:        100000,
		CompressionThreshold: 0.7,
		KeepRecentTokens:     2000,
		ProtectFirstN:        3,
	})

	if engine.Name() != "truncate" {
		t.Fatalf("name = %q", engine.Name())
	}

	engine.OnSessionStart(context.Background(), "test-model", 100000)
	if engine.ContextLength() != 100000 {
		t.Fatalf("context length = %d", engine.ContextLength())
	}

	engine.OnSessionReset()
	engine.OnSessionEnd() // should not panic
	engine.UpdateFromResponse(TokenUsage{})

	if engine.GetToolSchemas() != nil {
		t.Fatal("expected nil tool schemas")
	}
	if engine.LastSavingsPct() != 0 {
		t.Fatal("expected 0 savings pct")
	}
	if engine.CheckFeasibility(100000) != "" {
		t.Fatal("expected empty feasibility check")
	}
}

func TestTruncateEngineShouldCompact(t *testing.T) {
	engine := NewTruncateEngine(ContextEngineConfig{
		ContextWindow: 1000,
	})

	if engine.ShouldCompact(nil, nil, 1000) {
		t.Fatal("should not compact with no messages")
	}

	// Large messages: 4000 chars → ~1000 tokens > 750 threshold
	msgs := []Message{
		{Role: RoleUser, Content: string(make([]byte, 4000))},
	}
	if !engine.ShouldCompact(msgs, nil, 1000) {
		t.Fatal("should compact with large messages")
	}
}

func TestTruncateEngineCompressTooFewMessages(t *testing.T) {
	engine := NewTruncateEngine(ContextEngineConfig{})
	msgs := []Message{
		{Role: RoleUser, Content: "a"},
		{Role: RoleAssistant, Content: "b"},
	}
	result, cut, err := engine.Compress(context.Background(), msgs, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cut != 0 {
		t.Fatalf("expected cut=0, got %d", cut)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 messages unchanged, got %d", len(result))
	}
}

func TestTruncateEngineCompressBasic(t *testing.T) {
	engine := NewTruncateEngine(ContextEngineConfig{
		KeepRecentTokens: 100,
		ProtectFirstN:    2,
	})

	msgs := make([]Message, 10)
	for i := range msgs {
		content := string(make([]byte, 100)) // ~25 tokens each
		msgs[i] = Message{Role: RoleUser, Content: content}
	}

	result, cut, err := engine.Compress(context.Background(), msgs, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cut <= 0 {
		t.Fatal("expected positive cut")
	}
	if len(result) >= len(msgs) {
		t.Fatal("expected truncated result")
	}
}

func TestCompressorEngineLastSavingsPct(t *testing.T) {
	engine := NewCompressorEngine(ContextEngineConfig{})
	// newCompactionState initializes lastSavingsPct to 100.0
	if engine.LastSavingsPct() != 100.0 {
		t.Fatalf("expected 100, got %f", engine.LastSavingsPct())
	}
}
