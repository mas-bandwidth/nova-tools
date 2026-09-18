package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// THE GATE THAT NEVER RAN (issue #1465). A Go card inside the wall could not execute the
// toolchain the native step had configured GOMODCACHE, GOCACHE and GOTOOLCHAIN=local FOR,
// and the only record of it was one line in the card's own log:
//
//	/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied
//
// No `SANDBOX REFUSED`, no `Operation not permitted`, no fence rejection -- so WallRefused
// saw nothing, the card published an honest RESULT.md, the child exited 0, and the run read
// `NATIVE OK rc=0 harness=ok`. A commit nobody compiled was green.
//
// ExecRefused is the missing reader: the words a SHELL uses when the wall denies it a path
// it was told to RUN. It is deliberately narrow. `cat: /etc/shadow: Permission denied` is a
// READ a card was refused, which the card routes around, and it is not this class; the
// classifier fires only when the line's own first word is a shell, or the words are Go's
// own `fork/exec`.
func TestExecRefusedReadsTheShellsOwnWords(t *testing.T) {
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
		// THE NEGATIVES. Each is a real line out of a card's log that is NOT the class, and
		// each one firing would turn a finished card into a refusal.
		{name: "a_read_the_card_routes_around", line: "cat: /etc/shadow: Permission denied", want: ""},
		{name: "the_fakes_own_refusal_prose", line: "fake harness: read of /etc/somewhere: permission denied (refused)", want: ""},
		{name: "a_signal_not_a_path", line: "bash: kill: (123) - Operation not permitted", want: ""},
		{name: "the_fence_rejection", line: "!  permission requested: external_directory (/x/*); auto-rejecting", want: ""},
		{name: "a_relative_program", line: "bash: line 1: ./configure: Permission denied", want: ""},
		{name: "prose_holding_the_words", line: "I could not run the gate: permission denied is what the wall said", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExecRefused([]byte(tc.line + "\n"))
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
				t.Errorf("the refused path is %q, got %q", tc.want, got.Path)
			}
		})
	}
}

// TestExecRefusedCarriesTheStep: the step the card had reached rides with the path, the way
// a wall death's does, so the refusal says WHERE the gate died and not only what it was.
func TestExecRefusedCarriesTheStep(t *testing.T) {
	log := []byte("STEP 1 clone\nSTEP 4 run the gate\n/usr/bin/bash: line 1: /opt/sdk/go1.26.5/bin/go: Permission denied\n")
	got, ok := ExecRefused(log)
	if !ok {
		t.Fatal("the log holds a shell's exec refusal and was read as none")
	}
	if got.Step != "4" {
		t.Errorf("the refusal names the last step the card reached (4), got %q", got.Step)
	}
}

// TestExecRefusalRootsNameBothEntries: the remedy a coordinator is handed names the roots to
// open, and for a toolchain reached through a symlink that is TWO of them -- the directory
// holding the launcher and the tree the launcher resolves into. Getting it wrong costs a
// whole card, so the tool works it out rather than asking a person to.
func TestExecRefusalRootsNameBothEntries(t *testing.T) {
	// The temp dir is resolved once: on macOS t.TempDir() lands under /var, a symlink to
	// /private/var, and the roots this reports are resolved paths.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sdkBin := filepath.Join(dir, "sdk", "go1.26.5", "bin")
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
	roots := ExecRefusalRoots(filepath.Join(linkDir, "go"))
	want := map[string]bool{linkDir: false, filepath.Join(dir, "sdk", "go1.26.5"): false}
	for _, r := range roots {
		if _, named := want[r]; !named {
			t.Errorf("the remedy names a root nobody asked for: %s (roots %v)", r, roots)
			continue
		}
		want[r] = true
	}
	for r, found := range want {
		if !found {
			t.Errorf("the remedy does not name %s; it holds %v", r, roots)
		}
	}
}
