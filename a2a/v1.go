package a2a

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const a2aProtocolVersion = "1.0"

type v1AgentInterface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
}

type V1AgentProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url"`
}

type V1AgentSkill struct {
	ID                   string                  `json:"id"`
	Name                 string                  `json:"name"`
	Description          string                  `json:"description"`
	Tags                 []string                `json:"tags"`
	Examples             []string                `json:"examples,omitempty"`
	InputModes           []string                `json:"inputModes,omitempty"`
	OutputModes          []string                `json:"outputModes,omitempty"`
	SecurityRequirements []V1SecurityRequirement `json:"securityRequirements,omitempty"`
}

type v1AgentCapabilities struct {
	Streaming         bool               `json:"streaming,omitempty"`
	PushNotifications bool               `json:"pushNotifications,omitempty"`
	ExtendedAgentCard bool               `json:"extendedAgentCard,omitempty"`
	Extensions        []V1AgentExtension `json:"extensions,omitempty"`
}

type V1AgentExtension struct {
	URI         string         `json:"uri"`
	Description string         `json:"description,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
}

type V1HTTPAuthSecurityScheme struct {
	Description  string `json:"description,omitempty"`
	Scheme       string `json:"scheme"`
	BearerFormat string `json:"bearerFormat,omitempty"`
}

type V1APIKeySecurityScheme struct {
	Description string `json:"description,omitempty"`
	Location    string `json:"location"`
	Name        string `json:"name"`
}

type V1SecurityScheme struct {
	HTTPAuth *V1HTTPAuthSecurityScheme `json:"httpAuthSecurityScheme,omitempty"`
	APIKey   *V1APIKeySecurityScheme   `json:"apiKeySecurityScheme,omitempty"`
}

type V1StringList struct {
	List []string `json:"list,omitempty"`
}

type V1SecurityRequirement struct {
	Schemes map[string]V1StringList `json:"schemes"`
}

type v1AgentCard struct {
	Name                 string                      `json:"name"`
	Description          string                      `json:"description"`
	SupportedInterfaces  []v1AgentInterface          `json:"supportedInterfaces"`
	Provider             *V1AgentProvider            `json:"provider,omitempty"`
	Version              string                      `json:"version"`
	DocumentationURL     string                      `json:"documentationUrl,omitempty"`
	Capabilities         v1AgentCapabilities         `json:"capabilities"`
	SecuritySchemes      map[string]V1SecurityScheme `json:"securitySchemes,omitempty"`
	SecurityRequirements []V1SecurityRequirement     `json:"securityRequirements,omitempty"`
	DefaultInputModes    []string                    `json:"defaultInputModes"`
	DefaultOutputModes   []string                    `json:"defaultOutputModes"`
	Skills               []V1AgentSkill              `json:"skills"`
}

type v1Part struct {
	Text      *string `json:"text,omitempty"`
	Raw       *string `json:"raw,omitempty"`
	URL       *string `json:"url,omitempty"`
	Data      any     `json:"data,omitempty"`
	Filename  string  `json:"filename,omitempty"`
	MediaType string  `json:"mediaType,omitempty"`
	dataSet   bool
}

func (p *v1Part) UnmarshalJSON(data []byte) error {
	type partAlias v1Part
	var decoded partAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*p = v1Part(decoded)
	_, p.dataSet = fields["data"]
	return nil
}

func (p v1Part) MarshalJSON() ([]byte, error) {
	object := make(map[string]any)
	if p.Text != nil {
		object["text"] = *p.Text
	}
	if p.Raw != nil {
		object["raw"] = *p.Raw
	}
	if p.URL != nil {
		object["url"] = *p.URL
	}
	if p.dataSet || p.Data != nil {
		object["data"] = p.Data
	}
	if p.Filename != "" {
		object["filename"] = p.Filename
	}
	if p.MediaType != "" {
		object["mediaType"] = p.MediaType
	}
	return json.Marshal(object)
}

type v1Message struct {
	MessageID        string         `json:"messageId"`
	ContextID        string         `json:"contextId,omitempty"`
	TaskID           string         `json:"taskId,omitempty"`
	Role             string         `json:"role"`
	Parts            []v1Part       `json:"parts"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	Extensions       []string       `json:"extensions,omitempty"`
	ReferenceTaskIDs []string       `json:"referenceTaskIds,omitempty"`
}

type v1TaskStatus struct {
	State     string     `json:"state"`
	Message   *v1Message `json:"message,omitempty"`
	Timestamp string     `json:"timestamp,omitempty"`
}

type v1Artifact struct {
	ArtifactID string         `json:"artifactId"`
	Name       string         `json:"name,omitempty"`
	Parts      []v1Part       `json:"parts"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type v1Task struct {
	ID        string         `json:"id"`
	ContextID string         `json:"contextId,omitempty"`
	Status    v1TaskStatus   `json:"status"`
	Artifacts []v1Artifact   `json:"artifacts,omitempty"`
	History   []v1Message    `json:"history,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type v1SendMessageRequest struct {
	Message       v1Message            `json:"message"`
	Configuration *v1SendConfiguration `json:"configuration,omitempty"`
	Metadata      map[string]any       `json:"metadata,omitempty"`
}

type v1SendConfiguration struct {
	AcceptedOutputModes        []string                      `json:"acceptedOutputModes,omitempty"`
	HistoryLength              *int                          `json:"historyLength,omitempty"`
	ReturnImmediately          bool                          `json:"returnImmediately,omitempty"`
	TaskPushNotificationConfig *V1TaskPushNotificationConfig `json:"taskPushNotificationConfig,omitempty"`
}

