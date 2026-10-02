// The flags every verb parses through: one flag set per verb, every problem of an
// invocation named at once, and the defaults a bus file or the environment supplies.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// stringList is a flag that may be repeated, for `receipt --note`.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// flags is one verb's flag set, with package flag's two mouths closed. Its error text
// quotes the argument it could not parse and its usage dump follows, so an argument
// beginning with a dash could otherwise author a whole line of stderr before any code
// here ran.
type flags struct {
	verb string
	fs   *flag.FlagSet
	// alsoRefuse, when set, is asked after the flags are parsed and the required ones
	// checked: a line it returns is printed beside the missing-flag lines and refuses the
	// invocation with them, so a first run names every problem it has and not the first
	// (the receipt word count, whose sources beyond the flag are a file and a variable).
	alsoRefuse func() string
}

func newFlags(verb string) *flags {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return &flags{verb: verb, fs: fs}
}

// parse runs the flag set and enforces the no-guessing rule for every flag named in
// required, naming every problem of the invocation at once (ONBOARDING point 2): every
// value a flag cannot take, every missing required flag, and the verb's own line
// (alsoRefuse), one REFUSED line each. It prints the refusal; exit 2 belongs to the caller.
func (f *flags) parse(args []string, stderr io.Writer, required map[string]*string) bool {
	// -h, -help and --help never land here: verbflag.Parse raises the verb's help, which
	// run prints on stdout at exit 0.
	bad, err := parseEvery(f.fs, args)
	for _, b := range bad {
		refuse(stderr, f.verb, oneline.Cap(verbflag.Explain(f.fs, b), oneline.TailBytes), verbHelp(f.verb))
	}
	if err != nil { // the parse stopped here, so nothing after it was read
		refuse(stderr, f.verb, oneline.Cap(verbflag.Explain(f.fs, err), oneline.TailBytes), verbHelp(f.verb))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		refuse(stderr, f.verb, fmt.Sprintf("takes no positional arguments, got %d (flags come before arguments)", n), verbHelp(f.verb))
		return false
	}
	var missing []string
	for name, value := range required {
		if strings.TrimSpace(*value) == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		refuse(stderr, f.verb, "--"+name+" is required; it wants "+wantsOf(f.fs, name)+"; refusing to guess", verbHelp(f.verb))
	}
	more := false
	if f.alsoRefuse != nil {
		if line := f.alsoRefuse(); line != "" {
			fmt.Fprintln(stderr, oneline.Escape(line))
			more = true
		}
	}
	return len(bad) == 0 && len(missing) == 0 && !more
}

// wantsOf is what a flag wants, in its own description's words.
func wantsOf(fs *flag.FlagSet, name string) string {
	if fl := fs.Lookup(name); fl != nil {
		return strings.TrimSuffix(fl.Usage, " (required)")
	}
	return "a value"
}

// parseEvery is verbflag.Parse that goes on past a value its flag cannot take, so one run
// names every bad value (`--receipt-max-words abc --timeout 5x` names both). For the parse
// each flag's Set records its error and accepts nothing; the flags are restored before
// anything is worded or printed, help included. err is the error the parse stopped on (an
// unknown flag, a flag with no value), after which nothing was read.
func parseEvery(fs *flag.FlagSet, args []string) (bad []error, err error) {
	restore := map[*flag.Flag]flag.Value{}
	fs.VisitAll(func(fl *flag.Flag) {
		restore[fl] = fl.Value
		c := collecting{Value: fl.Value, name: fl.Name, bad: &bad}
		if b, ok := fl.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			fl.Value = collectingBool{c}
		} else {
			fl.Value = c
		}
	})
	defer func() {
		for fl, v := range restore {
			fl.Value = v
		}
	}()
	return bad, verbflag.Parse(fs, args)
}

// collecting is a flag.Value that records a value its flag cannot take, in the flag
// package's own words, instead of stopping the parse on it.
type collecting struct {
	flag.Value
	name string
	bad  *[]error
}

func (c collecting) Set(v string) error {
	if err := c.Value.Set(v); err != nil {
		*c.bad = append(*c.bad, fmt.Errorf("invalid value %q for flag -%s: %v", v, c.name, err))
	}
	return nil
}

// collectingBool is collecting for a boolean flag, which the flag package lets stand alone.
type collectingBool struct{ collecting }

func (collectingBool) IsBoolFlag() bool { return true }

