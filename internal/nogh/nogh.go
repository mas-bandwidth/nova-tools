// Package nogh installs a refusing gh command first on a child shell's PATH.
// The child returns requested forge actions to its coordinator and uses git
// for branch transport where permitted. This shim makes that routing visible
// when the child invokes gh by name.
//
// This is a command-routing aid, not a security boundary: an absolute path
// bypasses PATH lookup. Callers remain responsible for the child's permissions
// and credentials.
package nogh

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// Name is the file the shim is written as.
const Name = "gh"

// Refusal is the one line the refusing gh prints on stderr. It carries no
// single quote, so Script can quote it for sh as written.
const Refusal = "gh: REFUSED GitHub CLI is unavailable in this child shell; " +
	"return the requested forge action in your report to the coordinator; use git for branch transport where permitted"

// Script is the refusing gh's whole text.
func Script() string {
	return "#!/bin/sh\n# the refusing gh (nova-tools #3600): GitHub is a git remote only (#3594)\necho '" + Refusal + "' >&2\nexit 2\n"
}

// Install puts the refusing gh at <dir>/gh (making dir) and returns its path.
// The file is written under a unique name and renamed into place, so two
// children starting at once never exec a half-written file. On windows no
// shim is written and the path is "".
func Install(dir string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("the gh shim directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, Name)
	if err := atomicfile.Write(path, []byte(Script()), 0o755, atomicfile.ExactMode()); err != nil {
		return "", fmt.Errorf("the gh shim %s: %w", path, err)
	}
	return path, nil
}
