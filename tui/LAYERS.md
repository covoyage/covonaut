# TUI Module Layers

Dependency direction: high-number layers may depend on low-number layers, never the reverse.

| Layer | Package | Description | Depends on |
|-------|---------|-------------|------------|
| 0 Foundation | `tui/core` | Basic types, interfaces, rune utilities, fuzzy match, SpinnerStyle | None (only stdlib) |
| 1 Terminal I/O | `tui/terminal` | Terminal abstraction, key parsing, input buffer | Layer 0 |
| 2 Theming | `tui/theme` | Palette, semantic theme, JSON loading, file watch, Style | Layer 0, 1 |
| 3 Engine | `tui` (root) | TUI container, event loop, overlay system, ChatApp bridge | Layer 0–2, chat |
| 4 Components | `tui/component` | UI components (Editor, Markdown, Loader, etc.) + fuzzy provider | Layer 0–2, fuzzy |
| 5 Application | `tui/chat` | Chat application layer (ChatApp, ChatHistory) | Layer 0–2, 4 |
| 6 Stdio | `tui/stdio` | Procedural stdout/stdin tools (Spinner, Renderer, ProgressBar, LineReader) | Layer 0, 2 |
| 7 Adapter | `tui/agentadapter` | Agentcore → chat event conversion, BindAgent convenience | Layer 5, agentcore |

## Rules

- Higher layers may import lower layers; lower layers MUST NOT import higher layers.
- `tui/stdio` depends on Layer 0 and Layer 2 only; it MUST NOT depend on Layer 3–5.
- `tui/chat` depends on Layer 0–2 and 4 only; it does NOT depend on `tui/stdio` (stdio tools are for procedural stdout/stdin apps, not for the TUI chat app).
- `tui/chat` does NOT import `agentcore`. All agentcore integration is in `tui/agentadapter`.
- `tui/chat` uses `AppHost` interface instead of directly referencing `*TUI`, breaking the cycle.
- `tui/chat` uses `Subscriber` / `EventSubscriber` interfaces for event subscription, decoupled from agentcore.
- The root `tui` package does NOT re-export sub-package types. Consumers import sub-packages directly.
- The root `tui` package provides `NewChatApp` as a convenience constructor that creates both a `TUI` engine and a `chat.ChatApp`.

## Directory Structure

```
tui/
├── core/                  # Layer 0 — Foundation
│   ├── component.go       # Component/Updatable/Focusable interfaces, Container, CURSOR_MARKER
│   ├── message.go         # Msg/Cmd types, Batch/Sequence/Quit, MsgBase
│   ├── width.go           # East-Asian width, truncation, padding, wrapping
│   ├── runeutil.go        # Shared rune utilities (CellWidthOfRunes, SliceRunesByCells, etc.)
│   ├── fuzzy_match.go     # Fuzzy matching utilities
│   └── spinner_style.go   # SpinnerStyle type + preset vars (SpinnerDots, SpinnerLine, etc.)
│
├── terminal/              # Layer 1 — Terminal I/O
│   ├── keys.go            # Key parsing, MatchesKey, Kitty protocol, KeyID
│   ├── keybindings.go     # KeybindingsManager, DefaultKeybindings, KeybindingDef
│   ├── stdin_buffer.go    # StdinBuffer for reassembling fragmented input
│   ├── terminal.go        # Terminal interface, ProcessTerminal, VirtualTerminal
│   ├── terminal_darwin.go # macOS termios
│   ├── terminal_linux.go  # Linux termios
│   └── terminal_other.go  # Fallback for other OSes
│
├── theme/                 # Layer 2 — Theming
│   ├── style.go           # ANSI Style, Color, Attr, symbols, box-drawing, cursor helpers
│   ├── color_resolve.go   # Color mode detection, RGB-to-256
│   ├── semantic_theme.go  # SemanticTheme struct + defaults
│   ├── palette.go         # Palette struct, CurrentPalette(), BuildPalette, SyncPaletteGlobals
│   ├── global.go          # SetSemanticTheme, InitThemeFromEnv, SetOnSemanticThemeChange
│   ├── json.go            # JSON theme parsing
│   └── watch.go           # File-watch hot-reload for themes
│
├── component/             # Layer 4 — Components
│   ├── autocomplete.go    # Autocomplete dropdown, StaticProvider, FilePathProvider
│   ├── box.go             # Box (border/padding container)
│   ├── editor.go          # Multi-line text editor (Emacs-style)
│   ├── fuzzy_provider.go  # FuzzyContentProvider, NormalizeForMatch, SubstringFuzzyFilter
│   ├── image.go           # Kitty/Sixel/iTerm2 image display
│   ├── input.go           # Single-line input editor
│   ├── keyhelp.go         # Keybindings cheat sheet
│   ├── loader.go          # Animated spinner component (callback-based, uses core.SpinnerStyle)
│   ├── markdown.go        # Markdown rendering
│   ├── selectlist.go      # Selectable list with fuzzy filter
│   ├── settings.go        # Settings panel
│   ├── statusbar.go       # StatusBar
│   ├── syntax.go          # Syntax highlighting
│   └── text.go            # Text, TruncatedText
│
├── chat/                  # Layer 5 — Application
│   ├── chat_app.go        # ChatApp + chatLayout (AppHost interface, no *TUI dep)
│   ├── chat_history.go    # Scrollable chat transcript
│   └── events.go          # ChatEvent types, Subscriber/EventSubscriber interfaces
│
├── agentadapter/          # Layer 7 — Agentcore Adapter
│   └── adapter.go         # BindAgent, AgentRunner, event conversion (agentcore → chat)
│
├── stdio/                 # Layer 6 — Procedural stdio tools
│   ├── renderer.go        # Streaming markdown stdout renderer + ToolStatus/HandoffStatus helpers
│   ├── spinner.go         # Procedural spinner (stdout-based), uses core.SpinnerStyle
│   ├── progress.go        # ProgressBar, TokenUsageDisplay, Timer
│   └── linereader.go      # Blocking stdin helper, Confirm, PromptSelect
│
├── tui.go                 # Layer 3 — TUI container, event loop, diff renderer
├── overlay.go             # Layer 3 — Overlay positioning and composition
├── chat_bridge.go         # Layer 3 — NewChatApp convenience constructor + tuiAppHost adapter
└── LAYERS.md              # This file
```

