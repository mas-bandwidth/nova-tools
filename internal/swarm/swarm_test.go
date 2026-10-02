package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rule 8 and rule 15, at the parser: completion is EVIDENCE, separate from the count, and a
// malformed report yields no findings, ever.
func TestTheParserClassifiesWithoutAnOpinion(t *testing.T) {
	t.Parallel()

	head := "# t\n\n## Head\nfindings: %s\nnotes read: 1\nrepo: o/n\nrev: abc\na paragraph.\n\n## Findings\n%s\n## Per item\n| item | state | evidence |\n| --- | --- | --- |\n| an item | %s | x.go:1 |\n"
	finding := "- something `THE RULE, VERBATIM` internal/x.go:10\n"

	ok := ParseReport([]byte(strings.NewReplacer("%s", "").Replace("") + sprintf(head, "1", finding, "red")))
	if ok.Class != ClassOK || len(ok.FindingLines) != 1 {
		t.Errorf("a head with findings: 1 is ok with one finding line, got %s with %d", ok.Class, len(ok.FindingLines))
	}
	if !ok.FindingLines[0].Quoted() || ok.FindingLines[0].File != "internal/x.go" {
		t.Errorf("a finding with its rule quoted beside a file:line is quoted: %+v", ok.FindingLines[0])
	}

	clean := ParseReport([]byte(sprintf(head, "0", "", "green")))
	assert.Equal(t, ClassClean, clean.Class, "a head with findings: 0 is clean, got %s -- a classifier that failed it would be paying for findings", clean.Class)

	plan := ParseReport([]byte("# t\n\n## Plan\nI will read the files.\n\n## Findings\n" + finding))
	assert.Equal(t, ClassPlanOnly, plan.Class, "a report with no head is plan-only whatever else it holds, got %s", plan.Class)

	malformed := ParseReport([]byte(sprintf(head, "2", finding+finding, "probably")))
	require.Equal(t, ClassMalformed, malformed.Class, "a fourth state word is malformed, got %s", malformed.Class)
	assert.NotZero(t, malformed.MalformedLine, "a malformed report names the line")
	assert.Empty(t, malformed.FindingLines, "a malformed report yields NO finding, ever: a parser that salvaged the lines it liked would be a parser with an opinion")

	// A malformed report whose head says findings: 0 is malformed, not clean.
	got := ParseReport([]byte(sprintf(head, "0", "", "probably")))
	assert.Equal(t, ClassMalformed, got.Class, "a malformed report with findings: 0 is malformed, got %s", got.Class)
	// A head with no findings: line at all is malformed, and names the head's line.
	got = ParseReport([]byte("# t\n\n## Head\nrepo: o/n\n"))
	assert.Equal(t, ClassMalformed, got.Class, "a head with no findings: line is malformed, got %s", got.Class)
}

// RULE 8, VERBATIM (SPEC-SWARM.md:117): the evidence of completion is "the report's `##
// Head`, whose first line is `findings: <n>`". FIRST. A head that opened with `notes read:`
// or `repo:` was read as `ok` here, so the one shape a coordinator classifies on was not
// the shape the parser required, and a report could bury its completion evidence anywhere
// in the head.
func TestTheHeadsFirstLineIsTheFindingCount(t *testing.T) {
	t.Parallel()

	good := "# t\n\n## Head\nfindings: 1\nnotes read: 1\nrepo: o/n\nrev: abc\na paragraph.\n"
	if got := ParseReport([]byte(good)); got.Class != ClassOK {
		t.Fatalf("findings: first is the shape rule 8 names, got %s", got.Class)
	}
	// Every other opener is malformed, and the malformed line is the line that is wrong.
	for _, first := range []string{"notes read: 1", "repo: o/n", "rev: abc", "a paragraph."} {
		body := "# t\n\n## Head\n" + first + "\nfindings: 1\nrepo: o/n\nrev: abc\n"
		got := ParseReport([]byte(body))
		if !assert.Equal(t, ClassMalformed, got.Class, "a head opening %q is malformed, got %s", first, got.Class) {
			continue
		}
		assert.Equal(t, 4, got.MalformedLine, "the malformed line for %q wants 4, got %d", first, got.MalformedLine)
		assert.Empty(t, got.FindingLines, "a malformed report yields NO finding, ever: %q kept %d", first, len(got.FindingLines))
	}
	// AND A BLANK LINE IS A FIRST LINE. The parser skipped whitespace to find the count,
	// so `## Head` followed by an empty line and then `findings: 0` read `clean`: rule 8's
	// "whose FIRST line is `findings: <n>`" had a second reading, and the one shape a
	// coordinator classifies on was again not the shape the parser required (read 4, F7).
	blank := ParseReport([]byte("# t\n\n## Head\n\nfindings: 0\n"))
	assert.Equal(t, ClassMalformed, blank.Class, "a head whose first line is blank is malformed, got %s", blank.Class)
	assert.Equal(t, 4, blank.MalformedLine, "the malformed line is the blank one, 4, got %d", blank.MalformedLine)
}

