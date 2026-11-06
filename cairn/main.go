// Command nova-cairn is the record an AI keeps for its next session: the words
// a session chose to keep (open, append, index, receipt), a search and a check
// over those words and the notes already written (memory), and a reader that
// flags self-talk, sentences where the writer passes a standing verdict on
// themselves.
package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const intro = `nova-cairn: the record an AI keeps for its next session: kept words, keyword search,
and a self-talk check. Plain files; nothing is ever sent anywhere. Search is by
keywords only: a paraphrase that shares almost no words with the entry is not found.
`

const longHelp = "Long help for one verb: nova-cairn help <verb>. The full guide: cairn/SPEC.md.\n"

const exits = "Exit 0 done, 1 a finding or a conflict, 2 could not run. Every refusal ends with what to run next.\n"

// groups is the help: each verb's synopsis, under the notes for its group.
var groups = []struct {
	title, notes string
	verbs        [][2]string // verb path, synopsis after "nova-cairn "
}{
	{"Keep a session's words (a store is a directory you name):", `
      Every write is synced to disk before OK. An entry is never overwritten: the same id
      with the same words is duplicate=true (exit 0), with other words a conflict (exit 1).
      The words, new ids and source must be valid UTF-8, so what is accepted is kept
      byte for byte. receipt takes --store, --session and --entry (no stamp) and reads that entry
      back as stored. --publish records a policy for later: nothing is sent, and append and
      receipt say published=false. --max <n> caps each kind index lists (default 20, 0
      lists all); a capped kind ends with one MORE line for that kind.`, [][2]string{
		{"open", "open    --store <dir> --session <id> --publish <never|manual|deferred|immediate> [--source <ptr>]"},
		{"append", "append  --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] [--publish <policy>]"},
		{"index", "index   --store <dir> [--session <id>] [--max <n>]"},
		{"receipt", "receipt --store <dir> --session <id> --entry <id> [--text]"},
	}},
	{"Search and check the memory (each --root: .md notes, and the entries of any store in it):", `
      Every memory verb takes --exclude <glob>, repeatable, and writes nothing; the index is
      built in memory each run. search prints the k best files for the words, one hit each:
      its best paragraph. A hit from a store entry names its store, session, entry and
      stamp, for receipt. check does the same for each paragraph of a draft, and never
      judges. verify (notes only) always needs --links gate or info; --links info with no
      --coverage and no --frontmatter is refused, since it could not fail. It exits 1 on a
      note no index names (--coverage), a link to a missing file, a note with no
      frontmatter name: (--frontmatter), or with --links gate a [[wikilink]] to nothing.
      A capped kind of finding ends with one MORE line for that kind (--fail-max).
      Its globs match paths relative to --root (notes/*.md, not ~/memory/notes/*.md).`, [][2]string{
		{"memory search", "memory search --root <dir>... --channels bm25 --k <n> <words>..."},
		{"memory check", "memory check  --root <dir>... --channels bm25 --k <n> <file|->"},
		{"memory stats", "memory stats  --root <dir>..."},
		{"memory verify", "memory verify --root <dir> --links <gate|info> [--coverage <A:B>]... [--frontmatter <glob>]...\n                           [--exempt <prefix>]... [--fail-max <n>]"},
	}},
	{"Check self-talk:", `
      Flags a first-person claim with a word of failure (STANDING), or a standing verdict in
      neutral words (RANKING, FORECLOSURE, VERDICT-IDIOM, TRAIT). A dated claim is a record,
      never flagged. Exit 1 on findings. A green clears the known shapes, never the file.`, [][2]string{
		{"self-talk", "self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] <file|->..."},
	}},
	{"Other:", `
      help <verb> or <verb> -h prints one verb's help.`, [][2]string{
		{"help", "help [<verb>]"},
		{"version", "version"},
	}},
}

const verbList = "open, append, index, receipt, memory (search, check, stats, verify), self-talk, help, version"

// help prints the banner, or the help of the verbs whose path starts with path.
func help(w io.Writer, path string) bool {
	if path == "" { // the banner: one line per verb; the notes live in help <verb>
		fmt.Fprint(w, intro)
		for _, g := range groups {
			for _, v := range g.verbs {
				fmt.Fprintf(w, "  nova-cairn %s\n", strings.Join(strings.Fields(v[1]), " "))
			}
		}
		fmt.Fprint(w, longHelp, "\n", exits)
		return true
	}
	var b strings.Builder
	for _, g := range groups {
		var lines []string
		for _, v := range g.verbs {
			if path == "" || v[0] == path || strings.HasPrefix(v[0], path+" ") {
				lines = append(lines, "  nova-cairn "+v[1])
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\n%s\n%s%s\n", g.title, strings.Join(lines, "\n"), g.notes)
		}
	}
	if b.Len() == 0 {
		return false
	}
	fmt.Fprint(w, intro, b.String(), "\n", exits)
	return true
}

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		help(stdout, "")
		return 0
	}
	rest := args[1:]
	switch args[0] {
	case "open", "append", "index", "receipt":
		return runStore(args[0], rest, stdin, stdout, stderr)
	case "self-talk":
		return runSelfTalk(rest, stdin, stdout, stderr)
	case "memory":
		return runMemory(rest, stdin, stdout, stderr)
	case "version", "--version":
		fs := flags()
		if code := parse(fs, "version", rest, stdout, stderr); code >= 0 {
			return code
		}
		if fs.NArg() > 0 {
			return refused(stderr, "version", "version takes no argument", "")
		}
		fmt.Fprintf(stdout, "nova-cairn %s\n", version)
		return 0
	case "help":
		fs := flags()
		if code := parse(fs, "help", rest, stdout, stderr); code >= 0 {
			return code
		}
		path := strings.Join(fs.Args(), " ")
		if path == "help" {
			path = ""
		}
		if !help(stdout, path) {
			return refused(stderr, "help", "no verb "+path+"; the verbs are "+verbList, "nova-cairn help")
		}
		return 0
	}
	say(stderr, "nova-cairn", "unknown verb "+args[0]+"; the verbs are "+verbList, "nova-cairn help")
	return 2
}

