package onboarding

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// This file is the harness that EXECUTES a transcript rather than reading it.
//
// docs/TESTS.md is the document a stranger is pointed at first, and every block
// in it is real output pasted whole -- the document has no placeholder
// convention. So the promise it makes is exact: type these lines, see these
// lines, in this order and no others. A test that asks only whether each
// documented line resembles something the tool printed keeps that promise for
// the words and drops it for the count and the order, and an abridged or
// reordered transcript passes. Steps and Compare below keep the whole promise.
//
// The one thing a rerun cannot reproduce is a value that belongs to the RUN
// rather than to the document: the instant a record was created, an id derived
// from it, a temporary directory's path. Those are DECLARED, one Norm per
// value, by the test that runs the transcript -- so that what is not compared
// is a short list a reader can check, rather than whatever a loose pattern
// happened to swallow.
//
// Nothing here asserts, reads a file or runs a process: Steps parses, Compare
// reports, and Execute walks a Runner the caller supplies. The caller's test
// decides what is fatal. That is the same contract the rest of this package
// keeps.

// Step is one `$ ` line of a transcript and EVERY line the document says it
// prints, in the order the document prints them.
type Step struct {
	// Line is the `$ ...` line as the document writes it, for failure messages.
	Line string
	// Args is what a shell would hand the tool, with the tool's own name removed.
	Args []string
	// Stdin is the file named by a trailing `< path` redirect, or "" when the
	// command reads nothing. The path is as the document writes it.
	Stdin string
	// Want is the block written under the command, in order, with the blank
	// line the document leaves between commands dropped from the end.
	Want []string
}

// Steps cuts a transcript's lines into its commands and their outputs. A line
// opening with `$ ` starts a step; every line after it belongs to that step
// until the next `$ ` line.
//
// Output standing before the first command, and a `$` line for some other tool,
// are the document's own bugs and are returned as errors rather than skipped: a
// skipped line is exactly the abridgement this harness exists to catch.
func Steps(tool string, lines []string) ([]Step, error) {
	var steps []Step
	for _, line := range lines {
		cmd, isCommand := strings.CutPrefix(line, "$ ")
		if !isCommand {
			if len(steps) == 0 {
				if strings.TrimSpace(line) == "" {
					continue
				}
				return nil, fmt.Errorf("a transcript line stands before any command:\n  %s", line)
			}
			last := &steps[len(steps)-1]
			last.Want = append(last.Want, line)
			continue
		}
		cmd, stdin, err := cutRedirect(cmd)
		if err != nil {
			return nil, fmt.Errorf("the transcript line %q: %w", line, err)
		}
		args, err := SplitShell(cmd)
		if err != nil {
			return nil, fmt.Errorf("cannot split the transcript line %q: %w", line, err)
		}
		if len(args) == 0 || args[0] != tool {
			return nil, fmt.Errorf("the transcript line %q is not a %s command", line, tool)
		}
		steps = append(steps, Step{Line: line, Args: args[1:], Stdin: stdin})
	}
	// The document leaves a blank line between commands; it belongs to neither.
	for i := range steps {
		for len(steps[i].Want) > 0 && strings.TrimSpace(steps[i].Want[len(steps[i].Want)-1]) == "" {
			steps[i].Want = steps[i].Want[:len(steps[i].Want)-1]
		}
	}
	return steps, nil
}

// cutRedirect separates a trailing `< path` from a documented command line.
// Only the one redirect the transcripts use is understood: a pipeline, a `>`
// or a `2>&1` means the document is describing something this harness would
// have to run a shell for, and it says so instead of running a truncated
// command and comparing the wrong output.
func cutRedirect(cmd string) (string, string, error) {
	for _, unsupported := range []string{"|", ">", "&&", ";", "$("} {
		if strings.Contains(cmd, unsupported) {
			return "", "", fmt.Errorf("holds %q, which this harness does not run; a transcript line is one command", unsupported)
		}
	}
	head, tail, found := strings.Cut(cmd, "<")
	if !found {
		return cmd, "", nil
	}
	path := strings.TrimSpace(tail)
	if path == "" {
		return "", "", fmt.Errorf("ends in `<` and names no file to read")
	}
	if strings.ContainsAny(path, " \t") {
		return "", "", fmt.Errorf("redirects from %q, which is more than one word", path)
	}
	return strings.TrimSpace(head), path, nil
}

