package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLinesCoverPlanLineRendersTheDryRun: PlanLine is the change a dry run
// would record (docs/SPEC-CONFIG.md, "Lines"); the head names the op, kind,
// name and actor and says nothing was written, then the fields changeFields
// prints for that op: an add's after, a remove's before, a set's changed
// before>after pairs.
func TestLinesCoverPlanLineRendersTheDryRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ch   Change
		want string
	}{
		{
			name: "add prints every after field",
			ch:   Change{Op: OpAdd, Kind: "friend", Name: "rowan", Actor: "rowan", After: map[string]string{"slots": "32", "note": ""}},
			want: "CONFIG DRY-RUN op=add kind=friend name=rowan actor=rowan wrote=nothing note=- slots=32",
		},
		{
			name: "set prints only the changed fields as before>after",
			ch:   Change{Op: OpSet, Kind: "friend", Name: "rowan", Actor: "stella", Before: map[string]string{"slots": "32", "note": ""}, After: map[string]string{"slots": "64", "note": "wider now"}},
			want: `CONFIG DRY-RUN op=set kind=friend name=rowan actor=stella wrote=nothing note=->wider\x20now slots=32>64`,
		},
		{
			name: "remove prints every before field",
			ch:   Change{Op: OpRemove, Kind: "friend", Name: "rowan", Actor: "rowan", Before: map[string]string{"slots": "64"}},
			want: "CONFIG DRY-RUN op=remove kind=friend name=rowan actor=rowan wrote=nothing slots=64",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, PlanLine(tc.ch))
		})
	}
}

// TestLinesCoverPlanLineRefusesToInventAValue: an empty field on the plan
// head prints `-`, never a made-up value, and a change with no fields prints
// the head alone.
func TestLinesCoverPlanLineRefusesToInventAValue(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		"CONFIG DRY-RUN op=- kind=- name=- actor=- wrote=nothing",
		PlanLine(Change{}))
}
