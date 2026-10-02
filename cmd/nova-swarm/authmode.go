package main

import "os"

// AUTH FILE MODES ARE ASKED OF THE PLATFORM, NOT OF THE UNIX BITS.
//
// The current --auth native path requires an owner-only auth source and an owner-only copy that does
// not end 0600 (copyAuth). On a machine that carries unix permission bits a file written 0600
// reads back 0600, and each rule does what it says. NTFS carries no such bits: os.Stat reports
// 0666 for every readable file there (0444 when it is read-only), so a source written 0600
// read 0666 and BOTH checks refused the very file the caller had chmod'ed. On windows-latest
// the native --auth path treats a readable file as platform-valid when Windows cannot expose
// Unix permission bits, instead of rejecting a copy it cannot verify.
// mode (internal/swarm/key.go, which already answers nothing on windows).
//
// The platform answer is written where every platform compiles and tests it, so darwin and
// linux hold the windows answer to its contract: the bug it replaces could not be seen from
// those machines.

// authModeWiderThanOwner is the SOURCE rule. It reports whether an auth file's own mode lets
// anybody but its owner read it: on a platform that can express the bits, any group or other
// bit is the leak the native path refuses. Windows has no such bits to read, so no file is a
// refusal there.
func authModeWiderThanOwner(goos string, mode os.FileMode) bool {
	if goos == "windows" {
		return false
	}
	return mode.Perm()&0o077 != 0
}

// authModeNotOwnerOnly is the COPY rule. It reports whether a file this run just wrote did
// not end owner-only 0600. On a platform with the bits that is a refusal; Windows reports the
// same 0666 for every readable file, so the copy cannot be shown to be owner-only and is not
// refused for a mode the platform cannot express.
func authModeNotOwnerOnly(goos string, mode os.FileMode) bool {
	if goos == "windows" {
		return false
	}
	return mode.Perm() != 0o600
}