// SplitShell splits a documented command line the way the shell a reader is
// typing into would. Some transcripts' arguments are sentences -- a passage, a
// note, a reason -- so strings.Fields would hand the tool more arguments than
// the reader typed.
//
// The grammar is a NARROW subset of the shell's, and the part it does not read
// it REFUSES by name rather than guessing at: an argv that is not the reader's
// argv is a green that means nothing, and it is invisible in the failure
// message because the line printed there is still the document's.
//
//   - whitespace separates arguments;
//   - a single-quoted run is ONE argument, taken literally: inside it a
//     backslash, a double quote and a `$` are the argument's own characters,
//     exactly as the shell has it;
//   - a double-quoted run is ONE argument, and inside it a backslash escapes
//     only `"`, `\`, `$` and a backquote -- before any other character the
//     backslash is a character of the argument, which is again the shell's
//     rule and was the difference Stella's witness found;
//   - a backslash OUTSIDE quotes, a backquote anywhere, and an unterminated
//     quote of either kind are refused.
//
// NOTHING IS EXPANDED. `$PWD` reaches the Runner as the six characters the
// document writes, because only the caller's package knows what its transcript
// means by them -- cmd/nova-merge's test declares a Path norm for exactly that
// spelling. A transcript that needs a value expanded says so to its Runner; it
// does not get one from here.
func SplitShell(cmd string) ([]string, error) {
	const (
		bare = iota
		inSingle
		inDouble
	)
	var fields []string
	var cur strings.Builder
	state, started := bare, false
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch state {
		case inSingle:
			if c == '\'' {
				state = bare
				continue
			}
			cur.WriteByte(c)
		case inDouble:
			switch {
			case c == '"':
				state = bare
			case c == '`':
				return nil, fmt.Errorf("holds a backquote, which is a command substitution this harness does not run")
			case c == '\\' && i+1 < len(cmd) && isDoubleQuoteEscapable(cmd[i+1]):
				i++
				cur.WriteByte(cmd[i])
			default:
				cur.WriteByte(c)
			}
		default:
			switch c {
			case '\'':
				state, started = inSingle, true
			case '"':
				state, started = inDouble, true
			case '\\':
				return nil, fmt.Errorf("holds a backslash outside quotes; this harness does not read shell escapes, so quote the argument instead")
			case '`':
				return nil, fmt.Errorf("holds a backquote, which is a command substitution this harness does not run")
			case ' ', '\t':
				if started {
					fields = append(fields, cur.String())
					cur.Reset()
					started = false
				}
			default:
				cur.WriteByte(c)
				started = true
			}
		}
	}
	if state != bare {
		return nil, fmt.Errorf("unterminated quote")
	}
	if started {
		fields = append(fields, cur.String())
	}
	return fields, nil
}

// isDoubleQuoteEscapable is the shell's list: these four are the only
// characters a backslash escapes inside double quotes. Before anything else the
// backslash stands for itself, and eating it -- which is what this harness did
// -- silently changes the argument.
func isDoubleQuoteEscapable(c byte) bool {
	return c == '"' || c == '\\' || c == '$' || c == '`'
}

