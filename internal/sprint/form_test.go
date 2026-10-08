package sprint_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A card's form is checked by the machine at the finish, never by a reader
// (docs/SPEC-SPRINT.md, the report's form). ReadForm reads the FORM: block; CheckForm
// holds a report to every rule; DecideForm is the finish's decision (a miss refuses,
// three misses on one attempt finish it FAIL); ReportSkeleton is the fill-in report a
// brief carries under FILL IN. The grammar is swarm's, in the package the card runner
// already reads a brief's header with; the sprint side is the read's note and finding,
// which this file pins too. Every case here is pure: no socket, no store, no clock.

const formBrief = `STATUS: nova-sprint card s1-1, epoch 0: the work
FORM: outbox/s1-1~0/REPORT.md
line 1: Verdict: LAND
heading: 2 Gate
item: gate, result
each item: one command
count: 1..3 items
PATHS: internal/a/a.go
GOAL

Do the thing.
`

const formReportPass = `Verdict: LAND

## Gate

1. gate: build result: green command: go build ./... gave BUILD_OK
2. gate: vet result: green command: go vet ./... gave VET_OK
`

func TestParseFormReadsEveryRuleKind(t *testing.T) {
	t.Parallel()
	f, err := swarm.ReadForm(formBrief)
	require.NoError(t, err)
	assert.Equal(t, "outbox/s1-1~0/REPORT.md", f.Path)
	require.Len(t, f.Rules, 5)
	assert.Equal(t, "line1", f.Rules[0].Kind)
	assert.Equal(t, "Verdict: LAND", f.Rules[0].Text)
	assert.Equal(t, "heading", f.Rules[1].Kind)
	assert.Equal(t, 2, f.Rules[1].Level)
	assert.Equal(t, "Gate", f.Rules[1].RE)
	assert.Equal(t, []string{"gate", "result"}, f.Rules[2].Field)
	assert.Equal(t, "command", f.Rules[3].Noun)
	assert.Equal(t, 1, f.Rules[4].Min)
	assert.Equal(t, 3, f.Rules[4].Max)
}

func TestParseFormRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	const head = "STATUS: card s1-1\n"
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{"no form line", "PATHS: none\n", "no FORM: line"},
		{"no path", "FORM:\nline 1: Verdict: LAND\nPATHS: none\n", "names no report file"},
		{"no rule", "FORM: outbox/r.md\nPATHS: none\n", "carries no rule"},
		{"bad level", "FORM: outbox/r.md\nheading: 9 Gate\nPATHS: none\n", "is no heading level"},
		{"bad regex", "FORM: outbox/r.md\nheading: 2 [\nPATHS: none\n", "is no regular expression"},
		{"empty field", "FORM: outbox/r.md\nitem: gate, , result\nPATHS: none\n", "empty field"},
		{"bad noun", "FORM: outbox/r.md\neach item: two command\nPATHS: none\n", "wants `one <noun>`"},
		{"bad count", "FORM: outbox/r.md\ncount: 3\nPATHS: none\n", "wants `<min>..<max> items`"},
		{"bad count order", "FORM: outbox/r.md\ncount: 4..2 items\nPATHS: none\n", "min <= max"},
		{"unknown rule", "FORM: outbox/r.md\nline one: x\nPATHS: none\n", "is no rule the grammar names"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := swarm.ReadForm(head + tc.block)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCheckFormPassesAReportThatHoldsEveryRule(t *testing.T) {
	t.Parallel()
	f, err := swarm.ReadForm(formBrief)
	require.NoError(t, err)
	assert.Empty(t, swarm.CheckForm(f, formReportPass))
}

func TestCheckFormNamesEachMissAsTheFinishPrintsIt(t *testing.T) {
	t.Parallel()
	f, err := swarm.ReadForm(formBrief)
	require.NoError(t, err)
	const file = "outbox/s1-1~0/REPORT.md"
	cases := []struct {
		name   string
		report string
		want   string
	}{
		{
			"line 1",
			"Verdict: HOLD\n\n## Gate\n\n1. gate: build result: green command: go build ./...\n",
			"FORM: outbox/s1-1~0/REPORT.md:1: line 1: Verdict: LAND (the first line is \"Verdict: HOLD\")",
		},
		{
			"heading",
			"Verdict: LAND\n\n## Findings\n\n1. gate: build result: green command: go build ./...\n",
			"FORM: outbox/s1-1~0/REPORT.md:1: heading: 2 Gate (no level-2 heading matches \"Gate\")",
		},
		{
			"item field",
			"Verdict: LAND\n\n## Gate\n\n1. gate: build command: go build ./...\n",
			"FORM: outbox/s1-1~0/REPORT.md:5: item: gate, result (item 1 has no \"result\" field)",
		},
		{
			"each item",
			"Verdict: LAND\n\n## Gate\n\n1. gate: build result: green\n",
			"FORM: outbox/s1-1~0/REPORT.md:5: each item: one command (item 1 names 0 command)",
		},
		{
			"count",
			"Verdict: LAND\n\n## Gate\n\n1. gate: a result: green command: go build ./...\n2. gate: b result: green command: go build ./...\n3. gate: c result: green command: go build ./...\n4. gate: d result: green command: go build ./...\n",
			"FORM: outbox/s1-1~0/REPORT.md:1: count: 1..3 items (the report has 4 items)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			misses := swarm.CheckForm(f, tc.report)
			require.Len(t, misses, 1)
			assert.Equal(t, tc.want, misses[0].String(file))
		})
	}
}