// say prints "<head>: <why>; run: <remedy>", one line.
func say(w io.Writer, head, why, remedy string) {
	fmt.Fprintf(w, "%s: %s; run: %s\n", head, esc(why), esc(remedy))
}

// refused prints "nova-cairn <verb>: <why>; run: <remedy>" and returns exit 2.
// The remedy defaults to the verb's help.
func refused(w io.Writer, verb, why, remedy string) int {
	say(w, "nova-cairn "+verb, why, cmp.Or(remedy, "nova-cairn "+verb+" -h"))
	return 2
}

// more prints the one line that says a listing was cut short, and how to see all of it.
func more(w io.Writer, head, kind string, shown, total int, flag string) {
	fmt.Fprintf(w, "%s MORE kind=%s shown=%d total=%d %s <n> raises the ceiling, %s 0 prints all\n", head, kind, shown, total, flag, flag)
}

// raw is a value printed as it is, already quoted.
type raw string

// line prints "<HEAD> k=v ..."; a string value is made one token, an empty one is -.
func line(w io.Writer, head string, kv ...any) {
	fmt.Fprint(w, head)
	for i := 0; i < len(kv); i += 2 {
		v := kv[i+1]
		if s, ok := v.(string); ok {
			v = dash(s)
		}
		fmt.Fprintf(w, " %s=%v", kv[i], v)
	}
	fmt.Fprintln(w)
}

// dash is field(s), or - for an empty value.
func dash(s string) string { return field(cmp.Or(s, "-")) }

// esc renders s on one line: control characters, line and paragraph separators,
// and bytes that are not UTF-8 are written \xNN or \uNNNN.
func esc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x80 && unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case unicode.IsControl(r) || r == ' ' || r == ' ':
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += n
	}
	return b.String()
}

// field renders s as one k=v value: esc, and no blank or = either.
func field(s string) string {
	var b strings.Builder
	for _, r := range esc(s) {
		switch {
		case r != '=' && !unicode.IsSpace(r):
			b.WriteRune(r)
		case r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// flags is a flag set that prints nothing: parse turns its errors into refusals.
func flags() *flag.FlagSet {
	fs := flag.NewFlagSet("nova-cairn", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

var (
	undefined = regexp.MustCompile(`^flag provided but not defined: -+(.*)$`)
	noValue   = regexp.MustCompile(`^flag needs an argument: -+(.*)$`)
	badValue  = regexp.MustCompile(`^invalid (?:boolean )?value (".*") for flag -+([^:]*):`)
)

// parse parses args for the verb: flags, then positionals (fs.Args). It returns
// -1 to go on, or the exit code to return: 0 after printing the verb's help for
// -h or --help, 2 after a refusal.
func parse(fs *flag.FlagSet, verb string, args []string, stdout, stderr io.Writer) int {
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		help(stdout, verb)
		return 0
	}
	if err == nil {
		// Flags come before positionals: a flag after one is refused, except after --.
		rest := fs.Args()
		literal := len(rest) < len(args) && args[len(args)-len(rest)-1] == "--"
		for _, a := range rest {
			if literal || a == "--" {
				break
			}
			if len(a) > 1 && a[0] == '-' {
				return refused(stderr, verb, "late flag "+a+": flags come before the arguments (a literal starting with - goes after --)", "")
			}
		}
		return -1
	}
	why := err.Error()
	if m := undefined.FindStringSubmatch(why); m != nil {
		why = "unknown flag --" + m[1] + " for " + verb
	} else if m := noValue.FindStringSubmatch(why); m != nil {
		why = "--" + m[1] + " needs a value"
	} else if m := badValue.FindStringSubmatch(why); m != nil {
		why = "--" + m[2] + " does not take " + m[1]
		if f := fs.Lookup(m[2]); f != nil {
			why += ": it wants " + f.Usage
		}
	}
	return refused(stderr, verb, why, "")
}

// input reads a named file, or stdin for -.
func input(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(name)
}
