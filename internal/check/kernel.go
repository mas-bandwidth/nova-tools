package check

import (
	"fmt"
	"os"
)

// Kernel enforces a size budget on the kernel file. maxBytes must be
// positive; there is no default budget and zero does not mean unlimited.
// A missing kernel and an empty kernel are both failures, not errors:
// the check ran, and the answer is NO.
func Kernel(file string, maxBytes int64) (measured int64, failures []Failure, err error) {
	if maxBytes <= 0 {
		return 0, nil, fmt.Errorf("max-bytes must be positive, got %d", maxBytes)
	}
	// Lstat, not Stat: a symlinked kernel is refused as not a regular file,
	// never followed — the same posture as attest. See SPEC.md.
	fi, statErr := os.Lstat(file)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return 0, []Failure{{file, "does not exist; a missing kernel is the worst over-budget"}}, nil
		}
		return 0, nil, statErr
	}
	if !fi.Mode().IsRegular() {
		return 0, []Failure{{file, fmt.Sprintf("not a regular file (%s); symlinks are never followed", fi.Mode().Type())}}, nil
	}
	measured = fi.Size()
	if measured == 0 {
		failures = append(failures, Failure{file, "empty (0 bytes); under every budget and still not a kernel"})
	}
	if measured > maxBytes {
		failures = append(failures, Failure{file, fmt.Sprintf(
			"over budget: %d bytes, budget %d, over by %d", measured, maxBytes, measured-maxBytes)})
	}
	return measured, failures, nil
}
