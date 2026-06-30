package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/pkg/util"
	"github.com/covoyage/covonaut/provider/anthropic"
	"github.com/covoyage/covonaut/provider/gemini"
	"github.com/covoyage/covonaut/provider/openai"
	"github.com/covoyage/covonaut/session"
	"github.com/covoyage/covonaut/skill"
	agentstore "github.com/covoyage/covonaut/store"
	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui"
	"github.com/covoyage/covonaut/tui/agentadapter"
)

type threadStore interface {
	CreateThread(ctx context.Context) (*session.ThreadSnapshot, error)
	BranchThread(ctx context.Context, key, entryID string) (*session.ThreadSnapshot, error)
	GetThread(ctx context.Context, key string) (*session.ThreadSnapshot, error)
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn, // stderr — don't interfere with TUI rendering
	}))

	if err := theme.InitThemeFromEnv(); err != nil {
		log.Fatalf("theme: %v", err)
	}

	llm := buildProvider()
	model := util.EnvOrDefault("AGENT_MODEL", defaultModel())
	thinking := thinkingFromEnv()
	availableSkills, skillDiagnostics, err := loadSkillsFromEnv()
	if err != nil {
		log.Fatalf("load skills: %v", err)
	}
	selectedSkills := parseListEnv("AGENT_SKILLS")

	mode := agentcore.ModeSerial
	if os.Getenv("EXECUTION_MODE") == "parallel" {
		mode = agentcore.ModeParallel
	}

	var store agentcore.Store
	if dir := os.Getenv("STORE_DIR"); dir != "" {
		var err error
		store, err = agentstore.NewFileStore(dir)
		if err != nil {
			log.Fatalf("create store: %v", err)
		}
	}

	weatherSpecialist := agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Name:      "weather_specialist",
			Model:     model,
			Provider:  llm,
			Thinking:  thinking,
			Streaming: true,
		},
		SystemPrompt: "You are a weather specialist. Use the get_weather tool to answer weather questions concisely.",
		Tools:        []*agentcore.Tool{weatherTool()},
	}

	mathSpecialist := agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Name:      "math_specialist",
			Model:     model,
			Provider:  llm,
			Thinking:  thinking,
			Streaming: true,
		},
		SystemPrompt: "You are a math specialist. Use the calculator tool to solve math problems. Be concise.",
		Tools:        []*agentcore.Tool{calculatorTool()},
	}

	coordinatorCfg := agentcore.Config{
		ModelConfig: agentcore.ModelConfig{
			Name:      "coordinator",
			Model:     model,
			Provider:  llm,
			Thinking:  thinking,
			Streaming: true,
		},
		SystemPrompt: strings.Join([]string{
			"You are a coordinator agent.",
			"For weather questions, delegate to the weather specialist.",
			"For math questions, delegate to the math specialist.",
			"For general questions, answer directly.",
			"You may call multiple specialists in one turn.",
		}, " "),
		ExecutionConfig: agentcore.ExecutionConfig{
			MaxTurns:          10,
			ExecutionMode:     mode,
			Concurrency:       5,
			ValidateArguments: true,
			SteeringMode:      agentcore.SteeringAll,
			FollowUpMode:      agentcore.SteeringAll,
			GlobalBefore:      []agentcore.BeforeHook{agentcore.LoggingBeforeHook(logger)},
			GlobalAfter:       []agentcore.AfterHook{agentcore.LoggingAfterHook(logger)},
			Middleware:        []agentcore.Middleware{agentcore.TimeoutMiddleware(30 * time.Second)},
		},
		CompactionConfig: agentcore.CompactionConfig{
			ContextWindow:    128000,
			ReserveTokens:    32000,
			KeepRecentTokens: 4000,
		},
		SkillConfig: agentcore.SkillConfig{
			AvailableSkills: availableSkills,
			SelectedSkills:  selectedSkills,
		},
		Store:       store,
		RetryConfig: &agentcore.RetryConfig{
			MaxRetries:  3,
			BaseDelayMs: 1000,
			MaxDelayMs:  15000,
		},
		Handoffs: []agentcore.HandoffConfig{
			{
				Name:        "weather_specialist",
				Description: "Handles weather-related questions for any city or location",
				Mode:        agentcore.HandoffDelegate,
				AgentConfig: weatherSpecialist,
			},
			{
				Name:        "math_specialist",
				Description: "Handles math calculations and arithmetic",
				Mode:        agentcore.HandoffDelegate,
				AgentConfig: mathSpecialist,
			},
		},
	}

	providerName := util.EnvOrDefault("PROVIDER", "openai")

	var busy atomic.Bool
	var currentAgent atomic.Pointer[agentcore.Agent]
	var currentThreadID string

	// appPtr is used inside OnSubmit before the app is created (Go two-phase
	// init pattern — closures capture the pointer, not the value).
	var appPtr *chat.ChatApp

	createThread := func(ctx context.Context) (string, *session.ThreadSnapshot, error) {
		if store == nil {
			return "", nil, nil
		}
		if ts, ok := store.(threadStore); ok {
			thread, err := ts.CreateThread(ctx)
			if err != nil {
				return "", nil, err
			}
			return thread.Info.ID, thread, nil
		}
		return fmt.Sprintf("thread-%d", time.Now().UnixMilli()), nil, nil
	}

	loadAgentForThread := func(ctx context.Context, threadID string) (*agentcore.Agent, error) {
		agent := agentcore.New(coordinatorCfg)
		if threadID == "" || store == nil {
			return agent, nil
		}
		hasState, err := storeHasKey(ctx, store, threadID)
		if err != nil {
			return nil, err
		}
		if !hasState {
			return agent, nil
		}
		if err := agent.LoadState(ctx, threadID); err != nil {
			return nil, err
		}
		return agent, nil
	}

	renderThread := func(thread *session.ThreadSnapshot) {
		if thread == nil || appPtr == nil {
			return
		}
		appPtr.History().Clear()
		for _, item := range thread.Transcript {
			appPtr.History().Append(chatMessageFromAgentMessage(item.Message))
		}
	}

	switchConversation := func(ctx context.Context, threadID string, snapshot *session.ThreadSnapshot) error {
		agent, err := loadAgentForThread(ctx, threadID)
		if err != nil {
			return err
		}
		if prev := currentAgent.Load(); prev != nil {
			prev.Close()
		}
		currentAgent.Store(agent)
		currentThreadID = threadID
		if appPtr != nil {
			agentadapter.BindAgent(appPtr, agent)
			if snapshot != nil {
				renderThread(snapshot)
			} else if ts, ok := store.(threadStore); ok && threadID != "" {
				thread, err := ts.GetThread(ctx, threadID)
				if err != nil {
					return err
				}
				renderThread(thread)
			} else {
				appPtr.History().Clear()
			}
		}
		return nil
	}

	startNewConversation := func(ctx context.Context) error {
		threadID, snapshot, err := createThread(ctx)
		if err != nil {
			return err
		}
		return switchConversation(ctx, threadID, snapshot)
	}

	saveCurrentConversation := func(ctx context.Context) error {
		ag := currentAgent.Load()
		if ag == nil || store == nil || currentThreadID == "" {
			return nil
		}
		return ag.SaveState(ctx, currentThreadID)
	}

	applyThinking := func(cfg *agentcore.Config, thinking *agentcore.ThinkingConfig) {
		cfg.Thinking = cloneThinkingConfig(thinking)
		for i := range cfg.Handoffs {
			cfg.Handoffs[i].AgentConfig.Thinking = cloneThinkingConfig(thinking)
		}
	}
	applyThinking(&weatherSpecialist, thinking)
	applyThinking(&mathSpecialist, thinking)
	applyThinking(&coordinatorCfg, thinking)

	slashSuggestions := []core.Suggestion{
		{InsertText: "/help", Label: "/help", Description: "Show keybindings"},
		{InsertText: "/clear", Label: "/clear", Description: "Start a fresh conversation"},
		{InsertText: "/new", Label: "/new", Description: "Start a fresh conversation"},
		{InsertText: "/branch", Label: "/branch", Description: "Branch the current thread"},
		{InsertText: "/thinking", Label: "/thinking", Description: "Show or change thinking mode"},
		{InsertText: "/thinking summarized", Label: "/thinking summarized", Description: "Show summarized reasoning blocks"},
		{InsertText: "/thinking omitted", Label: "/thinking omitted", Description: "Hide visible reasoning blocks"},
		{InsertText: "/thinking effort medium", Label: "/thinking effort medium", Description: "Set reasoning effort"},
		{InsertText: "/thinking budget -1", Label: "/thinking budget -1", Description: "Use dynamic thinking budget"},
		{InsertText: "/skill:", Label: "/skill:", Description: "Explicitly invoke a loaded skill"},
		{InsertText: "/save", Label: "/save", Description: "Persist the current thread"},
		{InsertText: "/quit", Label: "/quit", Description: "Exit application"},
	}
	for _, item := range availableSkills {
		slashSuggestions = append(slashSuggestions, core.Suggestion{
			InsertText:  "/skill:" + item.Name + " ",
			Label:       "/skill:" + item.Name,
			Description: item.Description,
		})
	}

	reloadCurrentAgent := func(ctx context.Context) error {
		var snap *agentcore.StateSnapshot
		if ag := currentAgent.Load(); ag != nil {
			s := ag.State().Snapshot()
			snap = &s
		}

		agent := agentcore.New(coordinatorCfg)
		if snap != nil {
			agent.State().Restore(*snap)
		}
		if prev := currentAgent.Load(); prev != nil {
			prev.Close()
		}
		currentAgent.Store(agent)
		if appPtr != nil {
			agentadapter.BindAgent(appPtr, agent)
			if snap != nil {
				appPtr.History().Clear()
				for _, msg := range snap.Messages {
					appPtr.History().Append(chatMessageFromAgentMessage(msg))
				}
			}
		}
		return nil
	}

	printThinkingStatus := func() {
		appPtr.PrintSystem("thinking: " + formatThinkingConfig(coordinatorCfg.Thinking))
	}

	handleSubmit := func(_ context.Context, input string) {
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			return
		}
		if cmd, ok := skill.ParseCommand(trimmed); ok {
			if _, found := skill.FindByName(coordinatorCfg.AvailableSkills, cmd.Name); !found {
				appPtr.PrintError(fmt.Errorf("unknown skill %q", cmd.Name))
				return
			}
		}
		if strings.HasPrefix(trimmed, "/thinking") {
			if busy.Load() {
				appPtr.PrintSystem("(busy — please wait for the current reply)")
				return
			}
			next, changed, err := parseThinkingCommand(trimmed, coordinatorCfg.Thinking)
			if err != nil {
				appPtr.PrintError(err)
				return
			}
			if !changed {
				printThinkingStatus()
				return
			}
			applyThinking(&weatherSpecialist, next)
			applyThinking(&mathSpecialist, next)
			applyThinking(&coordinatorCfg, next)
			if err := reloadCurrentAgent(context.Background()); err != nil {
				appPtr.PrintError(err)
				return
			}
			printThinkingStatus()
			return
		}
		switch trimmed {
		case "/help":
			appPtr.ToggleKeyHelp()
			return
		case "/clear", "/new":
			if busy.Load() {
				appPtr.PrintSystem("(busy — please wait for the current reply)")
				return
			}
			if err := startNewConversation(context.Background()); err != nil {
				appPtr.PrintError(err)
				return
			}
			if currentThreadID != "" {
				appPtr.PrintSystem(fmt.Sprintf("started new thread: %s", currentThreadID))
			} else {
				appPtr.PrintSystem("started new conversation")
			}
			return
		case "/branch":
			if busy.Load() {
				appPtr.PrintSystem("(busy — please wait for the current reply)")
				return
			}
			ts, ok := store.(threadStore)
			if !ok || currentThreadID == "" {
				appPtr.PrintSystem("branching requires a session-backed store and an active thread")
				return
			}
			if err := saveCurrentConversation(context.Background()); err != nil {
				appPtr.PrintError(fmt.Errorf("save current thread: %w", err))
				return
			}
			thread, err := ts.BranchThread(context.Background(), currentThreadID, "")
			if err != nil {
				appPtr.PrintError(err)
				return
			}
			if err := switchConversation(context.Background(), thread.Info.ID, thread); err != nil {
				appPtr.PrintError(err)
				return
			}
			appPtr.PrintSystem(fmt.Sprintf("branched thread %s -> %s", thread.Info.ParentSession, thread.Info.ID))
			return
		case "/save":
			if store == nil {
				appPtr.PrintSystem("nothing to save (no store configured or no session yet)")
				return
			}
			if err := saveCurrentConversation(context.Background()); err != nil {
				appPtr.PrintError(fmt.Errorf("save state: %w", err))
			} else {
				if currentThreadID != "" {
					appPtr.PrintSystem(fmt.Sprintf("thread saved: %s", currentThreadID))
				} else {
					appPtr.PrintSystem("conversation saved")
				}
			}
			return
		case "/quit", "exit":
			_ = appPtr.Stop()
			return
		}

		if busy.Load() {
			appPtr.PrintSystem("(busy — please wait for the current reply)")
			return
		}

		agent := currentAgent.Load()
		if agent == nil {
			if err := startNewConversation(context.Background()); err != nil {
				appPtr.PrintError(err)
				return
			}
			agent = currentAgent.Load()
		}
		if agent == nil {
			appPtr.PrintError(fmt.Errorf("failed to initialize conversation"))
			return
		}

		busy.Store(true)
		go func() {
			defer busy.Store(false)
			_, runErr := agent.Run(context.Background(), trimmed)
			// Agent error is already printed via ChatEventAgentError event,
			// so we only handle non-agent errors (e.g. save failure) here.
			if saveErr := saveCurrentConversation(context.Background()); saveErr != nil {
				if runErr != nil {
					appPtr.PrintSystem(fmt.Sprintf("Conversation saved with errors: %v", saveErr))
				} else {
					appPtr.PrintError(fmt.Errorf("save thread: %w", saveErr))
				}
			}
		}()
	}

	app := tui.NewChatApp(chat.ChatAppConfig{
		Title: fmt.Sprintf(
			"covonaut · provider=%s model=%s mode=%s",
			providerName, model, mode,
		),
		ShowTimings: true,
		ShowTurns:   true,
		AltScreen:   true,
		MouseMode:   "auto",
		Providers: []core.AutocompleteProvider{
			&component.StaticProvider{
				TriggerStr:  "/",
				Suggestions: slashSuggestions,
			},
		},
		OnSubmit: handleSubmit,
	})
	appPtr = app

	theme.SetOnSemanticThemeChange(func() {
		app.History().SetTheme(chat.DefaultChatHistoryTheme())
	})
	themePath := strings.TrimSpace(os.Getenv("TUI_THEME"))
	if themePath == "" {
		themePath = strings.TrimSpace(os.Getenv("AGENT_TUI_THEME"))
	}
	if themePath != "" && os.Getenv("TUI_THEME_WATCH") != "0" {
		stop := theme.StartSemanticThemeWatcher(themePath, 0, nil)
		defer stop()
	}

	app.PrintSystem(strings.Join([]string{
		"Welcome! Type a question below.",
		"Slash commands: /help /clear /new /branch /thinking /skill:name /save /quit.",
		"Try \"What's the weather in Tokyo and what is 7 * 8?\"",
	}, "\n"))
	for _, diag := range skillDiagnostics {
		app.PrintSystem(fmt.Sprintf("skill warning: %s (%s)", diag.Message, diag.Path))
	}
	if len(availableSkills) > 0 {
		var names []string
		for _, item := range availableSkills {
			names = append(names, item.Name)
		}
		app.PrintSystem("available skills: " + strings.Join(names, ", "))
	}

	if err := startNewConversation(context.Background()); err != nil {
		log.Fatalf("start conversation: %v", err)
	}
	if currentThreadID != "" {
		app.PrintSystem(fmt.Sprintf("active thread: %s", currentThreadID))
	}
	printThinkingStatus()

	// Ctrl+/ opens the keybindings overlay. Esc closes it.
	app.Keybindings().Register("app.help", terminal.KeybindingDef{
		DefaultKeys: []terminal.KeyID{"ctrl+/"},
		Description: "Toggle keybindings help",
	})
	app.Host().AddChild(hotkeyRouter{app: app})

	if err := app.Start(); err != nil {
		log.Fatalf("start tui: %v", err)
	}
	<-app.Done()
}

