# Terminal UI

## Layered Architecture

| Layer | Responsibility |
| --- | --- |
| `tui/core` | Components, messages, commands, layout foundations |
| `tui/terminal` | Terminal I/O, key decoding, capabilities, raw mode |
| `tui/theme` | Styles, semantic palettes, theme configuration |
| `tui/component` | Editors, Markdown, inputs, tables, menus, images |
| `tui/chat` | Transcript, composer, chat events, application host |
| `tui/agentadapter` | Translate agent events into chat events |
| `tui/stdio` | Procedural renderers, progress, spinners, line input |

Chat depends on its own event contracts, not directly on the agent runtime.
This lets the same interface display simulated events, remote execution, or a
local agent through an adapter.

## A Chat App Without a Model

This complete program needs an interactive terminal but no provider credentials.
It echoes input and closes on `/quit`.

```go
package main

import (
    "context"
    "log"

    "github.com/covoyage/covonaut/tui"
    "github.com/covoyage/covonaut/tui/chat"
)

func main() {
    var app *chat.ChatApp
    cfg := chat.ChatAppConfig{
        Title: "Local chat",
        Context: context.Background(),
        EditorMinRows: 1,
        EditorMaxRows: 5,
        AltScreen: true,
    }
    cfg.OnSubmit = func(ctx context.Context, input string) {
        if input == "/quit" {
            app.Stop()
            return
        }
        app.PrintSystem("Received: " + input)
    }
    app = tui.NewChatApp(cfg)
    if err := app.Start(); err != nil {
        log.Fatal(err)
    }
    defer app.Stop()
    <-app.Done()
}
```

## Connect an Agent

Use `agentadapter.BindAgent(subscriber, agent)` with a chat subscriber to
translate lifecycle, text/thinking, resets, tools, handoffs, compaction, and retry
events. Bind once for the intended lifetime; repeated binding adds handlers and
the adapter does not expose a per-binding unsubscribe handle.

The host still owns `OnSubmit`, run cancellation, provider configuration,
persistence, and `Agent.Close`. Keep model calls off the UI loop and use the
ChatApp lifecycle context for pending work. A displayed Stop control should
cancel the actual run context rather than merely hiding its output.

## Components and Rendering

A `core.Component` renders lines within the supplied visible-column width and
supports `Invalidate`. `Updatable` receives messages and returns commands for
asynchronous effects. `Focusable` represents focus and hardware cursor placement.

Use terminal display width, not byte count, for CJK characters, emoji, and ANSI
sequences. Invalidate cached layout on resize/theme changes. Focused components
can mark the hardware cursor so IME candidate positioning remains useful.

The engine uses differential rendering. The `stdio` family instead targets
procedural stdout/stdin workflows; avoid running both against the same terminal
without an explicit ownership strategy.

## Transcript and Capabilities

Default chat history virtualizes the in-app viewport. Scrollback mode writes
completed content to native terminal scrollback and keeps pending content live.
Choose based on transcript size and interaction requirements.

Kitty keyboard, mouse reporting, bracketed paste, image protocols, and terminal
probing depend on the actual terminal and multiplexer. Provide a fallback rather
than assuming every user has those capabilities. Thinking output is controlled
by a reasoning renderer and is hidden by default when none is configured.

Run the [existing demos](../examples#terminal-ui) to inspect components and
capabilities without building a complete agent application first.