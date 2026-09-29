// nova-privacy screens a piece of outgoing writing against the author's
// private material before it goes out. The judgment lives in
// internal/privacy; this file parses flags, prints, and keeps the exit
// contract.
//
// Exit 0 UNPROVEN-CLEAN, 1 FLAGGED, 2 could not run, 3 could not verify
// (CORPUS-UNREADABLE, NO-PRIVATE-CORPUS, NOTHING-CAN-EVER-FIRE,
// PAYLOAD-HAS-NO-WORDS). Only 0 permits an outbound action.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

var version string

const (
	exitClean       = 0
	exitFlagged     = 1
	exitCouldNotRun = 2
	exitUnverified  = 3
)

const usage = `nova-privacy: screen outgoing writing against private material (see docs/SPEC-PRIVACY.md)

usage:
  nova-privacy screen [corpus flags] [--json] [--max <n>] <file|->
  nova-privacy corpus [corpus flags] [--json]
  nova-privacy version
  nova-privacy help [<verb>]

screen reads the payload from the file, or from standard input for -, and
measures it against every entry marked private in the sources. Three or more
shared rare terms raise a flag: a reading assignment for a mind, never a
verdict. The best answer is UNPROVEN-CLEAN, never clean: the screen cannot see
derivation that shares no vocabulary.

corpus prints what a screen would load: each source with its entries and
private entries, each background root with its documents, and every warning.
Run it before trusting a screen.

corpus flags (name the corpus with --root, --config or --source):
  --root <dir>             read <dir>/.nova-privacy
  --config <file>          read this configuration file instead
  --source <file>          a source of private material (repeatable); replaces
                           the configuration's sources
  --background <dir>       a background root walked recursively (repeatable)
  --background-flat <dir>  a background root read flat (repeatable); either
                           flag replaces the configuration's roots
  --pattern <glob>         file pattern of the flag roots, default *.md;
                           names match case-insensitively
  --marker <text>          the marker that declares an entry private,
                           default (private)
  --max-docs <n>           background documents read, at most, default 20000
  --max-bytes <n>[K|M|G]   background bytes read, at most, default 64M; over
                           either bound, the documents with the lowest hash of
                           their relative path, the same every run. A file
                           under two roots is one document
  --json                   print one JSON object on stdout instead of lines,
                           a refusal too (outcome COULD-NOT-RUN, exit 2)
  --max <n>                screen only: flag lines to print before one MORE
                           line, default 20, 0 prints all

configuration, one keyword per line, # for comments; paths are relative to
the file's directory:
  source <file>
  background recursive|flat <pattern> <dir>
  marker <text>
  entry <token>            a line starting "<token> " opens an entry;
                           default ## and -
  stop <word>...           extra stop words
  refuse <class> <regexp>  a shape that flags the payload on sight
  warn <class> <regexp>    a shape that is reported and does not flag
  allow <text>             a specimen that never fires
  max-docs <n>
  max-bytes <n>[K|M|G]

exit codes: 0 UNPROVEN-CLEAN, the only outcome that permits sending
  1 FLAGGED, a mind reads it before it goes out
  2 could not run: a bad invocation, a payload that is empty or not text
    (a NUL byte, invalid UTF-8; UTF-16 is read when it opens with a
    byte-order mark), an unreadable configuration
  3 could not verify: CORPUS-UNREADABLE, NO-PRIVATE-CORPUS,
    NOTHING-CAN-EVER-FIRE, or PAYLOAD-HAS-NO-WORDS (the payload holds no word)

First run, from the root of this checkout: copy the example corpus the lines
below read, then paste them as they are.

  cp -R cmd/nova-privacy/testdata/example ./example

example:
  nova-privacy corpus --root ./example
  nova-privacy screen --root ./example ./example/drafts/letter.md
  nova-privacy screen --root ./example ./example/drafts/leak.md

The first two exit 0 and the third exits 1, and that is the screen working: the
leak shares three rare words with a private entry, and the flag names the
entry and the words.
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func refuse(stderr io.Writer, where, what, next string) int {
	if next == "" {
		next = "nova-privacy help"
	}
	fmt.Fprintf(stderr, "nova-privacy%s: %s; run: %s\n", where, oneline.Escape(what), next)
	if rl, ok := stderr.(*refusalLog); ok {
		rl.reasons = append(rl.reasons, what)
		rl.remedies = append(rl.remedies, "run: "+next)
	}
	return exitCouldNotRun
}

// refusalLog is standard error with a record of every refusal written to it,
// so that under --json a run that could not run still prints one object.
type refusalLog struct {
	io.Writer
	reasons, remedies []string
}

// more adds a continuation line to the last refusal.
func (rl *refusalLog) more(line string) {
	if n := len(rl.reasons); n > 0 {
		rl.reasons[n-1] += "; " + line
	}
}

// wantsJSON reports whether the flags before any -- ask for --json.
func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "json" {
			continue
		}
		if !hasVal {
			return true
		}
		if b, err := strconv.ParseBool(val); err == nil && b {
			return true
		}
	}
	return false
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	defer verbflag.Recover(stdout, "nova-privacy", usage, &code)
	if len(args) > 0 && (args[0] == "screen" || args[0] == "corpus") && wantsJSON(args[1:]) {
		rl := &refusalLog{Writer: stderr}
		stderr = rl
		defer func() {
			if code == exitCouldNotRun && len(rl.reasons) > 0 {
				writeRefusalJSON(stdout, args[0], rl)
			}
		}()
	}
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; screen checks a file, corpus shows what it is checked against", "")
	}
	switch args[0] {
	case "screen":
		return cmdScreen(args[1:], stdin, stdout, stderr)
	case "corpus":
		return cmdCorpus(args[1:], stdout, stderr)
	case "version", "--version":
		verbflag.HelpIfAsked(args[1:], "version")
		if len(args) > 1 {
			return refuse(stderr, " version", fmt.Sprintf("takes no flags and no arguments, got %d", len(args)-1), "")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-privacy", version))
		return exitClean
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			return run(append(args[1:], "--help"), stdin, stdout, stderr)
		}
		fmt.Fprint(stdout, usage)
		return exitClean
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown verb %q; the verbs are screen, corpus, version, help", args[0]), "")
	}
}

// byteCount is a flag of bytes with an optional K, M or G suffix.
type byteCount int64

func (b *byteCount) String() string { return fmt.Sprint(int64(*b)) }
func (b *byteCount) Set(v string) error {
	n, err := privacy.ParseBytes(v)
	if err != nil {
		return err
	}
	*b = byteCount(n)
	return nil
}

type list []string

func (l *list) String() string { return strings.Join(*l, ",") }
func (l *list) Set(v string) error {
	if v == "" {
		return errors.New("wants a path")
	}
	*l = append(*l, v)
	return nil
}

// corpusFlags are the flags both verbs share.
type corpusFlags struct {
	opts                     privacy.Options
	sources, recursive, flat list
	maxBytes                 byteCount
	json                     bool
}

func newFlags(verb string) (*flag.FlagSet, *corpusFlags) {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	c := &corpusFlags{}
	fs.StringVar(&c.opts.Root, "root", "", "read <dir>/.nova-privacy")
	fs.StringVar(&c.opts.Config, "config", "", "read this configuration file")
	fs.Var(&c.sources, "source", "a source of private material (repeatable)")
	fs.Var(&c.recursive, "background", "a background root walked recursively (repeatable)")
	fs.Var(&c.flat, "background-flat", "a background root read flat (repeatable)")
	fs.StringVar(&c.opts.Pattern, "pattern", "", "file pattern of the flag roots, default *.md")
	fs.StringVar(&c.opts.Marker, "marker", "", "the marker that declares an entry private, default (private)")
	fs.IntVar(&c.opts.MaxDocs, "max-docs", 0, "background documents read, at most; default 20000")
	fs.Var(&c.maxBytes, "max-bytes", "background bytes read, at most, with an optional K, M or G; default 64M")
	fs.BoolVar(&c.json, "json", false, "one JSON object on stdout")
	return fs, c
}

func (c *corpusFlags) options() privacy.Options {
	o := c.opts
	o.Sources, o.Recursive, o.Flat = c.sources, c.recursive, c.flat
	o.MaxBytes = int64(c.maxBytes)
	return o
}

// corpusArgs are the flags that name the corpus, quoted to paste, so a
// remedy repeats the caller's own inputs.
func (c *corpusFlags) corpusArgs() string {
	var out []string
	add := func(flag, v string) {
		if v != "" {
			out = append(out, flag, shellQuote(v))
		}
	}
	add("--root", c.opts.Root)
	add("--config", c.opts.Config)
	for _, s := range c.sources {
		add("--source", s)
	}
	for _, d := range c.recursive {
		add("--background", d)
	}
	for _, d := range c.flat {
		add("--background-flat", d)
	}
	add("--pattern", c.opts.Pattern)
	add("--marker", c.opts.Marker)
	if c.opts.MaxDocs > 0 {
		add("--max-docs", fmt.Sprint(c.opts.MaxDocs))
	}
	if c.maxBytes > 0 {
		add("--max-bytes", fmt.Sprint(int64(c.maxBytes)))
	}
	return strings.Join(out, " ")
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:@%+=,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// parse runs a verb's flag set. Flags come before the positional arguments.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer) bool {
	if err := verbflag.Parse(fs, args); err != nil {
		refuse(stderr, " "+fs.Name(), oneline.Cap(err.Error(), oneline.TailBytes), "nova-privacy "+fs.Name()+" -h")
		return false
	}
	return true
}

// load resolves and loads the corpus, or refuses in one line.
func load(verb string, c *corpusFlags, stderr io.Writer) (privacy.Corpus, bool) {
	spec, err := c.options().Spec()
	if err != nil {
		// The next command is the corrected one, never the failing input
		// again: the command without --config when both name a
		// configuration, the verb's help for a bad option, and the
		// configuration format for a configuration that is missing or wrong.
		next := "nova-privacy help"
		switch {
		case errors.Is(err, privacy.ErrNoCorpus), errors.Is(err, privacy.ErrOption):
			next = "nova-privacy " + verb + " -h"
		case errors.Is(err, privacy.ErrRootAndConfig):
			fixed := *c
			fixed.opts.Config = ""
			next = "nova-privacy corpus " + fixed.corpusArgs()
		}
		for i, line := range strings.Split(err.Error(), "\n") {
			if i == 0 {
				refuse(stderr, " "+verb, line, next)
				continue
			}
			fmt.Fprintf(stderr, "  %s\n", oneline.Escape(line))
			if rl, ok := stderr.(*refusalLog); ok {
				rl.more(line)
			}
		}
		return privacy.Corpus{}, false
	}
	return privacy.Load(spec), true
}

const cleanNote = "nothing was proven: the screen measures shared rare vocabulary and cannot see derivation that shares none"

func cmdScreen(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs, c := newFlags("screen")
	max := fs.Int("max", bounded.Default, "flag lines to print before one MORE line; 0 prints all")
	if !parse(fs, args, stderr) {
		return exitCouldNotRun
	}
	bad := false
	if *max < 0 {
		refuse(stderr, " screen", fmt.Sprintf("--max must be zero or more (got %d); 0 prints them all", *max), "nova-privacy screen -h")
		bad = true
	}
	var target string
	switch fs.NArg() {
	case 0:
		refuse(stderr, " screen", "no payload named; give a file, or - to read standard input", "nova-privacy screen -h")
		bad = true
	case 1:
		target = fs.Arg(0)
	default:
		refuse(stderr, " screen", fmt.Sprintf("one payload at a time, got %d arguments; flags come before the file", fs.NArg()), "nova-privacy screen -h")
		bad = true
	}
	if bad {
		return exitCouldNotRun
	}
	payload, err := readPayload(target, stdin)
	if err != nil {
		return refuse(stderr, " screen", oneline.Err(err)+"; nothing was screened", "nova-privacy screen -h")
	}
	if strings.TrimSpace(payload) == "" {
		return refuse(stderr, " screen", fmt.Sprintf("the payload %s is empty, which cannot be told from content that never arrived; nothing was screened", describe(target)), "nova-privacy screen -h")
	}
	corpus, ok := load("screen", c, stderr)
	if !ok {
		return exitCouldNotRun
	}
	res := privacy.Judge(corpus, payload)
	rerun := strings.TrimSpace("nova-privacy screen " + c.corpusArgs() + " " + shellQuote(target))
	code := exitFor(res.Outcome)
	if c.json {
		return writeJSON(stdout, stderr, "screen", code, corpus, res)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stderr, "SCREEN WARN %s\n", oneline.Escape(w))
	}
	counts := fmt.Sprintf("chars=%d terms=%d private=%d checkable=%d entries=%d background=%d found=%d sample=%s config=%s",
		res.PayloadChars, res.PayloadTerms, res.Private, res.Checkable, res.Blocks, corpus.BackgroundDocs, corpus.BackgroundFound, corpus.SampleRule, configField(corpus.Config))
	switch code {
	case exitClean:
		fmt.Fprintf(stdout, "SCREEN UNPROVEN-CLEAN %s\n", counts)
		fmt.Fprintf(stdout, "SCREEN NOTE %s\n", cleanNote)
	case exitFlagged:
		for _, h := range res.StructureRefusals() {
			fmt.Fprintf(stderr, "SCREEN STRUCTURE class=%s specimen=%s\n", oneline.Field(h.Class), oneline.Field(oneline.Cap(h.Specimen, oneline.TailBytes)))
		}
		flags := bounded.Capped(stderr, *max, "SCREEN", "flag", "--max <n> raises the ceiling, --max 0 prints every flag")
		for _, f := range res.Flags {
			flags.Line(flagLine(f))
		}
		flags.More()
		fmt.Fprintf(stderr, "SCREEN FLAGGED flags=%d structure=%d %s\n", len(res.Flags), len(res.StructureRefusals()), counts)
		fmt.Fprintf(stderr, "SCREEN REMEDY %s; after an edit, run: %s\n", oneline.Escape(res.Remedy), rerun)
	case exitUnverified:
		fmt.Fprintf(stderr, "SCREEN %s %s\n", res.Outcome, oneline.Escape(res.Reason))
		fmt.Fprintf(stderr, "SCREEN REMEDY %s; then run: nova-privacy corpus %s\n", oneline.Escape(res.Remedy), c.corpusArgs())
		fmt.Fprintf(stderr, "SCREEN NOTE nothing was verified, so the payload is not cleared\n")
	default:
		return refuse(stderr, " screen", fmt.Sprintf("the screen returned %q, an outcome this tool cannot read; the payload is not cleared", res.Outcome), "nova-privacy version")
	}
	return code
}

// maxTerms bounds the shared terms a flag line prints; the count is always
// the whole number.
const maxTerms = 12

func flagLine(f privacy.Flag) string {
	shown := f.Shared
	if len(shown) > maxTerms {
		shown = shown[:maxTerms]
	}
	return fmt.Sprintf("SCREEN FLAG source=%s entry=%d shared=%d terms=%s title=%s",
		oneline.Field(f.Source), f.Index, len(f.Shared), oneline.Field(strings.Join(shown, ",")), oneline.Escape(oneline.Cap(f.Title, oneline.TailBytes)))
}

// exitFor is the exit contract in one switch. An outcome it does not know is
// not cleared.
func exitFor(o privacy.Outcome) int {
	switch {
	case o.Cleared():
		return exitClean
	case o == privacy.Flagged:
		return exitFlagged
	case o.CouldNotVerify():
		return exitUnverified
	}
	return exitCouldNotRun
}

func configField(p string) string {
	if p == "" {
		return "-"
	}
	return oneline.Field(p)
}

func describe(target string) string {
	if target == "-" {
		return "on standard input"
	}
	return target
}

// readPayload reads the payload and decodes it as text. Bytes that are not
// text are refused: measured as bytes they would yield no words and clear.
func readPayload(target string, stdin io.Reader) (string, error) {
	var b []byte
	var err error
	if target == "-" {
		b, err = privacy.ReadPayload(stdin, "standard input")
	} else {
		b, err = privacy.ReadBounded(target, privacy.MaxPayloadBytes)
		if errors.Is(err, privacy.ErrTooLarge) {
			err = fmt.Errorf("%w (MaxPayloadBytes)", err)
		}
	}
	if err != nil {
		return "", err
	}
	return privacy.DecodeText(b, describePayload(target))
}

func describePayload(target string) string {
	if target == "-" {
		return "standard input"
	}
	return target
}

func cmdCorpus(args []string, stdout, stderr io.Writer) int {
	fs, c := newFlags("corpus")
	if !parse(fs, args, stderr) {
		return exitCouldNotRun
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " corpus", fmt.Sprintf("takes no positional arguments, got %q", fs.Arg(0)), "nova-privacy corpus -h")
	}
	corpus, ok := load("corpus", c, stderr)
	if !ok {
		return exitCouldNotRun
	}
	res := privacy.JudgeCorpus(corpus)
	code := exitFor(res.Outcome)
	if c.json {
		return writeJSON(stdout, stderr, "corpus", code, corpus, res)
	}
	for _, s := range corpus.Sources {
		if s.Err != nil {
			fmt.Fprintf(stderr, "CORPUS SOURCE path=%s unreadable=%s\n", oneline.Field(s.Path), oneline.Field(oneline.Cap(s.Err.Error(), oneline.TailBytes)))
			continue
		}
		fmt.Fprintf(stdout, "CORPUS SOURCE path=%s entries=%d private=%d\n", oneline.Field(s.Path), s.Blocks, s.Private)
	}
	for _, r := range corpus.Roots {
		fmt.Fprintf(stdout, "CORPUS BACKGROUND root=%s mode=%s pattern=%s found=%d shared=%d read=%d\n",
			oneline.Field(r.Dir), map[bool]string{true: "recursive", false: "flat"}[r.Recursive], oneline.Field(r.Pattern), r.Found, r.Shared, r.Read)
	}
	fmt.Fprintf(stdout, "CORPUS SAMPLE found=%d read=%d bytes=%d rule=%s max-docs=%d max-bytes=%d\n",
		corpus.BackgroundFound, corpus.BackgroundDocs, corpus.BackgroundBytes, corpus.SampleRule, corpus.MaxDocs, corpus.MaxBytes)
	for _, w := range res.Warnings {
		fmt.Fprintf(stderr, "CORPUS WARN %s\n", oneline.Escape(w))
	}
	summary := fmt.Sprintf("sources=%d entries=%d private=%d checkable=%d background=%d config=%s",
		len(corpus.Sources), res.Blocks, res.Private, res.Checkable, corpus.BackgroundDocs, configField(corpus.Config))
	if code == exitClean {
		fmt.Fprintf(stdout, "CORPUS OK %s\n", summary)
		return exitClean
	}
	fmt.Fprintf(stderr, "CORPUS %s %s\n", res.Outcome, summary)
	fmt.Fprintf(stderr, "CORPUS REMEDY %s: %s; then run: nova-privacy corpus %s\n", oneline.Escape(res.Reason), oneline.Escape(res.Remedy), c.corpusArgs())
	return code
}

// report is the --json shape of both verbs.
type report struct {
	Verb       string        `json:"verb"`
	Outcome    string        `json:"outcome"`
	Exit       int           `json:"exit"`
	Cleared    bool          `json:"cleared"`
	Reason     string        `json:"reason,omitempty"`
	Remedy     string        `json:"remedy,omitempty"`
	Config     string        `json:"config,omitempty"`
	Chars      int           `json:"chars,omitempty"`
	Terms      int           `json:"terms,omitempty"`
	Entries    int           `json:"entries"`
	Private    int           `json:"private"`
	Checkable  int           `json:"checkable"`
	Background int           `json:"background"`
	Sample     jsonSample    `json:"sample"`
	Sources    []jsonSource  `json:"sources"`
	Roots      []jsonRoot    `json:"roots"`
	Flags      []jsonFlag    `json:"flags,omitempty"`
	Structure  []jsonHit     `json:"structure,omitempty"`
	Warnings   []string      `json:"warnings,omitempty"`
	Bounds     privacyBounds `json:"bounds"`
}

type jsonSource struct {
	Path    string `json:"path"`
	Entries int    `json:"entries"`
	Private int    `json:"private"`
	Error   string `json:"error,omitempty"`
}

type jsonRoot struct {
	Root    string `json:"root"`
	Mode    string `json:"mode"`
	Pattern string `json:"pattern"`
	Found   int    `json:"found"`
	Shared  int    `json:"shared"`
	Read    int    `json:"read"`
	Error   string `json:"error,omitempty"`
}

type jsonSample struct {
	Found    int    `json:"found"`
	Read     int    `json:"read"`
	Bytes    int64  `json:"bytes"`
	Rule     string `json:"rule"`
	MaxDocs  int    `json:"max_docs"`
	MaxBytes int64  `json:"max_bytes"`
}

type jsonFlag struct {
	Source string   `json:"source"`
	Entry  int      `json:"entry"`
	Title  string   `json:"title"`
	Shared []string `json:"shared"`
}

type jsonHit struct {
	Class    string `json:"class"`
	Specimen string `json:"specimen"`
	Refuse   bool   `json:"refuse"`
}

type privacyBounds struct {
	Rare       int `json:"rare"`
	Background int `json:"background"`
}

// writeRefusalJSON is the --json object of a run that could not run: the
// outcome COULD-NOT-RUN, exit 2, and every refusal's reason and remedy.
func writeRefusalJSON(stdout io.Writer, verb string, rl *refusalLog) {
	rep := report{
		Verb: verb, Outcome: "COULD-NOT-RUN", Exit: exitCouldNotRun, Cleared: false,
		Reason: strings.Join(rl.reasons, "; "), Remedy: strings.Join(rl.remedies, "; "),
		Sources: []jsonSource{}, Roots: []jsonRoot{},
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
}

func writeJSON(stdout, stderr io.Writer, verb string, code int, c privacy.Corpus, res privacy.Result) int {
	rep := report{
		Verb: verb, Outcome: string(res.Outcome), Exit: code, Cleared: res.Outcome.Cleared(),
		Reason: res.Reason, Remedy: res.Remedy, Config: c.Config,
		Chars: res.PayloadChars, Terms: res.PayloadTerms,
		Entries: res.Blocks, Private: res.Private, Checkable: res.Checkable, Background: c.BackgroundDocs,
		Sources: []jsonSource{}, Roots: []jsonRoot{}, Warnings: res.Warnings,
		Bounds: privacyBounds{Rare: res.Bounds.Rare, Background: res.Bounds.Background},
		Sample: jsonSample{Found: c.BackgroundFound, Read: c.BackgroundDocs, Bytes: c.BackgroundBytes, Rule: c.SampleRule, MaxDocs: c.MaxDocs, MaxBytes: c.MaxBytes},
	}
	for _, s := range c.Sources {
		js := jsonSource{Path: s.Path, Entries: s.Blocks, Private: s.Private}
		if s.Err != nil {
			js.Error = s.Err.Error()
		}
		rep.Sources = append(rep.Sources, js)
	}
	for _, r := range c.Roots {
		jr := jsonRoot{Root: r.Dir, Mode: map[bool]string{true: "recursive", false: "flat"}[r.Recursive], Pattern: r.Pattern, Found: r.Found, Shared: r.Shared, Read: r.Read}
		if r.Err != nil {
			jr.Error = r.Err.Error()
		}
		rep.Roots = append(rep.Roots, jr)
	}
	for _, f := range res.Flags {
		rep.Flags = append(rep.Flags, jsonFlag{Source: f.Source, Entry: f.Index, Title: f.Title, Shared: f.Shared})
	}
	for _, h := range res.Structure {
		rep.Structure = append(rep.Structure, jsonHit{Class: h.Class, Specimen: h.Specimen, Refuse: h.Refuse})
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		return refuse(stderr, " "+verb, "cannot write the JSON report: "+oneline.Err(err), "nova-privacy "+verb+" -h")
	}
	return code
}
