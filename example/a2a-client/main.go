package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/covoyage/covonaut/a2a"
)

func main() {
	var (
		serverURL   = flag.String("server", "http://localhost:8080", "A2A server URL")
		message     = flag.String("message", "Hello!", "Message to send")
		stream      = flag.Bool("stream", false, "Use streaming mode")
		apiKey      = flag.String("api-key", "", "API key for authentication")
		bearerToken = flag.String("bearer", "", "Bearer token for authentication")
	)
	flag.Parse()

	ctx := context.Background()

	var opts []a2a.ClientOption
	if *apiKey != "" {
		opts = append(opts, a2a.WithAPIKey(*apiKey))
	}
	if *bearerToken != "" {
		opts = append(opts, a2a.WithBearerToken(*bearerToken))
	}

	client := a2a.NewClient(*serverURL, opts...)

	// Discover agent
	fmt.Println("=== Discovering Agent ===")
	card, err := client.GetAgentCard(ctx)
	if err != nil {
		log.Fatalf("Failed to get agent card: %v", err)
	}

	printAgentCard(card)

	// Send task
	fmt.Println("\n=== Sending Task ===")
	taskID := fmt.Sprintf("task-%d", time.Now().Unix())

	if *stream {
		if err := sendStreaming(ctx, client, taskID, *message); err != nil {
			log.Fatalf("Streaming failed: %v", err)
		}
	} else {
		if err := sendSync(ctx, client, taskID, *message); err != nil {
			log.Fatalf("Send failed: %v", err)
		}
	}
}

func printAgentCard(card *a2a.AgentCard) {
	fmt.Printf("Name: %s\n", card.Name)
	fmt.Printf("Description: %s\n", card.Description)
	fmt.Printf("Version: %s\n", card.Version)
	fmt.Printf("URL: %s\n", card.URL)
	fmt.Printf("Streaming: %v\n", card.Capabilities.Streaming)
	fmt.Printf("Push Notifications: %v\n", card.Capabilities.PushNotifications)
	fmt.Printf("Skills (%d):\n", len(card.Skills))
	for _, skill := range card.Skills {
		fmt.Printf("  - %s (%s): %s\n", skill.ID, skill.Name, skill.Description)
	}
}

func sendSync(ctx context.Context, client *a2a.Client, taskID, message string) error {
	task, err := client.SendTask(ctx, a2a.SendTaskRequest{
		ID: taskID,
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				a2a.NewTextPart(message),
			},
		},
	})
	if err != nil {
		return err
	}

	fmt.Printf("Task ID: %s\n", task.ID)
	fmt.Printf("State: %s\n", task.State)

	if len(task.Artifacts) > 0 {
		fmt.Println("Artifacts:")
		for _, art := range task.Artifacts {
			for _, part := range art.Parts {
				if part.Type == a2a.PartTypeText {
					fmt.Printf("  %s: %s\n", art.Name, part.Text)
				}
			}
		}
	}

	if len(task.Messages) > 0 {
		fmt.Println("Messages:")
		for _, msg := range task.Messages {
			for _, part := range msg.Parts {
				if part.Type == a2a.PartTypeText {
					fmt.Printf("  [%s]: %s\n", msg.Role, part.Text)
				}
			}
		}
	}

	return nil
}

func sendStreaming(ctx context.Context, client *a2a.Client, taskID, message string) error {
	stream, err := client.SendTaskSubscribe(ctx, a2a.SendTaskRequest{
		ID: taskID,
		Message: a2a.Message{
			Role: "user",
			Parts: []a2a.Part{
				a2a.NewTextPart(message),
			},
		},
	})
	if err != nil {
		return err
	}
	defer stream.Close()

	fmt.Printf("Task ID: %s\n", taskID)
	fmt.Println("Streaming updates:")

	for {
		ev, ok := stream.Recv()
		if !ok {
			break
		}
		if ev.Error != nil {
			fmt.Printf("Error: %s\n", ev.Error.Message)
			return fmt.Errorf("stream error: %s", ev.Error.Message)
		}
		if ev.Result != nil {
			fmt.Printf("  State: %s", ev.Result.State)
			if len(ev.Result.Artifacts) > 0 {
				for _, art := range ev.Result.Artifacts {
					for _, part := range art.Parts {
						if part.Type == a2a.PartTypeText {
							fmt.Printf(" | %s", part.Text)
						}
					}
				}
			}
			fmt.Println()
			if ev.Final {
				fmt.Println("Stream complete!")
				break
			}
		}
	}

	return stream.Err()
}