type v1ListTasksRequest struct {
	ContextID            string `json:"contextId,omitempty"`
	Status               string `json:"status,omitempty"`
	PageSize             int    `json:"pageSize,omitempty"`
	PageToken            string `json:"pageToken,omitempty"`
	HistoryLength        *int   `json:"historyLength,omitempty"`
	IncludeArtifacts     bool   `json:"includeArtifacts,omitempty"`
	StatusTimestampAfter string `json:"statusTimestampAfter,omitempty"`
}

type v1GetTaskRequest struct {
	ID            string `json:"id"`
	HistoryLength *int   `json:"historyLength,omitempty"`
}

type v1TaskCursor struct {
	Timestamp string `json:"timestamp"`
	TaskID    string `json:"taskId"`
}

// Public A2A 1.0 wire types. Legacy package types remain available for 0.3 APIs.
type V1AgentInterface = v1AgentInterface
type V1AgentCapabilities = v1AgentCapabilities
type V1AgentCard = v1AgentCard
type V1Part = v1Part
type V1Message = v1Message
type V1TaskStatus = v1TaskStatus
type V1Artifact = v1Artifact
type V1Task = v1Task
type V1SendMessageRequest = v1SendMessageRequest
type V1SendConfiguration = v1SendConfiguration
type V1ListTasksRequest = v1ListTasksRequest
type V1GetTaskRequest = v1GetTaskRequest

type V1SendMessageResponse struct {
	Task    *V1Task    `json:"task,omitempty"`
	Message *V1Message `json:"message,omitempty"`
}

type V1ListTasksResponse struct {
	Tasks         []V1Task `json:"tasks"`
	NextPageToken string   `json:"nextPageToken"`
	PageSize      int      `json:"pageSize"`
	TotalSize     int      `json:"totalSize"`
}

type V1TaskStatusUpdateEvent struct {
	TaskID    string         `json:"taskId"`
	ContextID string         `json:"contextId"`
	Status    V1TaskStatus   `json:"status"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type V1TaskArtifactUpdateEvent struct {
	TaskID    string         `json:"taskId"`
	ContextID string         `json:"contextId"`
	Artifact  V1Artifact     `json:"artifact"`
	Append    *bool          `json:"append,omitempty"`
	LastChunk *bool          `json:"lastChunk,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type V1StreamResponse struct {
	Task           *V1Task                    `json:"task,omitempty"`
	Message        *V1Message                 `json:"message,omitempty"`
	StatusUpdate   *V1TaskStatusUpdateEvent   `json:"statusUpdate,omitempty"`
	ArtifactUpdate *V1TaskArtifactUpdateEvent `json:"artifactUpdate,omitempty"`
}

type V1AuthenticationInfo struct {
	Scheme      string `json:"scheme"`
	Credentials string `json:"credentials,omitempty"`
}

type V1TaskPushNotificationConfig struct {
	ID             string                `json:"id,omitempty"`
	TaskID         string                `json:"taskId"`
	URL            string                `json:"url"`
	Token          string                `json:"token,omitempty"`
	Authentication *V1AuthenticationInfo `json:"authentication,omitempty"`
}

type V1PushConfigRef struct {
	TaskID string `json:"taskId"`
	ID     string `json:"id"`
}

type V1ListPushConfigsRequest struct {
	TaskID    string `json:"taskId"`
	PageSize  int    `json:"pageSize,omitempty"`
	PageToken string `json:"pageToken,omitempty"`
}

type V1ListPushConfigsResponse struct {
	Configs       []V1TaskPushNotificationConfig `json:"configs"`
	NextPageToken string                         `json:"nextPageToken,omitempty"`
}

func NewV1TextPart(text string) V1Part {
	return V1Part{Text: &text, MediaType: "text/plain"}
}

func NewV1DataPart(value any, mediaType string) V1Part {
	return V1Part{Data: value, MediaType: mediaType, dataSet: true}
}

func NewV1RawPart(raw, filename, mediaType string) V1Part {
	return V1Part{Raw: &raw, Filename: filename, MediaType: mediaType}
}

func NewV1URLPart(rawURL, filename, mediaType string) V1Part {
	return V1Part{URL: &rawURL, Filename: filename, MediaType: mediaType}
}

func (s *Server) handleAgentCardV1(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	card := s.publicV1AgentCard()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(card)
}

func (s *Server) publicV1AgentCard() V1AgentCard {
	card := s.handler.Card()
	inputModes := append([]string(nil), card.DefaultInputModes...)
	if len(inputModes) == 0 {
		inputModes = []string{"text/plain"}
	}
	outputModes := append([]string(nil), card.DefaultOutputModes...)
	if len(outputModes) == 0 {
		outputModes = []string{"text/plain"}
	}
	version := card.Version
	if version == "" {
		version = "1.0.0"
	}
	securitySchemes, securityRequirements := s.v1SecurityMetadata()
	return V1AgentCard{
		Name:        card.Name,
		Description: card.Description,
		SupportedInterfaces: []v1AgentInterface{
			{
				URL:             card.URL,
				ProtocolBinding:  "JSONRPC",
				ProtocolVersion:  a2aProtocolVersion,
			},
			{
				URL:             card.URL + restPrefix,
				ProtocolBinding:  "HTTP_JSON",
				ProtocolVersion:  a2aProtocolVersion,
			},
		},
		Provider:         providerToV1(card.Provider),
		Version:          version,
		DocumentationURL: card.DocumentationURL,
		Capabilities: v1AgentCapabilities{
			Streaming:         card.Capabilities.Streaming,
			PushNotifications: card.Capabilities.PushNotifications,
			ExtendedAgentCard: card.Capabilities.ExtendedAgentCard || s.extendedCard != nil,
			Extensions:        append([]V1AgentExtension(nil), s.v1Extensions...),
		},
		SecuritySchemes:      securitySchemes,
		SecurityRequirements: securityRequirements,
		DefaultInputModes:    inputModes,
		DefaultOutputModes:   outputModes,
		Skills:               skillsToV1(card.Skills),
	}
}

