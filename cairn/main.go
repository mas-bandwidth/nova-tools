// Command nova-cairn keeps a session's words as plain files (open, append,
// index, receipt), and carries two small readers of your own markdown:
// self-talk flags sentences where a writer passes a standing verdict on
// themselves, and memory searches your notes and checks a draft against them.
package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const usage = `nova-cairn: a session's words kept as plain files, and two readers of your own notes.

  nova-cairn open    --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate>
  nova-cairn append  --store <dir> --session <id> --entry <id> (--text <words> | --file <path|->) [--source <ptr>] [--publish <policy>]
  nova-cairn index   --store <dir> [--session <id>] [--max <n>]
  nova-cairn receipt --store <dir> --session <id> --entry <id> [--text]
      A store is a directory, plain files only, synced to disk before OK. Same id and
      same words: duplicate=true, exit 0. Same id, other words: conflict, exit 1.
      --publish records your policy only: nothing is sent, every line says published=false.
      A listing takes --max <n> (default 20, 0 lists all).

  nova-cairn self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] <file|->...
      Flags sentences where a writer passes a standing verdict on themselves: a
      first-person claim with a word of failure (STANDING), or a neutral-worded
      verdict (RANKING, FORECLOSURE, VERDICT-IDIOM, TRAIT). A dated claim is a record,
      never flagged. Exit 1 on findings. A green clears the known shapes, never the file.

  nova-cairn memory stats  --root <dir>... [--exclude <glob>]...
  nova-cairn memory search --root <dir>... --channels bm25 --k <n> [--exclude <glob>]... <words>...
  nova-cairn memory check  --root <dir>... --channels bm25 --k <n> [--exclude <glob>]... <file|->
  nova-cairn memory verify --root <dir> --links <gate|info> [--coverage <A:B>]... [--frontmatter <glob>]...
                           [--exempt <prefix>]... [--exclude <glob>]... [--fail-max <n>]
      The index is built in memory each run; nothing is written. check returns the k
      best passages per paragraph of a draft and never judges; verify gates links,
      coverage and frontmatter (exit 1 on a gating finding).

  nova-cairn version
`

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
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
		fmt.Fprintf(stdout, "nova-cairn %s\n", version)
		return 0
	}
	fmt.Fprintf(stderr, "CAIRN REFUSED: unknown verb %s; the verbs are open, append, index, receipt, self-talk, memory, version; run: nova-cairn help\n", esc(args[0]))
	return 2
}

// esc renders s on one line: control characters, line breaks and bytes that are
// not UTF-8 are written \xNN or \uNNNN.
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
		if r == '=' || unicode.IsSpace(r) {
			if r < 0x80 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// quote is strconv.Quote, which keeps printable non-ASCII as it is.
func quote(s string) string { return strconv.Quote(s) }

// split moves flags and positionals into any order: it returns the args with
// the positionals pulled to the end, so the stdlib flag package sees them all.
// valueFlags are the flags that take a value.
func split(args []string, boolFlags ...string) (flags, pos []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return flags, append(pos, args[i+1:]...)
		case len(a) > 1 && a[0] == '-':
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if !strings.Contains(name, "=") && !slices.Contains(boolFlags, name) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		default:
			pos = append(pos, a)
		}
	}
	return flags, pos
}
