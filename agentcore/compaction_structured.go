package agentcore

import (
	"encoding/json"
	"fmt"
	"strings"
)

const structuredCompactionSystemPrompt = `You are a conversation summarizer. Create a structured JSON summary of the provided conversation that preserves all important context needed to continue.

Output a single JSON object ONLY (no markdown fences, no commentary) with these string fields:

active_task — Copy the user's most recent request or question verbatim.
goal — What the user is trying to accomplish overall.
constraints_preferences — User preferences, coding style, constraints, important decisions.
completed_actions — Numbered list with tool name, target, and outcome. Format: N. ACTION target — outcome.
active_state — Working directory, branch, modified files, test status, running processes.
in_progress — Work currently underway.
blocked — Any blockers, errors, or issues not yet resolved. Include exact error messages.
key_decisions — Important technical decisions and WHY they were made.
resolved_questions — Questions that were ALREADY answered — include the answer.
pending_user_asks — Questions or requests NOT yet answered or fulfilled.
relevant_files — Files read, modified, or created — with brief note on each.
remaining_work — What remains — framed as context, not instructions.
critical_context — Specific values, error messages, configuration details. NEVER include secrets.

Each value must be concise but preserve facts, decisions, tool outcomes, errors, and open tasks.
Use empty string "" only when a field truly has nothing to add.
Be CONCRETE — include file paths, command outputs, error messages, line numbers, and specific values.`

func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		// strip optional ```json ... ``` wrapper
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimPrefix(s, "json")
		s = strings.TrimSpace(s)
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = strings.TrimSpace(s[:idx])
		}
	}
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return s
	}
	return s[i : j+1]
}

func parseStructuredCompactionSummary(raw string) (StructuredCompactionSummary, error) {
	frag := extractJSONObject(raw)
	var out StructuredCompactionSummary
	if err := json.Unmarshal([]byte(frag), &out); err != nil {
		return out, fmt.Errorf("parse structured compaction: %w", err)
	}
	return out, nil
}
