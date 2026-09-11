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
