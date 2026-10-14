// clidoc generates docs/CLI.md whole from the built tools' own help, so the
// reference cannot drift from the binaries it documents.
//
// For every tool section `## <tool>` in the file it runs that tool's `help` and
// each verb's `-h` from the --bin directory and writes the generated block
// between the tool's `<!-- clidoc:begin <tool> -->` and
// `<!-- clidoc:end <tool> -->` markers. The only hand-written lines the file
// keeps are each tool's `### First run` prose — the one or two commands a
// stranger runs first — which clidoc reads back from the file and re-emits
// verbatim; everything else is regenerated, so a line someone writes by hand
// outside a `### First run` section is dropped the next time clidoc runs.
// The verb list is read from the help's own usage block, so a verb the tool
// adds joins the reference the next time clidoc runs.
//
// A pasted text keeps its typed lines whole — the usage synopsis, the flags
// with what each wants, the exit codes and the effect — and drops one thing
// the hand-written prose around the block already carries: a verb help's
// free-text paragraph. The banner is pasted whole, its `example:` block
// included; every example line of these banners runs through the comparator
// in the tool's own tests.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

func main() {
	bin := flag.String("bin", "", "directory holding the built tool binaries")
	file := flag.String("file", "docs/CLI.md", "the command reference to read")
	out := flag.String("out", "", "where to write the generated reference (default: the file it read)")
	flag.Parse()

	if *bin == "" {
		refuse("--bin is the directory the built tools were built into; it is never guessed")
	}
	if *out == "" {
		*out = *file
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		refuse(fmt.Sprintf("reading %s: %v", *file, err))
	}
	doc := string(raw)

	tools := sectionTools(doc)
	if len(tools) == 0 {
		refuse(fmt.Sprintf("%s carries no `## nova-<tool>` section; nothing to generate", *out))
	}
	firstRun := firstRunSections(doc)

	var b strings.Builder
	b.WriteString(preamble)
	for _, tool := range tools {
		if notGenerated[tool] {
			fmt.Fprintf(&b, "\n## %s\n\n### First run\n\n%s\n", tool, firstRun[tool])
			continue
		}
		ref, err := reference(*bin, tool)
		if err != nil {
			refuse(err.Error())
		}
		fmt.Fprintf(&b, "\n## %s\n\n<!-- clidoc:begin %s -->%s<!-- clidoc:end %s -->\n\n### First run\n\n%s\n",
			tool, tool, ref, tool, firstRun[tool])
	}
	if err := os.WriteFile(*out, []byte(b.String()), 0o644); err != nil {
		refuse(fmt.Sprintf("writing %s: %v", *out, err))
	}
	fmt.Printf("clidoc OK tools=%d file=%s\n", len(tools), *out)
}

// refuse prints one line naming what was wrong and what to do, and exits 2.
func refuse(why string) {
	fmt.Fprintf(os.Stderr, "clidoc REFUSED: %s; run: make clidoc\n", why)
	os.Exit(2)
}

// preamble is the fixed head clidoc writes before the first tool section: the
// title, the back link and the one paragraph that says the file is generated
// whole and only the `### First run` prose is hand-written. It carries no `## `
// or `### ` heading, so it stands cleanly before the first tool section.
const preamble = "# Command reference\n" +
	"\n" +
	"[Back to Nova Tools](../README.md)\n" +
	"\n" +
	"This file is generated whole: `make clidoc` runs each tool's `help` and every verb's `-h` from the built binaries and rewrites the file. The only hand-written lines are the `### First run` sections below; every other line is the binaries' own help. `-h` or `--help` after any verb prints that verb's help on stdout at exit 0 and runs nothing, so `<tool> <verb> -h` is always a safe first question. nova-fuse alone refuses `-h` after a verb, because its exit 0 means CLEAR. The first-run transcripts also live in [TESTS.md](TESTS.md), where the tests execute them line by line.\n"

// beginMarker matches one `<!-- clidoc:begin <tool> -->` marker and captures
// the tool.
var beginMarker = regexp.MustCompile(`<!-- clidoc:begin (nova-[a-z0-9-]+) -->`)

