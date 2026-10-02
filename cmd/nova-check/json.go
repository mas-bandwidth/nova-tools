package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/check"
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
	// JSON gets the original typed check values; duplicate legacy hint text is omitted.
	return len(p), nil
}
func (w *jsonOutput) refuse(where, why string) int {
	if w.refusal == nil {
		w.refusal = &tool.Out{Verb: strings.TrimSpace(where), Status: tool.Refused, Exit: 2, Remedy: "nova-check help"}
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

func renderFailures(w io.Writer, verb string, failures []check.Failure, max int, facts ...any) int {
	out := &tool.Out{Verb: verb, Status: tool.OK}
	for i := 0; i+1 < len(facts); i += 2 {
		out.Fact(facts[i].(string), facts[i+1])
	}
	out.Fact("findings", len(failures))
	appendFailures(out, "finding", failures, max)
	if len(failures) > 0 {
		out.Status = tool.Failed
		out.Exit = 1
	}
	return renderResult(w, out)
}

func renderLinks(w io.Writer, dir string, res check.LinksResult, max int) int {
	out := &tool.Out{Verb: "links", Status: tool.OK}
	out.Fact("dir", dir).Fact("files", res.MDFiles).Fact("links", res.Checked).Fact("excluded", res.Excluded).Fact("broken", len(res.Broken))
	shown := len(res.Broken)
	if max > 0 && shown > max {
		shown = max
	}
	for _, b := range res.Broken[:shown] {
		out.Item("broken", "file", b.File, "line", b.Line, "target", b.Target, "reason", b.Reason)
	}
	if shown < len(res.Broken) {
		out.More = []tool.More{{Kind: "broken", Shown: shown, Total: len(res.Broken), Remedy: failMaxRemedy}}
	}
	if len(res.Broken) > 0 {
		out.Status = tool.Failed
		out.Exit = 1
	}
	return renderResult(w, out)
}

func renderNoCodeList(w io.Writer, source string, deny []string, names map[string]bool, prefixes []string) int {
	out := &tool.Out{Verb: "nocode", Status: tool.OK}
	out.Fact("source", source).Fact("extensions", deny).Fact("names", sortedNames(names)).Fact("paths", prefixes)
	return renderResult(w, out)
}

func appendFailures(out *tool.Out, kind string, failures []check.Failure, max int) {
	shown := len(failures)
	if max > 0 && shown > max {
		shown = max
	}
	for _, f := range failures[:shown] {
		out.Item(kind, "subject", f.Subject, "reason", f.Reason)
	}
	if shown < len(failures) {
		out.More = append(out.More, tool.More{Kind: kind, Shown: shown, Total: len(failures), Remedy: failMaxRemedy})
	}
}

func renderCorpus(w io.Writer, ledger string, anchors, floor int, failures, malformed []check.Failure, max int) int {
	out := &tool.Out{Verb: "corpus", Status: tool.OK}
	out.Fact("ledger", ledger).Fact("anchors", anchors).Fact("floor", floor).Fact("failed", len(failures)).Fact("malformed", len(malformed))
	appendFailures(out, "anchor", failures, max)
	appendFailures(out, "malformed-row", malformed, max)
	if len(failures)+len(malformed) > 0 {
		out.Status = tool.Failed
		out.Exit = 1
	}
	return renderResult(w, out)
}

func renderSpelling(w io.Writer, dir string, res check.SpellingResult, write bool, max int) int {
	out := &tool.Out{Verb: "spelling", Status: tool.OK}
	out.Fact("dir", dir).Fact("files", res.FilesScanned).Fact("misspellings", len(res.Findings)).Fact("written", res.Corrected).Fact("excluded", res.Excluded)
	shown := len(res.Findings)
	if !write && max > 0 && shown > max {
		shown = max
	}
	for _, f := range res.Findings[:shown] {
		out.Item("misspelling", "file", f.File, "line", f.Line, "column", f.Column, "original", f.Original, "replacement", f.Replacement)
	}
	if shown < len(res.Findings) {
		out.More = []tool.More{{Kind: "misspelling", Shown: shown, Total: len(res.Findings), Remedy: failMaxRemedy}}
	}
	if !write && len(res.Findings) > 0 {
		out.Status = tool.Failed
		out.Exit = 1
	}
	return renderResult(w, out)
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

func renderVersion(w io.Writer, ver string) int {
	out := &tool.Out{Verb: "version", Status: tool.OK, Payload: buildinfo.Line("nova-check", ver)}
	return renderResult(w, out)
}
