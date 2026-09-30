//go:build shippedsmoke

package shippedsmoke

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// underCI says whether the run is a CI run: GitHub Actions sets both variables.
func underCI(getenv func(string) string) bool {
	return getenv("CI") == "true" || getenv("GITHUB_ACTIONS") == "true"
}

// resolveShippedBin reads the binary under test from NOVA_SHIPPED_BIN. The
// variable unset is fatal under CI: a certification smoke that smoked nothing
// must be RED, never a skip that reads as a pass. Outside CI (a developer's
// `go test -tags shippedsmoke` with no binary at hand) the test skips, loudly.
func resolveShippedBin(getenv func(string) string) (bin string, fatal, skip bool) {
	bin = getenv("NOVA_SHIPPED_BIN")
	if bin != "" {
		return bin, false, false
	}
	if underCI(getenv) {
		return "", true, false
	}
	return "", false, true
}

// shippedBin is the binary under test: fatal under CI when there is none, a
// loud skip outside it.
func shippedBin(t *testing.T) string {
	t.Helper()
	bin, fatal, skip := resolveShippedBin(os.Getenv)
	switch {
	case fatal:
		require.Fail(t, "NOTHING WAS SMOKED: NOVA_SHIPPED_BIN names no shipped nova-check binary, and under CI that is a red, never a skip; the certification smoke step must pass the variable")
	case skip:
		t.Skip("SKIPPED, NOTHING WAS SMOKED: NOVA_SHIPPED_BIN names no shipped nova-check binary (outside CI this skips)")
	}
	return bin
}

// TestShippedBinUnsetIsRedUnderCIAndASkipOutsideIt pins the rule above over
// fake environments: the variable set is never a problem, unset is fatal when
// CI or GITHUB_ACTIONS is "true" and a skip otherwise.
func TestShippedBinUnsetIsRedUnderCIAndASkipOutsideIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		env         map[string]string
		bin         string
		fatal, skip bool
	}{
		{"set, no CI", map[string]string{"NOVA_SHIPPED_BIN": "/x/nova-check"}, "/x/nova-check", false, false},
		{"set, under CI", map[string]string{"NOVA_SHIPPED_BIN": "/x/nova-check", "CI": "true"}, "/x/nova-check", false, false},
		{"unset, no CI", map[string]string{}, "", false, true},
		{"unset, CI=true", map[string]string{"CI": "true"}, "", true, false},
		{"unset, GITHUB_ACTIONS=true", map[string]string{"GITHUB_ACTIONS": "true"}, "", true, false},
		{"unset, CI=false", map[string]string{"CI": "false"}, "", false, true},
		{"unset under CI, empty value", map[string]string{"CI": "true", "NOVA_SHIPPED_BIN": ""}, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin, fatal, skip := resolveShippedBin(func(k string) string { return tc.env[k] })
			assert.Equal(t, tc.bin, bin)
			assert.Equal(t, tc.fatal, fatal, "fatal")
			assert.Equal(t, tc.skip, skip, "skip")
		})
	}
}

// posixOnly skips the fixtures that need an executable bit, chmod refusing a
// read, mkfifo or symlinks: four assertions of the seven cannot run on Windows,
// and the other three run there, so the binary is smoked on the one platform
// whose defects the portable steps exist to find.
func posixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only: needs an executable bit, chmod refusing a read, mkfifo or symlinks")
	}
}

// runBin runs the shipped binary and returns its combined output and exit code.
func runBin(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	require.NoError(t, err, "%s %s", bin, strings.Join(args, " "))
	return "", -1
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

// linkTo makes a symlink to dir in a directory of its own and returns its path.
func linkTo(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))
	return link
}

