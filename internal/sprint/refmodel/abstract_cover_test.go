package refmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestAbstractCoverJudgment covers judgment (abstract.go): a normal open
// note maps to its model judgment on its key's subject (the main path), a
// repeat note's type is trimmed of the repeat suffix first, the tick's
// no-member judgment lands on the subject "fleet", and an overdue note is
// refused, so the model's open and acknowledged sets never hold it.
func TestAbstractCoverJudgment(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		typ  string
		key  string
		want Judgment
		ok   bool
	}{
		"failed on a primary":      {typ: sprint.NWorkFailed, key: "n1|p1", want: Judgment{JFailed, "p1"}, ok: true},
		"repeat suffix is trimmed": {typ: sprint.NWorkFailed + sprint.NRepeatSuffix, key: "n1|p1", want: Judgment{JFailed, "p1"}, ok: true},
		"no member names the fleet": {
			typ: sprint.NNoMember, key: "n2|p7", want: Judgment{JNoMember, "fleet"}, ok: true,
		},
		"overdue is refused": {typ: sprint.NOverdue, key: "n3|p2", want: Judgment{}, ok: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := judgment(sprint.Open{Key: tc.key, Note: sprint.Note{Type: tc.typ}})
			assert.Equal(t, tc.ok, ok, "judgment(%q|%q)", tc.key, tc.typ)
			assert.Equal(t, tc.want, got, "judgment(%q|%q)", tc.key, tc.typ)
		})
	}
}

// TestAbstractCoverJudgmentType covers JudgmentType (abstract.go): the
// model's name of an engine judgment type the model holds (the main path),
// and a type the model has no name for, which comes back "other:" and the
// engine's words.
func TestAbstractCoverJudgmentType(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		in   string
		want string
	}{
		"work failed": {in: sprint.NWorkFailed, want: JFailed},
		"read broken": {in: sprint.NReadBroken, want: JBroken},
		"ci red":      {in: sprint.NCIRed, want: JCI},
		"sprint done": {in: sprint.NSprintDone, want: JDone},
		"no member":   {in: sprint.NNoMember, want: JNoMember},
		"type the model has no name for": {
			in:   "a judgment the model has no name for",
			want: "other:" + "a judgment the model has no name for",
		},
		"empty type": {in: "", want: "other:"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, JudgmentType(tc.in), "JudgmentType(%q)", tc.in)
		})
	}
}
