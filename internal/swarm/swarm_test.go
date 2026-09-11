package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE KEY IS READ AS DATA. One line, the two strips, whitespace out, and a second line that
// nothing may read -- the file somebody appends to tomorrow.
func TestTheKeyFileIsReadAsOneLine(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ name, body, want string }{
		{"a bare key", "sk-abc123\n", "sk-abc123"},
		{"a NAME= line", "FAKE_KEY=sk-abc123\n", "sk-abc123"},
		{"an exported line", "export FAKE_KEY=sk-abc123\n", "sk-abc123"},
		{"a second line", "sk-abc123\nsk-THE-WRONG-ONE\n", "sk-abc123"},
		{"trailing whitespace", "  sk-abc123  \n", "sk-abc123"},
	} {
		path := filepath.Join(dir, "key")
		if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadKey(path, "FAKE_KEY")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: read %q, want %q", c.name, got, c.want)
		}
	}
	// An empty file is a refusal carrying the command that writes one, and the refusal
	// never prints the path's contents.
	path := filepath.Join(dir, "empty")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadKey(path, "FAKE_KEY")
	if err == nil {
		t.Fatal("an empty key file is a refusal")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("the refusal wants the command that writes the file: %v", err)
	}
}

// Rule 8 and rule 15, at the parser: completion is EVIDENCE, separate from the count, and a
// malformed report yields no findings, ever.
func TestTheParserClassifiesWithoutAnOpinion(t *testing.T) {
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
	if clean.Class != ClassClean {
		t.Errorf("a head with findings: 0 is clean, got %s -- a classifier that failed it would be paying for findings", clean.Class)
	}

	plan := ParseReport([]byte("# t\n\n## Plan\nI will read the files.\n\n## Findings\n" + finding))
	if plan.Class != ClassPlanOnly {
		t.Errorf("a report with no head is plan-only whatever else it holds, got %s", plan.Class)
	}

	malformed := ParseReport([]byte(sprintf(head, "2", finding+finding, "probably")))
	if malformed.Class != ClassMalformed {
		t.Fatalf("a fourth state word is malformed, got %s", malformed.Class)
	}
	if malformed.MalformedLine == 0 {
		t.Error("a malformed report names the line")
	}
	if len(malformed.FindingLines) != 0 {
		t.Error("a malformed report yields NO finding, ever: a parser that salvaged the lines it liked would be a parser with an opinion")
	}

	// A malformed report whose head says findings: 0 is malformed, not clean.
	if got := ParseReport([]byte(sprintf(head, "0", "", "probably"))); got.Class != ClassMalformed {
		t.Errorf("a malformed report with findings: 0 is malformed, got %s", got.Class)
	}
	// A head with no findings: line at all is malformed, and names the head's line.
	if got := ParseReport([]byte("# t\n\n## Head\nrepo: o/n\n")); got.Class != ClassMalformed {
		t.Errorf("a head with no findings: line is malformed, got %s", got.Class)
	}
}