// TestShipped is the seven assertions, each a subtest, so every one runs and
// reports even when an earlier one fails: seven red subtests are seven facts,
// one red step and six skipped is one fact and a queue.
func TestShipped(t *testing.T) {
	t.Parallel()
	bin := shippedBin(t)

	t.Run("a clean prose tree passes, and is actually walked", func(t *testing.T) {
		t.Parallel()
		tree := t.TempDir()
		write(t, tree, "NOTES.md", "# notes\n")
		write(t, tree, "plain.txt", "plain text\n")
		out, rc := runBin(t, bin, "nocode", "--dir", tree)
		require.Equal(t, 0, rc, "want exit 0: %s", out)
		// files=2 is the load-bearing half. `nocode` prints NOCODE OK and exits
		// 0 for a tree it never opened, so exit 0 alone cannot tell a pass from
		// a walk that scanned nothing.
		require.Contains(t, out, "files=2 clean")
	})

	t.Run("a symlinked --dir is resolved, not passed over", func(t *testing.T) {
		t.Parallel()
		posixOnly(t)
		// os.Stat follows a link, so --dir naming a symlink to the tree was seen
		// as a single non-directory entry and returned NOCODE OK files=0, exit
		// 0: a clean pass over a tree never opened. None of the other fixtures
		// is itself a link.
		real := t.TempDir()
		write(t, real, "NOTES.md", "# n\n")
		write(t, real, "tool.py", "print(1)\n")
		out, rc := runBin(t, bin, "nocode", "--dir", linkTo(t, real))
		require.Equal(t, 1, rc, "symlinked --dir: want exit 1: %s", out)
		require.Contains(t, out, "tool.py", "symlinked --dir: the tree was not walked")
	})

	t.Run("a symlinked --dir is resolved, not passed over (links)", func(t *testing.T) {
		t.Parallel()
		posixOnly(t)
		// The same fail-open in the other checker: LINKS OK files=0 links=0,
		// exit 0, over a tree never walked, while the same tree by its real path
		// reported the broken link and exited 1. No other assertion hands
		// `links` a --dir that is itself a link.
		real := t.TempDir()
		write(t, real, "a.md", "fine\n\n[gone](gone.md)\n")
		out, rc := runBin(t, bin, "links", "--dir", linkTo(t, real))
		require.Equal(t, 1, rc, "symlinked --dir: want exit 1: %s", out)
		require.Contains(t, out, "a.md:3: gone.md (does not exist)", "symlinked --dir: the tree was not walked")
	})

	t.Run("a tree that classified nothing says so", func(t *testing.T) {
		t.Parallel()
		// The scanned==0 warning is the only backstop for the fail-open above in
		// a real repository. Exit 0 is correct for an empty tree, which holds no
		// machinery; what must not vanish is the sentence telling an operator
		// that nothing was classified.
		out, rc := runBin(t, bin, "nocode", "--dir", t.TempDir())
		require.Equal(t, 0, rc, "empty tree: want exit 0: %s", out)
		require.Contains(t, out, "classified NOTHING", "empty tree: the scanned==0 warning is gone")
	})

	t.Run("each condition refuses separately, with the right code and reason", func(t *testing.T) {
		t.Parallel()
		posixOnly(t)
		// One fixture per condition AND an assertion on which reason fired: one
		// fixture per condition alone would still go green if a regression
		// collapsed every case into one spurious finding.
		check := func(desc string, tree string, wantReason string) {
			out, rc := runBin(t, bin, "nocode", "--dir", tree)
			if assert.Equal(t, 1, rc, "%s: want exit 1: %s", desc, out) {
				assert.Contains(t, out, wantReason, desc)
			}
		}

		ext := t.TempDir()
		write(t, ext, "NOTES.md", "# n\n")
		write(t, ext, "tool.py", "print(1)\n")
		check("extension", ext, "code extension .py (floor-list)")

		exe := t.TempDir()
		write(t, exe, "NOTES.md", "# n\n")
		write(t, exe, "thing", "not a script\n")
		require.NoError(t, os.Chmod(filepath.Join(exe, "thing"), 0o755))
		check("executable bit", exe, "executable (mode")

		she := t.TempDir()
		write(t, she, "NOTES.md", "# n\n")
		write(t, she, "thing", "#!/bin/sh\necho hi\n")
		check("shebang", she, "executable script (shebang)")
	})

	t.Run("the fail-opens this CI exists for stay closed", func(t *testing.T) {
		t.Parallel()
		posixOnly(t)
		// Each was a real fail-open in a released check: a file nobody can open,
		// and a file that is not a regular file at all, must be FINDINGS rather
		// than passes. Making a file less readable must never make this gate
		// greener.
		// FAIL rather than skip. The hosted runners are non-root, so this
		// firing means the environment changed under us, and a silent skip
		// would turn the headline fail-open assertion into a no-op behind a
		// green check.
		require.NotEqual(t, 0, os.Getuid(), "running as root: chmod cannot refuse a read, so this assertion cannot run")
		unreadable := t.TempDir()
		write(t, unreadable, "NOTES.md", "# n\n")
		write(t, unreadable, "secret", "x\n")
		secret := filepath.Join(unreadable, "secret")
		require.NoError(t, os.Chmod(secret, 0))
		defer func() { assert.NoError(t, os.Chmod(secret, 0o644)) }()
		out, rc := runBin(t, bin, "nocode", "--dir", unreadable)
		if assert.Equal(t, 1, rc, "unreadable: want exit 1: %s", out) {
			assert.Contains(t, out, "cannot rule out machinery", "unreadable: wrong reason")
		}

		fifo := t.TempDir()
		write(t, fifo, "NOTES.md", "# n\n")
		require.NoError(t, makeFifo(filepath.Join(fifo, "pipe.sh")), "mkfifo")
		out, rc = runBin(t, bin, "nocode", "--dir", fifo)
		if assert.Equal(t, 1, rc, "fifo: want exit 1: %s", out) {
			assert.Contains(t, out, "not a regular file", "fifo: wrong reason")
		}
	})

	t.Run("a guard that cannot read its own list refuses, and says so", func(t *testing.T) {
		t.Parallel()
		// Exit 2, not merely non-zero: `nocode` also exits non-zero on an unknown
		// flag, so "non-zero" would pass with --deny-ext misspelled and the flag
		// under test never exercised. The message is asserted for the same
		// reason. The @path is RELATIVE on purpose: an absolute POSIX path is
		// rewritten by git-bash on Windows, so the exit code held and the
		// message assertion failed on a spelling this test never chose.
		tree := t.TempDir()
		write(t, tree, "NOTES.md", "# n\n")
		out, rc := runBin(t, bin, "nocode", "--dir", tree, "--deny-ext", "@no-such-deny-list.txt")
		require.Equal(t, 2, rc, "want exit 2: %s", out)
		require.Contains(t, out, "deny-list file: open no-such-deny-list.txt", "wrong message")
	})
}
