package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/decide/questions"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// runClassify is the generic verb of D3 (docs/SPEC-DECIDE.md:757-789): it asks
// any one question over evidence the caller supplies.
//
// It exists BESIDE the flags on the tools that own the acts, and the spec says
// why both: a verb alone can be skipped, and a flag alone hides the question
// inside one tool where no one else can ask or test it. So this is the door a
// stranger with none of our other tools comes through -- a shell script, a
// fixture, a person with a text file and a question.
//
// It prints exactly one line and exits 0 (an answer at or above the floor, or
// a stop the caller acts on), 3 (`unknown`) or 2 (a refusal).
func runClassify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-decide classify", flag.ContinueOnError)
	question := fs.String("question", "", "the question to ask: "+strings.Join(questions.Names(), " | "))
	version := fs.Int("version", 1, "the question's version")
	evidencePath := fs.String("evidence", "", "the evidence, a file or - for standard input")
	pointer := fs.String("pointer", "", "the item's id, for the line and the log")
	deciders := fs.String("decider", decide.DeciderRules, "the chain, in order: rules[,jev|local]")
	floor := fs.Float64("floor", 0.65, "the floor an answer must reach to stand")
	rulesPath := fs.String("rules", "", "the rule table: a TSV of <substring>\\t<member>")
	tamperPath := fs.String("tamper", "", "the tamper pattern table, one pattern a line")
	escalateTo := fs.String("escalate-to", "", "the stronger reader an unknown or tampered item goes to")
	logPath := fs.String("log", "", "append the decision row here (JSON lines; the evidence text is never written)")
	private := fs.Bool("private", false, "the evidence is private: no decider that leaves the machine may see it")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "CLASSIFY", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "CLASSIFY", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*question) == "" {
		return refuse(stderr, "CLASSIFY", "bad-arguments", "--question is required; it is one of "+strings.Join(questions.Names(), ", "))
	}
	q, ok := questions.Lookup(strings.TrimSpace(*question), *version)
	if !ok {
		return refuse(stderr, "CLASSIFY", "no-question", fmt.Sprintf(
			"there is no question %s/v%d; the questions are %s", oneline.Field(*question), *version, strings.Join(questions.Names(), ", ")))
	}
	if strings.TrimSpace(*evidencePath) == "" {
		return refuse(stderr, "CLASSIFY", "bad-arguments", "--evidence is required; refusing to classify an item with no evidence")
	}
	if strings.TrimSpace(*pointer) == "" {
		return refuse(stderr, "CLASSIFY", "bad-arguments", "--pointer is required; an answer nobody can join to an item is not a record")
	}

	// The floor is a confidence, and every other entry point already refuses
	// one that is not (main.go:236, route.go:101, decide/ladder.go:469). This
	// verb is the door a stranger comes through, so it refuses here too, BEFORE
	// the evidence is read and before anybody is asked. NaN compares false
	// against both bounds, so a bare range check gates a decision on a number
	// that is not one; -1 is a floor nothing can fall below and 1.1 one nothing
	// can reach, and both read as a tuned run rather than a mistyped flag.
	if err := decide.ValidFloor(*floor); err != nil {
		return refuse(stderr, "CLASSIFY", "bad-floor",
			fmt.Sprintf("--floor %v is not a confidence; it wants a number between 0 and 1, such as --floor 0.9", *floor))
	}

	text, err := readEvidence(*evidencePath)
	if err != nil {
		return refuse(stderr, "CLASSIFY", "bad-evidence", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	rules, err := readRules(*rulesPath)
	if err != nil {
		return refuse(stderr, "CLASSIFY", "bad-rules", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	tamper, err := readLines(*tamperPath)
	if err != nil {
		return refuse(stderr, "CLASSIFY", "bad-tamper", oneline.Cap(err.Error(), oneline.TailBytes))
	}

	chain, err := buildChain(*deciders, rules)
	if err != nil {
		return refuse(stderr, "CLASSIFY", "bad-decider", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	chain.Escalate = strings.TrimSpace(*escalateTo)
	chain.Tamper = tamper

	class := decide.EvidencePublic
	if *private {
		class = decide.EvidencePrivate
	}
	res := chain.Classify(context.Background(), q, decide.Evidence{
		Text: text, Class: class, Pointer: strings.TrimSpace(*pointer),
	}, *floor)

	if res.Exit() == 2 {
		return refuse(stderr, "CLASSIFY", res.Why, "the answer was not one this question asked for, so it is not a decision")
	}
	fmt.Fprintln(stdout, res.Line(strings.TrimSpace(*pointer)))
	if strings.TrimSpace(*logPath) != "" {
		if err := appendLine(*logPath, res.Row(strings.TrimSpace(*pointer))); err != nil {
			return refuse(stderr, "CLASSIFY", "bad-log", oneline.Cap(err.Error(), oneline.TailBytes))
		}
	}
	return res.Exit()
}

// buildChain walks the caller's --decider list. `rules` is always first whether
// named or not (D2), which the chain itself enforces; naming a decider this
// build does not offer is a refusal rather than a silent omission, because a
// chain that quietly dropped a member would answer by a weaker decider and say
// it was the one you asked for.
func buildChain(list string, rules map[string]string) (decide.Chain, error) {
	chain := decide.Chain{Deciders: []decide.ChainDecider{decide.NewRulesDecider(rules)}}
	for _, name := range strings.Split(list, ",") {
		switch strings.TrimSpace(name) {
		case "", decide.DeciderRules, decide.DeciderNone:
			// rules is already at the head; none adds nobody, which is what it
			// means.
		case decide.DeciderJev, decide.DeciderLocal:
			// The wire client is not built here: a verb that constructed a
			// provider from a flag would need a key to exist for a test to run,
			// and no test in this task dials anything. The seam is
			// decide.JevDecider, and the call site that has a client passes it.
			return chain, fmt.Errorf("the %s decider needs a client this verb does not construct; ask in process through decide.JevDecider, or use --decider rules", strings.TrimSpace(name))
		default:
			return chain, fmt.Errorf("there is no decider %q; the deciders are %s, %s, %s and %s",
				strings.TrimSpace(name), decide.DeciderRules, decide.DeciderJev, decide.DeciderLocal, decide.DeciderNone)
		}
	}
	return chain, nil
}

// readEvidence reads the item's text from a file or standard input.
func readEvidence(path string) (string, error) {
	if strings.TrimSpace(path) == "-" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read the evidence from standard input: %w", err)
		}
		return string(raw), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read the evidence %s: %w", path, err)
	}
	return string(raw), nil
}

// readRules reads the rule table: a TSV of <substring> TAB <member>. It is the
// caller's data, and an empty path is an empty table rather than a refusal --
// a question with no rows is answered by the chain or by `unknown`.
func readRules(path string) (map[string]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	rows := map[string]string{}
	for i, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("rule row %d is not <substring> TAB <member>: %q", i+1, oneline.Cap(line, 120))
		}
		rows[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return rows, nil
}

// readLines reads a data file, dropping blanks and # comments. An empty path is
// no file and no error: the shipped table is used.
func readLines(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out, nil
}

// appendLine appends one row. The file is the tool's own and is never rewritten.
func appendLine(path, row string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("append to %s: %w", path, err)
	}
	defer f.Close()
	_, err = f.WriteString(row + "\n")
	return err
}
