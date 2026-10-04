//go:build !darwin && !linux && !windows

package fuse

// checkBoxAncestors is disabled on unsupported platforms because their file
// ownership representation has not been defined by security finding 74.6.
func checkBoxAncestors(string) error { return nil }