func providerToV1(provider *AgentProvider) *V1AgentProvider {
	if provider == nil {
		return nil
	}
	return &V1AgentProvider{Organization: provider.Organization, URL: provider.URL}
}

func skillsToV1(skills []AgentSkill) []V1AgentSkill {
	result := make([]V1AgentSkill, 0, len(skills))
	for _, skill := range skills {
		tags := append([]string(nil), skill.Tags...)
		if tags == nil {
			tags = []string{}
		}
		result = append(result, V1AgentSkill{
			ID:          skill.ID,
			Name:        skill.Name,
			Description: skill.Description,
			Tags:        tags,
			Examples:    append([]string(nil), skill.Examples...),
			InputModes:  append([]string(nil), skill.InputModes...),
			OutputModes: append([]string(nil), skill.OutputModes...),
		})
	}
	return result
}

func (s *Server) v1SecurityMetadata() (map[string]V1SecurityScheme, []V1SecurityRequirement) {
	schemes := make(map[string]V1SecurityScheme)
	var requirements []V1SecurityRequirement
	if s.auth.APIKey != "" {
		schemes["apiKey"] = V1SecurityScheme{APIKey: &V1APIKeySecurityScheme{Location: "header", Name: "X-API-Key"}}
		requirements = append(requirements, V1SecurityRequirement{Schemes: map[string]V1StringList{"apiKey": {}}})
	}
	if s.auth.BearerToken != "" {
		schemes["bearerAuth"] = V1SecurityScheme{HTTPAuth: &V1HTTPAuthSecurityScheme{Scheme: "Bearer"}}
		requirements = append(requirements, V1SecurityRequirement{Schemes: map[string]V1StringList{"bearerAuth": {}}})
	}
	if len(schemes) == 0 {
		return nil, nil
	}
	return schemes, requirements
}

func (s *Server) handleV1CreatePushConfig(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var config V1TaskPushNotificationConfig
	if err := json.Unmarshal(req.Params, &config); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	if config.TaskID == "" || config.URL == "" {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, "taskId and url are required")
		return
	}
	if _, err := s.handler.GetTask(ctx, GetTaskRequest{ID: config.TaskID}); err != nil {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotFound, err.Error())
		return
	}
	if !s.allowPrivateV1Push {
		if err := validateWebhookURL(config.URL); err != nil {
			writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, fmt.Sprintf("invalid webhook URL: %v", err))
			return
		}
	}
	if config.ID == "" {
		config.ID = fmt.Sprintf("push-%d", time.Now().UnixNano())
	}
	s.storeV1PushConfig(config)
	writeJSONRPCResult(w, req.ID, config)
}

func (s *Server) storeV1PushConfig(config V1TaskPushNotificationConfig) {
	s.v1PushMu.Lock()
	defer s.v1PushMu.Unlock()
	configs := s.v1PushConfigs[config.TaskID]
	if configs == nil {
		configs = make(map[string]V1TaskPushNotificationConfig)
		s.v1PushConfigs[config.TaskID] = configs
	}
	configs[config.ID] = config
}

func (s *Server) handleV1GetPushConfig(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var ref V1PushConfigRef
	if err := json.Unmarshal(req.Params, &ref); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	if _, err := s.handler.GetTask(ctx, GetTaskRequest{ID: ref.TaskID}); err != nil {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotFound, err.Error())
		return
	}
	s.v1PushMu.RLock()
	config, ok := s.v1PushConfigs[ref.TaskID][ref.ID]
	s.v1PushMu.RUnlock()
	if !ok {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotFound, "push notification config not found")
		return
	}
	writeJSONRPCResult(w, req.ID, config)
}

func (s *Server) handleV1ListPushConfigs(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params V1ListPushConfigsRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	if _, err := s.handler.GetTask(ctx, GetTaskRequest{ID: params.TaskID}); err != nil {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotFound, err.Error())
		return
	}
	pageSize := params.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize < 1 || pageSize > 100 {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, "pageSize must be between 1 and 100")
		return
	}
	offset, err := decodeV1PageToken(params.PageToken)
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	s.v1PushMu.RLock()
	configs := make([]V1TaskPushNotificationConfig, 0, len(s.v1PushConfigs[params.TaskID]))
	for _, config := range s.v1PushConfigs[params.TaskID] {
		configs = append(configs, config)
	}
	s.v1PushMu.RUnlock()
	sort.Slice(configs, func(i, j int) bool { return configs[i].ID < configs[j].ID })
	if offset > len(configs) {
		offset = len(configs)
	}
	end := offset + pageSize
	if end > len(configs) {
		end = len(configs)
	}
	next := ""
	if end < len(configs) {
		next = encodeV1PageToken(end)
	}
	writeJSONRPCResult(w, req.ID, V1ListPushConfigsResponse{Configs: configs[offset:end], NextPageToken: next})
}

