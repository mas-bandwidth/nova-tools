// Package verbflag is the one seam every living tool's verbs parse their flags
// through (#3254; the verb-help rule, the CLI style's rule (b), #4505). A parse
// error comes back from Parse unchanged, so the verb prints its own one-line
// refusal. -h, -help or --help on a flag set that does not define them is
// HELP, not a mistake: it unwinds to the dispatcher's deferred Recover, which
// prints that verb's help on stdout (its lines from the tool's own help text,
// then every flag the set defines) and exits 0. Nothing but flag parsing has
// run by then: no file read, no store dial, no write.
package verbflag

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Help is the panic value -h raises from Parse. Only the dispatcher's
// Recover catches it; nothing ran before it but flag parsing.
type Help struct{ FS *flag.FlagSet }

// sink swallows the flag package's own output and remembers whether a parse
// error wrote its message: failf writes the message before calling Usage, the
// help path calls Usage with nothing written.
type sink struct{ wrote bool }

func (s *sink) Write(p []byte) (int, error) {
	s.wrote = true
	return len(p), nil
}

// New returns a ContinueOnError flag set named for the verb and subverb
// ("task push"): quiet on a parse error, Help on -h.
func New(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	s := &sink{}
	fs.SetOutput(s)
	fs.Usage = func() {
		if s.wrote {
			s.wrote = false
			return
		}
		panic(Help{FS: fs})
	}
	return fs
}

// JSON registers the standard --json flag on fs with the family's one usage text,
// returning a pointer to the parsed bool.
func JSON(fs *flag.FlagSet) *bool {
	return fs.Bool("json", false, "print one JSON object for a program instead of the lines")
}

// Parse is fs.Parse with the help rule applied, for a flag set the verb built
// itself. -h, -help or --help raises Help; any other result is exactly what
// fs.Parse returns, and a parse error writes exactly what it wrote before
// (the flag package's message, then the set's own Usage or its defaults, to
// the set's own output). On help nothing is written anywhere but by Recover.
func Parse(fs *flag.FlagSet, args []string) error {
	out, usage := fs.Output(), fs.Usage
	var held bytes.Buffer
	asked := false
	fs.SetOutput(&held)
	if usage != nil {
		fs.Usage = func() { asked = true }
	}
	err := fs.Parse(args)
	fs.SetOutput(out)
	fs.Usage = usage
	if errors.Is(err, flag.ErrHelp) {
		panic(Help{FS: fs})
	}
	if err != nil {
		_, _ = out.Write(held.Bytes())
		if asked {
			usage()
		}
	}
	return err
}

// Recover is deferred by the dispatcher. A Help panic becomes that verb's
// help on out (stdout) and *code 0; any other panic goes on unwinding. banner
// is the tool's own help text (what `<prog> help` prints), from which the
// verb's usage lines and examples are quoted; "" quotes nothing.
func Recover(out io.Writer, prog, banner string, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(Help)
	if !ok {
		panic(r)
	}
	Print(out, prog, banner, h.FS)
	*code = 0
}

// RecoverWith is Recover for a tool that adds lines of its own to a verb's
// help: extra is given the verb's name (as Verb gives it) and returns the
// lines to print above the flags, each line ending in a newline ("" for none).
// It is what a tool defers in place of Recover to show a worked example per
// verb. It is deferred directly, as Recover is.
func RecoverWith(out io.Writer, prog, banner string, code *int, extra func(verb string) string) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(Help)
	if !ok {
		panic(r)
	}
	var b strings.Builder
	Print(&b, prog, banner, h.FS)
	_, _ = io.WriteString(out, Insert(b.String(), extra(Verb(prog, h.FS))))
	*code = 0
}

// Insert puts lines into a printed verb help above its flags (above its
// exit codes when it lists none, at its end when it has neither).
func Insert(help, lines string) string {
	if lines == "" {
		return help
	}
	if i := strings.Index(help, "\nflags:\n"); i >= 0 {
		return help[:i+1] + lines + help[i+1:]
	}
	if i := strings.Index(help, "\nexit codes:"); i >= 0 {
		return help[:i+1] + lines + help[i+1:]
	}
	return help + lines
}

// IsHelp reports whether one argument asks for help.
func IsHelp(a string) bool { return a == "-h" || a == "-help" || a == "--help" || a == "--h" }

// Verb is the verb a flag set is named for, with the tool's name taken off
// the front: some sets are named "<tool> <verb>", most "<verb>".
func Verb(prog string, fs *flag.FlagSet) string {
	name := strings.TrimSpace(fs.Name())
	if name == prog {
		return ""
	}
	return strings.TrimPrefix(name, prog+" ")
}

