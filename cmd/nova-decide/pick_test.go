package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// pickFake answers the effort question with one choice and confidence and never
// dials anything.
type pickFake struct {
	choice string
	conf   float64
	err    error
	usage  decide.Usage
	calls  int
}

func (f *pickFake) Decide(_ context.Context, _ string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.calls++
	if f.err != nil {
		return nil, decide.Usage{}, f.err
	}
	q, ok := qs[decide.EffortQuestion]
	if !ok || len(q.Choice) == 0 {
		return nil, decide.Usage{}, nil
	}
	return map[string]decide.Answer{decide.EffortQuestion: {Type: "choice", Choice: f.choice, Confidence: f.conf}}, f.usage, nil
}

func usePickFake(t *testing.T, f *pickFake) {
	t.Helper()
	was := deciderOpener
	deciderOpener = func(string, string) (decide.Decider, error) { return f, nil }
	t.Cleanup(func() { deciderOpener = was })
}

// With --no-jev the verb answers from the table alone: no key, no network, one
// line, deterministic.
func TestPickNoJevAnswersFromTheTable(t *testing.T) {
	var first, second, stderr bytes.Buffer
	if code := run([]string{"pick", "--kind", "spec", "--no-jev"}, &first, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, first.String(), stderr.String())
	}
	if code := run([]string{"pick", "--kind", "spec", "--no-jev"}, &second, &stderr); code != 0 {
		t.Fatalf("second run exit = %d", code)
	}
	if first.String() != second.String() {
		t.Errorf("the table alone is deterministic:\n%s\n%s", first.String(), second.String())
	}
	line := strings.TrimSuffix(first.String(), "\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("exactly one line: %q", first.String())
	}
	for _, want := range []string{"PICK ", "kind=spec", "effort=opus", "model=claude-opus-5-5", "version=1", "conf=-", "source=table"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line is missing %q: %s", want, line)
		}
	}
}

// Jev picks the effort, and the model id follows from the table.
func TestPickJevChoosesTheEffort(t *testing.T) {
	usePickFake(t, &pickFake{choice: "fable", conf: 0.97, usage: decide.Usage{InputTokens: 41, HasInput: true, OutputTokens: 7, HasOutput: true}})
	usage := filepath.Join(t.TempDir(), "usage.tsv")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pick", "--kind", "design", "--usage", usage}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	line := stdout.String()
	if !strings.Contains(line, "effort=fable") || !strings.Contains(line, "model=claude-fable-5-1") || !strings.Contains(line, "source=jev") {
		t.Errorf("jev's pick is the answer: %s", line)
	}
	raw, err := os.ReadFile(usage)
	if err != nil {
		t.Fatalf("no usage was written: %v", err)
	}
	if !strings.Contains(string(raw), "tokens_in\t41") && !strings.Contains(string(raw), "41") {
		t.Errorf("the usage row carries the spend: %s", raw)
	}
}

// A pick the provider names outside the table falls back to the table default,
// and the call is still accounted for.
func TestPickJevFallsBackToTheTable(t *testing.T) {
	usePickFake(t, &pickFake{choice: "vibes", conf: 0.99})
	usage := filepath.Join(t.TempDir(), "usage.tsv")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pick", "--kind", "rebase", "--usage", usage}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "effort=haiku") || !strings.Contains(stdout.String(), "source=table") {
		t.Errorf("the table's default stands: %s", stdout.String())
	}
}

// A provider error is evidence too: the table answer stands, the call is a row.
func TestPickJevProviderErrorFallsBack(t *testing.T) {
	usePickFake(t, &pickFake{err: errors.New("provider down")})
	usage := filepath.Join(t.TempDir(), "usage.tsv")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pick", "--kind", "rebase", "--usage", usage}, &stdout, &stderr); code != 0 {
		t.Fatalf("a provider error falls back to the table, exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "source=table") {
		t.Errorf("the table answer stands: %s", stdout.String())
	}
}

// Below the floor the pick is a suggestion: exit 3, never an authorization.
func TestPickBelowTheFloorExits3(t *testing.T) {
	usePickFake(t, &pickFake{choice: "sonnet", conf: 0.4})
	usage := filepath.Join(t.TempDir(), "usage.tsv")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pick", "--kind", "new-verb", "--usage", usage, "--floor", "0.9"}, &stdout, &stderr); code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout=%q)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "conf=0.40") {
		t.Errorf("the line carries the confidence: %s", stdout.String())
	}
}

// Every wrong shape is a refusal on stderr at exit 2.
func TestPickRefuses(t *testing.T) {
	for name, args := range map[string][]string{
		"no kind":        {"pick"},
		"unknown kind":   {"pick", "--kind", "vibes", "--no-jev"},
		"bad table":      {"pick", "--kind", "rebase", "--no-jev", "--table", filepath.Join(t.TempDir(), "nope.json")},
		"bad floor":      {"pick", "--kind", "rebase", "--no-jev", "--floor", "2"},
		"lines and kind": {"pick", "--kind", "rebase", "--lines", "x"},
		"no accounting":  {"pick", "--kind", "rebase"},
		"stray argument": {"pick", "--kind", "rebase", "--no-jev", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stdout=%q stderr=%q)", name, code, stdout.String(), stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: a refusal belongs on stderr, stdout had %q", name, stdout.String())
		}
		if !strings.Contains(stderr.String(), "REFUSED reason=") {
			t.Errorf("%s: the refusal must say REFUSED reason=...: %q", name, stderr.String())
		}
	}
}

// A file of kinds picks every one from the table alone, then the total.
func TestPickLinesTotals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kinds.txt")
	body := "rebase\nspec\ndesign\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pick", "--lines", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("three picks and a total, got %d:\n%s", len(lines), stdout.String())
	}
	if lines[3] != "PICK TOTAL kinds=3 version=1" {
		t.Errorf("the closing line is the total: %q", lines[3])
	}
}

// The 30-line fixture renders byte-for-byte to the committed golden.
func TestPickFixture30(t *testing.T) {
	dir := filepath.Join("testdata", "pick-lines-30.txt")
	golden, err := os.ReadFile(filepath.Join("testdata", "pick-lines-30.golden"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pick", "--lines", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if stdout.String() != string(golden) {
		t.Fatalf("the fixture render differs from the golden:\n--- got ---\n%s--- want ---\n%s", stdout.String(), golden)
	}
}