func (s *Server) handleV1DeletePushConfig(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var ref V1PushConfigRef
	if err := json.Unmarshal(req.Params, &ref); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	if _, err := s.handler.GetTask(ctx, GetTaskRequest{ID: ref.TaskID}); err != nil {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotFound, err.Error())
		return
	}
	s.v1PushMu.Lock()
	if configs := s.v1PushConfigs[ref.TaskID]; configs != nil {
		delete(configs, ref.ID)
		if len(configs) == 0 {
			delete(s.v1PushConfigs, ref.TaskID)
		}
	}
	s.v1PushMu.Unlock()
	writeJSONRPCResult(w, req.ID, map[string]any{})
}

func (s *Server) handleV1GetExtendedAgentCard(w http.ResponseWriter, req JSONRPCRequest) {
	if s.extendedCard == nil {
		writeJSONRPCError(w, req.ID, A2AErrorExtendedCardNotConfigured, "extended Agent Card not configured")
		return
	}
	card := s.effectiveExtendedAgentCard()
	writeJSONRPCResult(w, req.ID, card)
}

func (s *Server) effectiveExtendedAgentCard() V1AgentCard {
	public := s.publicV1AgentCard()
	extended := *s.extendedCard
	if extended.Name == "" {
		extended.Name = public.Name
	}
	if extended.Description == "" {
		extended.Description = public.Description
	}
	if len(extended.SupportedInterfaces) == 0 {
		extended.SupportedInterfaces = public.SupportedInterfaces
	}
	if extended.Provider == nil {
		extended.Provider = public.Provider
	}
	if extended.Version == "" {
		extended.Version = public.Version
	}
	if extended.DocumentationURL == "" {
		extended.DocumentationURL = public.DocumentationURL
	}
	if !extended.Capabilities.Streaming && !extended.Capabilities.PushNotifications && !extended.Capabilities.ExtendedAgentCard && len(extended.Capabilities.Extensions) == 0 {
		extended.Capabilities = public.Capabilities
	}
	extended.Capabilities.ExtendedAgentCard = true
	if extended.SecuritySchemes == nil {
		extended.SecuritySchemes = public.SecuritySchemes
	}
	if extended.SecurityRequirements == nil {
		extended.SecurityRequirements = public.SecurityRequirements
	}
	if len(extended.DefaultInputModes) == 0 {
		extended.DefaultInputModes = public.DefaultInputModes
	}
	if len(extended.DefaultOutputModes) == 0 {
		extended.DefaultOutputModes = public.DefaultOutputModes
	}
	if extended.Skills == nil {
		extended.Skills = public.Skills
	}
	return extended
}

func (s *Server) handleV1SendMessage(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params v1SendMessageRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	legacyReq, err := params.toLegacy()
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	if params.Configuration != nil && len(params.Configuration.AcceptedOutputModes) > 0 {
		if err := ValidateOutputModes(params.Configuration.AcceptedOutputModes, s.handler.Card().DefaultOutputModes); err != nil {
			writeJSONRPCError(w, req.ID, A2AErrorContentTypeNotSupported, err.Error())
			return
		}
	}
	if handler, ok := s.handler.(V1MessageHandler); ok {
		response, err := handler.SendMessage(ctx, params)
		if err != nil {
			writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
			return
		}
		if response == nil || response.Task == nil == (response.Message == nil) {
			writeJSONRPCError(w, req.ID, A2AErrorInvalidAgentResponse, "SendMessage response must contain exactly one of task or message")
			return
		}
		if response.Task != nil && params.Configuration != nil && params.Configuration.TaskPushNotificationConfig != nil {
			if _, err := s.registerEmbeddedPushConfig(*params.Configuration.TaskPushNotificationConfig, response.Task.ID); err != nil {
				writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
				return
			}
		}
		writeJSONRPCResult(w, req.ID, response)
		return
	}
	var embeddedPushID string
	if params.Configuration != nil && params.Configuration.TaskPushNotificationConfig != nil {
		config, err := s.registerEmbeddedPushConfig(*params.Configuration.TaskPushNotificationConfig, legacyReq.ID)
		if err != nil {
			writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
			return
		}
		embeddedPushID = config.ID
	}
	if params.Configuration != nil && params.Configuration.ReturnImmediately {
		initial := &Task{
			ID:        legacyReq.ID,
			SessionID: legacyReq.SessionID,
			State:     TaskStateSubmitted,
			Messages:  []Message{legacyReq.Message},
			Metadata:  legacyReq.Metadata,
			History:   []TaskStatus{{State: TaskStateSubmitted, Timestamp: time.Now()}},
		}
		s.recordTask(initial)
		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			s.runV1TaskDetached(legacyReq)
		}()
		writeJSONRPCResult(w, req.ID, V1SendMessageResponse{Task: v1TaskPointer(taskToV1Options(initial, params.Configuration.HistoryLength, true))})
		return
	}
	task, err := s.handler.SendTask(ctx, legacyReq)
	if err != nil {
		s.removeV1PushConfig(legacyReq.ID, embeddedPushID)
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
		return
	}
	task, err = s.awaitV1TaskCompletion(ctx, task)
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
		return
	}
	s.recordTask(task)
	var historyLength *int
	if params.Configuration != nil {
		historyLength = params.Configuration.HistoryLength
	}
	converted := taskToV1Options(task, historyLength, true)
	s.notifyV1PushConfigs(task.ID, V1StreamResponse{Task: &converted})
	writeJSONRPCResult(w, req.ID, V1SendMessageResponse{Task: &converted})
}

