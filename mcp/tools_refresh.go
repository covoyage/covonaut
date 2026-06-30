package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/pkg/util"
)

type toolLoader func(context.Context) ([]*agentcore.Tool, error)

func refreshExtensionTools(
	ctx context.Context,
	mu *sync.Mutex,
	agent *agentcore.Agent,
	extensionName string,
	transport string,
	current *[]*agentcore.Tool,
	currentNames *[]string,
	load toolLoader,
) error {
	if agent == nil {
		return nil
	}
	tools, err := load(ctx)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	oldNames := append([]string(nil), (*currentNames)...)
	if len(*currentNames) > 0 {
		agent.UnregisterTools((*currentNames)...)
	}
	agent.RegisterTools(tools...)
	*current = tools
	*currentNames = toolNames(tools)
	agent.EmitEvent(ToolsRefreshedEvent{
		At:        time.Now(),
		Extension: extensionName,
		Transport: transport,
		OldTools:  oldNames,
		NewTools:  append([]string(nil), (*currentNames)...),
	})
	return nil
}

func (e *StdioExtension) refreshTools(ctx context.Context) error {
	if e.client == nil {
		return nil
	}
	return refreshExtensionTools(ctx, &e.refreshMu, e.agent, e.name, "stdio", &e.tools, &e.toolNames, e.client.AgentTools)
}

func (e *StdioExtension) scheduleRefresh() {
	if e.client == nil {
		return
	}
	scheduleRefresh(context.Background(), &e.refreshScheduleMu, &e.refreshInFlight, &e.refreshPending, func(ctx context.Context) error {
		return e.refreshTools(ctx)
	}, func(event RefreshEvent) {
		e.emitRefreshEvent(event)
	}, func(err error) {
		e.client.reportAsyncError("refresh", "tools/list_changed", err, true)
	}, e.name, "stdio")
}

func (e *HTTPExtension) refreshTools(ctx context.Context) error {
	if e.client == nil {
		return nil
	}
	return refreshExtensionTools(ctx, &e.refreshMu, e.agent, e.name, "http", &e.tools, &e.toolNames, e.client.AgentTools)
}

func (e *HTTPExtension) scheduleRefresh() {
	if e.client == nil {
		return
	}
	scheduleRefresh(e.client.bgCtx, &e.refreshScheduleMu, &e.refreshInFlight, &e.refreshPending, func(ctx context.Context) error {
		return e.refreshTools(ctx)
	}, func(event RefreshEvent) {
		e.emitRefreshEvent(event)
	}, func(err error) {
		if e.client.cfg.ErrorHandler != nil {
			e.client.cfg.ErrorHandler(e.client.bgCtx, err)
		}
	}, e.name, "http")
}

func scheduleRefresh(
	ctx context.Context,
	mu *sync.Mutex,
	inFlight *bool,
	pending *bool,
	run func(context.Context) error,
	emit func(RefreshEvent),
	report func(error),
	extensionName string,
	transport string,
) {
	if ctx != nil && ctx.Err() != nil {
		emitRefreshEvent(emit, extensionName, transport, RefreshPhaseSkipped, "closed", nil, false, false)
		return
	}
	mu.Lock()
	if *inFlight {
		*pending = true
		mu.Unlock()
		emitRefreshEvent(emit, extensionName, transport, RefreshPhaseCoalesced, "in_flight", nil, true, true)
		return
	}
	*inFlight = true
	mu.Unlock()

	go func() {
		for {
			if ctx != nil && ctx.Err() != nil {
				emitRefreshEvent(emit, extensionName, transport, RefreshPhaseSkipped, "closed", nil, false, false)
				mu.Lock()
				*inFlight = false
				*pending = false
				mu.Unlock()
				return
			}
			emitRefreshEvent(emit, extensionName, transport, RefreshPhaseStarted, "", nil, false, false)
			if err := run(ctx); err != nil {
				if ctx != nil && ctx.Err() != nil || errors.Is(err, errClientClosed) {
					emitRefreshEvent(emit, extensionName, transport, RefreshPhaseSkipped, "closed", err, false, false)
				} else {
					emitRefreshEvent(emit, extensionName, transport, RefreshPhaseFailed, "", err, false, false)
					if report != nil {
						report(err)
					}
				}
			} else {
				emitRefreshEvent(emit, extensionName, transport, RefreshPhaseSucceeded, "", nil, false, false)
			}
			mu.Lock()
			if !*pending {
				*inFlight = false
				mu.Unlock()
				return
			}
			*pending = false
			mu.Unlock()
		}
	}()
}

func emitRefreshEvent(emit func(RefreshEvent), extensionName, transport, phase, reason string, err error, coalesced bool, inFlight bool) {
	if emit == nil {
		return
	}
	emit(RefreshEvent{
		At:        time.Now(),
		Extension: extensionName,
		Transport: transport,
		Phase:     phase,
		Reason:    reason,
		Error:     util.ErrorString(err),
		Coalesced: coalesced,
		InFlight:  inFlight,
	})
}

func (e *StdioExtension) emitRefreshEvent(event RefreshEvent) {
	if e.agent != nil {
		e.agent.EmitEvent(event)
	}
}

func (e *HTTPExtension) emitRefreshEvent(event RefreshEvent) {
	if e.agent != nil {
		e.agent.EmitEvent(event)
	}
}

func toolNames(tools []*agentcore.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.Name == "" {
			continue
		}
		names = append(names, tool.Name)
	}
	return names
}

func decodeObjectArgs(args json.RawMessage, out any, errPrefix string) error {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	if err := json.Unmarshal(args, out); err != nil {
		return fmt.Errorf("%s: %w", errPrefix, err)
	}
	return nil
}
