// Demo for phase-3 TUI capabilities: ChatApp + Image + Kitty keyboard
// protocol negotiation. Drive it without an agent by simulating agent
// events from slash commands typed into the editor.
//
// Commands:
//
//	/img <path>   — embed a local image (auto protocol).
//	/kitty on|off — force Kitty keyboard protocol flag.
//	/tool         — simulate a tool call lifecycle.
//	/stream       — simulate a streamed assistant answer with markdown.
//	/clear        — clear the transcript.
//	/help         — show these commands.
//	/quit         — exit.
package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui"
)

func main() {
	cfg := chat.ChatAppConfig{
		Title:             "ChatApp demo (phase 3)",
		EditorMinRows:     1,
		EditorMaxRows:     6,
		EditorPrompt:      "> ",
		ShowTimings:       true,
		ShowTurns:         true,
		KittyKeyboardMode: "auto",
		AltScreen:         true,
		MouseMode:         "auto",
		Providers: []core.AutocompleteProvider{
			&component.StaticProvider{
				TriggerStr: "/",
				Suggestions: []core.Suggestion{
					{Label: "img", InsertText: "img ", Description: "embed an image"},
					{Label: "kitty on", InsertText: "kitty on", Description: "force enable kitty kbd"},
					{Label: "kitty off", InsertText: "kitty off", Description: "force disable kitty kbd"},
					{Label: "tool", InsertText: "tool", Description: "simulate a tool call"},
					{Label: "stream", InsertText: "stream", Description: "simulate streaming answer"},
					{Label: "clear", InsertText: "clear", Description: "clear transcript"},
					{Label: "help", InsertText: "help", Description: "show help"},
					{Label: "quit", InsertText: "quit", Description: "exit"},
				},
			},
			&component.FilePathProvider{TriggerStr: "@", RootDir: "."},
		},
	}

	var app *chat.ChatApp
	cfg.OnSubmit = func(ctx context.Context, input string) {
		go handleInput(app, input)
	}
	cfg.OnQuit = func() {}

	app = tui.NewChatApp(cfg)
	if err := app.Start(); err != nil {
		fmt.Println("start:", err)
		return
	}
	defer app.Stop()

	app.PrintSystem("welcome — type /help to see available commands.")
	app.PrintSystem(fmt.Sprintf(
		"image protocol: %s, kitty kbd supported: %v",
		imageProtocolName(component.DetectImageProtocol()),
		terminal.TerminalSupportsKittyKeyboard(),
	))

	<-app.Done()
}

func handleInput(app *chat.ChatApp, input string) {
	input = strings.TrimSpace(input)
	switch {
	case input == "/help":
		app.PrintSystem("commands: /img <path>, /tool, /stream, /clear, /help, /quit")
	case input == "/quit":
		app.Stop()
	case input == "/clear":
		app.History().Clear()
	case input == "/tool":
		simulateTool(app)
	case input == "/stream":
		simulateStream(app)
	case strings.HasPrefix(input, "/img"):
		path := strings.TrimSpace(strings.TrimPrefix(input, "/img"))
		if path == "" {
			app.History().Append(chat.ChatMessage{
				Role: chat.RoleAssistant,
				Text: "inline synthetic test image:",
			})
			img := syntheticImage()
			imComp := component.NewImage(img)
			imComp.SetMaxSize(40, 0)
			app.History().Append(chat.ChatMessage{Role: chat.RoleAssistant, Text: renderImageAsMarkdown(imComp, 40)})
			return
		}
		imComp, err := component.NewImageFromFile(path)
		if err != nil {
			app.PrintError(err)
			return
		}
		imComp.SetMaxSize(60, 0)
		app.History().Append(chat.ChatMessage{Role: chat.RoleAssistant, Text: renderImageAsMarkdown(imComp, 60)})
	case input == "":
		// no-op
	default:
		simulateAnswer(app, input)
	}
}

// simulateStream fakes a streaming assistant answer with rich markdown.
func simulateStream(app *chat.ChatApp) {
	body := "Sure — here's a **quick tour** of the rendering stack:\n\n" +
		"1. `component.Markdown` handles inline styling.\n" +
		"2. `chat.ChatHistory` wraps lines by viewport.\n" +
		"3. Streaming deltas append to the current bubble.\n\n" +
		"```go\nfunc main() {\n    fmt.Println(\"hello\")\n}\n```\n"

	app.Busy("streaming...")
	defer app.Idle()
	var id string
	for _, chunk := range splitChunks(body, 12) {
		id = app.History().AppendDelta(id, chunk)
		time.Sleep(40 * time.Millisecond)
	}
	app.History().Finalize(id)
}

func simulateAnswer(app *chat.ChatApp, input string) {
	app.Busy("thinking...")
	defer app.Idle()
	time.Sleep(300 * time.Millisecond)
	app.History().Append(chat.ChatMessage{
		Role: chat.RoleAssistant,
		Text: fmt.Sprintf("You said: *%s*", input),
	})
}

func simulateTool(app *chat.ChatApp) {
	h := app.History()
	h.Append(chat.ChatMessage{ID: "tool-demo", Role: chat.RoleTool, Meta: "fake_tool", Text: "running..."})
	time.Sleep(400 * time.Millisecond)
	h.PatchMessage("tool-demo", func(m *chat.ChatMessage) {
		m.Text = "✓ done"
		m.Duration = 400 * time.Millisecond
	})
}

func splitChunks(s string, size int) []string {
	var out []string
	for len(s) > 0 {
		n := size
		if n > len(s) {
			n = len(s)
		}
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

func renderImageAsMarkdown(c *component.Image, width int64) string {
	rows := c.Render(width)
	return strings.Join(rows, "\n")
}

func syntheticImage() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			r := uint8(x * 8)
			g := uint8(y * 16)
			b := uint8((x + y) * 4)
			img.Set(x, y, color.NRGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return img
}

func imageProtocolName(p component.ImageProtocol) string {
	switch p {
	case component.ImageProtocolKitty:
		return "kitty"
	case component.ImageProtocolITerm2:
		return "iterm2"
	case component.ImageProtocolHalfBlock:
		return "halfblock"
	case component.ImageProtocolASCII:
		return "ascii"
	default:
		return "auto"
	}
}
