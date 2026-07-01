package agentcore

import "errors"

// ErrInterrupt is returned by a tool to request the agent loop to pause.
// The agent saves its state and returns control so the caller can inspect
// the interrupt reason and call Resume() to continue.
//
// Usage in a tool:
//
//	func myTool(ctx context.Context, args json.RawMessage) (any, error) {
//	    return "User requested pause", NewInterruptError("user requested pause")
//	}
var ErrInterrupt = errors.New("interrupt")

// NewInterruptError wraps ErrInterrupt with a human-readable reason.
// The reason is persisted as a friendly tool result, not an error message.
func NewInterruptError(reason string) error {
	return &interruptError{reason: reason}
}

// NewInterruptErrorWithData is like NewInterruptError but also carries
// structured data that the caller can inspect via Interrupted().Data.
func NewInterruptErrorWithData(reason string, data map[string]any) error {
	return &interruptError{reason: reason, data: data}
}

type interruptError struct {
	reason string
	data   map[string]any
}

func (e *interruptError) Error() string  { return "interrupt: " + e.reason }
func (e *interruptError) Unwrap() error   { return ErrInterrupt }

// InterruptMessage extracts the human-readable reason from an interrupt error.
// Returns the error text if the error is not an interrupt.
func InterruptMessage(err error) string {
	var ie *interruptError
	if errors.As(err, &ie) {
		return ie.reason
	}
	return err.Error()
}

// InterruptData returns the structured data carried by an interrupt error.
func InterruptData(err error) map[string]any {
	var ie *interruptError
	if errors.As(err, &ie) {
		return ie.data
	}
	return nil
}

// IsInterrupt reports whether err indicates an agent interrupt.
func IsInterrupt(err error) bool {
	return errors.Is(err, ErrInterrupt)
}

// InterruptReason carries structured data about why an agent was interrupted.
type InterruptReason struct {
	ToolCallID string
	ToolName   string
	Reason     string
	Data       map[string]any
}