## Key Design Decisions

### No Re-exports

The root `tui` package does NOT re-export sub-package types. Since the library
has not been published yet, there is no backward-compatibility constraint.
All consumers import the specific sub-package they need:

```go
import (
    core "github.com/covoyage/covonaut/tui/core"
    "github.com/covoyage/covonaut/tui/terminal"
    "github.com/covoyage/covonaut/tui/theme"
    "github.com/covoyage/covonaut/tui/component"
    "github.com/covoyage/covonaut/tui/chat"
    "github.com/covoyage/covonaut/tui/agentadapter"
    "github.com/covoyage/covonaut/tui"
)
```

The root `tui` package exports only the `TUI` engine, `Overlay`, and the
`NewChatApp` convenience constructor.

### stdio vs component: Two Rendering Models

The TUI module has two parallel rendering models:

1. **TUI Engine** (Layer 3–5): Elm-architecture, differential rendering, `Component` interface.
   Components render into string arrays, the engine diffs and writes only changes.
   Used by `ChatApp`.

2. **stdio** (Layer 6): Procedural stdout/stdin, `\r` overwriting, `fmt.Fprint`.
   No component model, no differential rendering. Used by standalone scripts/examples
   that want plain stdout output without the TUI engine.

Both share `core.SpinnerStyle` (animation frame data) and `theme` (styling), but
operate on fundamentally different I/O models. The name `stdio` makes this
distinct — these tools work on raw stdin/stdout, not through the TUI engine.

### SpinnerStyle in Core

`SpinnerStyle` is a pure data type (animation frames + interval) with no
rendering dependency. It lives in `core` because both `component.Loader`
(TUI component) and `stdio.Spinner` (procedural spinner) need it. Putting it
in either consumer would force the other to import upwards.

### FuzzyContentProvider in Component

`FuzzyContentProvider` implements `core.AutocompleteProvider` and is a
component-level concept (an autocomplete data source). It was previously in
`util/fuzzy_bridge.go`, but since `util` has been reclassified as `stdio`
(procedural I/O tools), the provider belongs in `component` alongside
`StaticProvider` and `FilePathProvider`.

### Circular Dependency Break: AppHost Interface

`ChatApp` (in `tui/chat`) originally held a `*TUI` pointer, creating a cycle:
`chat` → `tui` (root) → `chat` (via re-exports).

Solution: `chat.AppHost` interface abstracts the operations ChatApp needs
(`Start`, `Stop`, `AddChild`, `Focus`, `RequestRender`, `PushOverlay`,
`RemoveOverlay`, `TerminalSize`). The root `tui` package provides a
`tuiAppHost` adapter that wraps `*TUI`, living in `chat_bridge.go`.

### Circular Dependency Break: Loader Callback

`Loader` (in `tui/component`) originally held a `*TUI` pointer to call
`RequestRender()`. Replaced with a `func()` callback injected at construction
time: `NewLoader(onRequestRender func(), message string)`.

### Decoupling: agentadapter Package

`tui/chat` does NOT import `agentcore`. Instead, `chat` defines its own event
types (`ChatEvent`, `ChatEventType`) and subscription interfaces (`Subscriber`,
`EventSubscriber`). The `tui/agentadapter` package provides `BindAgent()` which
converts `agentcore.Agent` events into `chat.ChatEvent` values and registers
them via the `Subscriber` interface. This keeps `chat` reusable without
agentcore and allows other event sources to integrate via the same interface.

### Internal Types Unexported

Types that are implementation details of `chat` are unexported:
`chatModel`, `chatLayout`, `chatAppendMsg`, `chatUpdateMsg`, `chatDeltaMsg`,
`chatFinalizeMsg`, `chatClearMsg`, `chatScrollMsg`. Only the public API types
(`ChatApp`, `ChatAppConfig`, `ChatHistory`, `ChatMessage`,
event types, interfaces) are exported.

### Msg Interface: MsgMarker() Method

`core.Msg` uses an exported `MsgMarker()` method instead of unexported `msg()`,
allowing external packages (e.g. `chat`) to implement the interface and use
type switches across package boundaries. External types can also embed
`core.MsgBase` for zero-effort compliance.

### Suggestion & AutocompleteProvider in Core

These interface/struct types live in `core` because multiple packages implement
`AutocompleteProvider` and return `Suggestion` values (`component.StaticProvider`,
`component.FuzzyContentProvider`, etc.). Placing them in `core` avoids upward
dependencies that would violate the layer ordering.