// endMarker matches one `<!-- clidoc:end <tool> -->` marker and captures the
// tool.
var endMarker = regexp.MustCompile(`<!-- clidoc:end (nova-[a-z0-9-]+) -->`)

// notGenerated names the tools whose reference stays hand-written in their
// `### First run` prose rather than generated from the binaries. Their help
// banners carry an `example:` block whose lines are not yet executed by a
// comparator test, and pasting them into the reference would trip the example
// machinery in internal/ci (TestPlatformsMatchCILegsAndUnexecutedExamplesOnlyShrink);
// each joins the generated set the day a comparator test covers its examples.
var notGenerated = map[string]bool{
	"nova-decide":  true,
	"nova-update":  true,
	"nova-version": true,
	// nova-sprint, nova-card and nova-work left this tree in v1.2.3 (the split)
	// for the nova-sprint repository; there is no cmd/ binary here to generate
	// their reference from, so their section is the First run prose alone.
	"nova-sprint": true,
	"nova-card":   true,
	"nova-work":   true,
}

// sectionTools returns the tools the file's `## nova-<tool>` headings name, in
// file order, each once: exactly the tools clidoc generates whole.
func sectionTools(doc string) []string {
	var tools []string
	seen := map[string]bool{}
	for _, line := range strings.Split(doc, "\n") {
		rest, ok := strings.CutPrefix(line, "## ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "nova-") || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		tools = append(tools, fields[0])
	}
	return tools
}

// firstRunSections returns each tool's `### First run` prose, keyed by tool,
// trimmed of the edges: the hand-written lines clidoc preserves when it
// regenerates the file whole. A tool with no `### First run` heading maps to
// empty.
func firstRunSections(doc string) map[string]string {
	out := map[string]string{}
	var cur string
	var buf []string
	inFirstRun := false
	flush := func() {
		if cur != "" {
			out[cur] = strings.TrimSpace(strings.Join(buf, "\n"))
		}
		buf = nil
		inFirstRun = false
	}
	for _, line := range strings.Split(doc, "\n") {
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			cur = strings.Fields(rest)[0]
			continue
		}
		if strings.TrimSpace(line) == "### First run" {
			inFirstRun = true
			continue
		}
		if inFirstRun {
			buf = append(buf, line)
		}
	}
	flush()
	return out
}

// reference runs tool's help and each verb's -h from binDir and returns the
// block between the tool's markers: one line and one fenced block per text.
func reference(binDir, tool string) (string, error) {
	banner, err := run(binDir, tool, []string{"help"})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n`%s help`:\n\n%s\n", tool, fence(strings.TrimRight(banner, "\n")))
	for _, verb := range usageVerbs(banner, tool) {
		help, err := verbHelp(binDir, tool, verb)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\n`%s %s -h`:\n\n%s\n", tool, verb, fence(typedLines(tool, help)))
	}
	return b.String(), nil
}

// verbHelp runs one verb's -h, or `help <verb>` where the tool refuses -h after
// a verb (nova-fuse: its exit 0 means CLEAR), and returns the help text.
func verbHelp(binDir, tool, verb string) (string, error) {
	words := strings.Fields(verb)
	out, err := run(binDir, tool, append(words, "-h"))
	if err == nil {
		return out, nil
	}
	return run(binDir, tool, append([]string{"help"}, words...))
}

// run executes one tool command from binDir and returns its whole output.
func run(binDir, tool string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), subproc.ToolBudget)
	defer cancel()
	cmd, stop := subproc.Command(ctx, subproc.Tool, filepath.Join(binDir, tool), args...)
	defer stop()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %v: %s", tool, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// usageVerbs reads the verb names from a help banner's usage block: every line
