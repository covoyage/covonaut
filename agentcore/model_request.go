package agentcore

import "context"

// PreviewModelRequest shares the loop's request construction, without running
// lifecycle hooks, dispatching a provider or changing conversation state.
func (a *Agent) PreviewModelRequest(ctx context.Context, messages []Message) (*ProviderRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config := a.Config()
	msgs := make([]Message, len(messages))
	for i, message := range messages {
		msgs[i] = message.Clone()
	}
	if transform := a.transformContext(); transform != nil {
		msgs = transform(ctx, msgs)
	}
	converter := config.ConvertToLLM
	if converter == nil {
		converter = DefaultConvertToLLM
	}
	msgs = NormalizeToolCallHistory(converter(msgs))
	all, err := a.ToolDefinitionsForContext(ctx)
	if err != nil {
		return nil, err
	}
	tools, err := selectToolDefinitions(ctx, config.ToolSelection, msgs, all)
	if err != nil {
		return nil, err
	}
	if config.ArtifactOffload != nil && config.ArtifactOffload.Store != nil {
		tools = ensureToolDefinition(tools, all, "artifact_read")
	}
	return &ProviderRequest{Model: config.Model, Messages: msgs, Tools: tools, Temperature: config.Temperature,
		FrequencyPenalty: config.FrequencyPenalty, PresencePenalty: config.PresencePenalty, MaxTokens: config.MaxTokens,
		ResponseFormat: config.ResponseFormat, Thinking: config.Thinking, FastMode: config.FastMode}, nil
}
