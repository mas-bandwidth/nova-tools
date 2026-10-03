package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// jsonOutput renders check values through the shared STANDARD section 2 envelope.
// Plain hints are suppressed; refusals pass their original reasons directly.
type jsonOutput struct {
	writer     io.Writer
	jsonWriter io.Writer
	enabled    *bool
	refusal    *tool.Out
}

func jsonWriters(stdout, stderr io.Writer, enabled *bool) (io.Writer, io.Writer) {
	return &jsonOutput{writer: stdout, jsonWriter: stdout, enabled: enabled}, &jsonOutput{writer: stderr, jsonWriter: stdout, enabled: enabled}
}
func (w *jsonOutput) Write(p []byte) (int, error) {
	if !*w.enabled {
		return fmt.Fprintf(w.writer, "%s", p)
	}
	// JSON gets the original typed check values; duplicate hint text is omitted.
	return len(p), nil
}
func (w *jsonOutput) refuse(where, why string) int {
	if w.refusal == nil {
		w.refusal = &tool.Out{Verb: strings.TrimSpace(where), Status: tool.Refused, Exit: 2, Remedy: "nova-dev help"}
	}
	w.refusal.Why = append(w.refusal.Why, why)
	return w.refusal.Exit
}

func (w *jsonOutput) finish() {
	if w.refusal != nil {
		w.refusal.Render(w.jsonWriter, true)
	}
}

func renderResult(w io.Writer, out *tool.Out) int {
	out.Render(w.(*jsonOutput).jsonWriter, true)
	return out.Exit
}

func renderHygiene(w io.Writer, findings []hygiene.Finding, max int, remedy, repo, base, head, paths string) int {
	out := &tool.Out{Verb: "hygiene", Status: tool.OK}
	out.Fact("repo", repo).Fact("base", base).Fact("head", head).Fact("paths", paths).Fact("findings", len(findings))
	shown := len(findings)
	if max > 0 && shown > max {
		shown = max
	}
	for _, f := range findings[:shown] {
		out.Item("finding", "reason", f.Token, "at", f.At, "why", f.Why)
	}
	if shown < len(findings) {
		out.More = []tool.More{{Kind: "finding", Shown: shown, Total: len(findings), Remedy: remedy}}
	}
	if len(findings) > 0 {
		out.Status = tool.Failed
		out.Exit = 1
	}
	return renderResult(w, out)
}
