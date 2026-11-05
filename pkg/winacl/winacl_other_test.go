//go:build !windows

package winacl

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestOwnerOnlyRefusesOffWindows: off Windows the mode is the check, and asking the ACL
// check is refused, never passed.
func TestOwnerOnlyRefusesOffWindows(t *testing.T) {
	t.Parallel()
	assert.Error(t, OwnerOnly("/etc/hostname"))
}