func (s *Server) registerEmbeddedPushConfig(config V1TaskPushNotificationConfig, taskID string) (V1TaskPushNotificationConfig, error) {
	if config.TaskID != "" && config.TaskID != taskID {
		return config, fmt.Errorf("taskPushNotificationConfig.taskId must be empty or match the generated task ID")
	}
	if config.URL == "" {
		return config, fmt.Errorf("taskPushNotificationConfig.url is required")
	}
	if !s.allowPrivateV1Push {
		if err := validateWebhookURL(config.URL); err != nil {
			return config, fmt.Errorf("invalid webhook URL: %w", err)
		}
	}
	config.TaskID = taskID
	if config.ID == "" {
		config.ID = fmt.Sprintf("push-%d", time.Now().UnixNano())
	}
	s.storeV1PushConfig(config)
	return config, nil
}

func (s *Server) removeV1PushConfig(taskID, configID string) {
	if configID == "" {
		return
	}
	s.v1PushMu.Lock()
	defer s.v1PushMu.Unlock()
	configs := s.v1PushConfigs[taskID]
	delete(configs, configID)
	if len(configs) == 0 {
		delete(s.v1PushConfigs, taskID)
	}
}

func (s *Server) runV1TaskDetached(req SendTaskRequest) {
	ctx := s.bgCtx
	var cancel context.CancelFunc
	if s.taskTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.taskTimeout)
		defer cancel()
	}
	_, err := s.handler.SendTask(ctx, req)
	if err != nil {
		failed := &Task{ID: req.ID, SessionID: req.SessionID, State: TaskStateFailed, Messages: []Message{req.Message}, Metadata: req.Metadata}
		s.recordTask(failed)
		if _, publishes := s.handler.(StreamingHandler); !publishes {
			s.PublishTaskUpdate(req.ID, &TaskUpdateEvent{Result: failed, Error: &JSONRPCError{Code: JSONRPCInternalError, Message: err.Error()}, Final: true})
		} else {
			converted := taskToV1(failed)
			s.notifyV1PushConfigs(req.ID, V1StreamResponse{Task: &converted})
		}
		return
	}
	task, err := s.handler.GetTask(ctx, GetTaskRequest{ID: req.ID})
	if err != nil {
		failed := &Task{ID: req.ID, SessionID: req.SessionID, State: TaskStateFailed, Messages: []Message{req.Message}, Metadata: req.Metadata}
		s.recordTask(failed)
		return
	}
	task = cloneTask(task)
	s.recordTask(task)
	if _, publishes := s.handler.(StreamingHandler); !publishes {
		s.PublishTaskUpdate(req.ID, &TaskUpdateEvent{Result: task, Final: isTerminalState(task.State)})
	} else {
		converted := taskToV1(task)
		s.notifyV1PushConfigs(req.ID, V1StreamResponse{Task: &converted})
	}
}

func (s *Server) awaitV1TaskCompletion(ctx context.Context, task *Task) (*Task, error) {
	if task == nil {
		return nil, fmt.Errorf("handler returned a nil task")
	}
	for !isTerminalState(task.State) && task.State != TaskStateInputRequired && task.State != TaskStateAuthRequired {
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		updated, err := s.handler.GetTask(ctx, GetTaskRequest{ID: task.ID})
		if err != nil {
			return nil, err
		}
		task = updated
	}
	return task, nil
}

func v1TaskPointer(task V1Task) *V1Task { return &task }

func (s *Server) handleV1SendStreamingMessage(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params v1SendMessageRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	if handler, ok := s.handler.(V1StreamingMessageHandler); ok {
		stream, err := handler.SendStreamingMessage(ctx, params)
		if err != nil {
			writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
			return
		}
		flusher, ok := prepareV1SSE(w)
		if !ok {
			writeJSONRPCError(w, req.ID, JSONRPCInternalError, "streaming not supported")
			return
		}
		first := true
		taskStream := false
		for {
			select {
			case <-ctx.Done():
				return
			case event, open := <-stream:
				if !open {
					if first {
						writeV1SSEError(w, flusher, req.ID, A2AErrorInvalidAgentResponse, "stream closed before its initial task or message")
					}
					return
				}
				if err := validateNativeV1StreamEvent(event, first, taskStream); err != nil {
					writeV1SSEError(w, flusher, req.ID, A2AErrorInvalidAgentResponse, err.Error())
					return
				}
				if first {
					taskStream = event.Task != nil
					first = false
				}
				if !writeV1SSEEvent(w, flusher, req.ID, event) {
					return
				}
				if event.Message != nil {
					return
				}
				if event.StatusUpdate != nil && isTerminalOrInterruptedV1State(event.StatusUpdate.Status.State) {
					return
				}
			}
		}
	}
	legacyReq, err := params.toLegacy()
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	flusher, ok := prepareV1SSE(w)
	if !ok {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, "streaming not supported")
		return
	}
	updates := s.subscribeToTask(legacyReq.ID)
	defer s.unsubscribeFromTask(legacyReq.ID, updates)

	type taskResult struct {
		task *Task
		err  error
	}
	resultCh := make(chan taskResult, 1)
	go func() {
		task, err := s.handler.SendTask(ctx, legacyReq)
		resultCh <- taskResult{task: task, err: err}
	}()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	var initialTask *Task
	for initialTask == nil {
		select {
		case <-ctx.Done():
			return
		case result := <-resultCh:
			if result.err != nil {
				writeV1SSEError(w, flusher, req.ID, JSONRPCInternalError, result.err.Error())
				return
			}
			initialTask = result.task
		case <-heartbeat.C:
			if !s.writeSSEComment(w, flusher, "heartbeat") {
				return
			}
		}
	}
	s.recordTask(initialTask)
	if !writeV1SSEEvent(w, flusher, req.ID, map[string]any{"task": taskToV1(initialTask)}) {
		return
	}
	if isTerminalState(initialTask.State) || initialTask.State == TaskStateInputRequired {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-updates.done:
			return
		case event := <-updates.events:
			if !writeV1SSEEvent(w, flusher, req.ID, streamResponseFromLegacy(legacyReq.ID, legacyReq.SessionID, event)) {
				return
			}
			if event.Final {
				return
			}
		case <-heartbeat.C:
			if !s.writeSSEComment(w, flusher, "heartbeat") {
				return
			}
		}
	}
}

