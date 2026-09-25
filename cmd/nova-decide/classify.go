package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	deciders := fs.String("decider", decide.DeciderRules, "the chain, in order: rules[,jev|local]; jev and local need --log and --usage")
	floor := fs.Float64("floor", 0.65, "the floor an answer must reach to stand")
	rulesPath := fs.String("rules", "", "the rule table: a TSV of <substring>\\t<member>")
	tamperPath := fs.String("tamper", "", "the tamper pattern table, one pattern a line")
	escalateTo := fs.String("escalate-to", "", "the stronger reader an unknown or tampered item goes to")
	logPath := fs.String("log", "", "append the decision row here (JSON lines; the evidence text is never written)")
	private := fs.Bool("private", false, "the evidence is private: no decider that leaves the machine may see it")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "with jev or local: the environment variable holding the key; never a file, never argv")
	baseURL := fs.String("base-url", decide.DefaultBaseURL, "with jev or local: the Jev endpoint")
	usagePath := fs.String("usage", "", "append what a provider call spent to this usage TSV; required with jev or local")
	recordDir := fs.String("record", "", "write the provider's answer to this directory as a fixture")
	replayDir := fs.String("replay", "", "answer from a fixture --record wrote instead of dialling the provider; no key, no spend")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	observeVerbFlags("classify", fs)
	if err := fs.Parse(args); err != nil {
		if answerHelp(err, stdout, "classify") {
			return 0
		}
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

	// The chain is read BEFORE anything else is: a name this build does not
	// offer is a refusal, and a chain that asks a provider has to account for
	// the call and hold a key before the evidence is even opened.
	asks, err := chainAsks(*deciders)
	if err != nil {
		return refuse(stderr, "CLASSIFY", "bad-decider", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if strings.TrimSpace(*recordDir) != "" && strings.TrimSpace(*replayDir) != "" {
		return refuse(stderr, "CLASSIFY", "bad-flags", "--record dials the provider and --replay does not; pass one of them")
	}
	// Accounting is not optional (route.go, the same rule): a provider call
	// nobody can account for is refused rather than made, so --log and --usage
	// are both named before the key is read.
	if asks {
		var missing []string
		if strings.TrimSpace(*usagePath) == "" {
			missing = append(missing, "--usage")
		}
		if strings.TrimSpace(*logPath) == "" {
			missing = append(missing, "--log")
		}
		if len(missing) > 0 {
			return refuse(stderr, "CLASSIFY", "no-accounting", fmt.Sprintf(
				"a jev call must be accounted for: %s missing; pass %s, or --decider rules to answer by the table alone with no call to account for",
				strings.Join(missing, " and "), remedyFor(missing)))
		}
	}
	fixture := fixturePath(*replayDir, q, *pointer)
	var client decide.Decider
	var spent *spendMeter
	if asks {
		if strings.TrimSpace(*replayDir) != "" {
			client = replayDecider{path: fixture}
		} else {
			opened, err := deciderOpener(*baseURL, *keyEnv)
			if err != nil {
				return refuse(stderr, "CLASSIFY", "no-key", oneline.Cap(err.Error(), oneline.TailBytes))
			}
			spent = &spendMeter{inner: opened}
			client = spent
			if strings.TrimSpace(*recordDir) != "" {
				client = recordDecider{inner: spent, path: fixturePath(*recordDir, q, *pointer)}
			}
		}
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

	chain, err := buildChain(*deciders, rules, client)
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

	// The spend is written BEFORE any refusal is returned: a call that was made
	// was paid for, and an answer outside the set does not unspend it. A
	// classification that made no call writes no row, because an empty row
	// would claim one was made.
	if spent != nil && spent.usage.Calls > 0 {
		unit := decide.Unit{ID: strings.TrimSpace(*pointer)}
		reg, _ := decide.LoadRegistry("") // the embedded ladder's rate table; none is a dash and a NOTE
		if err := appendUsageAs("CLASSIFY", *usagePath, decide.RouteResult{Unit: unit.ID, Usage: spent.usage}, unit, reg, stderr); err != nil {
			return refuse(stderr, "CLASSIFY", "bad-usage", oneline.Cap(err.Error(), oneline.TailBytes))
		}
	}
	if spent != nil && spent.recordErr != nil {
		return refuse(stderr, "CLASSIFY", "bad-record", oneline.Cap(spent.recordErr.Error(), oneline.TailBytes))
	}

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

// chainAsks reads the caller's --decider list and reports whether any member
// asks a provider. Naming a decider this build does not offer is a refusal
// rather than a silent omission, because a chain that quietly dropped a member
// would answer by a weaker decider and say it was the one you asked for.
func chainAsks(list string) (bool, error) {
	asks := false
	for _, name := range strings.Split(list, ",") {
		switch strings.TrimSpace(name) {
		case "", decide.DeciderRules, decide.DeciderNone:
		case decide.DeciderJev, decide.DeciderLocal:
			asks = true
		default:
			return false, fmt.Errorf("there is no decider %q; the deciders are %s, %s, %s and %s",
				strings.TrimSpace(name), decide.DeciderRules, decide.DeciderJev, decide.DeciderLocal, decide.DeciderNone)
		}
	}
	return asks, nil
}

// buildChain walks the caller's --decider list. `rules` is always first whether
// named or not (D2), which the chain itself enforces. `jev` and `local` ask
// through the one client the verb opened from --key-env and --base-url (or the
// fixture --replay names); `local` differs only in what it may see.
func buildChain(list string, rules map[string]string, client decide.Decider) (decide.Chain, error) {
	chain := decide.Chain{Deciders: []decide.ChainDecider{decide.NewRulesDecider(rules)}}
	if _, err := chainAsks(list); err != nil {
		return chain, err
	}
	for _, name := range strings.Split(list, ",") {
		switch strings.TrimSpace(name) {
		case decide.DeciderJev, decide.DeciderLocal:
			if client == nil {
				return chain, fmt.Errorf("the %s decider has no client; pass --key-env and --base-url, or --decider rules", strings.TrimSpace(name))
			}
			chain.Deciders = append(chain.Deciders, decide.JevDecider{Client: client, Local: strings.TrimSpace(name) == decide.DeciderLocal})
		}
	}
	return chain, nil
}

// spendMeter stands between the verb and the provider and counts what every
// call made through it spent: the calls, the tokens per counter as the
// provider reported them, and whether a call failed. It is the fact the usage
// row is written from, so the row is never inferred from the answer line.
type spendMeter struct {
	inner     decide.Decider
	usage     decide.RouteUsage
	recordErr error
}

func (m *spendMeter) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	start := time.Now()
	answers, usage, err := m.inner.Decide(ctx, state, qs)
	m.usage.Calls++
	m.usage.Ms += int(time.Since(start).Milliseconds())
	m.usage.HasMs = true
	if usage.HasInput {
		m.usage.InputTokens += usage.InputTokens
		m.usage.HasInput = true
	}
	if usage.HasOutput {
		m.usage.OutputTokens += usage.OutputTokens
		m.usage.HasOutput = true
	}
	if err != nil {
		m.usage.Failed = true
	}
	return answers, usage, err
}

// classifyFixture is one recorded provider answer: what came back and what it
// reported spending. The key and the evidence are never in it; the state the
// provider saw carries a fresh nonce, so the fixture is keyed by the question
// and the item's pointer instead.
type classifyFixture struct {
	Answers map[string]decide.Answer `json:"answers"`
	Usage   decide.Usage             `json:"usage"`
}

// fixturePath is <dir>/<question>-v<n>-<pointer>.json, the pointer reduced to
// file-safe characters. An empty dir is no fixture.
func fixturePath(dir string, q questions.Question, pointer string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, strings.TrimSpace(pointer))
	return filepath.Join(dir, fmt.Sprintf("%s-v%d-%s.json", q.Name, q.Version, safe))
}

// recordDecider asks through the metered client and writes the answer as a
// fixture. A fixture that cannot be written is recorded on the meter and
// refused after the spend is accounted for.
type recordDecider struct {
	inner *spendMeter
	path  string
}

func (r recordDecider) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	answers, usage, err := r.inner.Decide(ctx, state, qs)
	if err != nil {
		return answers, usage, err
	}
	raw, merr := json.MarshalIndent(classifyFixture{Answers: answers, Usage: usage}, "", "  ")
	if merr == nil {
		if merr = os.MkdirAll(filepath.Dir(r.path), 0o755); merr == nil {
			merr = os.WriteFile(r.path, append(raw, '\n'), 0o644)
		}
	}
	if merr != nil {
		r.inner.recordErr = fmt.Errorf("cannot write the fixture %s: %w", r.path, merr)
	}
	return answers, usage, nil
}

// replayDecider answers from a fixture --record wrote. It dials nothing and
// spends nothing, so it reports no usage; a missing fixture is a provider
// error, which the chain walks past to unknown rather than answering from
// nothing.
type replayDecider struct{ path string }

func (r replayDecider) Decide(_ context.Context, _ string, _ map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return nil, decide.Usage{}, fmt.Errorf("decide: no fixture to replay: %w", err)
	}
	var f classifyFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, decide.Usage{}, fmt.Errorf("decide: fixture %s: %w", r.path, err)
	}
	return f.Answers, decide.Usage{}, nil
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
