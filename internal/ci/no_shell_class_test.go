package ci

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: NO SHELL SCRIPT SHIPS (Glenn, 2026-10-04).
//
// No bash or zsh script in anything the tree ships: every loop or helper is a Go
// verb, and a test's stand-in for a program is a Go test binary. The rule reads
// `git ls-files` and refuses a *.sh, *.zsh or *.bash file, and a file whose
// first line is a shell shebang, unless its path is a line of
// testdata/shell-ledger.txt. The ledger only shrinks: a line whose file is gone,
// or is no longer a shell script, fails too, so it is deleted with the file.
// PowerShell is not a shell of this class (it is not bash, zsh or POSIX sh), and a
// workflow `run:` line is not a file; both are out of the rule.

const shellLedgerPath = "testdata/shell-ledger.txt"

// shellShebangs are the interpreters a shebang may name for the file to be shell.
var shellShebangs = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "ash": true}

// isShellScript is whether a tracked file at p, whose first line is first, is a
// shell script: by extension, or by a shebang naming a shell directly or through env.
func isShellScript(p, first string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".sh", ".zsh", ".bash":
		return true
	}
	rest, ok := strings.CutPrefix(first, "#!")
	if !ok {
		return false
	}
	f := strings.Fields(rest)
	if len(f) > 0 && path.Base(f[0]) == "env" {
		for _, a := range f[1:] {
			if !strings.HasPrefix(a, "-") && !strings.Contains(a, "=") {
				return shellShebangs[a]
			}
		}
		return false
	}
	return len(f) > 0 && shellShebangs[path.Base(f[0])]
}

// shellFindings is every tracked file that is shell and not in the ledger, and
// every ledger line that is stale (its file gone, or not shell), as sorted
// messages. firstLine returns a tracked file's first line, or ok false when the
// file is not on disk.
func shellFindings(tracked, ledger []string, firstLine func(string) (string, bool)) []string {
	inLedger := map[string]bool{}
	for _, l := range ledger {
		inLedger[l] = true
	}
	isTracked := map[string]bool{}
	var out []string
	for _, p := range tracked {
		isTracked[p] = true
		first, ok := firstLine(p)
		if ok && isShellScript(p, first) && !inLedger[p] {
			out = append(out, fmt.Sprintf("%s is a shell script; write it as a Go verb (or a Go test binary for a test's stand-in); the ledger %s does not grow", p, shellLedgerPath))
		}
	}
	for _, l := range ledger {
		first, ok := firstLine(l)
		switch {
		case !isTracked[l] || !ok:
			out = append(out, fmt.Sprintf("%s: ledger line for a file that is gone; delete the line from %s", l, shellLedgerPath))
		case !isShellScript(l, first):
			out = append(out, fmt.Sprintf("%s: ledger line for a file that is no longer shell; delete the line from %s", l, shellLedgerPath))
		}
	}
	sort.Strings(out)
	return out
}

// readShellLedger parses the ledger: one path per line, blank lines and # lines skipped.
func readShellLedger(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(readFile(t, shellLedgerPath), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// TestNoShellScriptsShip walks the tracked files and refuses every shell script
// outside the ledger, and every ledger line that outlived its file.
func TestNoShellScriptsShip(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	raw, err := cmd.Output()
	require.NoError(t, err, "git ls-files in %s", root)
	tracked := strings.FieldsFunc(string(raw), func(r rune) bool { return r == 0 })
	require.NotEmpty(t, tracked, "an empty tree is not a pass")

	first := func(p string) (string, bool) {
		f, err := os.Open(filepath.Join(root, p))
		if err != nil {
			return "", false
		}
		defer func() { _ = f.Close() }()
		line, _ := bufio.NewReaderSize(f, 256).ReadString('\n')
		return strings.TrimRight(line, "\r\n"), true
	}
	assert.Empty(t, shellFindings(tracked, readShellLedger(t), first), "no shell script ships (docs/STANDARD.md, working in the tree)")
}

// TestShellFindingsNamesEveryWayAShellScriptShips pins the rule on fakes: each
// row is a tree the rule must refuse (or pass), so a loosened extension list, a
// missed shebang form or a ledger that stops shrinking turns a row red.
func TestShellFindingsNamesEveryWayAShellScriptShips(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"a/run.sh": "", "a/x.zsh": "", "a/y.bash": "", "a/Z.SH": "",
		"bin/bash-shebang": "#!/bin/bash", "bin/sh-shebang": "#!/bin/sh -e", "bin/env-zsh": "#!/usr/bin/env zsh",
		"bin/env-s-bash": "#!/usr/bin/env -S bash -eu", "bin/space": "#! /bin/sh",
		"ok/main.go": "package main", "ok/tools.ps1": "#!/usr/bin/env pwsh", "ok/py": "#!/usr/bin/env python3",
		"ok/shname":  "#!/bin/shell-like-not",
		"ok/envbare": "#!/usr/bin/env",
	}
	first := func(p string) (string, bool) { s, ok := files[p]; return s, ok }
	var tracked []string
	for p := range files {
		tracked = append(tracked, p)
	}

	t.Run("each shell form is refused and no other file is", func(t *testing.T) {
		t.Parallel()
		var named []string
		for _, f := range shellFindings(tracked, nil, first) {
			named = append(named, strings.SplitN(f, " ", 2)[0])
		}
		assert.Equal(t, []string{"a/Z.SH", "a/run.sh", "a/x.zsh", "a/y.bash", "bin/bash-shebang", "bin/env-s-bash", "bin/env-zsh", "bin/sh-shebang", "bin/space"}, named)
	})
	t.Run("a ledger line allows its file", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, shellFindings([]string{"a/run.sh"}, []string{"a/run.sh"}, first))
	})
	t.Run("a ledger line whose file is gone fails", func(t *testing.T) {
		t.Parallel()
		got := shellFindings([]string{"ok/main.go"}, []string{"a/gone.sh"}, first)
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "a file that is gone")
	})
	t.Run("a ledger line whose file stopped being shell fails", func(t *testing.T) {
		t.Parallel()
		got := shellFindings([]string{"ok/main.go"}, []string{"ok/main.go"}, first)
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "no longer shell")
	})
}
