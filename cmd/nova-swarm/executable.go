package main

import (
	"os"
	"path/filepath"
	"strings"
)

// IS THIS FILE SOMETHING THIS MACHINE CAN EXECUTE? ONE HELPER, TWO PLATFORM BODIES.
//
// The refusal "the harness binary <path> is not executable" was written against the unix
// permission bits: a regular file with none of 0o111 set cannot be exec'd, and saying so
// before the fork is worth more than a fork that fails. That check is kept exactly as it
// was, in executableMode (executable_unix.go).
//
// WINDOWS CANNOT EXPRESS IT. NTFS carries no execute bit, and os.Stat reports mode 0666 for
// every readable file there (0444 when it is read-only), so `Mode().Perm()&0o111 == 0` is
// TRUE of every file on the machine -- a real `.exe` included -- and the check refused every
// harness that existed: on windows-latest every `native` card died with "the harness binary
// C:\...\fake-harness.exe is not executable". The question the unix bit asks has a different
// answer there: the loader decides by the file's EXTENSION, the list in PATHEXT
// (`.COM;.EXE;.BAT;.CMD;...`). executableMode (executable_windows.go) asks that instead, so
// the refusal still names a file the platform really will not run, and never one it will.
//
// Every caller in this package goes through isExecutable; nothing else in it reads 0o111.

// isExecutable reports whether path names a file this machine will run: it exists, it is a
// regular file, and it is executable by the platform's own rule. A directory is never
// executable. A path that cannot be stat'ed is not executable either -- callers that want
// to tell "missing" from "not executable" stat it themselves first, as native does.
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || !fi.Mode().IsRegular() {
		return false
	}
	return executableMode(path, fi)
}

// defaultPathExt is the loader's list when PATHEXT is unset or empty. It is the set the
// windows shell ships with, narrowed to the entries that name a program rather than a
// script interpreter's file (`.VBS`, `.JS` and the rest are left out on purpose: this
// question is asked about a harness binary, and admitting a script here would move a clear
// refusal to a fork that fails).
var defaultPathExt = []string{".COM", ".EXE", ".BAT", ".CMD"}

// executableByExtension is the WINDOWS rule, written where every platform compiles and tests
// it: a file is executable iff its suffix is one of PATHEXT's. `pathext` is the raw value of
// the environment variable, empty for a machine that sets none. Entries are compared
// case-insensitively and a suffix spelled without its dot -- which the loader tolerates -- is
// read the same way. A file with NO extension is refused, which is the answer the loader gives.
//
// It lives here rather than beside the windows body so that darwin and linux can hold the
// windows rule to its contract: the bug this replaces could not be caught on the machines
// this repository is written on.
func executableByExtension(path, pathext string) bool {
	ext := strings.ToUpper(filepath.Ext(path))
	if ext == "" {
		return false
	}
	for _, want := range splitPathExt(pathext) {
		if ext == want {
			return true
		}
	}
	return false
}

// splitPathExt is PATHEXT split into upper-cased suffixes, each with its leading dot, or
// defaultPathExt when the value names none.
func splitPathExt(pathext string) []string {
	if strings.TrimSpace(pathext) == "" {
		return defaultPathExt
	}
	var out []string
	for _, e := range strings.Split(pathext, ";") {
		e = strings.ToUpper(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return defaultPathExt
	}
	return out
}
