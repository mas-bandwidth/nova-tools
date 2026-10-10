package onboarding

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var documented = []string{
	`$ nova-bus post --topic pit --text "the wall is up"`,
	"BUS POST id=3f2a1b at=2026-09-19T11:02:03Z topic=pit",
	"",
	"$ nova-bus read --topic pit",
	"BUS READ topic=pit n=1",
	"  3f2a1b the wall is up",
	"BUS OK n=1",
}

func theRun() []Result {
	return []Result{
		{Code: 0, Stdout: "BUS POST id=3f2a1b at=2026-09-19T11:02:03Z topic=pit\n"},
		{Code: 0, Stdout: "BUS READ topic=pit n=1\n  3f2a1b the wall is up\nBUS OK n=1\n"},
	}
}

func parse(t *testing.T, lines []string) []Step {
	t.Helper()
	steps, err := Steps("nova-bus", lines)
	require.NoError(t, err)
	return steps
}

func copyWith(edit func(lines []string) []string) []string {
	lines := make([]string, len(documented))
	copy(lines, documented)
	return edit(lines)
}

func compare(t *testing.T, what string, doc []string, run []Result, fields []Field, want int) []Problem {
	t.Helper()
	problems := CompareTranscript(parse(t, doc), run, fields)
	if len(problems) != want {
		require.FailNowf(t, "assertion failed", "%s drew %d problem(s), want %d:\n%s", what, len(problems), want, joinProblems(problems))
	}
	return problems
}

func TestCompareAcceptsTheDocumentTheToolPrints(t *testing.T) {
	t.Parallel()
	compare(t, "the document the tool printed", documented, theRun(), nil, 0)
}

func TestCompareRejectsADroppedLine(t *testing.T) {
	t.Parallel()
	seeded := copyWith(func(lines []string) []string { return append(lines[:6:6], lines[7:]...) })
	problems := compare(t, "a line dropped", seeded, theRun(), nil, 1)
	assert.Contains(t, problems[0].Message, "prints 3 line(s) and the document shows 2")
}

func TestCompareRejectsAnAlteredValue(t *testing.T) {
	t.Parallel()
	seeded := copyWith(func(lines []string) []string { lines[4] = "BUS READ topic=pit n=2"; return lines })
	problems := compare(t, "an altered value", seeded, theRun(), nil, 1)
	assert.Contains(t, problems[0].Message, "n=2")
	assert.Contains(t, problems[0].Message, "n=1")
}

func TestCompareRejectsAMovedLine(t *testing.T) {
	t.Parallel()
	seeded := copyWith(func(lines []string) []string { lines[5], lines[6] = lines[6], lines[5]; return lines })
	problems := compare(t, "a moved line", seeded, theRun(), nil, 2)
	for _, p := range problems {
		assert.Contains(t, p.Message, "the document's line")
	}
}

func TestVolatileFieldOutsideTheTableIsRefused(t *testing.T) {
	t.Parallel()
	problems := compare(t, "an invented volatile field", documented, theRun(), []Field{{Name: "elapsed"}}, 1)
	msg := problems[0].Message
	assert.Contains(t, msg, "elapsed")
	assert.Contains(t, msg, "onboarding.Volatile")
	for _, name := range VolatileNames() {
		assert.Contains(t, msg, name)
	}
}

func TestTheVolatileTableHoldsTheNamedRunOwnedValues(t *testing.T) {
	t.Parallel()
	want := []string{"at", "took", "created", "tmpdir", "id", "sha", "recorded", "branch", "commit"}
	got := VolatileNames()
	require.Equal(t, len(want), len(got))
	for i := range want {
		assert.Equal(t, want[i], got[i])
	}
	for _, f := range Volatile {
		assert.NotEmpty(t, strings.TrimSpace(f.What))
	}
}

func TestAVolatileFieldFromTheTableIsMatchedByShape(t *testing.T) {
	t.Parallel()
	run := theRun()
	run[0].Stdout = "BUS POST id=3f2a1b at=2026-09-19T14:55:01Z topic=pit\n"
	compare(t, "an instant that belongs to the run", documented, run, nil, 1)
	compare(t, "`at` named from the table", documented, run, []Field{{Name: "at"}}, 0)
	run[0].Stdout = "BUS POST id=000000 at=2026-09-19T14:55:01Z topic=pit\n"
	compare(t, "a norm declared for `at` swallowing the id", documented, run, []Field{{Name: "at"}}, 1)
}

