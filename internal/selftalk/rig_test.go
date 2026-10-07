package selftalk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Rig owns the selftalk test's package setup and checks (STANDARD section 8).
type Rig struct {
	t *testing.T
}

// NewRig binds the package rig to its test (STANDARD section 8).
func NewRig(t *testing.T) *Rig {
	t.Helper()
	return &Rig{t: t}
}

// FlattenWithLines runs the package flattening scenario (STANDARD section 8).
func (r *Rig) FlattenWithLines(text string) (string, []int) {
	r.t.Helper()
	return flattenWithLines(text)
}

// Locations checks that flattening preserves one source location per byte (STANDARD section 8).
func (r *Rig) Locations(text, flattened string, lines []int) {
	r.t.Helper()
	assert.Len(r.t, lines, len(flattened), "text %q", text)
}