// Norm is one DECLARED normalisation: a value that belongs to the run rather
// than to the document, named so a reader of the test knows what is not being
// compared. It is applied to the document's line and to the tool's line alike,
// and a line the document spells any other way does not match, stays what the
// document wrote, and fails.
type Norm struct {
	// Name is read in a failure message, so it is a noun phrase.
	Name string
	// Re matches the whole value INCLUDING its field name, so that a norm
	// declared for one field cannot quietly swallow another's value. For a norm
	// built by Instant or HexID it is anchored and matched against ONE token of
	// the line at a time -- see field below.
	Re *regexp.Regexp
	// As is what a match becomes on both sides of the comparison.
	As string

	// field is the name Instant and HexID were given. When it is set, this norm
	// is applied token by token and may replace only a COMPLETE
	// `field=value` token of the output grammar. An unanchored pattern is what
	// let `HexID("id", 8)` normalise `parent_id=` and `Instant("created")`
	// normalise `last_created=`: the comparison then found no problem on a line
	// whose undeclared field had changed, which is the opposite of what this
	// type promises. Go's regexp has no look-behind, so the boundary is drawn
	// here rather than in the pattern.
	field string
	// valid, when set, is asked whether the matched text is really a value of
	// the kind the constructor named -- not merely its shape. A value it
	// refuses is LEFT ON THE LINE, so the comparison shows it: a tool printing
	// `created=2026-99-99T99:99:99Z` is a finding, not a run-owned value.
	valid func(value string) bool
	// run says the pattern covers a whole RUN of whitespace-delimited tokens
	// rather than a `field=value` token, and must begin and end on a token
	// boundary. It is the same rule as field, for the norms that have no field
	// name to anchor to: a `version` line's build triple is two tokens and the
	// version word is one, and neither of them may be matched from the middle
	// of a longer token. A norm with neither field nor run is a plain
	// substitution -- Path and Elide, where the caller wrote the pattern and
	// owns its boundaries.
	run bool
}

func (n Norm) apply(line string) string {
	if n.run {
		return n.applyRun(line)
	}
	if n.field == "" {
		return n.Re.ReplaceAllString(line, n.As)
	}
	var out strings.Builder
	for i := 0; i < len(line); {
		if line[i] == ' ' || line[i] == '\t' {
			out.WriteByte(line[i])
			i++
			continue
		}
		j := i
		for j < len(line) && line[j] != ' ' && line[j] != '\t' {
			j++
		}
		out.WriteString(n.replaceToken(line[i:j]))
		i = j
	}
	return out.String()
}

// replaceToken normalises one whitespace-delimited token of a line, or hands it
// back exactly as it stood. Every neighbouring character of the line is kept:
// the only thing that can change is a token this norm names in full.
func (n Norm) replaceToken(token string) string {
	value, named := strings.CutPrefix(token, n.field+"=")
	if !named || !n.Re.MatchString(token) {
		return token
	}
	if n.valid != nil && !n.valid(value) {
		return token
	}
	return n.As
}

// applyRun replaces every match of Re that begins and ends on a whitespace
// boundary, and leaves every match that does not -- so a pattern written for a
// whole token, or a whole run of them, cannot be taken from the middle of a
// longer one. Everything between the matches is copied through untouched.
func (n Norm) applyRun(line string) string {
	var out strings.Builder
	last := 0
	for _, m := range n.Re.FindAllStringIndex(line, -1) {
		start, end := m[0], m[1]
		if !boundary(line, start-1) || !boundary(line, end) {
			continue
		}
		if n.valid != nil && !n.valid(line[start:end]) {
			continue
		}
		out.WriteString(line[last:start])
		out.WriteString(n.As)
		last = end
	}
	out.WriteString(line[last:])
	return out.String()
}

// boundary answers whether index i of line is off the end of it or a space --
// that is, whether a token may start after it or end before it.
func boundary(line string, i int) bool {
	if i < 0 || i >= len(line) {
		return true
	}
	return line[i] == ' ' || line[i] == '\t'
}

// Normalize applies every declared norm to a line, in the order declared.
func Normalize(line string, norms []Norm) string {
	for _, n := range norms {
		line = n.apply(line)
	}
	return line
}

// Instant declares that the named field's value is an RFC3339 instant in UTC --
// the format these binaries print -- and belongs to the run. The value is
// PARSED, not merely shaped: what the tool printed has to be a real instant
// before this norm will agree that it is the run's, because a normalisation
// that erases an impossible date erases the finding with it.
func Instant(field string) Norm {
	return Norm{
		Name:  field + "= (the instant of this run)",
		Re:    regexp.MustCompile(`^` + regexp.QuoteMeta(field) + `=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z$`),
		As:    field + "=<the instant of this run>",
		field: field,
		valid: isInstant,
	}
}

