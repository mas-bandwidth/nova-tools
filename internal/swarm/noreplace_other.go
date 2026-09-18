//go:build !windows

package swarm

import "errors"

// errNoNoReplaceRename is returned when the filesystem does not support hard links
// and this build has no native create-exclusive rename.
var errNoNoReplaceRename = errors.New("this build has no OS no-replace rename")

func noReplaceRename(from, to string) error {
	return errNoNoReplaceRename
}
