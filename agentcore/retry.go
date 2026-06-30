package agentcore

import (
	"regexp"
	"time"
)

// RetryConfig controls LLM-level automatic retry behavior.
type RetryConfig struct {
	MaxRetries  int64 // max retry attempts; default 3
	BaseDelayMs int64 // initial delay in ms; default 1000
	MaxDelayMs  int64 // max delay cap in ms; default 30000
}

var retryablePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)429`),
	regexp.MustCompile(`(?i)rate.?limit`),
	regexp.MustCompile(`(?i)too.?many.?requests`),
	regexp.MustCompile(`(?i)5\d{2}`),
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

// retryDelay computes the wait duration for a given attempt using exponential backoff.
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
