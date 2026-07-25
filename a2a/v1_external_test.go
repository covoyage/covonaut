package a2a_test

import (
	"context"
	"testing"

	"github.com/covoyage/covonaut/a2a"
)

func TestV1PublicAPICompilesExternally(t *testing.T) {
	request := a2a.V1SendMessageRequest{
		Message: a2a.V1Message{
			MessageID: "message",
			Role:      "ROLE_USER",
			Parts:     []a2a.V1Part{a2a.NewV1TextPart("hello")},
		},
		Configuration: &a2a.V1SendConfiguration{ReturnImmediately: true},
	}
	client := a2a.NewClient("http://127.0.0.1").V1()
	if client == nil || request.Message.MessageID == "" {
		t.Fatal("public v1 API is unavailable")
	}

	var _ func(context.Context, a2a.V1SendMessageRequest) (*a2a.V1SendMessageResponse, error) = client.SendMessage
	var _ func(context.Context, a2a.V1SendMessageRequest) (*a2a.V1TaskStream, error) = client.SendStreamingMessage
	var _ func(context.Context, a2a.V1GetTaskRequest) (*a2a.V1Task, error) = client.GetTask
	var _ func(context.Context, a2a.V1ListTasksRequest) (*a2a.V1ListTasksResponse, error) = client.ListTasks
}
