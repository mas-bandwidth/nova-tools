package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A DENIAL IN THE CAPTURE IS NEVER AN OK, AND NEVER A DIAGNOSIS EITHER (issue #1465, and
// Stella's HOLD on PR #1478, comment 5737662335).
//
// The run of #1465: a Go card inside the wall could not compile its own test, said so in its
// RESULT.md, exited 0, and the tool reported `NATIVE OK rc=0 harness=ok`. The only record was
//
//	/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied
//
// ShellDenied is the reader that makes that disposition impossible. It is narrow at the head
// of the line -- the first field must be a SHELL, or the words must be Go's own `fork/exec`
// -- and it takes the PATH WHOLE, because a shell delimits its fields with `: ` and spaces
// and parentheses are ordinary pathname characters (Stella's P1).
func TestShellDeniedReadsTheShellsOwnWords(t *testing.T) {
	for _, tc := range []struct {
		name, line, want string
	}{
		{
			name: "bash_by_absolute_path",
			line: "/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied",
			want: "/home/glenn/go/bin/go",
		},
		{
			name: "bash_by_name",
			line: "bash: /home/glenn/go/bin/go: Permission denied",
			want: "/home/glenn/go/bin/go",
		},
		{
			name: "dash_numbers_its_line",
			line: "sh: 1: /opt/sdk/go1.26.5/bin/go: Permission denied",
			want: "/opt/sdk/go1.26.5/bin/go",
		},
		{
			name: "zsh_puts_the_path_last",
			line: "zsh: permission denied: /opt/sdk/go1.26.5/bin/go",
			want: "/opt/sdk/go1.26.5/bin/go",
		},
		{
			name: "go_own_exec_words",
			line: "fork/exec /opt/sdk/go1.26.5/bin/go: permission denied",
			want: "/opt/sdk/go1.26.5/bin/go",
		},
		{
			name: "painted",
			line: "\x1b[31m/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied\x1b[0m",
			want: "/home/glenn/go/bin/go",
		},
		// STELLA'S P1. A space, a parenthesis and both together are ordinary pathname
		// characters, and the first version of this grammar read each of them as prose --
		// which reopened the whole of #1465 through an everyday absolute path. The path is
		// the WHOLE segment between the shell's own `: ` delimiters, taken complete: a path
		// truncated at its first space is one a coordinator cannot act on.
		{
			name: "a_space_is_a_pathname_character",
			line: "/usr/bin/bash: line 1: /opt/sdk tool/bin/go: Permission denied",
			want: "/opt/sdk tool/bin/go",
		},
		{
			name: "parentheses_are_too",
			line: "bash: /opt/sdk (old)/bin/go: Permission denied",
			want: "/opt/sdk (old)/bin/go",
		},
		{
			name: "several_spaces",
			line: "/bin/bash: line 12: /opt/my sdk/go 1.26/bin/go: Permission denied",
			want: "/opt/my sdk/go 1.26/bin/go",
		},
		{
			name: "zsh_with_a_space",
			line: "zsh: permission denied: /opt/sdk tool/bin/go",
			want: "/opt/sdk tool/bin/go",
		},
		{
			name: "quoted_by_the_shell",
			line: `fork/exec "/opt/sdk tool/bin/go": permission denied`,
			want: "/opt/sdk tool/bin/go",
		},
		// STELLA'S P2, AS A POSITIVE. A redirection to a path the card may not write prints
		// exactly these words, attempts no program, and the card RECOVERS and exits 0. It IS
		// a denial and is read as one; what it is NOT is an exec, and no field here says it
		// was.
		{
			name: "a_redirection_the_card_recovered_from",
			line: "/bin/bash: /opt/out/report.txt: Permission denied",
			want: "/opt/out/report.txt",
		},
		// THE NEGATIVES. Each is a real line that is NOT this class, and each firing would
		// turn a finished card into a refusal.
		{name: "another_programs_complaint", line: "cat: /etc/shadow: Permission denied", want: ""},
		{name: "the_fakes_own_refusal_prose", line: "fake harness: read of /etc/somewhere: permission denied (refused)", want: ""},
		{name: "a_signal_not_a_path", line: "bash: kill: (123) - Operation not permitted", want: ""},
		{name: "the_fence_rejection", line: "!  permission requested: external_directory (/x/*); auto-rejecting", want: ""},
		{name: "a_relative_program", line: "bash: line 1: ./configure: Permission denied", want: ""},
		{name: "prose_holding_the_words", line: "I could not run the gate: permission denied is what the wall said", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ShellDenied([]byte(tc.line + "\n"))
			if tc.want == "" {
				if ok {
					t.Fatalf("%q is not the class, and it was read as one: %+v", tc.line, got)
				}
				return
			}
			if !ok {
				t.Fatalf("%q is the class and was read as nothing", tc.line)
			}
			if got.Path != tc.want {
				t.Errorf("the denied path is %q, got %q", tc.want, got.Path)
			}
			// The line itself rides along: it is the only thing a person can act on, and the
			// refusal quotes it rather than paraphrasing it.
			if !strings.Contains(got.Line, "ermission denied") {
				t.Errorf("the denial carries the shell's own line; it holds %q", got.Line)
			}
		})
	}
}