// Print writes `usage: <prog> <verb> [flags]`, the verb's lines quoted from
// the tool's help text, one line per flag the set defines (sorted, with its
// value type; no defaults, since a default can come from the environment and
// a help line must never print a secret), and the tool's exit codes.
func Print(out io.Writer, prog, banner string, fs *flag.FlagSet) {
	var b strings.Builder
	verb := Verb(prog, fs)
	fmt.Fprintf(&b, "usage: %s [flags]\n", strings.TrimSpace(prog+" "+verb))
	if lines := Excerpt(banner, prog, verb); len(lines) > 0 {
		fmt.Fprintf(&b, "from `%s help`:\n", prog)
		for _, l := range lines {
			b.WriteString("  " + l + "\n")
		}
	}
	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	if len(names) > 0 {
		b.WriteString("flags:\n")
	}
	for _, n := range names {
		f := fs.Lookup(n)
		kind, text := flag.UnquoteUsage(f)
		line := "  --" + n
		if kind != "" {
			line += " <" + kind + ">"
		}
		if text != "" {
			line += "  " + text
		}
		b.WriteString(line + "\n")
	}
	for _, l := range exitCodes(banner, prog) {
		b.WriteString(l + "\n")
	}
	_, _ = io.WriteString(out, b.String())
}

// exitCodesLabel finds the label of a banner's exit-code paragraph, whatever its
// case: `exit codes:`, `Exit codes:`, `exit code:`.
//
// `exit:` is the same label in its short spelling, and only where it opens a
// line, since `on exit:` inside a sentence is not one.
var (
	exitCodesLabel = regexp.MustCompile(`(?i)\bexit codes?\s*:`)
	exitShortLabel = regexp.MustCompile(`(?i)^\s*exit\s*:`)
)

// exitCodes is the tool's own `exit codes` paragraph from its help text, since a
// tool's codes are its own (a sandbox refuses at 125, a fuse says BLOWN at 1).
// The label is matched without regard to case, and a label that opens a line
// wins over one inside a sentence (`Flags come before files. Exit codes: ...`),
// where the paragraph is quoted from the label on. With no help text the
// family's common line stands; with help text that states none, the line points
// there rather than guess.
func exitCodes(banner, prog string) []string {
	if banner == "" {
		return []string{"exit codes: 0 done, 1 refused, 2 usage"}
	}
	lines := strings.Split(banner, "\n")
	start, from := -1, 0
	for i, l := range lines {
		loc := exitCodesLabel.FindStringIndex(l)
		if loc == nil {
			loc = exitShortLabel.FindStringIndex(l)
			if loc != nil {
				loc[0] = len(l) - len(strings.TrimLeft(l, " \t"))
			}
		}
		if loc == nil {
			continue
		}
		if strings.TrimSpace(l[:loc[0]]) == "" {
			start, from = i, loc[0]
			break
		}
		if start < 0 {
			start, from = i, loc[0]
		}
	}
	if start < 0 {
		return []string{"exit codes: see `" + prog + " help`"}
	}
	out := []string{strings.TrimSpace(lines[start][from:])}
	for j := start + 1; j < len(lines) && j < start+excerptFollow && strings.TrimSpace(lines[j]) != ""; j++ {
		out = append(out, strings.TrimSpace(lines[j]))
	}
	return out
}

// excerptFollow caps the indented lines quoted after one matching line.
const excerptFollow = 8

// Excerpt returns the lines of banner that speak for `<prog> <verb>`: every
// line whose words begin with the tool and the verb's words (its usage
// synopses and its examples), each with the more-indented lines that continue
// it, left-trimmed, in order, each once. "" for the verb quotes nothing.
func Excerpt(banner, prog, verb string) []string {
	if verb == "" || banner == "" {
		return nil
	}
	want := append([]string{prog}, strings.Fields(verb)...)
	lines := strings.Split(banner, "\n")
	var got []string
	seen := map[string]bool{}
	add := func(l string) {
		l = strings.TrimSpace(l)
		if l != "" && !seen[l] {
			seen[l] = true
			got = append(got, l)
		}
	}
	for i := 0; i < len(lines); i++ {
		words := strings.Fields(lines[i])
		if len(words) < len(want) || strings.Join(words[:len(want)], " ") != strings.Join(want, " ") {
			continue
		}
		add(lines[i])
		indent := indentOf(lines[i])
		for j := i + 1; j < len(lines) && j <= i+excerptFollow; j++ {
			if strings.TrimSpace(lines[j]) == "" || indentOf(lines[j]) <= indent {
				break
			}
			add(lines[j])
			i = j
		}
	}
	return got
}

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }

// Asked reports whether args hold -h, -help or --help before any --: the
// question a process-boundary check (a PATH or home read before the
// dispatcher) asks so that it can stand aside for help.
func Asked(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if IsHelp(a) {
			return true
		}
	}
	return false
}

// HelpIfAsked is for the verbs that read their flags by hand, or take none:
// when args hold -h, -help or --help (before any --), it raises Help with a
// flag set of the named string flags, so the dispatcher prints the same
// usage text.
func HelpIfAsked(args []string, name string, flags ...string) {
	if !Asked(args) {
		return
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	for _, f := range flags {
		fs.String(f, "", "")
	}
	panic(Help{FS: fs})
}