// it carries that begins with the tool's name names a verb path, the leading
// bare words after the tool's name, at most until a flag, a placeholder or a
// bracket group. `help` is left out: its -h is the banner, already pasted.
func usageVerbs(banner, tool string) []string {
	var verbs []string
	seen := map[string]bool{}
	inUsage := false
	for _, line := range strings.Split(banner, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "usage:" {
			inUsage = true
			continue
		}
		if !inUsage {
			continue
		}
		if trimmed != "" && !strings.HasPrefix(line, "  ") {
			inUsage = false
			continue
		}
		// A synopsis line is the tool's name at the two-column usage indent; a
		// continuation line indented deeper that happens to open with the tool's
		// name (`nova-swarm mirror as a nova-config loop row.`) is prose, not a
		// verb, and is skipped because its indent is deeper than a synopsis line.
		rest, ok := strings.CutPrefix(line, "  "+tool+" ")
		if !ok {
			continue
		}
		words := verbWords(firstColumn(rest))
		if len(words) == 0 || words[0] == "help" || seen[strings.Join(words, " ")] {
			continue
		}
		seen[strings.Join(words, " ")] = true
		verbs = append(verbs, strings.Join(words, " "))
	}
	return verbs
}

// firstColumn cuts a synopsis line at its description column: a run of two or
// more blanks separates the command from the text that follows it (`nova-fuse
// version    print this build identity`), while one gap never does.
func firstColumn(s string) string {
	if i := columnGap.FindStringIndex(s); i != nil {
		return s[:i[0]]
	}
	return s
}

// columnGap is the run of two or more blanks separating a synopsis line's
// command and its description column.
var columnGap = regexp.MustCompile(`\s{2,}`)

// verbWords returns the leading bare words of one usage line: a verb path may
// nest (`lift quarantine`, `dogfood gate`), and the path ends at the first word
// that is not a lowercase verb word — a flag (`--dir`), a placeholder
// (`<dir>`), a bracket group, or a note word the banner capitalizes
// (`NOTE:`, which nova-cairn's usage block carries and is no verb).
func verbWords(rest string) []string {
	var words []string
	for _, w := range strings.Fields(rest) {
		if !isVerbWord(w) {
			break
		}
		words = append(words, w)
	}
	return words
}

// isVerbWord reports whether a token is a lowercase verb word: lowercase
// letters, then lowercase letters, digits or hyphens. A flag, a placeholder, an
// uppercase word or a token carrying a colon or bracket is no verb.
func isVerbWord(w string) bool {
	if w == "" {
		return false
	}
	for i, r := range w {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// sectionWords are the line prefixes that open a typed section of a verb help.
var sectionWords = []string{"usage:", "from `", "flags:", "exit codes", "effect:", "Help:", "example: "}

// typedLines drops a verb help's free-text paragraph and keeps its typed
// sections: the usage synopsis, the flags with what each wants, the exit codes
// and the effect. A line opens a kept section when it starts with one of the
// section words or with the tool's own name (a group verb such as
// `nova-update release` lists its sub-verbs as bare synopsis lines with no
// `usage:` prefix, and those are typed lines too); the prose between sections
// is the hand-written prose's job.
func typedLines(tool, text string) string {
	var kept []string
	keep := true
	for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case i == 0 || hasSectionWord(trimmed) || strings.HasPrefix(trimmed, tool+" "):
			keep = true
		case trimmed == "":
		case strings.HasPrefix(line, " "):
		default:
			keep = false
		}
		if keep {
			kept = append(kept, line)
		}
	}
	return strings.TrimRight(collapseBlanks(kept), "\n")
}

// hasSectionWord reports whether a trimmed line opens a typed section.
func hasSectionWord(trimmed string) bool {
	for _, w := range sectionWords {
		if strings.HasPrefix(trimmed, w) {
			return true
		}
	}
	return false
}

// collapseBlanks trims the edges and runs of blank lines the dropped prose
// leaves behind, so a block never carries two blank lines in a row.
func collapseBlanks(lines []string) string {
	var out []string
	for _, line := range lines {
		if line == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// fence wraps one pasted text in a fenced block, widening the fence when the
// text itself opens a line with a fence run.
func fence(body string) string {
	mark := "```"
	for _, line := range strings.Split(body, "\n") {
		if run := strings.TrimLeft(line, "`"); len(line)-len(run) >= 3 {
			mark = "````"
		}
	}
	return mark + "\n" + body + "\n" + mark
}