func TestTheRunsTemporaryDirectoryIsNamedWithBothItsSpellings(t *testing.T) {
	t.Parallel()
	doc := []string{"$ nova-bus read --root /tmp/nova-bus-1", "BUS READ root=/tmp/nova-bus-1 n=0"}
	run := []Result{{Code: 0, Stdout: "BUS READ root=/var/folders/q5/T/nova-bus-9f3 n=0\n"}}
	compare(t, "the run's directory", doc, run, []Field{{Name: "tmpdir", Doc: "/tmp/nova-bus-1", Run: "/var/folders/q5/T/nova-bus-9f3"}}, 0)
	problems := compare(t, "`tmpdir` without path", doc, run, []Field{{Name: "tmpdir"}}, 1)
	assert.Contains(t, problems[0].Message, "tmpdir")
}

func TestAShapeFieldGivenAPathIsRefused(t *testing.T) {
	t.Parallel()
	compare(t, "a shape field handed a path", documented, theRun(), []Field{{Name: "at", Doc: "/tmp/x", Run: "/tmp/y"}}, 1)
}

func TestCompareRefusesATranscriptWithNoCommand(t *testing.T) {
	t.Parallel()
	problems := CompareTranscript(nil, nil, nil)
	require.Len(t, problems, 1)
}

func TestCompareRefusesARunThatIsShorterThanTheDocument(t *testing.T) {
	t.Parallel()
	problems := compare(t, "a short run", documented, theRun()[:1], nil, 1)
	assert.Contains(t, problems[0].Message, "2 command(s)")
}

func joinProblems(problems []Problem) string {
	var b strings.Builder
	for _, p := range problems {
		b.WriteString(p.Message)
		b.WriteString("\n")
	}
	return b.String()
}

func TestAVolatileEntryNeverSwallowsANeighbouringFieldsValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, field, docValue, runValue, mine string }{
		{name: "at", field: "at", docValue: "2026-09-19T11:02:03Z", runValue: "2026-09-19T14:55:01Z", mine: "2026-09-19T11:02:03Z"},
		{name: "took", field: "took", docValue: "5ms", runValue: "9h", mine: "8ms"},
		{name: "created", field: "created", docValue: "2026-09-16T08:22:37Z", runValue: "2026-09-16T09:00:00Z", mine: "2026-09-16T08:22:37Z"},
		{name: "sha", field: "sha", docValue: "abc1234", runValue: "0000000", mine: "def5678"},
		{name: "commit", field: "at", docValue: "0a19082d2973:", runValue: "5be0c1d2e3f4:", mine: "7c1e2d3f4a5b:"},
		{name: "branch", field: "branch", docValue: "seal/air-GH_TOKEN-20260927-013000", runValue: "seal/air-GH_TOKEN-20260927-020000", mine: "seal/air-GH_TOKEN-20260926-120000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			neighbour := "base_" + tc.field
			doc := []string{"$ nova-bus read", fmt.Sprintf("BUS READ %s=%s %s=%s", neighbour, tc.docValue, tc.field, tc.mine)}
			run := []Result{{Stdout: fmt.Sprintf("BUS READ %s=%s %s=%s\n", neighbour, tc.runValue, tc.field, tc.mine)}}
			compare(t, fmt.Sprintf("declaring %q normalised %s=", tc.name, neighbour), doc, run, []Field{{Name: tc.name}}, 1)
			runOwn := []Result{{Stdout: fmt.Sprintf("BUS READ %s=%s %s=%s\n", neighbour, tc.docValue, tc.field, tc.runValue)}}
			compare(t, fmt.Sprintf("declaring %q did not normalise its own", tc.name), doc, runOwn, []Field{{Name: tc.name}}, 0)
		})
	}
}

func TestTheDirectoryEntryTouchesNothingButThatDirectory(t *testing.T) {
	t.Parallel()
	doc := []string{"$ nova-bus read --root /tmp/nova-bus-1", "BUS READ root=/tmp/nova-bus-1 home=/tmp/nova-bus-1x/cache n=0"}
	run := []Result{{Stdout: "BUS READ root=/run/T/nova-bus-9f3 home=/tmp/nova-bus-1x/cache n=0\n"}}
	field := Field{Name: "tmpdir", Doc: "/tmp/nova-bus-1", Run: "/run/T/nova-bus-9f3"}
	compare(t, "the run's directory was not normalised", doc, run, []Field{field}, 0)
	moved := []Result{{Stdout: "BUS READ root=/run/T/nova-bus-9f3 home=/run/T/nova-bus-9f3x/cache n=0\n"}}
	compare(t, "a longer path was swallowed", doc, moved, []Field{field}, 1)
	descendantDoc := []string{"$ nova-bus read --root /tmp/nova-bus-1", "BUS READ root=/tmp/nova-bus-1/cache n=0"}
	descendantRun := []Result{{Stdout: "BUS READ root=/run/T/nova-bus-9f3/cache n=0\n"}}
	compare(t, "a descendant was not normalised", descendantDoc, descendantRun, []Field{field}, 0)
}

