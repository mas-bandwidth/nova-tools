package verbflag

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE ONE USAGE TABLE (nova-tools#4352 A, the cold read of #4399). Every
// nova-sprint verb path ("card cut", "stream open", "census") has one entry
// in Usages: its forms (the line after `nova-sprint <path> `) and its
// examples (whole lines after `nova-sprint `). Three printers read it and
// nothing else prints a usage:
//
//   - WriteHelp: `-h` on a noun lists its subverbs' forms and examples; on a
//     path, its forms, the flags its flag set defines and its examples;
//   - Tail: the end of every refusal, the refused path's own forms (never
//     tail that sent a session to the whole help, never a wall of every verb);
//   - RetiredError: a retired spelling is refused with the whole corrected
//     line (Parse), which is the refusal's end.
//
// The class test in cmd/nova-sprint (TestGrammarEveryPathHelp) runs -h on
// every path, holds its flag set's name to the path and each example's flags
// to that flag set, and every noun registered to an entry.

// Usage is one verb path's entry.
type Usage struct {
	Forms    []string // the line after "nova-sprint <path> ", one per form
	Examples []string // whole lines after "nova-sprint "
	NoFlags  bool     // the path takes no flags: -h never reaches a flag set
	SetOf    string   // the path whose flag set this one parses with (fleet build set: fleet build)
}

var prefixes = func() map[string]bool {
	m := map[string]bool{}
	for p := range Usages {
		w := strings.Fields(p)
		for i := 1; i <= len(w); i++ {
			m[strings.Join(w[:i], " ")] = true
		}
	}
	return m
}()

// Known is whether path is an entry or a noun (or noun and subverb) that
// entries extend.
func Known(path string) bool { return prefixes[path] }

// Resolve is the longest known path the leading words spell: words that
// start with - end it. "" when the first word is no noun.
func Resolve(words []string) string {
	path := ""
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			break
		}
		cand := strings.TrimSpace(path + " " + w)
		if !Known(cand) {
			break
		}
		path = cand
	}
	return path
}

// Subs are the paths one word longer than path, sorted.
func Subs(path string) []string {
	var out []string
	n := len(strings.Fields(path)) + 1
	for p := range prefixes {
		if strings.HasPrefix(p, path+" ") && len(strings.Fields(p)) == n {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Lines are path's forms as whole lines; a path with no forms is
// "nova-sprint <path> [flags]", a noun with none "nova-sprint <noun> <a|b|c>".
func Lines(path string) []string {
	u := Usages[path]
	var out []string
	for _, f := range u.Forms {
		out = append(out, strings.TrimSpace("nova-sprint "+path+" "+f))
	}
	if len(out) > 0 {
		return out
	}
	if subs := Subs(path); len(subs) > 0 {
		names := make([]string, len(subs))
		for i, s := range subs {
			names[i] = s[len(path)+1:]
		}
		return []string{"nova-sprint " + path + " <" + strings.Join(names, "|") + "> [flags]"}
	}
	if u.NoFlags {
		return []string{"nova-sprint " + path}
	}
	return []string{"nova-sprint " + path + " [flags]"}
}

// Examples are path's examples, or for a noun the first of each subverb's,
// as whole lines.
func Examples(path string) []string {
	var out []string
	for _, e := range Usages[path].Examples {
		out = append(out, "nova-sprint "+e)
	}
	if len(out) > 0 {
		return out
	}
	for _, s := range Subs(path) {
		if ex := Examples(s); len(ex) > 0 {
			out = append(out, ex[0])
		}
	}
	return out
}

// WriteHelp prints path's usage: its forms, its subverbs' forms, the flags
// fs defines (nil: none reached), its examples and the exit codes. No flag
// default is printed: a default can come from the environment, and a help
// line never prints a secret.
func WriteHelp(out io.Writer, path string, fs *flag.FlagSet) {
	var b strings.Builder
	for i, l := range Lines(path) {
		if i == 0 {
			b.WriteString("usage: " + l + "\n")
		} else {
			b.WriteString("       " + l + "\n")
		}
	}
	subs := Subs(path)
	if len(subs) > 0 {
		b.WriteString("subverbs:\n")
		for _, s := range subs {
			for _, l := range Lines(s) {
				b.WriteString("  " + l + "\n")
			}
		}
	}
	if fs != nil {
		var names []string
		fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
		sort.Strings(names)
		if len(names) > 0 {
			b.WriteString("flags:\n")
		}
		for _, n := range names {
			kind, text := flag.UnquoteUsage(fs.Lookup(n))
			line := "  --" + n
			if kind != "" {
				line += " <" + kind + ">"
			}
			if text != "" {
				line += "  " + text
			}
			b.WriteString(line + "\n")
		}
	}
	if ex := Examples(path); len(ex) > 0 {
		b.WriteString("example:\n")
		for _, e := range ex {
			b.WriteString("  " + e + "\n")
		}
	}
	if len(subs) > 0 {
		fmt.Fprintf(&b, "nova-sprint %s <subverb> -h prints that subverb's flags and examples.\n", path)
	}
	b.WriteString("exit codes: 0 done, 1 refused, 2 usage\n")
	_, _ = io.WriteString(out, b.String())
}

// Tail is what a refusal of verb ends in: its path's forms, "usage:
// nova-sprint <path> <form> | nova-sprint <path> <form>". "" when verb names
// no known path (the caller names what there is).
func Tail(verb string) string {
	path := Resolve(strings.Fields(verb))
	if path == "" {
		return ""
	}
	return "usage: " + strings.Join(Lines(path), " | ")
}

// Final is whether a refusal's text already ends in a line to run or a
// usage: a corrected line (RetiredError), a remedy or a usage. Tail is not
// added to it.
func Final(what string) bool {
	return strings.Contains(what, "run: nova-sprint ") || strings.Contains(what, "usage: nova-sprint ")
}

// Refusal is the one refusal line of verb: `nova-sprint <verb>: <what>;
// <Tail>`, or with no tail when what is Final.
func Refusal(verb, what string) string {
	verb = strings.TrimSpace(verb)
	head := "nova-sprint"
	if verb != "" {
		head += " " + verb
	}
	line := head + ": " + oneline.Escape(what)
	if !Final(what) {
		if t := Tail(verb); t != "" {
			line += "; " + t
		}
	}
	return line
}

// Refuse prints Refusal on w and returns 2, the usage exit.
func Refuse(w io.Writer, verb, what string) int {
	_, _ = fmt.Fprintln(w, Refusal(verb, what))
	return 2
}
