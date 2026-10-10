// clidoc rewrites the reference blocks between docs/CLI.md's clidoc markers
// from the built tools' own help, so the reference cannot drift from the
// binaries it documents.
//
// For every `<!-- clidoc:begin <tool> -->` marker in the file it runs that
// tool's `help` and each verb's `-h` from the --bin directory and rewrites only
// the block between the marker and its `<!-- clidoc:end <tool> -->`; the prose
// and the worked examples outside the markers are hand-written and stay as
// they are. The verb list is read from the help's own usage block, so a verb
// the tool adds joins the reference the next time clidoc runs.
//
// A pasted text keeps its typed lines whole — the usage synopsis, the flags
// with what each wants, the exit codes and the effect — and drops the free-text
// paragraphs and examples that the hand-written sections carry and test.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

func main() {
	bin := flag.String("bin", "", "directory holding the built tool binaries")
	file := flag.String("file", "docs/CLI.md", "the command reference to read")
	out := flag.String("out", "", "where to write the rewritten reference (default: the file it read)")
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

	tools := markerTools(doc)
	if len(tools) == 0 {
		refuse(fmt.Sprintf("%s carries no <!-- clidoc:begin <tool> --> marker; nothing to generate", *out))
	}
	for _, tool := range tools {
		ref, err := reference(*bin, tool)
		if err != nil {
			refuse(err.Error())
		}
		doc = replaceSection(doc, tool, ref)
	}
	if err := os.WriteFile(*out, []byte(doc), 0o644); err != nil {
		refuse(fmt.Sprintf("writing %s: %v", *out, err))
	}
	fmt.Printf("clidoc OK tools=%d file=%s\n", len(tools), *out)
}

// refuse prints one line naming what was wrong and what to do, and exits 2.
func refuse(why string) {
	fmt.Fprintf(os.Stderr, "clidoc REFUSED: %s; run: make clidoc\n", why)
	os.Exit(2)
}

// beginMarker matches one `<!-- clidoc:begin <tool> -->` marker and captures
// the tool.
var beginMarker = regexp.MustCompile(`<!-- clidoc:begin (nova-[a-z0-9-]+) -->`)

// markerTools returns the tools the file's begin markers name, in file order,
// each once.
func markerTools(doc string) []string {
	var tools []string
	seen := map[string]bool{}
	for _, m := range beginMarker.FindAllStringSubmatch(doc, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			tools = append(tools, m[1])
		}
	}
	return tools
}

// replaceSection rewrites the block between one tool's begin and end markers,
// leaving everything outside them as it stands.
func replaceSection(doc, tool, ref string) string {
	re := regexp.MustCompile(`(?s)(<!-- clidoc:begin ` + regexp.QuoteMeta(tool) + ` -->).*?(<!-- clidoc:end ` + regexp.QuoteMeta(tool) + ` -->)`)
	return re.ReplaceAllString(doc, "$1"+ref+"$2")
}

// reference runs tool's help and each verb's -h from binDir and returns the
// block between the tool's markers: one line and one fenced block per text.
func reference(binDir, tool string) (string, error) {
	banner, err := run(binDir, tool, []string{"help"})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n`%s help`:\n\n%s\n", tool, fence(bannerReference(banner)))
	for _, verb := range usageVerbs(banner, tool) {
		help, err := verbHelp(binDir, tool, verb)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\n`%s %s -h`:\n\n%s\n", tool, verb, fence(typedLines(help)))
	}
	return b.String(), nil
}

// bannerReference leaves worked examples to the hand-written documentation.
func bannerReference(banner string) string {
	if before, _, ok := strings.Cut(banner, "\nexample:\n"); ok {
		banner = before
	}
	if before, _, ok := strings.Cut(banner, "\nreading the inbox and answering a judgment:"); ok {
		banner = before
	}
	return strings.TrimRight(banner, "\n")
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
		// A wrapped explanation may begin with the tool name at a deeper
		// indentation. Only rows with the exact usage indent declare verbs.
		rest, ok := strings.CutPrefix(line, "  "+tool+" ")
		if !ok {
			continue
		}
		words := verbWords(firstColumn(rest))
		if len(words) == 0 || words[0] == "help" || strings.HasSuffix(words[0], ":") || seen[strings.Join(words, " ")] {
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
// nest (`lift quarantine`, `dogfood gate`), and the path ends at the first
// flag, placeholder or bracket group.
func verbWords(rest string) []string {
	var words []string
	for _, w := range strings.Fields(rest) {
		if strings.ContainsAny(w, "-<[|(") {
			break
		}
		words = append(words, w)
	}
	return words
}

// sectionWords are the line prefixes that open a typed section of a verb help.
var sectionWords = []string{"usage:", "from `", "flags:", "exit codes", "effect:", "Help:"}

// typedLines drops a verb help's free-text paragraph and keeps its typed
// sections: the usage synopsis, the flags with what each wants, the exit codes
// and the effect. A line opens a kept section when it starts with one of the
// section words; the prose between sections is the hand-written prose's job.
func typedLines(text string) string {
	var kept []string
	keep := true
	for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case i == 0 || hasSectionWord(trimmed):
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
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
		if run := strings.TrimLeft(line, "`"); len(line)-len(run) >= 3 {
			mark = "````"
		}
	}
	return mark + "\n" + strings.Join(lines, "\n") + "\n" + mark
}
