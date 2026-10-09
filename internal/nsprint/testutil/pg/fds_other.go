//go:build !linux

package pg

func closeInheritedFDsOnExec() error { return nil }
