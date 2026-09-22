package swarm

import "strings"

// AskedQuestion returns the question the card ended on, or the empty string.
// The last assistant turn ends with "?", or matches "Would you like me" /
// "Should I" / "Do you want". A question inside a fenced code block or a
// quoted span is not the turn asking.
func AskedQuestion(tail string) string {
	if tail == "" {
		return ""
	}

	lines := strings.Split(tail, "\n")

	var lastTurn []string
	inFence := false
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}

		if isToolCallLine(trimmed) {
			lastTurn = nil
			continue
		}

		if inFence {
			continue
		}

		lastTurn = append(lastTurn, trimmed)
	}

	if len(lastTurn) == 0 {
		return ""
	}

	turn := strings.Join(lastTurn, "\n")
	if isQuestion(turn) {
		return turn
	}
	return ""
}

// isToolCallLine reports whether a line is a tool call that separates turns.
func isToolCallLine(line string) bool {
	return strings.HasPrefix(line, "$ ") ||
		strings.HasPrefix(line, "\u2192 ") ||
		strings.HasPrefix(line, "\u2190 ") ||
		strings.HasPrefix(line, "STEP ")
}

// isQuestion reports whether the last line of text is a question the model is
// asking the user. A question inside a quoted span (inline backticks) on the
// last line does not count.
func isQuestion(text string) bool {
	lastLine := lastNonEmptyLine(text)
	if lastLine == "" {
		return false
	}

	if strings.Contains(lastLine, "Would you like me") ||
		strings.Contains(lastLine, "Should I") ||
		strings.Contains(lastLine, "Do you want") {
		return true
	}

	if !strings.HasSuffix(lastLine, "?") {
		return false
	}

	if questionInQuotedSpan(lastLine) {
		return false
	}

	return true
}

// lastNonEmptyLine returns the last non-empty line from a multi-line string.
func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// questionInQuotedSpan reports whether the last "?" on a line is inside
// backtick quotes.
func questionInQuotedSpan(line string) bool {
	lastQ := strings.LastIndex(line, "?")
	if lastQ < 0 {
		return false
	}
	inTick := false
	for i, ch := range line {
		if ch == '`' {
			inTick = !inTick
		} else if ch == '?' && i == lastQ && inTick {
			return true
		}
	}
	return false
}
