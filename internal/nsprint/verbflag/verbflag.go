// Package verbflag is the one seam every living tool's verbs parse their flags
// through, so help and the CLI standard hold the same way for every verb. A parse
// error comes back from Parse unchanged, so the verb prints its own one-line
// refusal, worded by Explain. -h, -help or --help on a flag set that does not
// define them is HELP, not a mistake: it unwinds to the dispatcher's deferred Recover, which
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
	"slices"
	"sort"
	"strconv"
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
		// ignored: the flag package's own message to the set's output; the parse error is the one returned
		_, _ = out.Write(held.Bytes())
		if asked {
			usage()
		}
	}
	return err
}

// The flag package's words for the three parse errors a caller can fix:
// `flag provided but not defined: -x`, `flag needs an argument: -x`, and
// `invalid value "v" for flag -x: why` (`invalid boolean value` for a bool).
var (
	undefinedRe = regexp.MustCompile(`^flag provided but not defined: -+(.+)$`)
	needsRe     = regexp.MustCompile(`^flag needs an argument: -+(.+)$`)
	badValueRe  = regexp.MustCompile(`^invalid (?:boolean )?value ".*" for (?:flag )?-+([^\s:]+): (.*)$`)
)

// Explain words a parse error from fs (Parse's, or fs.Parse's) for an AI that
// has only the help and acts on one reading: an unknown flag is named with the
// flags of the verb and the nearest of them, a flag missing its value and a
// value its flag cannot take with what that flag wants. Explain never repeats
// the value given, since it may be a secret (a flag.Value of the tool's own may
// name it in its reason, which follows the flag). Any other error is its own
// words. A tool prints it as `<VERB> REFUSED: <Explain>; run: <tool> <verb> -h`.
// Parse returns the flag package's error unchanged, because tools still match
// its words; Explain is the one wording of it.
func Explain(fs *flag.FlagSet, err error) string {
	msg := err.Error()
	if m := undefinedRe.FindStringSubmatch(msg); m != nil {
		var names []string
		fs.VisitAll(func(f *flag.Flag) { names = append(names, "--"+f.Name) })
		if len(names) == 0 {
			return "unknown flag --" + m[1] + "; " + fs.Name() + " takes no flags"
		}
		near := Nearest("--"+m[1], names)
		if near != "" {
			near = "; did you mean " + near + "?"
		}
		return "unknown flag --" + m[1] + "; the flags of " + fs.Name() + " are " + List(names) + near
	}
	if m := needsRe.FindStringSubmatch(msg); m != nil {
		if f := fs.Lookup(m[1]); f != nil {
			return "--" + f.Name + " needs a value: it wants " + wants(f)
		}
	}
	if m := badValueRe.FindStringSubmatch(msg); m != nil {
		if f := fs.Lookup(m[1]); f != nil {
			why := ""
			if m[2] != "parse error" {
				why = " (" + m[2] + ")"
			}
			return "invalid value for --" + f.Name + why + ": it wants " + wants(f)
		}
	}
	return msg
}

// BoolGiven reports whether args set the boolean flag name of fs, read as the
// flag package reads them, so it holds when the parse stopped at an error (a
// refusal asked for as --json is rendered as JSON): a flag fs defines with a
// value takes the next word, whatever it looks like (`--store --json` gives
// --store the value --json); `--name=v` is v; the terminator `--` and the
// first argument end the flags. A flag fs does not define is passed over, and
// a word after it that is not a flag ends the reading, since that word is its
// value or the first argument and nothing after it is surely a flag.
func BoolGiven(fs *flag.FlagSet, args []string, name string) bool {
	given := false
	isFlag := func(a string) bool { return len(a) > 1 && a[0] == '-' }
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !isFlag(a) {
			return given
		}
		n, v, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		f := fs.Lookup(n)
		switch {
		case n == name:
			b, err := strconv.ParseBool(v)
			given = !inline || (err == nil && b)
		case f == nil:
			if !inline && i+1 < len(args) && !isFlag(args[i+1]) {
				return given
			}
		case !inline && !isBoolFlag(f):
			i++ // its value
		}
	}
	return given
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// kindWants is what a value of each of the flag package's kinds is, in words.
var kindWants = map[string]string{
	"int": "a whole number", "uint": "a whole number of zero or more", "float": "a number",
	"duration": "a duration such as 30s or 5m",
}