// TestShellDenialCarriesTheStep: the step the card had reached rides with the path, the way a
// wall death's does, so the refusal says WHERE the card was and not only what was named.
func TestShellDenialCarriesTheStep(t *testing.T) {
	log := []byte("STEP 1 clone\nSTEP 4 run the gate\n/usr/bin/bash: line 1: /opt/sdk tool/bin/go: Permission denied\n")
	got, ok := ShellDenied(log)
	if !ok {
		t.Fatal("the log holds a shell's denial and was read as none")
	}
	if got.Step != "4" {
		t.Errorf("the denial names the last step the card reached (4), got %q", got.Step)
	}
}

// TestShellDenialReasonAssertsNoCauseItCannotProve is Stella's P2 at the words themselves.
// The same line comes out of an exec denial, a redirection the card recovered from, and a cd
// into an unreadable directory. The reason may name none of them, and it must say so.
func TestShellDenialReasonAssertsNoCauseItCannotProve(t *testing.T) {
	d := ShellDenial{Path: "/opt/out/report.txt", Step: "3", Line: "/bin/bash: /opt/out/report.txt: Permission denied"}
	for _, tc := range []struct{ name, wall string }{
		{name: "unwalled", wall: SandboxNoneByFlag},
		{name: "walled", wall: "landlock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ShellDenialReason("a-card", "/jobs/a-card", tc.wall, 0, d)
			for _, forbidden := range []string{
				"never executed", "never compiled", "nothing compiled",
				"the program", "the gate never", "could not execute",
			} {
				if strings.Contains(got, forbidden) {
					t.Errorf("the reason asserts %q, which this line cannot establish:\n%s", forbidden, got)
				}
			}
			for _, want := range []string{"operation=unverified", "Permission denied", "step=3"} {
				if !strings.Contains(got, want) {
					t.Errorf("the reason carries %q:\n%s", want, got)
				}
			}
			// The remedy is a MEASUREMENT, not a guess at a cause.
			if !strings.Contains(got, "re-run the card's own gate") {
				t.Errorf("the reason's remedy is to run the gate and read its stderr:\n%s", got)
			}
		})
	}
}

// TestShellDenialReasonAttributesNothingToAWallThatWasNotThere: the detector runs on
// `--no-wall` runs too, where there is no sandbox at all, so the text cannot attribute the
// denial to one -- and must not offer a read set that does not exist as a remedy.
func TestShellDenialReasonAttributesNothingToAWallThatWasNotThere(t *testing.T) {
	d := ShellDenial{Path: "/opt/sdk tool/bin/go", Step: "3", Line: "/bin/bash: /opt/sdk tool/bin/go: Permission denied"}
	got := ShellDenialReason("a-card", "/jobs/a-card", SandboxNoneByFlag, 0, d)
	for _, forbidden := range []string{"read_roots", "the wall refused"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("an unwalled run attributes nothing to a wall or its read set (%q):\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "no sandbox") {
		t.Errorf("an unwalled run says so in the reason:\n%s", got)
	}
	// A walled one may offer the read set, and must call it a candidate.
	walled := ShellDenialReason("a-card", "/jobs/a-card", "landlock", 0, d)
	if !strings.Contains(walled, "read_roots") || !strings.Contains(walled, "/opt/sdk tool/bin") {
		t.Errorf("a walled run offers the complete candidate root:\n%s", walled)
	}
	if !strings.Contains(walled, "candidate and not the diagnosis") {
		t.Errorf("the candidate is named as a candidate:\n%s", walled)
	}
}

// TestDeniedPathRootsNameBothEntries: IF the denied path was one the child had to read or
// execute, the read set a coordinator would open is worked out rather than guessed at, and a
// path reached through a symlink is TWO roots -- the launcher's directory and the tree it
// resolves into -- because the kernel checks the grant against the resolved target. Named
// with a space in it, because a path with a space is an ordinary path.
func TestDeniedPathRootsNameBothEntries(t *testing.T) {
	// The temp dir is resolved once: on macOS t.TempDir() lands under /var, a symlink to
	// /private/var, and the roots this reports are resolved paths.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sdkBin := filepath.Join(dir, "sdk tool", "go1.26.5", "bin")
	if err := os.MkdirAll(sdkBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkBin, "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(dir, "go", "bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sdkBin, "go"), filepath.Join(linkDir, "go")); err != nil {
		t.Skipf("this filesystem refuses a symlink: %v", err)
	}
	roots := DeniedPathRoots(filepath.Join(linkDir, "go"))
	want := map[string]bool{linkDir: false, filepath.Join(dir, "sdk tool", "go1.26.5"): false}
	for _, r := range roots {
		if _, named := want[r]; !named {
			t.Errorf("the candidate set names a root nobody asked for: %s (roots %v)", r, roots)
			continue
		}
		want[r] = true
	}
	for r, found := range want {
		if !found {
			t.Errorf("the candidate set does not name %s; it holds %v", r, roots)
		}
	}
}
