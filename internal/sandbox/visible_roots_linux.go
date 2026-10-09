//go:build linux

package sandbox

func systemVisibleRoots() []string { return linuxRoots() }
