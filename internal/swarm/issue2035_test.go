package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue2386 reproduces nova-tools#2386 as its title states it: "nova-pulse harvest
// runs the gate itself, outside the wall, and writes the gate line into the PR body; a
// card's self-reported gate is data, never a gate".
//
// THE BREAK the issue measured: card-tools22-fix-2035's RESULT.md said
// `gofmt -l internal/swarm/ -> (no output) OK`; the lane's gate on hulk at the same head
// printed `internal/swarm/issue2035_test.go`. `gofmt -l` exits 0 even when it lists
// files, so a card can report a gate it did not get, and a reader trusts the PR body.
//
// This file is named for that false green: it is the file the card said was formatted
// and the lane said was not. The test drives the gate the harvest runs at the swarm
// layer -- RunLegGate, the machinery nova-pulse harvest stages the branch outside the
// wall and calls -- with a fake runner standing in for go, gofmt and nova-swarm, so no
// test runs the real tools. The fake answers `gofmt -l .` with the listing AND exit 0:
// the exact hole. The gate must still be red.
//
// The card's own RESULT.md -- its `## Gates` row claiming pass, and even a `## Harvest
// gate` section it typed itself -- is handed only to the PR-body composition, never to
// the gate, and it must survive the body verbatim as data while the harvest's own line
// replaces whatever the card typed under the harvest's heading.

// gateFake is the gate's command seam in tests: it records every call's directory and
// argv, and answers what the fixture wants, so the test proves WHICH commands the gate
// ran without running go, gofmt or nova-swarm.
type gateFake struct {
	dir    string
	calls  [][]string
	answer func(argv []string) GateRun
}

func (f *gateFake) run(dir string, argv ...string) GateRun {
	f.dir = dir
	f.calls = append(f.calls, argv)
	return f.answer(argv)
}

// gateCall is the call whose argv begins with want, or nil when the gate made none.
func (f *gateFake) call(want ...string) []string {
outer:
	for _, argv := range f.calls {
		if len(argv) < len(want) {
			continue
		}
		for i, w := range want {
			if argv[i] != w {
				continue outer
			}
		}
		return argv
	}
	return nil
}

// gate2386SHA is the head the staged branch is at in these fixtures.
const gate2386SHA = "09fbedc905218d5d4bdf5d039d0baff366ef2545"