// gitArgs checks the two flags that become git's own argv. A --remote or --branch
// beginning with a dash is an OPTION to git rather than a name, and this tool would run
// it; the charset narrows the rest. It prints its own refusal; exit 2 belongs to the
// caller, because a flag this tool will not pass on is a bad invocation and not a bus
// that failed.
func (f *flags) gitArgs(remote, branch string, stderr io.Writer) bool {
	for _, c := range []struct {
		what, value string
	}{{"remote", remote}, {"branch", branch}} {
		if err := bus.ValidGitArg(c.what, c.value); err != nil {
			fmt.Fprintf(stderr, "%s REFUSED: %s\n", strings.ToUpper(f.verb), oneline.WithRemedy(oneline.Err(err), verbHelp(f.verb)))
			return false
		}
	}
	return true
}

// atLeastZero reads an integer flag whose floor is zero rather than one, which is the
// floor a THRESHOLD has: --open-warn 0 says "tell me on every run", and that is a thing a
// reader may reasonably mean. A negative one is not, and is a bad invocation.
func (f *flags) atLeastZero(name string, value int, stderr io.Writer) bool {
	if value < 0 {
		fmt.Fprintf(stderr, "%s REFUSED: --%s counts entries, so it is 0 or more, got %d; run: nova-bus %s -h\n", strings.ToUpper(f.verb), name, value, oneline.Field(f.verb))
		return false
	}
	return true
}

// count reads a required positive integer flag.
func (f *flags) count(name string, value int, stderr io.Writer) bool {
	if value < 1 {
		fmt.Fprintf(stderr, "%s REFUSED: --%s must be given and at least 1, got %d; refusing to guess; run: nova-bus %s -h\n", strings.ToUpper(f.verb), name, value, oneline.Field(f.verb))
		return false
	}
	return true
}

// set answers whether a flag was named on the command line at all, so a default source can
// tell "absent" from "given a value that is not usable", which are different mistakes.
func (f *flags) set(name string) bool {
	given := false
	f.fs.Visit(func(fl *flag.Flag) {
		if fl.Name == name {
			given = true
		}
	})
	return given
}

// receiptMaxWords resolves the one count a body is a receipt under. The flag wins; when it
// is absent, a `receipt-max-words=<n>` line in <bus>/.nova-bus/defaults is read, then the
// NOVA_BUS_RECEIPT_MAX_WORDS environment variable. It refuses only when none of the three
// yields a positive number, and the refusal names the two default sources as the remedy.
func (f *flags) receiptMaxWords(flagValue int, flagWasSet bool, busDir string, stderr io.Writer) (int, bool) {
	v, ok := resolveReceiptMaxWords(flagValue, flagWasSet, busDir)
	if !ok {
		fmt.Fprintln(stderr, oneline.Escape(receiptMaxWordsRefusal(f.verb, flagValue)))
		return 0, false
	}
	return v, true
}

// resolveReceiptMaxWords is the count the three sources give: the flag, then the file line,
// then the variable; false when none yields a positive number.
func resolveReceiptMaxWords(flagValue int, flagWasSet bool, busDir string) (int, bool) {
	if !flagWasSet {
		if v, ok := receiptMaxWordsFromDefaults(busDir); ok {
			flagValue = v
		} else if v, ok := receiptMaxWordsFromEnv(); ok {
			flagValue = v
		}
	}
	return flagValue, flagValue >= 1
}

// receiptMaxWordsRefusal is the one line for a missing receipt word count, which names
// the three places it can be given.
func receiptMaxWordsRefusal(verb string, got int) string {
	return fmt.Sprintf("%s REFUSED: --receipt-max-words must be given and at least 1, got %d; refusing to guess; give the flag, or a `receipt-max-words=<n>` line in <bus>/.nova-bus/defaults (a plain text file, one key=value per line, that you create), or set the NOVA_BUS_RECEIPT_MAX_WORDS environment variable; run: nova-bus %s -h", oneline.Field(strings.ToUpper(verb)), got, oneline.Field(verb))
}

