package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/graph"
	"github.com/covoyage/covonaut/workflow"
)

type document struct {
	Text   string
	Length int
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	typed := graph.NewTypedGraph[document]()
	if err := typed.AddNode("normalize", agentcore.NewInvokeRunnable(
		func(ctx context.Context, input document) (document, error) {
			input.Text = strings.ToLower(strings.TrimSpace(input.Text))
			return input, nil
		},
	)); err != nil {
		return err
	}
	if err := typed.AddNode("measure", agentcore.NewInvokeRunnable(
		func(ctx context.Context, input document) (document, error) {
			input.Length = len(input.Text)
			return input, nil
		},
	)); err != nil {
		return err
	}
	if err := typed.AddEdge("normalize", "measure"); err != nil {
		return err
	}
	compiled, err := typed.Compile(graph.TypedCompileOptions[document]{EntryNode: "normalize"})
	if err != nil {
		return err
	}
	result, err := compiled.Run(ctx, document{Text: "  Covonaut Workflows  "})
	if err != nil {
		return err
	}
	fmt.Printf("Typed: %s (%d bytes)\n", result.Text, result.Length)

	pipeline := &workflow.Pipeline{Steps: []workflow.Step{
		workflow.NewFuncStep(func(ctx context.Context, input string) (string, error) {
			return strings.ToUpper(input), nil
		}),
		&workflow.Router{
			Route: func(ctx context.Context, input string) string { return "publish" },
			Steps: map[string]workflow.Step{
				"publish": workflow.NewFuncStep(func(ctx context.Context, input string) (string, error) {
					return "Published: " + input, nil
				}),
			},
		},
	}}
	output, err := pipeline.Run(ctx, result.Text)
	if err != nil {
		return err
	}
	fmt.Println(output)

	streaming := agentcore.NewStreamRunnable(func(ctx context.Context, input string) (*agentcore.StreamReader[string], error) {
		stream := agentcore.NewStreamReader[string](2)
		go func() {
			defer stream.Close()
			for _, chunk := range []string{input, "!"} {
				if !stream.Send(chunk) {
					return
				}
			}
		}()
		return stream, nil
	})
	last, err := streaming.Invoke(ctx, "hello")
	if err != nil {
		return err
	}
	stream, err := streaming.Stream(ctx, "hello")
	if err != nil {
		return err
	}
	defer stream.Close()
	joined, err := agentcore.CollectString(stream)
	if err != nil {
		return err
	}
	fmt.Printf("Stream: last=%q joined=%q\n", last, joined)
	return nil
}