// wants is what flag f wants: its kind in words, then its description.
func wants(f *flag.Flag) string {
	kind, text := flag.UnquoteUsage(f)
	if isBoolFlag(f) {
		kind = "true or false"
	} else if w := kindWants[kind]; w != "" {
		kind = w
	} else {
		kind = ""
	}
	switch {
	case kind == "" && text == "":
		return "a value"
	case kind == "":
		return text
	case text == "":
		return kind
	}
	return kind + " (" + text + ")"
}

// ListMax bounds a list of names in a refusal to one readable line; help lists the rest.
const ListMax = 16

// List is names joined, at most ListMax of them, with how many more there are.
func List(names []string) string {
	if len(names) <= ListMax {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:ListMax], ", "), len(names)-ListMax)
}

// Nearest is the one name nearest to got within a third of its length (dashes
// aside, rounded up) in edits, else "": the name an unknown one was meant as.
func Nearest(got string, names []string) string {
	best, bestD := "", (len(strings.TrimLeft(got, "-"))+2)/3+1
	for _, n := range names {
		if d := distance(got, n); d < bestD {
			best, bestD = n, d
		}
	}
	return best
}

// distance is the Levenshtein edit distance between a and b, by bytes.
func distance(a, b string) int {
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(a); i++ {
		diag := row[0]
		row[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			diag, row[j] = row[j], min(row[j]+1, row[j-1]+1, diag+cost)
		}
	}
	return row[len(b)]
}

// RecoverWith is deferred directly by the dispatcher. A Help panic becomes
// that verb's help on out (stdout) and *code 0 (1 when out refuses the write);
// any other panic goes on unwinding. banner is the tool's own help text (what
// `<prog> help` prints), from which the verb's usage lines and examples are
// quoted; "" quotes nothing. extra is given the verb's name (as Verb gives it)
// and returns lines of the tool's own to print above the flags, each ending in
// a newline ("" for none): a worked example per verb. A line of extra that
// opens `exit codes:` and the lines after it are that verb's own exit codes:
// they stand where the tool's paragraph would.
func RecoverWith(out io.Writer, prog, banner string, code *int, extra func(verb string) string) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(Help)
	if !ok {
		panic(r)
	}
	lines, exit := extra(Verb(prog, h.FS)), []string(nil)
	if i := strings.Index("\n"+lines, "\nexit codes:"); i >= 0 {
		lines, exit = lines[:i], strings.Split(strings.TrimRight(lines[i:], "\n"), "\n")
	}
	var b strings.Builder
	Print(&b, prog, banner, h.FS, exit...)
	*code = 0
	if _, err := io.WriteString(out, Insert(b.String(), lines)); err != nil {
		// the help did not reach its reader (a closed stdout): the exit code says so
		*code = 1
	}
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

// Print writes the usage line (UsageLine: a group's names its verbs), the
// verb's lines quoted from the tool's help text, one line per flag the set
// defines (sorted, with its value type and what it wants; no defaults, since a
// default can come from the environment and a help line must never print a
// secret), and the exit codes: exit, the verb's own lines, when given, else
// the tool's paragraph from its help text.
func Print(out io.Writer, prog, banner string, fs *flag.FlagSet, exit ...string) {
	var b strings.Builder
	verb := Verb(prog, fs)
	var subs []string
	if !hasFlags(fs) {
		subs = Subverbs(banner, prog, verb)
	}
	b.WriteString(UsageLine(prog, verb, subs) + "\n")
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
	if len(exit) == 0 {
		exit = exitCodes(banner, prog)
	}
	for _, l := range exit {
		b.WriteString(l + "\n")
	}
	// ignored: help written to stdout; a closed stdout has no reader to tell
	_, _ = io.WriteString(out, b.String())
}

func hasFlags(fs *flag.FlagSet) bool {
	has := false
	fs.VisitAll(func(*flag.Flag) { has = true })
	return has
}

// UsageLine is `usage: <prog> <verb> [flags]`, and for a group, a verb whose
// words only lead to its verbs (subs), `usage: <prog> <group> <a|b> [flags]`.
// The bracketed word is the placeholder for a caller that passes no synopsis
// (UsageLineSynopsis). A tool whose help already prints that placeholder keeps it.
func UsageLine(prog, verb string, subs []string) string {
	return UsageLineSynopsis(prog, verb, subs, "")
}

