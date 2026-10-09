//go:build darwin

package sandbox

func systemVisibleRoots() []string { return fixedDarwinPrefixes }
