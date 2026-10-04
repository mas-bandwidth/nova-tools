//go:build !unix && !windows

package fuse

import "errors"

// checkBoxAncestors fails closed where security finding 74.6's UID ownership
// boundary cannot be evaluated.
func checkBoxAncestors(string) error {
	return errors.New("cannot verify box ancestor ownership on this platform; run on Windows or a Unix platform with UID ownership support")
}