// UsageLineSynopsis is UsageLine with the verb's own synopsis in place of the
// placeholder [flags]: positionals and the flags the verb takes, as its usage
// line names them. An empty synopsis keeps the placeholder. A group (subs)
// keeps `<a|b> [flags]`; its verbs are the synopsis.
func UsageLineSynopsis(prog, verb string, subs []string, synopsis string) string {
	name := strings.TrimSpace(prog + " " + verb)
	if len(subs) > 0 {
		name += " <" + strings.Join(subs, "|") + ">"
	} else if s := strings.TrimSpace(synopsis); s != "" {
		return "usage: " + name + " " + s
	}
	return "usage: " + name + " [flags]"
}

// FlagSynopsis names each flag of fs for a usage line, `[--name]` or
// `[--name <kind>]`, sorted by name. Empty when fs is nil or defines none.
// It never prints the placeholder [flags], and it never prints a default.
func FlagSynopsis(fs *flag.FlagSet) string {
	if fs == nil {
		return ""
	}
	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		kind, _ := flag.UnquoteUsage(fs.Lookup(n))
		if kind == "" {
			parts = append(parts, "[--"+n+"]")
			continue
		}
		parts = append(parts, "[--"+n+" <"+kind+">]")
	}
	return strings.Join(parts, " ")
}

// subverbRe is one word that names a verb, or several joined by | (list|show).
var subverbRe = regexp.MustCompile(`^[a-z][a-z0-9-]*(\|[a-z][a-z0-9-]*)*$`)

// Subverbs is the verbs of a group as the help text's usage block (the lines
// above its `example:` line) names them: when every line there that begins
// with the tool and the verb's words (a word of `fleet|sprint` is either)
// goes on with a verb word, the distinct words, in order; else nil, the verb
// is no group, or one whose verbs a line with a placeholder in its place
// (`<kind> list`) leaves unnamed. A line's command ends at a run of two
// spaces or a tab, where a pasted help puts its description.
func Subverbs(banner, prog, verb string) []string {
	if verb == "" {
		return nil
	}
	want := append([]string{prog}, strings.Fields(verb)...)
	n := len(want)
	var subs []string
	for _, l := range strings.Split(banner, "\n") {
		l = strings.TrimSpace(l)
		if strings.EqualFold(l, "example:") {
			break
		}
		if i := strings.Index(l, "  "); i >= 0 {
			l = l[:i]
		}
		if i := strings.IndexByte(l, '\t'); i >= 0 {
			l = l[:i]
		}
		words, stands := strings.Fields(l), false
		if len(words) < n || !slices.EqualFunc(words[:n], want, func(w, v string) bool {
			stands = stands || strings.HasPrefix(w, "<")
			return strings.HasPrefix(w, "<") || slices.Contains(strings.Split(w, "|"), v)
		}) {
			continue
		}
		if stands { // `<kind> list` stands for this verb too: its verbs are not all named here
			if len(words) > n && (subverbRe.MatchString(words[n]) || strings.HasPrefix(words[n], "<")) {
				return nil
			}
			continue // a sentence about every verb (`<verb> REFUSED: ...`)
		}
		if len(words) == n || !subverbRe.MatchString(words[n]) {
			return nil
		}
		for _, s := range strings.Split(words[n], "|") {
			if !slices.Contains(subs, s) {
				subs = append(subs, s)
			}
		}
	}
	return subs
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

// Flag is one flag a verb reads by hand, for its help: its name, what it
// wants (its unit, its role, an example value; a `word` in backquotes names
// the value as the flag package's usage does), and whether it takes no value.
type Flag struct {
	Name, Wants string
	Bool        bool
}

// HelpIfAsked is for the verbs that read their flags by hand, or take none:
// when args hold -h, -help or --help (before any --), it raises Help with a
// flag set of the flags given, each with what it wants, so the dispatcher
// prints the same usage text as for a verb that parses through Parse.
func HelpIfAsked(args []string, name string, flags ...Flag) {
	if !Asked(args) {
		return
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	for _, f := range flags {
		if f.Bool {
			fs.Bool(f.Name, false, f.Wants)
		} else {
			fs.String(f.Name, "", f.Wants)
		}
	}
	panic(Help{FS: fs})
}

// BoolAsked reports whether args set the boolean flag name (-name, --name,
// or =true) before any --: the question a dispatcher asks of a flag it must
// honour before a flag set has parsed, such as --json on a refusal.
func BoolAsked(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "-" + name, "--" + name, "-" + name + "=true", "--" + name + "=true":
			return true
		}
	}
	return false
}
