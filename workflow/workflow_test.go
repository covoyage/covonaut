package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFuncStep(t *testing.T) {
	step := NewFuncStep(func(ctx context.Context, input string) (string, error) {
		return strings.ToUpper(input), nil
	})

	result, err := step.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("FuncStep.Run: %v", err)
	}
	if result != "HELLO" {
		t.Errorf("FuncStep: got %q, want %q", result, "HELLO")
	}
}

func TestFuncStepError(t *testing.T) {
	expectedErr := errors.New("step failed")
	step := NewFuncStep(func(ctx context.Context, input string) (string, error) {
		return "", expectedErr
	})

	_, err := step.Run(context.Background(), "input")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPipeline(t *testing.T) {
	p := &Pipeline{
		Steps: []Step{
			NewFuncStep(func(ctx context.Context, input string) (string, error) {
				return strings.ToUpper(input), nil
			}),
			NewFuncStep(func(ctx context.Context, input string) (string, error) {
				return input + "!", nil
			}),
		},
	}

	result, err := p.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}
	if result != "HELLO!" {
		t.Errorf("Pipeline: got %q, want %q", result, "HELLO!")
	}
}

func TestPipelineError(t *testing.T) {
	p := &Pipeline{
		Steps: []Step{
			NewFuncStep(func(ctx context.Context, input string) (string, error) {
				return "", errors.New("fail")
			}),
		},
	}

	_, err := p.Run(context.Background(), "input")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPipelineEmpty(t *testing.T) {
	p := &Pipeline{Steps: nil}
	result, err := p.Run(context.Background(), "input")
	if err != nil {
		t.Fatalf("empty pipeline: %v", err)
	}
	if result != "input" {
		t.Errorf("empty pipeline: got %q, want %q", result, "input")
	}
}

func TestParallel(t *testing.T) {
	p := &Parallel{
		Steps: []Step{
			NewFuncStep(func(ctx context.Context, input string) (string, error) {
				return "A:" + input, nil
			}),
			NewFuncStep(func(ctx context.Context, input string) (string, error) {
				return "B:" + input, nil
			}),
		},
		Merge: func(results []string) string {
			return strings.Join(results, ",")
		},
	}

	result, err := p.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Parallel.Run: %v", err)
	}

	// Order may vary due to concurrency, check both parts
	if !strings.Contains(result, "A:test") || !strings.Contains(result, "B:test") {
		t.Errorf("Parallel: got %q, expected both A:test and B:test", result)
	}
}

func TestParallelError(t *testing.T) {
	p := &Parallel{
		Steps: []Step{
			NewFuncStep(func(ctx context.Context, input string) (string, error) {
				return "ok", nil
			}),
			NewFuncStep(func(ctx context.Context, input string) (string, error) {
				return "", errors.New("fail")
			}),
		},
		Merge: func(results []string) string {
			return strings.Join(results, ",")
		},
	}

	_, err := p.Run(context.Background(), "test")
	if err == nil {
		t.Error("expected error from parallel step")
	}
}

func TestParallelEmpty(t *testing.T) {
	p := &Parallel{
		Steps: nil,
		Merge: func(results []string) string {
			return strings.Join(results, ",")
		},
	}
	result, err := p.Run(context.Background(), "input")
	if err != nil {
		t.Fatalf("empty parallel: %v", err)
	}
	if result != "" {
		t.Errorf("empty parallel: got %q, want empty", result)
	}
}