// isInstant answers whether v is an instant and not only the shape of one.
// `2026-99-99T99:99:99Z` has the shape; no month is 99.
func isInstant(v string) bool {
	_, err := time.Parse(time.RFC3339, v)
	return err == nil
}

// HexID declares that the named field's value is n lower-case hex digits and
// belongs to the run. An id a tool derives from its content reproduces exactly
// and should NOT be declared here: it is part of what the document promises.
// EXACTLY n digits: an id of another length is the tool disagreeing with the
// document, and it stays on the line to be compared.
func HexID(field string, n int) Norm {
	return Norm{
		Name:  fmt.Sprintf("%s= (an id of this run, %d hex digits)", field, n),
		Re:    regexp.MustCompile(fmt.Sprintf(`^%s=[0-9a-f]{%d}$`, regexp.QuoteMeta(field), n)),
		As:    field + "=<an id of this run>",
		field: field,
	}
}

// Path declares that a path the document writes stands for a directory this run
// made. `from` is what the document says; both sides are reduced to it, so the
// documented spelling is what a failure message shows.
func Path(from, to string) Norm {
	return Norm{
		Name: fmt.Sprintf("%s (the directory of this run, written %s)", to, from),
		Re:   regexp.MustCompile(regexp.QuoteMeta(to)),
		As:   from,
	}
}

// Elide declares a normalisation this package has no constructor for. The name
// is what a reader of the failing test is told is not compared, so it says what
// the value IS rather than what it looks like.
func Elide(name, pattern, as string) (Norm, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Norm{}, err
	}
	return Norm{Name: name, Re: re, As: as}, nil
}

// Result is what a reader saw when they typed one documented command.
type Result struct {
	Code           int
	Stdout, Stderr string
}

// Lines returns the lines a reader saw. Every command in this repository writes
// its outcome to exactly ONE stream -- results on stdout, refusals on stderr --
// so a step that wrote to both is reported rather than guessed at: nothing here
// can know in which order a terminal interleaved them, and a transcript that
// shows one interleaving is then a promise the tool does not keep.
//
// A blank line the tool printed is KEPT and counted, because the document would
// have to show it.
func (r Result) Lines() ([]string, error) {
	out, errs := splitOutput(r.Stdout), splitOutput(r.Stderr)
	if len(out) > 0 && len(errs) > 0 {
		return nil, fmt.Errorf("wrote to stdout AND stderr; a transcript cannot say in which order a terminal showed them.\nstdout:\n%s\nstderr:\n%s", Block(out), Block(errs))
	}
	if len(errs) > 0 {
		return errs, nil
	}
	return out, nil
}