// hotkeyRouter is a zero-size Component that captures global hotkeys.
type hotkeyRouter struct {
	app *chat.ChatApp
}

func (h hotkeyRouter) Render(int64) []string { return nil }
func (h hotkeyRouter) HandleInput(data string) {
	if terminal.MatchesKey(data, "ctrl+/") {
		h.app.ToggleKeyHelp()
	}
}
func (hotkeyRouter) Invalidate() {}

// --- provider setup ---

func buildProvider() agentcore.Provider {
	providerType := util.EnvOrDefault("PROVIDER", "openai")

	switch providerType {
	case "anthropic":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("API_KEY")
		}
		if apiKey == "" {
			log.Fatal("ANTHROPIC_API_KEY or API_KEY is required")
		}
		return anthropic.New(anthropic.Config{
			APIKey:  apiKey,
			BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
		})
	case "gemini":
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("GOOGLE_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("API_KEY")
		}
		if apiKey == "" {
			log.Fatal("GEMINI_API_KEY or GOOGLE_API_KEY or API_KEY is required")
		}
		return gemini.New(gemini.Config{
			APIKey:  apiKey,
			BaseURL: os.Getenv("GEMINI_BASE_URL"),
		})
	default:
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("API_KEY")
		}
		if apiKey == "" {
			log.Fatal("OPENAI_API_KEY or API_KEY is required")
		}
		return openai.New(openai.Config{
			APIKey:  apiKey,
			BaseURL: os.Getenv("OPENAI_BASE_URL"),
		})
	}
}