func validateNativeV1StreamEvent(event V1StreamResponse, first, taskStream bool) error {
	fields := 0
	if event.Task != nil {
		fields++
	}
	if event.Message != nil {
		fields++
	}
	if event.StatusUpdate != nil {
		fields++
	}
	if event.ArtifactUpdate != nil {
		fields++
	}
	if fields != 1 {
		return fmt.Errorf("StreamResponse must contain exactly one response field")
	}
	if first {
		if event.Task == nil && event.Message == nil {
			return fmt.Errorf("stream must start with a task or message")
		}
		return nil
	}
	if !taskStream || event.Task != nil || event.Message != nil {
		return fmt.Errorf("only task streams may continue, using statusUpdate or artifactUpdate events")
	}
	return nil
}

func isTerminalOrInterruptedV1State(state string) bool {
	switch state {
	case "TASK_STATE_COMPLETED", "TASK_STATE_FAILED", "TASK_STATE_CANCELED", "TASK_STATE_REJECTED", "TASK_STATE_INPUT_REQUIRED", "TASK_STATE_AUTH_REQUIRED":
		return true
	default:
		return false
	}
}

func (s *Server) handleV1SubscribeToTask(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params GetTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	updates := s.subscribeToTask(params.ID)
	defer s.unsubscribeFromTask(params.ID, updates)
	task, err := s.handler.GetTask(ctx, params)
	if err != nil {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotFound, err.Error())
		return
	}
	if isTerminalState(task.State) {
		writeJSONRPCError(w, req.ID, A2AErrorUnsupportedOperation, "cannot subscribe to a terminal task")
		return
	}
	flusher, ok := prepareV1SSE(w)
	if !ok {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, "streaming not supported")
		return
	}
	if !writeV1SSEEvent(w, flusher, req.ID, map[string]any{"task": taskToV1(task)}) {
		return
	}

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates.done:
			return
		case event := <-updates.events:
			if !writeV1SSEEvent(w, flusher, req.ID, streamResponseFromLegacy(task.ID, task.SessionID, event)) {
				return
			}
			if event.Final {
				return
			}
		case <-heartbeat.C:
			if !s.writeSSEComment(w, flusher, "heartbeat") {
				return
			}
		}
	}
}

func prepareV1SSE(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return flusher, true
}

func writeV1SSEEvent(w http.ResponseWriter, flusher http.Flusher, id any, result any) bool {
	payload, err := json.Marshal(JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func writeV1SSEError(w http.ResponseWriter, flusher http.Flusher, id any, code int, message string) bool {
	payload, err := json.Marshal(JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &JSONRPCError{Code: code, Message: message}})
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func streamResponseFromLegacy(taskID, contextID string, event *TaskUpdateEvent) V1StreamResponse {
	if event.Artifact != nil {
		parts := make([]v1Part, 0, len(event.Artifact.Parts))
		for _, part := range event.Artifact.Parts {
			parts = append(parts, partFromLegacy(part))
		}
		return V1StreamResponse{ArtifactUpdate: &V1TaskArtifactUpdateEvent{
			TaskID:    taskID,
			ContextID: contextID,
			Artifact: V1Artifact{
				ArtifactID: fmt.Sprintf("%s-artifact-%d", taskID, event.Artifact.Index+1),
				Name:       event.Artifact.Name,
				Parts:      parts,
				Metadata:   event.Artifact.Metadata,
			},
			Append:    event.Artifact.Append,
			LastChunk: event.Artifact.LastChunk,
		}}
	}
	if event.Result != nil {
		return V1StreamResponse{StatusUpdate: &V1TaskStatusUpdateEvent{
			TaskID:    taskID,
			ContextID: contextID,
			Status: V1TaskStatus{
				State: taskStateToV1(event.Result.State),
			},
		}}
	}
	return V1StreamResponse{StatusUpdate: &V1TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: contextID,
		Status: V1TaskStatus{State: "TASK_STATE_UNSPECIFIED"},
	}}
}

func (s *Server) handleV1GetTask(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params v1GetTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	if params.HistoryLength != nil && *params.HistoryLength < 0 {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, "historyLength must be non-negative")
		return
	}
	task, err := s.handler.GetTask(ctx, GetTaskRequest{ID: params.ID})
	if err != nil {
		task = s.recordedTask(params.ID)
		if task == nil {
			writeJSONRPCError(w, req.ID, A2AErrorTaskNotFound, err.Error())
			return
		}
	}
	writeJSONRPCResult(w, req.ID, taskToV1Options(task, params.HistoryLength, true))
}