// receiptMaxWordsFromDefaults reads the `receipt-max-words=<n>` line out of
// <bus>/.nova-bus/defaults, the file default source. A missing file, a missing key, or an
// unusable value is "absent".
func receiptMaxWordsFromDefaults(busDir string) (int, bool) {
	val, ok := fromDefaults(busDir, "receipt-max-words")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// fromDefaults is the value of one key in <bus>/.nova-bus/defaults, a plain file of
// key=value lines with # comments, and false when the file or the key is not there. The
// first line naming the key is the one read.
func fromDefaults(busDir, key string) (string, bool) {
	if busDir == "" {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(busDir, ".nova-bus", "defaults"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, val, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(val), true
		}
	}
	return "", false
}

// host resolves the machine a note is posted from. The flag wins; when it is absent, a
// `host=<name>` line in <bus>/.nova-bus/defaults is read. There is NO default beyond that
// and no refusal when none is found: a bus whose lines each post from one machine has
// nothing to disambiguate, and a note with no Host line is the note this tool has always
// written. A value that is given and unusable IS a refusal, because a host silently
// dropped is the `[bud air]` subject convention all over again.
func (f *flags) host(flagValue string, flagWasSet bool, busDir string, stderr io.Writer) (string, bool) {
	if !flagWasSet {
		flagValue = hostFromDefaults(busDir)
		if flagValue == "" {
			return "", true
		}
	}
	if err := bus.ValidHost(flagValue); err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", strings.ToUpper(f.verb), oneline.WithRemedy(oneline.Err(err), verbHelp(f.verb)))
		return "", false
	}
	return flagValue, true
}

// hostFromDefaults reads the `host=<name>` line out of <bus>/.nova-bus/defaults, the same
// key=value file receipt-max-words is read from. A missing file or a missing key is "no
// host"; a key whose value is unusable is returned as it stands, so the caller refuses it
// by name rather than posting as nobody.
func hostFromDefaults(busDir string) string {
	val, _ := fromDefaults(busDir, "host")
	return val
}

// receiptMaxWordsFromEnv reads NOVA_BUS_RECEIPT_MAX_WORDS, the environment default source.
// An empty or unusable value is "absent".
func receiptMaxWordsFromEnv() (int, bool) {
	s := strings.TrimSpace(os.Getenv("NOVA_BUS_RECEIPT_MAX_WORDS"))
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// defaultAttempts is the retry budget when the caller names none.
//
// THE ONE FLAG THAT GETS A DEFAULT, and it is a departure from the rule above, so here is
// the measurement that bought it. In the scenario run, five lines sent three notes each at
// once with `--attempts 3`: fifteen were sent and SIX LANDED. With `--attempts 25` all
// fifteen landed and the deepest any one of them went was nine attempts. A retry budget is
// not a fact about a bus that only its owner can supply -- it is the number of times this
// tool will keep trying against a remote that is moving under it, and a caller made to
// invent one invents a small one and loses notes. Twenty-five is nine with room, and the
// backoff caps the whole of it at a person's wait rather than a schedule.
const defaultAttempts = 25

// attempts checks the retry budget. Unlike count it has a default, so a zero here is a
// caller who asked for one and asked for none.
func (f *flags) attempts(value int, stderr io.Writer) bool {
	if value < 1 {
		fmt.Fprintf(stderr, "%s REFUSED: --attempts is a number of tries and is at least 1, got %d; run: nova-bus %s -h\n", strings.ToUpper(f.verb), value, oneline.Field(f.verb))
		return false
	}
	return true
}

// gitTimeoutFlag sets the per-subprocess budget every git this run starts is held to. A
// hung fetch is a tool that has stopped saying anything, which is indistinguishable from a
// tool that is working.
func (f *flags) gitTimeoutFlag(seconds int, stderr io.Writer) bool {
	if err := gitTimeoutProblem(seconds); err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", strings.ToUpper(f.verb), oneline.WithRemedy(oneline.Err(err), verbHelp(f.verb)))
		return false
	}
	return true
}

// gitTimeoutProblem is the same check with its answer RETURNED rather than printed, for the
// one verb that collects every problem in an invocation before it prints any of them. One
// spelling, two callers: a check that printed for one caller and returned for the other
// would be two rules wearing one name.
func gitTimeoutProblem(seconds int) error {
	if seconds < 1 {
		return fmt.Errorf("--git-timeout is a whole number of seconds and at least 1, got %d", seconds)
	}
	return bus.SetGitTimeout(time.Duration(seconds) * time.Second)
}

// defaultGitTimeoutSeconds is DefaultGitTimeout as the flag spells it.
const defaultGitTimeoutSeconds = 60
