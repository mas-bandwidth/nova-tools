//go:build windows

package main

import "os"

// executableMode is the windows rule. There is no execute bit to read (see executable.go),
// so the file's extension is what decides, against this machine's PATHEXT. The FileInfo is
// not consulted: its permission bits are the same 0666 for every readable file on the volume.
func executableMode(path string, _ os.FileInfo) bool {
	return executableByExtension(path, os.Getenv("PATHEXT"))
}
