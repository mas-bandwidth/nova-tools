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
		t.Fatal("NOTHING WAS SMOKED: NOVA_SHIPPED_BIN names no shipped nova-check binary, and under CI that is a red, never a skip; the certification smoke step must pass the variable")
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
			if bin != tc.bin || fatal != tc.fatal || skip != tc.skip {
				t.Fatalf("got (%q, fatal %t, skip %t), want (%q, fatal %t, skip %t)", bin, fatal, skip, tc.bin, tc.fatal, tc.skip)
			}
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
	t.Fatalf("%s %s: %v", bin, strings.Join(args, " "), err)
	return "", -1
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// linkTo makes a symlink to dir in a directory of its own and returns its path.
func linkTo(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
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
		if rc != 0 {
			t.Fatalf("want exit 0, got %d: %s", rc, out)
		}
		// files=2 is the load-bearing half. `nocode` prints NOCODE OK and exits
		// 0 for a tree it never opened, so exit 0 alone cannot tell a pass from
		// a walk that scanned nothing.
		if !strings.Contains(out, "files=2 clean") {
			t.Fatalf("want files=2 clean, got: %s", out)
		}
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
		if rc != 1 {
			t.Fatalf("symlinked --dir: want exit 1, got %d: %s", rc, out)
		}
		if !strings.Contains(out, "tool.py") {
			t.Fatalf("symlinked --dir: the tree was not walked: %s", out)
		}
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
		if rc != 1 {
			t.Fatalf("symlinked --dir: want exit 1, got %d: %s", rc, out)
		}
		if !strings.Contains(out, "a.md:3: gone.md (does not exist)") {
			t.Fatalf("symlinked --dir: the tree was not walked: %s", out)
		}
	})

	t.Run("a tree that classified nothing says so", func(t *testing.T) {
		t.Parallel()
		// The scanned==0 warning is the only backstop for the fail-open above in
		// a real repository. Exit 0 is correct for an empty tree, which holds no
		// machinery; what must not vanish is the sentence telling an operator
		// that nothing was classified.
		out, rc := runBin(t, bin, "nocode", "--dir", t.TempDir())
		if rc != 0 {
			t.Fatalf("empty tree: want exit 0, got %d: %s", rc, out)
		}
		if !strings.Contains(out, "classified NOTHING") {
			t.Fatalf("empty tree: the scanned==0 warning is gone: %s", out)
		}
	})

	t.Run("each condition refuses separately, with the right code and reason", func(t *testing.T) {
		t.Parallel()
		posixOnly(t)
		// One fixture per condition AND an assertion on which reason fired: one
		// fixture per condition alone would still go green if a regression
		// collapsed every case into one spurious finding.
		check := func(desc string, tree string, wantReason string) {
			out, rc := runBin(t, bin, "nocode", "--dir", tree)
			if rc != 1 {
				t.Errorf("%s: want exit 1, got %d: %s", desc, rc, out)
				return
			}
			if !strings.Contains(out, wantReason) {
				t.Errorf("%s: want reason %q, got: %s", desc, wantReason, out)
			}
		}

		ext := t.TempDir()
		write(t, ext, "NOTES.md", "# n\n")
		write(t, ext, "tool.py", "print(1)\n")
		check("extension", ext, "code extension .py (floor-list)")

		exe := t.TempDir()
		write(t, exe, "NOTES.md", "# n\n")
		write(t, exe, "thing", "not a script\n")
		if err := os.Chmod(filepath.Join(exe, "thing"), 0o755); err != nil {
			t.Fatal(err)
		}
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
		if os.Getuid() == 0 {
			// FAIL rather than skip. The hosted runners are non-root, so this
			// firing means the environment changed under us, and a silent skip
			// would turn the headline fail-open assertion into a no-op behind a
			// green check.
			t.Fatal("running as root: chmod cannot refuse a read, so this assertion cannot run")
		}
		unreadable := t.TempDir()
		write(t, unreadable, "NOTES.md", "# n\n")
		write(t, unreadable, "secret", "x\n")
		secret := filepath.Join(unreadable, "secret")
		if err := os.Chmod(secret, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(secret, 0o644)
		out, rc := runBin(t, bin, "nocode", "--dir", unreadable)
		if rc != 1 {
			t.Errorf("unreadable: want exit 1, got %d: %s", rc, out)
		} else if !strings.Contains(out, "cannot rule out machinery") {
			t.Errorf("unreadable: wrong reason: %s", out)
		}

		fifo := t.TempDir()
		write(t, fifo, "NOTES.md", "# n\n")
		if err := makeFifo(filepath.Join(fifo, "pipe.sh")); err != nil {
			t.Fatalf("mkfifo: %v", err)
		}
		out, rc = runBin(t, bin, "nocode", "--dir", fifo)
		if rc != 1 {
			t.Errorf("fifo: want exit 1, got %d: %s", rc, out)
		} else if !strings.Contains(out, "not a regular file") {
			t.Errorf("fifo: wrong reason: %s", out)
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
		if rc != 2 {
			t.Fatalf("want exit 2, got %d: %s", rc, out)
		}
		if !strings.Contains(out, "deny-list file: open no-such-deny-list.txt") {
			t.Fatalf("wrong message: %s", out)
		}
	})
}
