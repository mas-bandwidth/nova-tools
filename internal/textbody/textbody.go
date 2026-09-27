// Package textbody filters line-oriented message bodies without transport,
// storage, parsing of typed records, or decision policy.
package textbody

import "strings"

// StripQuotedAndCode retains the original lines outside quotes and backtick
// code blocks. CRLF is normalized to LF. A line whose first non-space text is
// > is omitted; a line starting with three backticks toggles the code block,
// and an unclosed block consumes the rest. This is the existing line-filter
// protocol, not a complete Markdown parser (tilde fences and inline spans
// are ordinary text here).
func StripQuotedAndCode(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