// RULE 2, VERBATIM (SPEC-SWARM.md:82-84): "Every claim quotes its rule verbatim, beside the
// line. A finding line carries the rule it rests on, quoted word for word, with
// `file:line`, on the same line or the next."
//
// OR THE NEXT. The parser read one line per finding and nothing else, so a finding that
// wrapped its quote onto the following line had no Rule and no File: it was counted
// `unquoted`, it got no de-duplication key, and it was folded into nobody's page. The
// parser discarded the exact evidence rule 2 exists to demand.
func TestAFindingCarriesItsQuoteOnTheSameLineOrTheNext(t *testing.T) {
	t.Parallel()

	head := "# t\n\n## Head\nfindings: %s\nrepo: o/n\nrev: abc\na paragraph.\n\n## Findings\n"

	// The next line carries the quote and the file:line.
	wrapped := ParseReport([]byte(sprintf(head, "1") +
		"- the reclaim line omits a field the grammar names\n" +
		"  `RUN RECLAIM slot=<n> id=<id> end=<...> dest=<done|failed|->` internal/swarm/run.go:147\n"))
	require.Len(t, wrapped.FindingLines, 1, "the continuation is part of the finding above it, not a second finding: got %d", len(wrapped.FindingLines))
	f := wrapped.FindingLines[0]
	assert.True(t, f.Quoted(), "a finding whose quote is on the next line IS quoted (rule 2): %+v", f)
	if f.File != "internal/swarm/run.go" || f.FileLine != "147" {
		t.Errorf("the file:line on the next line is the finding's file:line, got %q:%q", f.File, f.FileLine)
	}
	assert.Equal(t, "RUN RECLAIM slot=<n> id=<id> end=<...> dest=<done|failed|->", f.Rule, "the rule quoted on the next line is the finding's rule, got %q", f.Rule)
	_, ok := f.Key("o/n", "abc")
	assert.True(t, ok, "a finding quoted on the next line has a de-duplication key like any other (rule 15)")

	// ONLY the next. A quote two lines below is not what rule 2 allows, and a finding with
	// no quote is still counted unquoted -- the parser gains no opinion here.
	far := ParseReport([]byte(sprintf(head, "1") +
		"- a claim with no quote beside it\n" +
		"\n" +
		"  `THE RULE` internal/x.go:10\n"))
	if len(far.FindingLines) != 1 || far.FindingLines[0].Quoted() {
		t.Errorf("rule 2 says the same line or the NEXT, and nothing below that: %+v", far.FindingLines)
	}

	// A finding that quoted its rule on its own line is NOT re-read from the line below it.
	own := ParseReport([]byte(sprintf(head, "2") +
		"- a complete claim `THE RULE` internal/a.go:1\n" +
		"  `A DIFFERENT RULE` internal/b.go:2\n" +
		"- a second claim `RULE TWO` internal/c.go:3\n"))
	require.Len(t, own.FindingLines, 2, "two bullets are two findings, got %d", len(own.FindingLines))
	if own.FindingLines[0].File != "internal/a.go" || own.FindingLines[0].Rule != "THE RULE" {
		t.Errorf("a finding complete on its own line keeps its own quote, got %+v", own.FindingLines[0])
	}
	assert.Equal(t, "internal/c.go", own.FindingLines[1].File, "the second bullet is the second finding, got %+v", own.FindingLines[1])
}