// RULE 8, VERBATIM (SPEC-SWARM.md:117): the evidence of completion is "the report's `##
// Head`, whose first line is `findings: <n>`". FIRST. A head that opened with `notes read:`
// or `repo:` was read as `ok` here, so the one shape a coordinator classifies on was not
// the shape the parser required, and a report could bury its completion evidence anywhere
// in the head.
func TestTheHeadsFirstLineIsTheFindingCount(t *testing.T) {
	good := "# t\n\n## Head\nfindings: 1\nnotes read: 1\nrepo: o/n\nrev: abc\na paragraph.\n"
	if got := ParseReport([]byte(good)); got.Class != ClassOK {
		t.Fatalf("findings: first is the shape rule 8 names, got %s", got.Class)
	}
	// Every other opener is malformed, and the malformed line is the line that is wrong.
	for _, first := range []string{"notes read: 1", "repo: o/n", "rev: abc", "a paragraph."} {
		body := "# t\n\n## Head\n" + first + "\nfindings: 1\nrepo: o/n\nrev: abc\n"
		got := ParseReport([]byte(body))
		if got.Class != ClassMalformed {
			t.Errorf("a head opening %q is malformed, got %s", first, got.Class)
			continue
		}
		if got.MalformedLine != 4 {
			t.Errorf("the malformed line for %q wants 4, got %d", first, got.MalformedLine)
		}
		if len(got.FindingLines) != 0 {
			t.Errorf("a malformed report yields NO finding, ever: %q kept %d", first, len(got.FindingLines))
		}
	}
	// AND A BLANK LINE IS A FIRST LINE. The parser skipped whitespace to find the count,
	// so `## Head` followed by an empty line and then `findings: 0` read `clean`: rule 8's
	// "whose FIRST line is `findings: <n>`" had a second reading, and the one shape a
	// coordinator classifies on was again not the shape the parser required (read 4, F7).
	blank := ParseReport([]byte("# t\n\n## Head\n\nfindings: 0\n"))
	if blank.Class != ClassMalformed {
		t.Errorf("a head whose first line is blank is malformed, got %s", blank.Class)
	}
	if blank.MalformedLine != 4 {
		t.Errorf("the malformed line is the blank one, 4, got %d", blank.MalformedLine)
	}
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
	head := "# t\n\n## Head\nfindings: %s\nrepo: o/n\nrev: abc\na paragraph.\n\n## Findings\n"

	// The next line carries the quote and the file:line.
	wrapped := ParseReport([]byte(sprintf(head, "1") +
		"- the reclaim line omits a field the grammar names\n" +
		"  `RUN RECLAIM slot=<n> id=<id> end=<...> dest=<done|failed|->` internal/swarm/run.go:147\n"))
	if len(wrapped.FindingLines) != 1 {
		t.Fatalf("the continuation is part of the finding above it, not a second finding: got %d", len(wrapped.FindingLines))
	}
	f := wrapped.FindingLines[0]
	if !f.Quoted() {
		t.Errorf("a finding whose quote is on the next line IS quoted (rule 2): %+v", f)
	}
	if f.File != "internal/swarm/run.go" || f.FileLine != "147" {
		t.Errorf("the file:line on the next line is the finding's file:line, got %q:%q", f.File, f.FileLine)
	}
	if f.Rule != "RUN RECLAIM slot=<n> id=<id> end=<...> dest=<done|failed|->" {
		t.Errorf("the rule quoted on the next line is the finding's rule, got %q", f.Rule)
	}
	if _, ok := f.Key("o/n", "abc"); !ok {
		t.Error("a finding quoted on the next line has a de-duplication key like any other (rule 15)")
	}

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
	if len(own.FindingLines) != 2 {
		t.Fatalf("two bullets are two findings, got %d", len(own.FindingLines))
	}
	if own.FindingLines[0].File != "internal/a.go" || own.FindingLines[0].Rule != "THE RULE" {
		t.Errorf("a finding complete on its own line keeps its own quote, got %+v", own.FindingLines[0])
	}
	if own.FindingLines[1].File != "internal/c.go" {
		t.Errorf("the second bullet is the second finding, got %+v", own.FindingLines[1])
	}
}

// Rule 15's normalization: ./internal/x.go:10 and internal\x.go:10 are one file.
func TestAPathIsNormalizedBeforeTheCompare(t *testing.T) {
	a := parseFinding(1, "something `RULE` ./internal/x.go:10")
	b := parseFinding(1, `something `+"`RULE`"+` internal\x.go:10`)
	ka, oka := a.Key("o/n", "abc")
	kb, okb := b.Key("o/n", "abc")
	if !oka || !okb || ka != kb {
		t.Errorf("the same finding spelled two ways wants one key:\n%q\n%q", ka, kb)
	}
	// Two findings under different revisions stay two findings.
	if k, _ := a.Key("o/n", "def"); k == ka {
		t.Error("equal file:line and rule in two revisions are two findings")
	}
	// A report with no rev merges with nothing.
	if _, ok := a.Key("o/n", ""); ok {
		t.Error("a head without rev: has no de-duplication key")
	}
}

