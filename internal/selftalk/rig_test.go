package selftalk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rig is the test harness for selftalk tests.
type Rig struct {
	t *testing.T
}

// NewRig returns a new test rig.
func NewRig(t *testing.T) *Rig {
	return &Rig{t: t}
}

// Scan checks that Scan returns the expected number of claims.
func (r *Rig) Scan(got []Claim, want int) {
	r.t.Helper()
	require.Len(r.t, got, want)
}

// ScanEmpty checks that Scan found nothing.
func (r *Rig) ScanEmpty(got []Claim) {
	r.t.Helper()
	assert.Empty(r.t, got)
}

// ScanNonEmpty checks that Scan found something.
func (r *Rig) ScanNonEmpty(got []Claim) {
	r.t.Helper()
	assert.NotEmpty(r.t, got)
}

// Installation checks that ScanInstallation returns the expected number of findings.
func (r *Rig) Installation(got []Installation, want int) {
	r.t.Helper()
	require.Len(r.t, got, want)
}

// InstallationEmpty checks that ScanInstallation found nothing.
func (r *Rig) InstallationEmpty(got []Installation) {
	r.t.Helper()
	assert.Empty(r.t, got)
}

// InstallationNonEmpty checks that ScanInstallation found something.
func (r *Rig) InstallationNonEmpty(got []Installation) {
	r.t.Helper()
	assert.NotEmpty(r.t, got)
}

// Shape checks that the first finding has the expected shape.
func (r *Rig) Shape(got []Installation, want Shape) {
	r.t.Helper()
	if assert.NotEmpty(r.t, got) {
		assert.Equal(r.t, want, got[0].Shape)
	}
}

// Line checks that the first finding has the expected line number.
func (r *Rig) Line(got []Installation, want int) {
	r.t.Helper()
	if assert.NotEmpty(r.t, got) {
		assert.Equal(r.t, want, got[0].Line)
	}
}
