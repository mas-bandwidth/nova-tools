//go:build !windows

package winacl

import (
	"errors"
	"runtime"
)

// OwnerOnly is the Windows ACL check; elsewhere the POSIX mode is the check, and a caller
// that asks this here is refused, never passed.
func OwnerOnly(string) error {
	return errors.New("no Windows access list on " + runtime.GOOS + "; the file mode is the check here")
}