// The prompt is this tool's output, and every sentence in it is a failure from the record.
func TestThePromptCarriesEverySentenceTheRecordBought(t *testing.T) {
	prompt := string(Prompt(PromptInput{
		ID: "job-1", JobDir: "/j", Deadline: 20 * time.Minute, Files: 7, Tokens: "100000",
		Board: "mas-bandwidth/schema#876", Task: []byte("the task"),
		NoteFile: "/j/note", Result: "/j/RESULT.md", ResultTmp: "/j/RESULT.md.tmp",
	}))
	for _, want := range []string{
		"/j",                        // the job directory
		"1200 SECONDS",              // the deadline, in seconds, held by machinery
		"7 FILES",                   // the file budget
		"REFUSED READ OR WRITE",     // the sandbox sentence, reads AND writes
		"DOES NOT END THIS RUN",     // ... and what a refusal means
		"THE MOMENT IT EXISTS",      // append as found
		"RESULT.md.tmp",             // publication by rename, with the command
		"mv /j/RESULT.md.tmp",       // ... spelled out
		"findings: 0",               // 0 is a complete answer
		"THERE IS NO BUS",           // no bus
		"Do not loop, poll or wait", // no polling
		"ONE PROCESS",               // one job is one process
		"/j/note",                   // the note file
		"notes read:",               // and the count that is mandatory
		"mas-bandwidth/schema#876",  // the board, and dup: before filing
		"100000",                    // the token budget
		"the task",                  // and the task itself
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not contain %q", want)
		}
	}
}

// The harness config carries the variable's NAME and never its value: a value written there
// would be a key at rest in a directory nobody treats as a secret store.
func TestTheHarnessConfigCarriesTheNameNotTheValue(t *testing.T) {
	w := Worker{Provider: "fake", Model: "m", EnvVar: "FAKE_KEY", BaseURL: "https://example.invalid"}
	cfg := string(w.HarnessConfig())
	if !strings.Contains(cfg, "{env:FAKE_KEY}") {
		t.Errorf("the config wants the variable's name as a reference:\n%s", cfg)
	}
	if strings.Contains(cfg, "sk-") {
		t.Errorf("the config must never hold a key:\n%s", cfg)
	}
}

// A template is text and nothing else, and `add --template` writes the file budget into the
// condition that names it, so the number in the prompt is the number the machinery holds.
func TestTemplatesCarryTheirConditions(t *testing.T) {
	for _, name := range TemplateNames() {
		body, err := Template(name)
		if err != nil || strings.TrimSpace(body) == "" {
			t.Fatalf("template %s: %v", name, err)
		}
	}
	wrapped, err := WrapTemplate("read-pr", 12, []byte("read PR #42"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"OWED LIST FIRST", "QUOTE EVERY RULE VERBATIM", "THE MOMENT IT EXISTS", "12 files", "read PR #42"} {
		if !strings.Contains(string(wrapped), want) {
			t.Errorf("the wrapped task does not contain %q", want)
		}
	}
	if _, err := WrapTemplate("result", 3, nil); err == nil {
		t.Error("`result` is the report's shape and not a task template")
	}
}

// A worker description is decoded STRICTLY: an unknown field is a refusal, because a
// misspelled field in a file that names a key's location is a silent default.
func TestAnUnknownFieldInAWorkerDescriptionIsARefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.json")
	if err := os.WriteFile(path, []byte(`{"name":"x","key_fil":"/k"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, problems := LoadWorker(path); len(problems) == 0 {
		t.Error("an unknown field is a refusal")
	}
	// And a description missing several fields names them all in one run.
	if err := os.WriteFile(path, []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, problems := LoadWorker(path)
	if len(problems) < 4 {
		t.Errorf("one run names every independent problem, got %d: %v", len(problems), problems)
	}
}

func sprintf(format string, args ...any) string {
	out := format
	for _, a := range args {
		out = strings.Replace(out, "%s", a.(string), 1)
	}
	return out
}

// RULE 11, VERBATIM (SPEC-SWARM.md:148-155): a process of the job's own group that outlives
// the job is a background subtask the prompt forbids, and the line carries `background=<n>`.
//
// ONE SURVIVOR IS ONE. The dispatcher asked the group twice -- alive before the reap, alive
// after it -- and added one for each yes, so a single backgrounded child could be reported
// as two (read 4, F8). The number a person reads tomorrow is a count of processes.
func TestOneSurvivorIsCountedOnce(t *testing.T) {
	for _, c := range []struct {
		name                      string
		aliveBefore, survivedReap bool
		want                      int
	}{
		{"the group was empty", false, false, 0},
		{"alive before the reap, gone after it", true, false, 1},
		{"alive before the reap and after it", true, true, 1},
		{"seen only by the reap", false, true, 1},
	} {
		if got := survivorsSeen(c.aliveBefore, c.survivedReap); got != c.want {
			t.Errorf("%s: background=%d, want %d", c.name, got, c.want)
		}
	}
}
