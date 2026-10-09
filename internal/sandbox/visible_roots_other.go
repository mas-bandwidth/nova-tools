//go:build !linux && !darwin

package sandbox

func systemVisibleRoots() []string { return nil }
