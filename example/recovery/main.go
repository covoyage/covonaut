package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/covoyage/covonaut/agentcore"
)

type reviewProvider struct{}

func (reviewProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	for _, message := range req.Messages {
		if message.Role == agentcore.RoleTool {
			return &agentcore.ProviderResponse{Content: "Review recorded. No external action was performed."}, nil
		}
	}
	return &agentcore.ProviderResponse{ToolCalls: []agentcore.ToolCall{{
		ID: "review-1", Name: "request_review", Arguments: `{}`,
	}}}, nil
}

func (reviewProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return nil, errors.New("this offline example does not support streaming")
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	saver := agentcore.NewMemoryCheckpointSaver()
	saver.MaxCheckpointsPerThread = 10
	cfg := agentcore.NewConfig(
		agentcore.WithModel("offline-review"),
		agentcore.WithProvider(reviewProvider{}),
		agentcore.WithMaxTurns(5),
		agentcore.WithCheckpoint(&agentcore.CheckpointSettings{
			Saver: saver, ThreadID: "review-thread",
		}),
		agentcore.WithTools(&agentcore.Tool{
			Name: "request_review", Description: "Pause for a host review",
			Parameters: map[string]any{"type": "object"},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				return nil, agentcore.NewInterruptErrorWithData("host review required", map[string]any{
					"request_id": "preview-1",
				})
			},
		}),
	)
	original, err := agentcore.NewWithError(cfg)
	if err != nil {
		return err
	}
	defer original.Close()
	if _, err := original.Run(ctx, "Prepare a request for review."); err != nil {
		return err
	}
	if original.Interrupted() == nil {
		return errors.New("expected a review interrupt")
	}
	fmt.Println("Paused:", original.Interrupted().Reason)

	restored, err := agentcore.NewWithError(cfg)
	if err != nil {
		return err
	}
	defer restored.Close()
	if err := restored.RestoreLatestCheckpoint(ctx, "review-thread"); err != nil {
		return err
	}
	if restored.Interrupted() == nil {
		return errors.New("checkpoint did not restore the interrupt")
	}
	fmt.Println("Restored:", restored.Interrupted().Data["request_id"])
	restored.Steer(agentcore.Message{Role: agentcore.RoleUser, Content: "I reviewed the preview. Continue without external actions."})
	output, err := restored.Resume(ctx)
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}