func TestAVolatileEntryLeavesAnInvalidValueOnTheLine(t *testing.T) {
	t.Parallel()
	doc := []string{"$ nova-bus read", "BUS READ took=5ms"}
	run := []Result{{Stdout: "BUS READ took=soon\n"}}
	compare(t, "`took=soon` was normalised", doc, run, []Field{{Name: "took"}}, 1)
}

func TestTookAcceptsEveryGoDuration(t *testing.T) {
	t.Parallel()
	doc := []string{"$ nova-bus read", "BUS READ took=5ms"}
	for _, duration := range []string{"1h3m1ns", "1h3m1us", "1h3m1µs"} {
		t.Run(duration, func(t *testing.T) {
			run := []Result{{Stdout: "BUS READ took=" + duration + "\n"}}
			compare(t, "a duration accepted by time.ParseDuration", doc, run, []Field{{Name: "took"}}, 0)
		})
	}
}

func TestTheRecordedEntryReplacesTheWholeNameOnly(t *testing.T) {
	t.Parallel()
	doc := []string{"$ nova-bus read --org $ORG --repo $ORG/$REPO", "REPO OK org=$ORG repo=$ORG/$REPO other=widgets"}
	fields := []Field{{Name: "recorded", Doc: "$ORG", Run: "acme"}, {Name: "recorded", Doc: "$REPO", Run: "widget"}}
	run := []Result{{Stdout: "REPO OK org=acme repo=acme/widget other=widgets\n"}}
	compare(t, "the recorded names were written as the reader's variables", doc, run, fields, 0)
	longer := []Result{{Stdout: "REPO OK org=acme repo=acme/widget other=widgetz\n"}}
	compare(t, "a longer name was swallowed", doc, longer, fields, 1)
	problems := compare(t, "a recorded name without its run spelling", doc, run, []Field{{Name: "recorded", Doc: "$ORG"}}, 1)
	require.Contains(t, problems[0].Message, "BOTH spellings")
}

func TestTheRecordedEntryRefusesADeclarationThatRewritesTheDocument(t *testing.T) {
	t.Parallel()
	doc := []string{"$ nova-bus read --org $ORG --repo $ORG/$REPO", "IMPORT OK org=$ORG repo=$ORG/$REPO issues=20 state=open"}
	failing := []Result{{Stdout: "IMPORT FAIL org=acme repo=acme/open issues=19 state=open\n"}}
	for _, tc := range []struct {
		name   string
		fields []Field
		want   string
	}{
		{"a Doc that is not a shell variable", []Field{{Name: "recorded", Doc: "OK", Run: "FAIL"}, {Name: "recorded", Doc: "20", Run: "19"}}, "shell variable"},
		{"a recorded name the document prints as written", []Field{{Name: "recorded", Doc: "$ORG", Run: "acme"}, {Name: "recorded", Doc: "$REPO", Run: "open"}}, "appears in the document as written"},
		{"one recorded name under two variables", []Field{{Name: "recorded", Doc: "$ORG", Run: "acme"}, {Name: "recorded", Doc: "$REPO", Run: "acme"}}, "twice"},
		{"one variable declared twice", []Field{{Name: "recorded", Doc: "$ORG", Run: "acme"}, {Name: "recorded", Doc: "$ORG", Run: "other"}}, "twice"},
		{"a variable no command types", []Field{{Name: "recorded", Doc: "$TEAM", Run: "acme"}}, "no documented command types"},
	} {
		problems := CompareTranscript(parse(t, doc), failing, tc.fields)
		if len(problems) == 0 || !strings.Contains(joinProblems(problems), tc.want) {
			assert.Failf(t, "assertion failed", "%s: want a refusal saying %q, got:\n%s", tc.name, tc.want, joinProblems(problems))
		}
	}
}
