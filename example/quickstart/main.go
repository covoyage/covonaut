package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/chatcompat"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	apiKey, model := os.Getenv("OPENAI_API_KEY"), os.Getenv("AGENT_MODEL")
	if apiKey == "" || model == "" {
		return errors.New("set OPENAI_API_KEY and AGENT_MODEL before running")
	}

	agent, err := agentcore.NewWithError(agentcore.NewConfig(
		agentcore.WithName("assistant"),
		agentcore.WithModel(model),
		agentcore.WithProvider(chatcompat.New(chatcompat.Config{
			APIKey:  apiKey,
			BaseURL: os.Getenv("OPENAI_BASE_URL"),
		})),
		agentcore.WithSystemPrompt("Use the greet tool when asked to greet someone."),
		agentcore.WithMaxTurns(5),
		agentcore.WithValidateArguments(true),
		agentcore.WithTools(&agentcore.Tool{
			Name:        "greet",
			Description: "Greet a person by name",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required": []string{"name"},
			},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				var input struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(args, &input); err != nil {
					return nil, err
				}
				return "Hello, " + input.Name + "!", nil
			},
		}),
	))
	if err != nil {
		return err
	}
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	output, err := agent.Run(ctx, "Greet Ada by name.")
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}