func TestCheckFormCapsTheMissesAtTen(t *testing.T) {
	t.Parallel()
	f, err := swarm.ReadForm(formBrief)
	require.NoError(t, err)
	var b strings.Builder
	b.WriteString("Verdict: HOLD\n\n## Findings\n")
	for i := 1; i <= 8; i++ {
		b.WriteString("\n" + string(rune('0'+i)) + ". nothing here\n")
	}
	assert.Len(t, swarm.CheckForm(f, b.String()), 10)
}

// The finish spends no attempt on a form miss: the first two misses refuse, and the third
// on one attempt finishes it FAIL with the misses.
func TestTheFinishRefusesAFormMissTwiceThenFailsTheThird(t *testing.T) {
	t.Parallel()
	f, err := swarm.ReadForm(formBrief)
	require.NoError(t, err)
	bad := "Verdict: HOLD\n\n## Findings\n"
	for prior, want := range []swarm.FormOutcome{swarm.FormRefuse, swarm.FormRefuse, swarm.FormFail} {
		out, misses := swarm.DecideForm(prior, f, bad)
		assert.Equal(t, want, out, "prior=%d", prior)
		assert.NotEmpty(t, misses, "prior=%d", prior)
	}
	out, misses := swarm.DecideForm(0, f, formReportPass)
	assert.Equal(t, swarm.FormPass, out)
	assert.Empty(t, misses)
}

func TestReportSkeletonIsTheFillInReportForAForm(t *testing.T) {
	t.Parallel()
	f, err := swarm.ReadForm(formBrief)
	require.NoError(t, err)
	got := swarm.ReportSkeleton(f)
	assert.True(t, strings.HasPrefix(got, "Verdict: LAND\n"), "the first line:\n%s", got)
	assert.Contains(t, got, "\n## Gate\n")
	assert.Contains(t, got, "\n1. gate: result: command: \n", "the labelled fields empty, in order:\n%s", got)
	assert.NotContains(t, got, "\n2. ", "count: 1..3 items renders the fewest items:\n%s", got)
}

// The sprint side: a card with a FORM: block tells its reader the form passed the lint,
// and a broken read that names the form's own report file is refused.
func TestAReadOfAFormCardIsToldTheFormPassedAndAFormOnlyFindingIsRefused(t *testing.T) {
	t.Parallel()
	const plain = "STATUS: card s1-1\nPATHS: internal/a/a.go\nGOAL\n\nwork\n"
	note := sprint.FormReadNote(formBrief)
	assert.Contains(t, note, "judge the substance")
	assert.NotContains(t, note, "line by line")
	assert.Empty(t, sprint.FormReadNote(plain))

	assert.Empty(t, sprint.FormFinding(formBrief, "internal/sprint/form.go:12 the check is wrong"))
	assert.Contains(t, sprint.FormFinding(formBrief, "outbox/s1-1~0/REPORT.md:5 the heading is wrong"), "report's form")
	assert.Empty(t, sprint.FormFinding(plain, "outbox/s1-1~0/REPORT.md:5 the heading is wrong"))
}

// The finish reads the report as the blob at --head. A missing file in the
// process directory is not that report, so the miss names the commit.
func TestHeadFileMissNamesTheCommitNotADirectoryFile(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	got := sprint.HeadFileMiss(head, errors.New("fatal: path 'outbox/s1-1~0/REPORT.md' does not exist in '"+head+"'"))
	assert.Contains(t, got, "not in "+head)
	assert.Contains(t, got, "process directory is not the report")
	assert.NotContains(t, got, "os.ReadFile")
	none := sprint.HeadFileMiss("", errors.New("the finish names no commit (--head)"))
	assert.Contains(t, none, "no commit")
	assert.Contains(t, none, "process directory is not the report")
}
