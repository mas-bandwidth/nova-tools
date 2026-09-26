// Package verbflag is the one FlagSet constructor for the nova-sprint verbs
// (#3254, #4352 A). A parse error stays quiet and comes back from Parse, so the
// verb prints its own one-line refusal; -h, -help or --help on a flag set that
// does not define them instead unwinds to the dispatcher, which prints the
// verb's usage line and every flag the set defines on stdout and exits 2.
//
// One grammar (nova-tools #4352 A): `nova-sprint <noun> <verb> [--flags]
// [positionals]`; the same flag means the same thing on every verb, lists are
// comma-separated, a worker is friend:<f> or bench:<b>. A spelling the grammar
// retired is refused by Parse naming the one it is spelled now, never kept as
// an alias; Retired is that table, and the class test in internal/ci holds
// every verb to it.
package verbflag

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Retired maps every flag spelling the one grammar retired to the spelling it
// has now. Parse refuses the old one naming the new one; the class test
// (internal/ci, cli-style) fails any verb that still defines an old one.
//
//	--as      the worker the verb acts as or on (friend:<f> or bench:<b>);
//	          the actor of a receipt is the seat, never a flag
//	--ids     the ids the verb acts on, comma-separated (n=1 is a list of one)
//	--why     the reason a write records
//	--stream  the work stream(s), comma-separated
//	--redis   the sprint store
//	--to      the target worker
var Retired = map[string]string{
	"actor":     "as",
	"by":        "as",
	"consumer":  "as",
	"who":       "as",
	"friend":    "as",
	"id":        "ids",
	"label":     "ids",
	"reason":    "why",
	"streams":   "stream",
	"to-stream": "stream",
	"scope":     "stream",
	"to-friend": "to",
	"addr":      "redis",
	"store":     "redis",
}

// The shared vocabulary: one help line per flag of the grammar, used by every
// verb that takes it, so -h says the same thing everywhere and the class test
// can hold a vocabulary flag to its one meaning.
const (
	HelpRedis  = "the sprint store, host:port (default the seat's, else NOVA_SPRINT_REDIS)"
	HelpSprint = "the sprint"
	HelpAs     = "the worker this verb acts as or on: friend:<f> or bench:<b> (default the seat's)"
	HelpIDs    = "the ids to act on, comma-separated; @<file> or @- reads them one per line"
	HelpStream = "the work stream(s), comma-separated"
	HelpWhy    = "the reason, recorded on the receipt"
	HelpN      = "how many; on a verb that names one pull request, its number (as --pr)"
	HelpTo     = "the target worker: friend:<f> or bench:<b>"
	HelpIdem   = "an idempotency key: the same key twice is one write, the second ALREADY"
	HelpDryRun = "print what this would write, in receipt form, and write nothing"
	HelpRepo   = "the repository, owner/name"
	HelpPR     = "the pull request number"
	HelpFrom   = "the input: a file, a directory of files, or - for stdin"
	HelpSince  = "how far back, as a duration (10m, 2h, 1d)"
	HelpJSON   = "print JSON, one object per line, instead of the table"
)

// Vocabulary maps each flag of the shared vocabulary to the one help line it
// carries on every verb: the class test refuses a vocabulary flag with any
// other help.
var Vocabulary = map[string]string{
	"redis": HelpRedis, "sprint": HelpSprint, "as": HelpAs, "ids": HelpIDs, "stream": HelpStream,
	"why": HelpWhy, "n": HelpN, "to": HelpTo, "idem": HelpIdem, "dry-run": HelpDryRun,
	"repo": HelpRepo, "pr": HelpPR, "from": HelpFrom, "since": HelpSince, "json": HelpJSON,
}

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

// Set is a verb's flag set: a ContinueOnError flag.FlagSet named for the
// noun and verb ("task push"), quiet on a parse error, Help on -h, and a
// Parse that refuses a retired spelling naming the current one.
type Set struct{ *flag.FlagSet }

// New returns the verb's Set.
func New(name string) *Set {
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
	return &Set{FlagSet: fs}
}

// notDefined is the flag package's message for an unknown flag.
const notDefined = "flag provided but not defined: -"

// Parse parses args. An unknown flag whose spelling the grammar retired comes
// back as `--old is spelled --new` when this set defines --new, else as
// `--old is retired; nova-sprint <verb> -h lists the flags`.
//
// --n names the pull request on a verb that takes --pr and counts nothing
// (nova-tools#4352 A): pr record and pr lines spell the PR number --n, so
// a session that learned them types `ci status --repo nova-tools --n 4371`,
// and that line is the one it meant, never a refusal.
func (s *Set) Parse(args []string) error {
	if s.Lookup("n") == nil && s.Lookup("pr") != nil {
		args = PRFromN(args)
	}
	err := s.FlagSet.Parse(args)
	if err == nil {
		return nil
	}
	old, ok := strings.CutPrefix(err.Error(), notDefined)
	if !ok {
		return err
	}
	old = strings.TrimLeft(old, "-")
	now, retired := Retired[old]
	if !retired {
		return err
	}
	if s.Lookup(now) != nil {
		return fmt.Errorf("--%s is spelled --%s", old, now)
	}
	return fmt.Errorf("--%s is retired; nova-sprint %s -h lists the flags", old, s.Name())
}

// PRFromN is args with every --n (-n, --n=<v>, -n=<v>) before a "--"
// spelled --pr: the rewrite Parse makes on a set that defines --pr and not
// --n. It returns a new slice and leaves args alone.
func PRFromN(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		switch {
		case a == "--":
			return out
		case a == "-n" || a == "--n":
			out[i] = "--pr"
		case strings.HasPrefix(a, "-n=") || strings.HasPrefix(a, "--n="):
			_, v, _ := strings.Cut(a, "=")
			out[i] = "--pr=" + v
		}
	}
	return out
}

// List splits a comma-separated flag value into its items, trimmed, with
// empty items dropped: the one list shape of the grammar.
func List(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// Recover is deferred by the dispatcher. A Help panic becomes the usage text
// on out and *code 2; any other panic goes on unwinding.
func Recover(out io.Writer, prog string, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(Help)
	if !ok {
		panic(r)
	}
	Print(out, prog, h.FS)
	*code = 2
}

// Print writes `usage: <prog> <name> [flags]`, one line per flag the set
// defines (sorted, with its value type and its one-line help; no defaults,
// since a default can come from the environment and a help line must never
// print a secret), and the exit codes.
func Print(out io.Writer, prog string, fs *flag.FlagSet) {
	var b strings.Builder
	fmt.Fprintf(&b, "usage: %s %s [flags]\n", prog, fs.Name())
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
	b.WriteString("exit codes: 0 done, 1 refused, 2 usage\n")
	_, _ = io.WriteString(out, b.String())
}