// Rule 15's normalization: ./internal/x.go:10 and internal\x.go:10 are one file.
func TestAPathIsNormalizedBeforeTheCompare(t *testing.T) {
	t.Parallel()

	a := parseFinding(1, "something `RULE` ./internal/x.go:10")
	b := parseFinding(1, `something `+"`RULE`"+` internal\x.go:10`)
	ka, oka := a.Key("o/n", "abc")
	kb, okb := b.Key("o/n", "abc")
	if !oka || !okb || ka != kb {
		t.Errorf("the same finding spelled two ways wants one key:\n%q\n%q", ka, kb)
	}
	// Two findings under different revisions stay two findings.
	k, _ := a.Key("o/n", "def")
	assert.NotEqual(t, ka, k, "equal file:line and rule in two revisions are two findings")
	// A report with no rev merges with nothing.
	_, ok := a.Key("o/n", "")
	assert.False(t, ok, "a head without rev: has no de-duplication key")
}

// The harness config carries the variable's NAME and never its value: a value written there
// would be a key at rest in a directory nobody treats as a secret store.
func TestTheHarnessConfigCarriesTheNameNotTheValue(t *testing.T) {
	t.Parallel()

	w := Worker{Provider: "fake", Model: "m", EnvVar: "FAKE_KEY", BaseURL: "https://example.invalid"}
	cfg := string(w.HarnessConfig())
	assert.Contains(t, cfg, "{env:FAKE_KEY}", "the config wants the variable's name as a reference:\n%s", cfg)
	assert.NotContains(t, cfg, "sk-", "the config must never hold a key:\n%s", cfg)
}

// A template is text and nothing else, and `add --template` writes the file budget into the
// condition that names it, so the number in the prompt is the number the machinery holds.
func TestTemplatesCarryTheirConditions(t *testing.T) {
	t.Parallel()

	for _, name := range TemplateNames() {
		body, err := Template(name)
		require.NoError(t, err, "template %s: %v", name, err)
		require.NotEmpty(t, strings.TrimSpace(body), "template %s: %v", name, err)
	}
	wrapped, err := WrapTemplate("read-pr", 12, []byte("read PR #42"))
	require.NoError(t, err)
	for _, want := range []string{"OWED LIST FIRST", "QUOTE EVERY RULE VERBATIM", "THE MOMENT IT EXISTS", "12 files", "read PR #42", "omit progress narration", "severity", "every valid", "never hard-truncate", "## Head", "## Gates", "## One line"} {
		assert.Contains(t, string(wrapped), want, "the wrapped task does not contain %q", want)
	}
	for n := 1; n <= 6; n++ {
		assert.Contains(t, string(wrapped), fmt.Sprintf("%d.", n), "the wrapped read-pr task lost rule %d", n)
	}
	_, err = WrapTemplate("result", 3, nil)
	assert.Error(t, err, "`result` is the report's shape and not a task template")
}

// ISSUE #65: the read-pr template carries a severity floor. The reader states the
// floor in its own output and a finding below it is not emitted, which cuts reader
// output directly. The floor decides which findings are emitted, not how they are
// written, so the verbatim-quote condition stays.
func TestReadPRTemplateCarriesASeverityFloor(t *testing.T) {
	t.Parallel()

	wrapped, err := WrapTemplate("read-pr", 12, []byte("read PR #65"))
	require.NoError(t, err)
	got := string(wrapped)
	for _, want := range []string{"SEVERITY FLOOR", "HIGH", "not emitted", "floor: HIGH"} {
		assert.Contains(t, got, want, "the wrapped read-pr task does not carry the severity floor: missing %q", want)
	}
	assert.Contains(t, got, "QUOTE EVERY RULE VERBATIM", "the severity floor must not replace the verbatim-quote condition")
	assert.Contains(t, got, "7.", "the wrapped read-pr task lost the floor's own rule number")
}

