// Package binstamp names the state of a binary's file: a loop that runs for days
// (nova-sprint run, nova-swarm member) stamps its own file when it begins and
// again before each tick, and stops when the stamp changed, so its supervisor
// starts the build installed under it.
package binstamp

import (
	"fmt"
	"os"
)

// Of is the file at path as it is on disk now: its path, size and modification
// time; "" when it cannot be read (a binary removed or replaced by a rename on a
// system that reports the old one as gone).
func Of(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s %d %d", path, fi.Size(), fi.ModTime().UnixNano())
}
