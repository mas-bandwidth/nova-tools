package cardhdr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A card's class is read from its header, never from a step: CLASS: script with a SCRIPT
// program is a script card, no CLASS line is a model card, and a line that cannot be
// read is named with what to write (docs/SPEC-SPRINT.md, the script read).
func TestReadClassReadsTheHeaderAndNeverAStep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, brief string
		want        Class
		why         string
	}{
		{"no class line is a model card", "STATUS: x\n\nTHE TASK. do it.\n", Class{Name: ClassModel}, ""},
		{"a script card names its program and deadline", "STATUS: x\n\nCLASS: script\nSCRIPT: go run ./tools/regen --all\nDEADLINE: finish within 150 minutes\n\nSTEP 1. go.\n",
			Class{Name: ClassScript, Script: "go run ./tools/regen --all", Deadline: 150 * time.Minute}, ""},
		{"a step's own SCRIPT line is the tree's", "CLASS: model\n\nSTEP 1.\nSCRIPT: regex\n", Class{Name: ClassModel}, ""},
		{"a fenced line is prose", "CLASS: script\n```\nSCRIPT: no\n```\nSCRIPT: yes\n", Class{Name: ClassScript, Script: "yes"}, ""},
		{"a script card with no program is refused", "CLASS: script\n", Class{Name: ClassModel}, "CLASS: script names no program; write SCRIPT: <command> (run from the repository root at the start commit)"},
		{"a program on a model card is refused", "SCRIPT: make x\n", Class{Name: ClassModel}, "SCRIPT: make x names a program on a card whose class is model; write CLASS: script or drop the SCRIPT line"},
		{"an unknown class is refused", "CLASS: robot\n", Class{Name: ClassModel}, "CLASS: robot is not model or script"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, why := ReadClass(tc.brief)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.why, why)
		})
	}
}