// A worker description is decoded STRICTLY: an unknown field is a refusal, because a
// misspelled field in a file that names a key's location is a silent default.
func TestAnUnknownFieldInAWorkerDescriptionIsARefusal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "w.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"name":"x","key_fil":"/k"}`), 0o644))
	_, problems := LoadWorker(path)
	assert.NotEmpty(t, problems, "an unknown field is a refusal")
	// And a description missing several fields names them all in one run.
	require.NoError(t, os.WriteFile(path, []byte(`{"name":"x"}`), 0o644))
	_, problems = LoadWorker(path)
	assert.GreaterOrEqual(t, len(problems), 4, "one run names every independent problem, got %d: %v", len(problems), problems)
}

func sprintf(format string, args ...any) string {
	out := format
	for _, a := range args {
		out = strings.Replace(out, "%s", a.(string), 1)
	}
	return out
}

// ISSUE #881: a worker description may name its secret instead of a key file on disk
// (docs/SPEC-SWARM.md, the worker description; nova-secrets delivers the value into the
// run's own environment, never a file). The loader accepts `secret`, refuses a
// description with NEITHER key_file nor secret, and refuses one carrying both -- the two
// mechanisms contradict. The old key_file shape stays accepted.
func TestASecretNamedWorkerDescriptionIsAcceptedByTheLoader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	home := filepath.Join(dir, "worker")
	require.NoError(t, os.MkdirAll(home, 0o755))
	write := func(body string) string {
		path := filepath.Join(dir, "w.json")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}
	withSecret := `{"name":"w","provider":"p","model":"m","env_var":"FAKE_KEY","secret":"FAKE_SECRET","usage":"none","harness":"h","harness_args":["run","--model","{model}","--","{prompt}"],"worker_dir":` + strconv.Quote(home) + `,"deadline":"5m"}`
	_, problems := LoadWorker(write(withSecret))
	assert.Empty(t, problems, "a description naming a secret is accepted, got %d problems: %v", len(problems), problems)
	// Neither key_file nor secret: refused.
	neither := strings.Replace(withSecret, `,"secret":"FAKE_SECRET"`, "", 1)
	_, problems = LoadWorker(write(neither))
	assert.NotEmpty(t, problems, "a description with neither key_file nor secret is a refusal")
	keyPath := filepath.Join(dir, "key")
	// Both key_file and secret: refused, because the two mechanisms contradict.
	both := `{"name":"w","provider":"p","model":"m","env_var":"FAKE_KEY","key_file":` + strconv.Quote(keyPath) + `,"secret":"FAKE_SECRET","usage":"none","harness":"h","harness_args":["run","--model","{model}","--","{prompt}"],"worker_dir":` + strconv.Quote(home) + `,"deadline":"5m"}`
	_, problems = LoadWorker(write(both))
	assert.NotEmpty(t, problems, "a description carrying both key_file and secret is a refusal")
	// The old shape: key_file alone stays accepted.
	old := strings.Replace(withSecret, `,"secret":"FAKE_SECRET"`, `,"key_file":`+strconv.Quote(keyPath), 1)
	_, problems = LoadWorker(write(old))
	assert.Empty(t, problems, "the key_file shape stays accepted, got %d problems: %v", len(problems), problems)
}

// ISSUE #881 (secret implies env_var): a worker description that names `secret` but no
// `env_var` loads with env_var defaulting to the secret NAME (docs/SPEC-SWARM.md, the
// worker description: the key is delivered by `nova-secrets exec` under that NAME, and the
// harness config carries the variable's NAME).
func TestSecretImpliesEnvVar(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	home := filepath.Join(dir, "worker")
	require.NoError(t, os.MkdirAll(home, 0o755))
	body := `{"name":"w","provider":"p","model":"m","secret":"MY_SECRET_881","usage":"none","harness":"h","harness_args":["run","--model","{model}","--","{prompt}"],"worker_dir":` + strconv.Quote(home) + `,"deadline":"5m"}`
	path := filepath.Join(dir, "w.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	w, problems := LoadWorker(path)
	require.Empty(t, problems, "a description with secret alone loads, got %d problems: %v", len(problems), problems)
	require.Equal(t, "MY_SECRET_881", w.EnvVar, "env_var defaults to the secret NAME, got %q want %q", w.EnvVar, "MY_SECRET_881")
}