func loadSkillsFromEnv() ([]skill.Skill, []skill.Diagnostic, error) {
	paths := parsePathListEnv("SKILL_DIRS")
	if len(paths) == 0 {
		return nil, nil, nil
	}
	return skill.Load(paths...)
}

func parsePathListEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, string(filepath.ListSeparator))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func parseListEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func defaultModel() string {
	switch util.EnvOrDefault("PROVIDER", "openai") {
	case "anthropic":
		return "claude-sonnet-4-20250514"
	case "gemini":
		return "gemini-2.5-flash"
	default:
		return "gpt-4o-mini"
	}
}

// --- tool definitions ---

func weatherTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "get_weather",
		Description: "Get the current weather for a given city",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{
					"type":        "string",
					"description": "City name, e.g. Tokyo",
				},
			},
			"required":             []string{"location"},
			"additionalProperties": false,
		},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Location string `json:"location"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			return map[string]any{
				"location":    p.Location,
				"temperature": "22°C",
				"condition":   "sunny",
				"humidity":    "45%",
			}, nil
		},
	}
}

func calculatorTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "calculator",
		Description: "Evaluate a simple math expression",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "Math expression, e.g. 7*8",
				},
			},
			"required":             []string{"expression"},
			"additionalProperties": false,
		},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			return map[string]string{
				"expression": p.Expression,
				"result":     "56",
				"note":       "stub calculator",
			}, nil
		},
	}
}

func storeHasKey(ctx context.Context, store agentcore.Store, key string) (bool, error) {
	return store.Has(ctx, key)
}

func chatMessageFromAgentMessage(msg agentcore.Message) chat.ChatMessage {
	role := chat.RoleSystem
	switch msg.Role {
	case agentcore.RoleUser:
		role = chat.RoleUser
	case agentcore.RoleAssistant:
		role = chat.RoleAssistant
	case agentcore.RoleTool:
		role = chat.RoleTool
	case agentcore.RoleSystem:
		role = chat.RoleSystem
	}
	text := msg.Content
	if text == "" && len(msg.ToolCalls) > 0 {
		text = fmt.Sprintf("tool calls: %d", len(msg.ToolCalls))
	}
	return chat.ChatMessage{
		Role: role,
		Text: text,
	}
}

func thinkingFromEnv() *agentcore.ThinkingConfig {
	includeRaw := strings.TrimSpace(os.Getenv("THINKING_INCLUDE_THOUGHTS"))
	displayRaw := strings.TrimSpace(os.Getenv("THINKING_DISPLAY"))
	effortRaw := strings.TrimSpace(os.Getenv("THINKING_EFFORT"))
	budgetRaw := strings.TrimSpace(os.Getenv("THINKING_BUDGET"))
	if includeRaw == "" && displayRaw == "" && effortRaw == "" && budgetRaw == "" {
		return nil
	}

	cfg := &agentcore.ThinkingConfig{}
	if includeRaw != "" {
		if v, err := strconv.ParseBool(includeRaw); err == nil {
			cfg.IncludeThoughts = v
		}
	}
	if displayRaw != "" {
		cfg.Display = agentcore.ThinkingDisplay(strings.ToLower(displayRaw))
	}
	if effortRaw != "" {
		cfg.Effort = agentcore.ThinkingEffort(strings.ToLower(effortRaw))
	}
	if budgetRaw != "" {
		if v, err := strconv.ParseInt(budgetRaw, 10, 64); err == nil {
			cfg.Budget = v
		}
	}
	return cfg
}

func cloneThinkingConfig(cfg *agentcore.ThinkingConfig) *agentcore.ThinkingConfig {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	return &cp
}

func compactThinkingConfig(cfg *agentcore.ThinkingConfig) *agentcore.ThinkingConfig {
	if cfg == nil {
		return nil
	}
	if !cfg.IncludeThoughts &&
		cfg.Display == agentcore.ThinkingDisplayDefault &&
		cfg.Effort == agentcore.ThinkingEffortDefault &&
		cfg.Budget == 0 {
		return nil
	}
	return cfg
}

func formatThinkingConfig(cfg *agentcore.ThinkingConfig) string {
	if cfg == nil {
		return "default"
	}
	parts := []string{
		"display=" + string(cfg.NormalizedDisplay()),
	}
	if cfg.Effort != "" {
		parts = append(parts, "effort="+string(cfg.Effort))
	}
	if cfg.Budget != 0 {
		parts = append(parts, fmt.Sprintf("budget=%d", cfg.Budget))
	}
	parts = append(parts, fmt.Sprintf("include_thoughts=%t", cfg.IncludeThoughts))
	return strings.Join(parts, " ")
}

func parseThinkingCommand(input string, current *agentcore.ThinkingConfig) (*agentcore.ThinkingConfig, bool, error) {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) <= 1 {
		return cloneThinkingConfig(current), false, nil
	}

	next := cloneThinkingConfig(current)
	if next == nil {
		next = &agentcore.ThinkingConfig{}
	}

	switch strings.ToLower(fields[1]) {
	case "reset":
		return nil, true, nil
	case "on", "summarized":
		next.IncludeThoughts = true
		next.Display = agentcore.ThinkingDisplaySummarized
		return compactThinkingConfig(next), true, nil
	case "off", "omitted":
		next.IncludeThoughts = false
		next.Display = agentcore.ThinkingDisplayOmitted
		return compactThinkingConfig(next), true, nil
	case "effort":
		if len(fields) < 3 {
			return nil, false, fmt.Errorf("usage: /thinking effort <low|medium|high|max|default>")
		}
		switch strings.ToLower(fields[2]) {
		case "default", "reset":
			next.Effort = agentcore.ThinkingEffortDefault
		case "low", "medium", "high", "max":
			next.Effort = agentcore.ThinkingEffort(strings.ToLower(fields[2]))
		default:
			return nil, false, fmt.Errorf("invalid thinking effort %q", fields[2])
		}
		return compactThinkingConfig(next), true, nil
	case "budget":
		if len(fields) < 3 {
			return nil, false, fmt.Errorf("usage: /thinking budget <n|default>")
		}
		if strings.EqualFold(fields[2], "default") || strings.EqualFold(fields[2], "reset") {
			next.Budget = 0
			return compactThinkingConfig(next), true, nil
		}
		v, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("invalid thinking budget %q", fields[2])
		}
		next.Budget = v
		return compactThinkingConfig(next), true, nil
	case "include":
		if len(fields) < 3 {
			return nil, false, fmt.Errorf("usage: /thinking include <true|false>")
		}
		v, err := strconv.ParseBool(fields[2])
		if err != nil {
			return nil, false, fmt.Errorf("invalid thinking include value %q", fields[2])
		}
		next.IncludeThoughts = v
		if next.Display == agentcore.ThinkingDisplayDefault {
			if v {
				next.Display = agentcore.ThinkingDisplaySummarized
			} else {
				next.Display = agentcore.ThinkingDisplayOmitted
			}
		}
		return compactThinkingConfig(next), true, nil
	default:
		return nil, false, fmt.Errorf("usage: /thinking [on|off|summarized|omitted|effort <...>|budget <...>|include <true|false>|reset]")
	}
}
