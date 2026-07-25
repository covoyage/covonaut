package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// WithRESTTransport configures the V1Client to use HTTP+JSON (REST) transport
// instead of JSON-RPC. The baseURL should point to the REST endpoint
// (e.g. http://host:port/rest).
func WithRESTTransport() ClientOption {
	return func(c *Client) {
		c.rest = true
	}
}

// restSendMessage sends a message via REST POST /message:send.
func (c *V1Client) restSendMessage(ctx context.Context, req V1SendMessageRequest) (*V1SendMessageResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.client.baseURL+"/message:send", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	c.client.setAuthHeaders(request)
	response, err := c.client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, parseRESTError(response)
	}
	var result V1SendMessageResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode REST response: %w", err)
	}
	return &result, nil
}

// restSendStreamingMessage sends a streaming message via REST POST /message:stream.
func (c *V1Client) restSendStreamingMessage(ctx context.Context, req V1SendMessageRequest) (*V1TaskStream, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.client.baseURL+"/message:stream", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	c.client.setAuthHeaders(request)
	response, err := c.client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		return nil, fmt.Errorf("REST stream HTTP %d: %s", response.StatusCode, body)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &V1TaskStream{body: response.Body, scanner: scanner, rest: true}, nil
}

// restGetTask gets a task via REST GET /tasks/{id}.
func (c *V1Client) restGetTask(ctx context.Context, req V1GetTaskRequest) (*V1Task, error) {
	url := c.client.baseURL + "/tasks/" + req.ID
	if req.HistoryLength != nil {
		url += "?historyLength=" + strconv.Itoa(*req.HistoryLength)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.client.setAuthHeaders(request)
	response, err := c.client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, parseRESTError(response)
	}
	var result V1Task
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode REST response: %w", err)
	}
	return &result, nil
}

// restCancelTask cancels a task via REST POST /tasks/{id}:cancel.
func (c *V1Client) restCancelTask(ctx context.Context, id string) (*V1Task, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.client.baseURL+"/tasks/"+id+":cancel", nil)
	if err != nil {
		return nil, err
	}
	c.client.setAuthHeaders(request)
	response, err := c.client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, parseRESTError(response)
	}
	var result V1Task
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode REST response: %w", err)
	}
	return &result, nil
}

// restSubscribeToTask subscribes to a task via REST POST /tasks/{id}:subscribe.
func (c *V1Client) restSubscribeToTask(ctx context.Context, id string) (*V1TaskStream, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.client.baseURL+"/tasks/"+id+":subscribe", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	c.client.setAuthHeaders(request)
	response, err := c.client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		return nil, fmt.Errorf("REST subscribe HTTP %d: %s", response.StatusCode, body)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &V1TaskStream{body: response.Body, scanner: scanner, rest: true}, nil
}

// restCreatePushConfig creates a push notification config via REST.
func (c *V1Client) restCreatePushConfig(ctx context.Context, config V1TaskPushNotificationConfig) (*V1TaskPushNotificationConfig, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	url := c.client.baseURL + "/tasks/" + config.TaskID + "/pushNotificationConfigs"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	c.client.setAuthHeaders(request)
	response, err := c.client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, parseRESTError(response)
	}
	var result V1TaskPushNotificationConfig
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode REST response: %w", err)
	}
	return &result, nil
}

// parseRESTError reads an error response body in REST format.
func parseRESTError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return fmt.Errorf("REST HTTP %d: %s", response.StatusCode, body)
}
