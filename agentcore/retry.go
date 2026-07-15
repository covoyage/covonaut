package agentcore

import (
	"errors"
	"math/rand"
	"regexp"
	"strings"
	"time"
)

// ErrRepetitionLoop is returned (wrapped) when a provider middleware detects
// the model has degenerated into repeating the same text/tokens verbatim
// mid-stream. Unlike transport errors, this is never retried with the same
// request via callProviderWithRetry — the caller (runLoop) is expected to
// catch it with IsRepetitionLoopError and drive its own recovery ladder
// (inject a corrective steering message and try again, up to a limit) rather
// than blindly resending the identical request.
var ErrRepetitionLoop = errors.New("stream repetition loop detected")

// IsRepetitionLoopError returns true if err is (or wraps) ErrRepetitionLoop.
func IsRepetitionLoopError(err error) bool {
	return errors.Is(err, ErrRepetitionLoop)
}

// RepetitionKind identifies which detector triggered a repetition-recovery
// nudge, so a custom Prompt function (or the built-in default) can tailor
// the corrective message to what actually went wrong.
type RepetitionKind string

const (
	// RepetitionKindStream: mid-stream degeneration reported by a provider
	// middleware (e.g. covo-agent's stream_health n-gram/periodicity detector).
	RepetitionKindStream RepetitionKind = "stream"
	// RepetitionKindText: the model produced (near-)identical assistant text
	// across consecutive turns.
	RepetitionKindText RepetitionKind = "text"
	// RepetitionKindTool: the model made the same tool call (name+arguments)
	// across consecutive turns without progress.
	RepetitionKindTool RepetitionKind = "tool"
)

// RepetitionRecoveryConfig controls the soft repetition-loop recovery ladder
// used by runLoop: when a degeneration/repetition loop is detected (mid-
// stream via a provider middleware, or across turns via repeated content or
// repeated tool calls), a corrective steering message is injected and the
// model gets another chance — escalating in severity — before the turn is
// finally given up on with a terminal error.
type RepetitionRecoveryConfig struct {
	// MaxAttempts is how many corrective nudges to send (per detector) before
	// giving up and ending the turn with a terminal error. <= 0 uses the
	// built-in default (2), matching a "mild nudge, then a stronger replan
	// nudge, then give up" ladder.
	MaxAttempts int64

	// Prompt returns the corrective steering message for the given detector
	// and 0-based attempt number (0 = first/mildest nudge, increasing
	// severity thereafter). If nil, a built-in English default ladder is
	// used. Callers wanting localized text should supply this.
	Prompt func(kind RepetitionKind, attempt int64) string
}

var whitespaceCollapsePattern = regexp.MustCompile(`\s+`)
var repetitionLeadInPattern = regexp.MustCompile(`(?i)^(let me |i'll |i will |let's )`)

