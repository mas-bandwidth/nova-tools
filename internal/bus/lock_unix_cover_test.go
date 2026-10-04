//go:build unix

package bus

import (
	"errors"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLockUnixCoverPlatformTransientLockCollision pins the unix answer to the
// platform's transient-lock-collision question: the filesystem here does not put
// a just-released file into a delete-pending state, so no removal failure is a
// collision to wait out. The main path is an ordinary removal error, which is a
// real failure; the refusal row is a would-block errno, which another platform's
// predicate treats as transient and unix must not.
func TestLockUnixCoverPlatformTransientLockCollision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "main path: an ordinary removal error is not transient", err: errors.New("remove failed")},
		{name: "refusal: a would-block collision is not transient on unix", err: syscall.EWOULDBLOCK},
		{name: "refusal: an access-denied collision is not transient on unix", err: syscall.EACCES},
		{name: "main path: no error is not transient", err: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, platformTransientLockCollision(tc.err), "platformTransientLockCollision(%v) on unix", tc.err)
		})
	}
}