func (s *Server) handleV1ListTasks(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params v1ListTasksRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	legacyState, err := taskStateFromV1(params.Status)
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	if params.HistoryLength != nil && *params.HistoryLength < 0 {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, "historyLength must be non-negative")
		return
	}
	pageSize := params.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize < 1 || pageSize > 100 {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, "pageSize must be between 1 and 100")
		return
	}
	var statusAfter time.Time
	if params.StatusTimestampAfter != "" {
		statusAfter, err = time.Parse(time.RFC3339Nano, params.StatusTimestampAfter)
		if err != nil {
			writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, "statusTimestampAfter must be an RFC3339 timestamp")
			return
		}
	}
	cursor, err := decodeV1TaskCursor(params.PageToken)
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	result, err := s.handler.QueryTasks(ctx, QueryTasksRequest{SessionID: params.ContextID, State: legacyState})
	if err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInternalError, err.Error())
		return
	}
	sort.SliceStable(result.Tasks, func(i, j int) bool {
		left, right := taskUpdatedAt(result.Tasks[i]), taskUpdatedAt(result.Tasks[j])
		if left.Equal(right) {
			return result.Tasks[i].ID < result.Tasks[j].ID
		}
		return left.After(right)
	})
	matching := make([]*Task, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		updatedAt := taskUpdatedAt(task)
		if !statusAfter.IsZero() && updatedAt.Before(statusAfter) {
			continue
		}
		matching = append(matching, task)
	}
	filtered := make([]*Task, 0, len(matching))
	for _, task := range matching {
		if cursor != nil && !taskAfterV1Cursor(task, *cursor) {
			continue
		}
		filtered = append(filtered, task)
	}
	total := len(matching)
	end := pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	tasks := make([]v1Task, 0, end)
	for _, task := range filtered[:end] {
		tasks = append(tasks, taskToV1Options(task, params.HistoryLength, params.IncludeArtifacts))
	}
	nextPageToken := ""
	if end < len(filtered) && end > 0 {
		nextPageToken = encodeV1TaskCursor(filtered[end-1])
	}
	writeJSONRPCResult(w, req.ID, map[string]any{
		"tasks":         tasks,
		"nextPageToken": nextPageToken,
		"pageSize":      pageSize,
		"totalSize":     total,
	})
}

func (s *Server) handleV1CancelTask(ctx context.Context, w http.ResponseWriter, req JSONRPCRequest) {
	var params CancelTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, JSONRPCInvalidParams, err.Error())
		return
	}
	task, err := s.handler.CancelTask(ctx, params)
	if err != nil {
		writeJSONRPCError(w, req.ID, A2AErrorTaskNotCancelable, err.Error())
		return
	}
	s.recordTask(task)
	writeJSONRPCResult(w, req.ID, taskToV1(task))
}

func (p v1SendMessageRequest) toLegacy() (SendTaskRequest, error) {
	if p.Message.MessageID == "" {
		return SendTaskRequest{}, fmt.Errorf("message.messageId is required")
	}
	if len(p.Message.Parts) == 0 {
		return SendTaskRequest{}, fmt.Errorf("message.parts must not be empty")
	}
	message, err := messageToLegacy(p.Message)
	if err != nil {
		return SendTaskRequest{}, err
	}
	taskID := p.Message.TaskID
	if taskID == "" {
		taskID = fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	contextID := p.Message.ContextID
	if contextID == "" {
		contextID = fmt.Sprintf("context-%d", time.Now().UnixNano())
	}
	return SendTaskRequest{ID: taskID, SessionID: contextID, Message: message, Metadata: p.Metadata}, nil
}

func messageToLegacy(msg v1Message) (Message, error) {
	role := strings.ToUpper(msg.Role)
	if role != "ROLE_USER" && role != "USER" {
		return Message{}, fmt.Errorf("message.role must be ROLE_USER")
	}
	parts := make([]Part, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		contentFields := 0
		if part.Text != nil {
			contentFields++
		}
		if part.Raw != nil {
			contentFields++
		}
		if part.URL != nil {
			contentFields++
		}
		if part.dataSet || part.Data != nil {
			contentFields++
		}
		if contentFields != 1 {
			return Message{}, fmt.Errorf("each part must contain exactly one of text, raw, url, or data")
		}
		switch {
		case part.Text != nil:
			parts = append(parts, NewTextPart(*part.Text))
		case part.Raw != nil:
			parts = append(parts, NewFilePartBytes(part.Filename, part.MediaType, *part.Raw))
		case part.URL != nil:
			parts = append(parts, NewFilePartURI(part.Filename, part.MediaType, *part.URL))
		case part.dataSet || part.Data != nil:
			if data, ok := part.Data.(map[string]any); ok {
				parts = append(parts, NewDataPart(data))
				parts[len(parts)-1].Data.MIMEType = part.MediaType
			} else {
				parts = append(parts, Part{Type: PartTypeData, Data: &DataPart{MIMEType: part.MediaType, Value: part.Data}})
			}
		}
	}
	return Message{
		MessageID:        msg.MessageID,
		ContextID:        msg.ContextID,
		TaskID:           msg.TaskID,
		Role:             string(RoleUser),
		Parts:            parts,
		Metadata:         msg.Metadata,
		Extensions:       append([]string(nil), msg.Extensions...),
		ReferenceTaskIDs: append([]string(nil), msg.ReferenceTaskIDs...),
	}, nil
}

func taskToV1(task *Task) v1Task {
	return taskToV1Options(task, nil, true)
}

func taskToV1Options(task *Task, historyLength *int, includeArtifacts bool) v1Task {
	if task == nil {
		return v1Task{}
	}
	status := v1TaskStatus{State: taskStateToV1(task.State)}
	if n := len(task.History); n > 0 && !task.History[n-1].Timestamp.IsZero() {
		status.Timestamp = task.History[n-1].Timestamp.UTC().Format(time.RFC3339Nano)
	}
	history := make([]v1Message, 0, len(task.Messages))
	for i, message := range task.Messages {
		history = append(history, messageFromLegacy(message, fmt.Sprintf("%s-message-%d", task.ID, i+1), task.ID, task.SessionID))
	}
	if historyLength != nil {
		if *historyLength == 0 {
			history = nil
		} else if len(history) > *historyLength {
			history = history[len(history)-*historyLength:]
		}
	}
	artifacts := make([]v1Artifact, 0, len(task.Artifacts))
	if includeArtifacts {
		for i, artifact := range task.Artifacts {
			parts := make([]v1Part, 0, len(artifact.Parts))
			for _, part := range artifact.Parts {
				parts = append(parts, partFromLegacy(part))
			}
			artifacts = append(artifacts, v1Artifact{
				ArtifactID: fmt.Sprintf("%s-artifact-%d", task.ID, i+1),
				Name:       artifact.Name,
				Parts:      parts,
				Metadata:   artifact.Metadata,
			})
		}
	}
	return v1Task{ID: task.ID, ContextID: task.SessionID, Status: status, Artifacts: artifacts, History: history, Metadata: task.Metadata}
}

func taskUpdatedAt(task *Task) time.Time {
	if task == nil || len(task.History) == 0 {
		return time.Time{}
	}
	return task.History[len(task.History)-1].Timestamp
}

func encodeV1TaskCursor(task *Task) string {
	cursor := v1TaskCursor{Timestamp: taskUpdatedAt(task).UTC().Format(time.RFC3339Nano), TaskID: task.ID}
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeV1TaskCursor(token string) (*v1TaskCursor, error) {
	if token == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("invalid pageToken")
	}
	var cursor v1TaskCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.TaskID == "" {
		return nil, fmt.Errorf("invalid pageToken")
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.Timestamp); err != nil {
		return nil, fmt.Errorf("invalid pageToken")
	}
	return &cursor, nil
}

func taskAfterV1Cursor(task *Task, cursor v1TaskCursor) bool {
	cursorTime, _ := time.Parse(time.RFC3339Nano, cursor.Timestamp)
	taskTime := taskUpdatedAt(task)
	return taskTime.Before(cursorTime) || taskTime.Equal(cursorTime) && task.ID > cursor.TaskID
}

func encodeV1PageToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeV1PageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("invalid pageToken")
	}
	offset, err := strconv.Atoi(string(data))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid pageToken")
	}
	return offset, nil
}