// normalizeForLoopDetection reduces assistant text to a comparable core for
// cross-turn repetition detection: case-folded, whitespace-collapsed, common
// narration lead-ins stripped (so "Let me check X" and "I'll check X"
// compare equal when the model is stuck repeating the same underlying
// action with slightly different wording), and capped in length (a long
// shared prefix is already strong signal; comparing full multi-paragraph
// text needlessly misses turns that only diverge near the end).
func normalizeForLoopDetection(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = whitespaceCollapsePattern.ReplaceAllString(s, " ")
	s = repetitionLeadInPattern.ReplaceAllString(s, "")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// defaultRepetitionPrompt is the built-in English recovery ladder used when
// Config.RepetitionRecovery (or its Prompt field) is not set. attempt == 0 is
// the mild first nudge; attempt >= 1 escalates to a stronger "replan"
// message.
func defaultRepetitionPrompt(kind RepetitionKind, attempt int64) string {
	strong := attempt > 0
	switch kind {
	case RepetitionKindTool:
		if strong {
			return "CRITICAL: you are STILL calling the same tools with the same arguments after a previous warning. You MUST abandon this approach entirely, explain to the user what you attempted and why it failed, and ask for guidance. Do not call any more tools."
		}
		return "You have been calling the same tools repeatedly without progress. Stop this loop immediately. Do not call any more tools. Report to the user what you attempted and why it failed, and ask for guidance."
	case RepetitionKindStream:
		if strong {
			return "CRITICAL: your output is still degenerating into repeated text after a previous warning. You MUST completely change your approach: state what you were trying to do, why it failed, and propose a different plan. Do not repeat the same wording again."
		}
		return "Your recent output contained repeated phrases. Stop repeating yourself: vary your wording and reasoning, try a different tool or approach, or explain what is blocking you instead of looping."
	default: // RepetitionKindText
		if strong {
			return "CRITICAL: you are STILL repeating the same response after a previous warning. You MUST abandon your current approach, explain what you were trying to do and why it failed, and ask the user for guidance."
		}
		return "You have been repeating the same response. Stop this loop immediately. Do not call any more tools. Give a final answer based on what you have so far, or clearly state that you cannot complete the request and ask the user for guidance."
	}
}

// RetryConfig controls LLM-level automatic retry behavior.
type RetryConfig struct {
	MaxRetries  int64 // max retry attempts; default 3
	BaseDelayMs int64 // initial delay in ms; default 1000
	MaxDelayMs  int64 // max delay cap in ms; default 30000
}

var retryablePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b429\b`),
	regexp.MustCompile(`(?i)rate.?limit`),
	regexp.MustCompile(`(?i)too.?many.?requests`),
	regexp.MustCompile(`(?i)\b5\d{2}\b`),
	regexp.MustCompile(`(?i)server.?error`),
	regexp.MustCompile(`(?i)internal.?server`),
	regexp.MustCompile(`(?i)service.?unavailable`),
	regexp.MustCompile(`(?i)gateway.?timeout`),
	regexp.MustCompile(`(?i)bad.?gateway`),
	regexp.MustCompile(`(?i)timeout`),
	regexp.MustCompile(`(?i)timed?.?out`),
	regexp.MustCompile(`(?i)connection`),
	regexp.MustCompile(`(?i)ECONNRESET`),
	regexp.MustCompile(`(?i)ECONNREFUSED`),
	regexp.MustCompile(`(?i)overloaded`),
	regexp.MustCompile(`(?i)CannotParse|extra data after|invalid.*JSON|unmarshal.*error`),
}

var contextOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)context.?(length|window|limit)`),
	regexp.MustCompile(`(?i)token.?(limit|exceeded|maximum)`),
	regexp.MustCompile(`(?i)maximum.?context`),
	regexp.MustCompile(`(?i)too.?long`),
	regexp.MustCompile(`(?i)content.?too.?large`),
	regexp.MustCompile(`(?i)max.?tokens`),
	regexp.MustCompile(`(?i)exceeds.*model.*limit`),
}

// IsRetryableError returns true if the error is transient and worth retrying.
// Context overflow errors are explicitly excluded (they should trigger compaction instead).
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if IsContextOverflowError(err) {
		return false
	}
	msg := err.Error()
	for _, pat := range retryablePatterns {
		if pat.MatchString(msg) {
			return true
		}
	}
	return false
}

// IsContextOverflowError returns true if the error indicates the context
// window has been exceeded. These errors should trigger compaction, not retry.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, pat := range contextOverflowPatterns {
		if pat.MatchString(msg) {
			return true
		}
	}
	return false
}

// retryDelay computes the base wait duration for a given attempt using
// exponential backoff (no jitter). Callers performing an actual sleep should
// pass the result through applyFullJitter to avoid a thundering-herd effect
// when many clients retry in lockstep (e.g. after a shared rate limit or
// outage clears).
func retryDelay(attempt int64, cfg *RetryConfig) time.Duration {
	base := cfg.BaseDelayMs
	if base <= 0 {
		base = 1000
	}
	maxD := cfg.MaxDelayMs
	if maxD <= 0 {
		maxD = 30000
	}
	delay := base
	for i := int64(0); i < attempt-1; i++ {
		delay *= 2
		if delay > maxD {
			delay = maxD
			break
		}
	}
	return time.Duration(delay) * time.Millisecond
}

// applyFullJitter returns a random duration in [0, delay], implementing the
// "full jitter" strategy for exponential backoff: spreading retries out
// randomly instead of having every caller wake up and retry at the exact
// same instant. A non-positive delay is returned unchanged.
func applyFullJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	return time.Duration(rand.Int63n(int64(delay) + 1))
}
