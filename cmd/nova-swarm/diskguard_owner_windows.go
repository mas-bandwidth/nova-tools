//go:build windows

package main

import "os"

// fileOwner says nothing on Windows, where disk-guard refuses to run.
func fileOwner(os.FileInfo) (int, bool) { return 0, false }