func messageFromLegacy(message Message, messageID, taskID, contextID string) v1Message {
	parts := make([]v1Part, 0, len(message.Parts))
	for _, part := range message.Parts {
		parts = append(parts, partFromLegacy(part))
	}
	role := "ROLE_AGENT"
	if strings.EqualFold(message.Role, string(RoleUser)) {
		role = "ROLE_USER"
	}
	if message.MessageID != "" {
		messageID = message.MessageID
	}
	if message.ContextID != "" {
		contextID = message.ContextID
	}
	if message.TaskID != "" {
		taskID = message.TaskID
	}
	return v1Message{
		MessageID:        messageID,
		ContextID:        contextID,
		TaskID:           taskID,
		Role:             role,
		Parts:            parts,
		Metadata:         message.Metadata,
		Extensions:       append([]string(nil), message.Extensions...),
		ReferenceTaskIDs: append([]string(nil), message.ReferenceTaskIDs...),
	}
}

func partFromLegacy(part Part) v1Part {
	switch part.Type {
	case PartTypeText:
		text := part.Text
		return v1Part{Text: &text, MediaType: "text/plain"}
	case PartTypeFile:
		if part.File == nil {
			return v1Part{}
		}
		if part.File.Bytes != "" {
			raw := part.File.Bytes
			return v1Part{Raw: &raw, Filename: part.File.Name, MediaType: part.File.MIMEType}
		}
		uri := part.File.URI
		return v1Part{URL: &uri, Filename: part.File.Name, MediaType: part.File.MIMEType}
	case PartTypeData:
		if part.Data == nil {
			return v1Part{}
		}
		value := part.Data.Value
		if value == nil {
			value = part.Data.Data
		}
		return v1Part{Data: value, MediaType: part.Data.MIMEType}
	default:
		return v1Part{}
	}
}

func taskStateToV1(state TaskState) string {
	switch state {
	case TaskStateSubmitted:
		return "TASK_STATE_SUBMITTED"
	case TaskStateWorking:
		return "TASK_STATE_WORKING"
	case TaskStateInputRequired:
		return "TASK_STATE_INPUT_REQUIRED"
	case TaskStateAuthRequired:
		return "TASK_STATE_AUTH_REQUIRED"
	case TaskStateCompleted:
		return "TASK_STATE_COMPLETED"
	case TaskStateFailed:
		return "TASK_STATE_FAILED"
	case TaskStateCanceled:
		return "TASK_STATE_CANCELED"
	case TaskStateRejected:
		return "TASK_STATE_REJECTED"
	default:
		return "TASK_STATE_UNSPECIFIED"
	}
}

func taskStateFromV1(state string) (TaskState, error) {
	switch state {
	case "", "TASK_STATE_UNSPECIFIED":
		return "", nil
	case "TASK_STATE_SUBMITTED":
		return TaskStateSubmitted, nil
	case "TASK_STATE_WORKING":
		return TaskStateWorking, nil
	case "TASK_STATE_INPUT_REQUIRED":
		return TaskStateInputRequired, nil
	case "TASK_STATE_AUTH_REQUIRED":
		return TaskStateAuthRequired, nil
	case "TASK_STATE_COMPLETED":
		return TaskStateCompleted, nil
	case "TASK_STATE_FAILED":
		return TaskStateFailed, nil
	case "TASK_STATE_CANCELED":
		return TaskStateCanceled, nil
	case "TASK_STATE_REJECTED":
		return TaskStateRejected, nil
	default:
		return "", fmt.Errorf("unsupported task state %q", state)
	}
}