func splitOutput(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Problem is one disagreement between the document and the run, spelled as the
// sentence a reader of the failing test is shown.
type Problem struct {
	// Step is the documented command the disagreement is under.
	Step Step
	// Message is the whole complaint, already formatted.
	Message string
}

func (p Problem) String() string { return p.Message }
func (p Problem) Error() string  { return p.Message }

// Compare checks one command's whole output against the block written under it:
// same number of lines, same lines, same order, after the declared norms are
// applied to both sides.
//
// NO EXIT CODE IS REQUIRED, and that is deliberate. The transcripts do not write
// one down, and the codes are not one alphabet across these tools: nova-ci exits
// 2 both for a package over its budget -- which its own first run SHOWS as the
// ordinary outcome -- and for an invocation that could not run at all. What a
// reader checks their screen against is the lines, so the lines are what is
// compared, and the code is carried into the failure message as context.
func Compare(s Step, res Result, norms []Norm) []Problem {
	got, err := res.Lines()
	if err != nil {
		return []Problem{{Step: s, Message: fmt.Sprintf("the documented command\n  %s\n%v", s.Line, err)}}
	}
	if len(got) != len(s.Want) {
		return []Problem{{Step: s, Message: fmt.Sprintf(
			"the documented command\n  %s\nprints %d line(s) and the document shows %d (it exited %d).\nwhat the document says:\n%s\nwhat the tool printed:\n%s%s",
			s.Line, len(got), len(s.Want), res.Code, Block(s.Want), Block(got), declared(norms))}}
	}
	var problems []Problem
	for i := range s.Want {
		if Normalize(s.Want[i], norms) == Normalize(got[i], norms) {
			continue
		}
		problems = append(problems, Problem{Step: s, Message: fmt.Sprintf(
			"under\n  %s\nthe document's line %d of %d reads\n  %s\nand the tool printed\n  %s\nRe-run the command and paste what it said.%s",
			s.Line, i+1, len(s.Want), s.Want[i], got[i], declared(norms))})
	}
	return problems
}

// declared names what was not compared, under the lines that disagreed, so that
// a reader never has to guess whether a norm ate the difference.
func declared(norms []Norm) string {
	if len(norms) == 0 {
		return "\nEvery value on the line is compared: this transcript declares no normalisation."
	}
	names := make([]string, 0, len(norms))
	for _, n := range norms {
		names = append(names, "  "+n.Name)
	}
	return "\nCompared after these declared normalisations:\n" + strings.Join(names, "\n")
}

// Runner runs one documented command and returns what the reader would see. The
// caller supplies it because only the caller's package knows how to call its own
// binary in process, and what a `< path` in its transcript is relative to.
type Runner func(s Step) (Result, error)

// Execute runs every step in order -- one sitting, as a reader would -- and
// returns everything the document got wrong. It STOPS at the first command that
// could not be invoked at all, because every line after it would then be
// compared against a state that never happened.
func Execute(steps []Step, run Runner, norms ...Norm) []Problem {
	var problems []Problem
	for _, s := range steps {
		res, err := run(s)
		if err != nil {
			return append(problems, Problem{Step: s, Message: fmt.Sprintf("the documented command\n  %s\ncould not be run: %v", s.Line, err)})
		}
		problems = append(problems, Compare(s, res, norms)...)
	}
	return problems
}

// Block indents a set of lines for a failure message, so the document's block
// and the tool's stand under each other and can be read side by side.
func Block(lines []string) string {
	if len(lines) == 0 {
		return "  (nothing)"
	}
	return "  " + strings.Join(lines, "\n  ")
}

// GoBuild declares the tail of a `version` line -- `<goos>/<goarch> go<version>`
// -- which is the machine the transcript was recorded on rather than anything
// the document promises. The version word before it is NOT covered here; that
// is Version's, and a `version` line wants both declared.
//
// Several sections already paste a real triple (`devel linux/amd64 go1.26.5`),
// so this reduces both sides to the same sentence rather than asking the
// document to carry a placeholder it has no convention for.
//
// The two tokens must stand as two whole tokens: an unanchored pattern here was
// a ReplaceAll over every line of every step of the sitting, which is the thing
// the Norm contract above exists to forbid.
func GoBuild() Norm {
	return Norm{
		Name: "<goos>/<goarch> go<version> (the machine this run is on)",
		Re:   regexp.MustCompile(`[a-z0-9]+/[a-z0-9]+ go[0-9]+(\.[0-9]+)*`),
		As:   "<the machine this run is on>",
		run:  true,
	}
}

// Version declares the version word of a `version` line: the word a build
// stamps itself with.
//
// It has to be declared, and the reason is worth writing down. Under `go test`
// the binary is not stamped and every one of these tools prints `devel`
// (internal/buildinfo's Unknown). A build a reader makes -- `go build
// ./cmd/nova-review && ./nova-review version` -- prints the stamp, today
// `v0.16.0-dev.<base>.0.<date>-<sha>`. So a transcript that pastes `devel` is
// green under `go test` and FALSE for the reader it is written for, and one
// that pastes the stamp is true for the reader and red in the test, and the sha
// in it changes with every commit. The document therefore shows what a reader
// sees, and the word is compared as a SHAPE: a stamp or `devel`, and nothing
// else -- a tool that answered `unknown`, or printed nothing at all, still
// fails.
func Version() Norm {
	return Norm{
		Name: "the version word (`devel` under `go test`, a stamp in a build)",
		Re:   regexp.MustCompile(`devel|v[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+-]*`),
		As:   "<the version of this build>",
		run:  true,
	}
}
