package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// writeFuseJSON renders the command's standard receipt envelope.
func writeFuseJSON(w io.Writer, out *tool.Out) int { return out.Render(w, true) }

func fuseRefusal(verb, reason, remedy string) *tool.Out {
	return fuseRefusals(verb, []string{reason}, remedy)
}

func fuseRefusals(verb string, reasons []string, remedy string) *tool.Out {
	out := tool.Refuse(reasons...)
	out.Verb = verb
	out.Remedy = remedy
	out.Reasons = make([]string, len(reasons))
	for i := range reasons {
		out.Reasons[i] = "refused"
	}
	return out
}

func parsedRefusalReasons(verb, message string) []string {
	prefix := "nova-fuse " + verb + " REFUSED: "
	var reasons []string
	for _, line := range strings.Split(strings.TrimSpace(message), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		line = strings.TrimPrefix(line, prefix)
		line = strings.TrimSuffix(line, "; run: nova-fuse help")
		if line != "" {
			reasons = append(reasons, line)
		}
	}
	return reasons
}

// parseFailure keeps the legacy refusal untouched without --json and turns its
// exact message into a structured refusal when the caller selected JSON.
func parseFailure(verb string, asJSON bool, message string, stdout, stderr io.Writer) int {
	if !asJSON {
		_, _ = fmt.Fprint(stderr, message)
		return 2
	}
	reasons := parsedRefusalReasons(verb, message)
	if len(reasons) == 0 {
		reasons = []string{fmt.Sprintf("%s could not parse this invocation", oneline.Field(verb))}
	}
	return writeFuseJSON(stdout, fuseRefusals(verb, reasons, "nova-fuse help"))
}

func fuseState(blown bool) string {
	if blown {
		return "blown"
	}
	return "clear"
}

func fuseBoxReasonCode(err error) string {
	if errors.Is(err, fuse.ErrNoBox) {
		return "box_missing"
	}
	return "box_unreadable"
}
