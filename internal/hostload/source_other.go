//go:build !darwin && !linux

package hostload

import "runtime"

// localSource reads nothing here: only a given value is known.
func localSource() Source { return Source{NCPU: runtime.NumCPU()} }
