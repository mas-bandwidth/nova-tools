package doctor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSelfFixNamesEveryBrokenBinary pins that the self check's fix names every
// binary whose `version` call failed, comma separated in the evidence's order,
// with the one reinstalling command, so one run fixes them all at once
// (docs/SPEC-DOCTOR.md, "self").
func TestSelfFixNamesEveryBrokenBinary(t *testing.T) {
	t.Parallel()
	env := selfRig(t, map[string]string{
		"nova-bus":   "v1.2.0",
		"nova-cairn": "",
		"nova-card":  "",
	})
	r := checkSelf(context.Background(), env)
	assert.Equal(t, Fail, r.Status, r)
	assert.Equal(t, "nova-update apply --file <manifest> nova-cairn, nova-card", r.Fix)
	assert.Contains(t, r.Fix, "nova-cairn")
	assert.Contains(t, r.Fix, "nova-card")
	assert.NotContains(t, r.Fix, "nova-bus")
}