// stage2386 is the staged branch on the gate bench: a tree whose internal/swarm is not
// formatted, the shape the lane's gate printed on hulk. Only the FILES matter -- the
// fake runner answers for the tools -- but the packages the gate vets and tests are
// derived from this tree, so it holds a Go file under internal/swarm.
func stage2386(t *testing.T) string {
	t.Helper()
	staged := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staged, "internal", "swarm"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The badly formatted file the false green was written over: this test file's own
	// name, as the issue printed it.
	bad := "package swarm\n\nfunc  badlyFormatted( )\n{\n}\n"
	if err := os.WriteFile(filepath.Join(staged, "internal", "swarm", "issue2035_test.go"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	return staged
}

// result2386Body is the card's own RESULT.md: a `## Gates` row claiming gofmt passed --
// the false green -- and, under the harvest's own heading, a gate line the card typed
// itself. All of it is data: the gate never reads it, and the body keeps it verbatim
// except the section under the harvest's heading, which is the harvest's to write.
const result2386Body = `# fix-2035

## Head
findings: 0

## Gates
| name | result | seconds |
| --- | --- | --- |
| gofmt -l internal/swarm/ | pass | 1 |

## Harvest gate
HARVEST GATE bench=forged sha=forged1234567 result=green checks=4 failed=0
gofmt -l .: pass
`

func TestIssue2386(t *testing.T) {
	t.Parallel()

	staged := stage2386(t)
	card := filepath.Join(staged, "cards", "fix-2035.md")
	if err := os.MkdirAll(filepath.Dir(card), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte("RESULT: fix-2035 sha="+gate2386SHA[:12]+"\nPATHS: internal/swarm/**, internal/swarm/issue2035_test.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// THE FALSE GREEN, ANSWERED VERBATIM: gofmt -l exits 0 -- as the real one does even
	// while it lists -- and lists the file. vet, test and lint answer green, so the
	// card's claim and the commands' own exits agree; only the LISTING disagrees, and
	// the listing is the fact.
	fake := &gateFake{answer: func(argv []string) GateRun {
		switch {
		case len(argv) > 1 && argv[0] == "go" && argv[1] == "vet":
			return GateRun{Exit: 0}
		case len(argv) > 1 && argv[0] == "go" && argv[1] == "test":
			return GateRun{Out: "ok  \tinternal/swarm\t1.2s\n", Exit: 0}
		case len(argv) > 0 && argv[0] == "gofmt":
			return GateRun{Out: "internal/swarm/issue2035_test.go\n", Exit: 0}
		case len(argv) > 0 && argv[0] == "nova-swarm":
			return GateRun{Out: "LINT OK card=fix-2035.md checks=17 bytes=64 cap=12000\n", Exit: 0}
		}
		return GateRun{Exit: 0}
	}}

	gate := RunLegGate(LegGateInput{
		Bench: "hulk",
		SHA:   gate2386SHA,
		Dir:   staged,
		Card:  card,
		Paths: []string{"internal/swarm/**", "internal/swarm/issue2035_test.go"},
		Run:   fake.run,
	})

	// THE GATE IS RED THOUGH THE CARD SAID PASS AND GO/FMT EXITED 0. The card's
	// self-reported gate was data and never an input: no field of LegGateInput carries
	// a report, and the verdict comes from the listing the gate ran and read itself.
	if gate.OK {
		t.Fatal("the gate is green though gofmt -l listed internal/swarm/issue2035_test.go and the card's own ## Gates row claims pass: a card's self-reported gate is data, never a gate")
	}

	// THE GATE RAN THE COMMANDS ITSELF, outside the wall, in the staged branch: four
	// calls, every one with the staged tree as its directory, and the gofmt call is the
	// one the issue names -- `gofmt -l` -- reading the whole staged tree.
	if got := len(fake.calls); got != 4 {
		t.Fatalf("the gate ran %d commands, want 4 (go vet, go test, gofmt -l, nova-swarm lint --card)", got)
	}
	if fake.dir != staged {
		t.Errorf("the gate ran its commands in %q, want the staged branch %q: the gate runs outside the wall, on the branch harvest staged", fake.dir, staged)
	}
	if argv := fake.call("go", "vet"); argv == nil || !strings.Contains(strings.Join(argv, " "), "./internal/swarm") {
		t.Errorf("go vet did not run over the touched packages derived from the card's PATHS: %v", argv)
	}
	// -count=1: a cached `ok` is a read of a stamp, not a run, and this gate runs.
	if argv := fake.call("go", "test"); argv == nil || !strings.Contains(strings.Join(argv, " "), "-count=1") || !strings.Contains(strings.Join(argv, " "), "./internal/swarm") {
		t.Errorf("go test did not run -count=1 over the touched packages: %v", argv)
	}
	if argv := fake.call("gofmt"); argv == nil || len(argv) != 3 || argv[1] != "-l" || argv[2] != "." {
		t.Errorf("the gofmt check is not `gofmt -l .` over the staged tree: %v", argv)
	}
	if argv := fake.call("nova-swarm", "lint"); argv == nil || len(argv) != 4 || argv[2] != "--card" || argv[3] != card {
		t.Errorf("the lint check is not `nova-swarm lint --card <the card the harvest holds>`: %v", argv)
	}

	// The gofmt row carries the verbatim listing and names the hole: a listing is a red
	// gate whatever gofmt's own exit was.
	var gofmtRow *GateCommand
	for i := range gate.Commands {
		if gate.Commands[i].Name == "gofmt" {
			gofmtRow = &gate.Commands[i]
		}
	}
	if gofmtRow == nil {
		t.Fatal("the gate holds no gofmt command")
	}
	if gofmtRow.Result != "fail" {
		t.Errorf("gofmt -l listed a file and exited 0; the gate's verdict on it is %q, want fail", gofmtRow.Result)
	}
	if gofmtRow.Out != "internal/swarm/issue2035_test.go" {
		t.Errorf("the gofmt row's output is %q, want the verbatim listing \"internal/swarm/issue2035_test.go\"", gofmtRow.Out)
	}
	if !strings.Contains(gofmtRow.Why, "a listing is a red gate whatever gofmt's own exit was") {
		t.Errorf("the gofmt row's why does not name the false-green hole: %q", gofmtRow.Why)
	}

	// THE GATE LINE carries the bench and the sha it ran at, and the counts.
	if want := "HARVEST GATE bench=hulk sha=09fbedc90521 result=red checks=4 failed=1"; LegGateLine(gate) != want {
		t.Errorf("LegGateLine = %q, want %q", LegGateLine(gate), want)
	}

	// THE PR BODY: the card's own report stays verbatim as data, the harvest's gate
	// line goes under the fixed heading, and the section the card typed under the
	// harvest's heading is replaced -- a card-written gate line never survives as the
	// harvest's.
	body := AppendHarvestGate(result2386Body, gate, 0)
	if !strings.Contains(body, "| gofmt -l internal/swarm/ | pass | 1 |") {
		t.Errorf("the card's own ## Gates row is data and must survive the body verbatim:\n%s", body)
	}
	if got := strings.Count(body, HarvestGateHeading); got != 1 {
		t.Errorf("the body carries %d %q headings, want exactly 1 (the harvest's own; the card's typed copy is stripped):\n%s", got, HarvestGateHeading, body)
	}
	if strings.Contains(body, "bench=forged") {
		t.Errorf("a gate line the card typed itself survived under the harvest's heading:\n%s", body)
	}
	if at := strings.Index(body, HarvestGateHeading); at < 0 || !strings.Contains(body[at:], "HARVEST GATE bench=hulk sha=09fbedc90521 result=red checks=4 failed=1") {
		t.Errorf("the harvest's own gate line is not under the fixed heading:\n%s", body)
	}
	if !strings.Contains(body, "gofmt -l .: fail") {
		t.Errorf("the body does not carry the gofmt row with its verdict:\n%s", body)
	}
	if !strings.Contains(body, "internal/swarm/issue2035_test.go\n") {
		t.Errorf("the body does not carry the verbatim listing under the gate row:\n%s", body)
	}
	// A second append rewrites the section, never stacks it: the PR edit path writes
	// the whole body again.
	if again := AppendHarvestGate(body, gate, 0); again != body {
		t.Errorf("a second AppendHarvestGate did not rewrite the section in place:\n%s", again)
	}

	// THE GREEN PATH: nothing listed, every exit 0, and the gate is green -- the same
	// tree answered clean.
	clean := &gateFake{answer: func(argv []string) GateRun {
		if len(argv) > 0 && argv[0] == "gofmt" {
			return GateRun{Exit: 0}
		}
		return GateRun{Out: "ok\n", Exit: 0}
	}}
	green := RunLegGate(LegGateInput{Bench: "hulk", SHA: gate2386SHA, Dir: staged, Card: card,
		Paths: []string{"internal/swarm/**", "internal/swarm/issue2035_test.go"}, Run: clean.run})
	if !green.OK {
		t.Errorf("a gate whose four commands ran and passed is not green: %+v", green)
	}
	if want := "HARVEST GATE bench=hulk sha=09fbedc90521 result=green checks=4 failed=0"; LegGateLine(green) != want {
		t.Errorf("LegGateLine(green) = %q, want %q", LegGateLine(green), want)
	}

	// A LEG WITH NO GO: a card whose PATHS name no Go package has nothing to vet or
	// test. Those two checks are `not run` -- the report vocabulary's own word -- and
	// they do not redden the gate; gofmt and lint still run over the staged tree.
	docs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(docs, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "docs", "EVAL.md"), []byte("docs only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notRun := &gateFake{answer: func(argv []string) GateRun { return GateRun{Exit: 0} }}
	docsGate := RunLegGate(LegGateInput{Bench: "hulk", SHA: gate2386SHA, Dir: docs, Card: card,
		Paths: []string{"docs/EVAL.md", "docs/**"}, Run: notRun.run})
	if !docsGate.OK {
		t.Errorf("a docs-only card's gate is red: %+v", docsGate)
	}
	for _, name := range []string{"vet", "test"} {
		var row *GateCommand
		for i := range docsGate.Commands {
			if docsGate.Commands[i].Name == name {
				row = &docsGate.Commands[i]
			}
		}
		if row == nil || row.Result != "not run" {
			t.Errorf("the docs-only card's %s check is %+v, want not run with the reason named", name, row)
		}
	}
	if notRun.call("gofmt") == nil || notRun.call("nova-swarm") == nil {
		t.Errorf("the docs-only card's gate did not still run gofmt and lint: %v", notRun.calls)
	}

	// A COMMAND THE GATE COULD NOT RUN IS A RED GATE, never a silent pass: "a gate that
	// cannot go green is not a gate".
	broken := &gateFake{answer: func(argv []string) GateRun {
		if len(argv) > 0 && argv[0] == "gofmt" {
			return GateRun{Err: errGate2386CouldNotRun}
		}
		return GateRun{Exit: 0}
	}}
	stuck := RunLegGate(LegGateInput{Bench: "hulk", SHA: gate2386SHA, Dir: staged, Card: card,
		Paths: []string{"internal/swarm/**"}, Run: broken.run})
	if stuck.OK {
		t.Errorf("a gate whose gofmt could not run at all read green: %+v", stuck)
	}
	for _, c := range stuck.Commands {
		if c.Name == "gofmt" && !strings.Contains(c.Why, "could not run") {
			t.Errorf("the could-not-run gofmt row does not say so: %+v", c)
		}
	}

	// NO GUESSED PATHS: a gate handed no staged tree runs nothing -- least of all in
	// the process's own working directory -- and says the refusal in the rows.
	silent := &gateFake{answer: func(argv []string) GateRun { return GateRun{Exit: 0} }}
	empty := RunLegGate(LegGateInput{Bench: "hulk", SHA: gate2386SHA, Run: silent.run})
	if empty.OK {
		t.Errorf("a gate with no staged branch read green: %+v", empty)
	}
	if len(silent.calls) != 0 {
		t.Errorf("a gate with no staged branch ran %d commands; it refuses to guess a directory", len(silent.calls))
	}
	for _, name := range []string{"vet", "test", "gofmt"} {
		var row *GateCommand
		for i := range empty.Commands {
			if empty.Commands[i].Name == name {
				row = &empty.Commands[i]
			}
		}
		if row == nil || !strings.Contains(row.Why, "refuses to guess a directory") {
			t.Errorf("the no-directory %s row does not name the refusal: %+v", name, row)
		}
	}
	var lintRow *GateCommand
	for i := range empty.Commands {
		if empty.Commands[i].Name == "lint" {
			lintRow = &empty.Commands[i]
		}
	}
	if lintRow == nil || lintRow.Result != "not run" || lintRow.Why != "no card named" {
		t.Errorf("the no-card lint row does not say why it did not run: %+v", lintRow)
	}

	// A card with no staged tree still gets its card linted -- the lint reads the card,
	// not the tree -- and it runs in the CARD'S OWN directory, never a guessed one.
	cardOnly := &gateFake{answer: func(argv []string) GateRun { return GateRun{Exit: 0} }}
	noTree := RunLegGate(LegGateInput{Bench: "hulk", SHA: gate2386SHA, Card: card, Run: cardOnly.run})
	if noTree.OK {
		t.Errorf("a gate with no staged branch read green: %+v", noTree)
	}
	if len(cardOnly.calls) != 1 || cardOnly.calls[0][0] != "nova-swarm" {
		t.Errorf("a gate with no tree and a named card ran %v; only the lint may run", cardOnly.calls)
	}
	if cardOnly.dir != filepath.Dir(card) {
		t.Errorf("the card-only lint ran in %q, want the card's own directory %q", cardOnly.dir, filepath.Dir(card))
	}
}

// errGate2386CouldNotRun stands in for a bench that could not run the command at all.
var errGate2386CouldNotRun = &testGateError{}

type testGateError struct{}

func (testGateError) Error() string { return "the bench refused the command" }

// TestIssue2386CRLFBodyStaysVerbatim: AppendHarvestGate promises the card's body stays
// verbatim, so a CRLF (or mixed) body keeps every line ending outside the stripped
// section; only the card-typed harvest section is removed. And a gate with no bench
// prints bench=-, the same fallback the sha has.
func TestIssue2386CRLFBodyStaysVerbatim(t *testing.T) {
	t.Parallel()

	head := "RESULT: card\r\nDONE\r\n\r\n## Gates\r\n| gofmt | pass |\r\nmixed line\n"
	tail := "## After\r\nkept\r\n"
	body := head + "\r\n## Harvest gate\r\nHARVEST GATE bench=forged sha=x result=green checks=0 failed=0\r\n\r\n" + tail
	if got, want := stripHarvestGateSection(body), head+"\r\n"+tail; strings.TrimRight(got, "\r\n") != strings.TrimRight(want, "\r\n") {
		t.Errorf("stripHarvestGateSection changed text outside the section:\n got %q\nwant %q", got, want)
	}

	gate := LegGateResult{SHA: "09fbedc90521aaaa"}
	out := AppendHarvestGate(body, gate, 0)
	if !strings.HasPrefix(out, head+"\r\n"+strings.TrimRight(tail, "\r\n")+"\n\n"+HarvestGateHeading+"\n") {
		t.Errorf("the card's CRLF body did not survive verbatim ahead of the harvest section:\n%q", out)
	}
	if strings.Contains(out, "bench=forged") {
		t.Errorf("the card-typed harvest section survived:\n%q", out)
	}
	if !strings.Contains(out, "HARVEST GATE bench=- sha=09fbedc90521 ") {
		t.Errorf("an empty bench did not print the - fallback:\n%q", out)
	}
	if again := AppendHarvestGate(out, gate, 0); again != out {
		t.Errorf("a second append over a CRLF body did not rewrite in place:\n%q", again)
	}
}

// SPEC-SWARM, issue #2035: a MODE: explore card without TURNS: is refused at
// admission with the remedy naming TURNS:, because managers that run as a
// conversation fill context in ~60 minutes and idle the fleet every time they
// stop to report. A TURNS: budget bounds each explore card's loop.
func TestIssue2035Repro(t *testing.T) {
	t.Parallel()

	// An explore card with no TURNS: line is refused — this is the defect.
	why := admitWhyOf(t, t.TempDir(), "e1", "opencode/deepseek-v4-flash",
		"MODE: explore\nRESULT: find the bug\nSTEP 1 grep\n")
	if why == "" {
		t.Fatal("card-shape: `MODE: explore` requires `TURNS: <n>`; an explore card without a declared turn budget is refused at admission (SPEC-SWARM issue #2035)")
	}
	if !strings.Contains(why, "TURNS:") {
		t.Fatalf("the refusal names the required field %q", why)
	}

	// An explore card WITH a TURNS: line is admitted.
	why = admitWhyOf(t, t.TempDir(), "e2", "opencode/deepseek-v4-flash",
		"MODE: explore\nTURNS: 5\nRESULT: find the bug\nSTEP 1 grep\n")
	if why != "" {
		t.Fatalf("an explore card with TURNS: is admitted, got %q", why)
	}

	// The rule is universal admission, not a DeepSeek practice-17 check: a non-DeepSeek
	// model's explore card without TURNS: is refused too, and admitted once it has one.
	why = admitWhyOf(t, t.TempDir(), "e3", "anthropic/claude-sonnet-4",
		"MODE: explore\nRESULT: find the bug\nSTEP 1 grep\n")
	if !strings.Contains(why, "TURNS:") {
		t.Fatalf("a non-DeepSeek explore card without TURNS: is refused naming the field, got %q", why)
	}
	why = admitWhyOf(t, t.TempDir(), "e4", "anthropic/claude-sonnet-4",
		"MODE: explore\nTURNS: 5\nRESULT: find the bug\nSTEP 1 grep\n")
	if why != "" {
		t.Fatalf("a non-DeepSeek explore card with TURNS: is admitted, got %q", why)
	}

	// A non-explore pipeline card still needs no TURNS:.
	why = admitWhyOf(t, t.TempDir(), "p1", "opencode/deepseek-v4-flash",
		"RESULT: fix the rule\nSTEP 1 read\nSTEP 2 write\nSTEP 3 check\n")
	if why != "" {
		t.Fatalf("a non-explore card has no TURNS: requirement, got %q", why)
	}
}
