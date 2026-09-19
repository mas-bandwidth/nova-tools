package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// THE DEFECT THIS IS FOR. `nova-wake serve --as Johnny` polls the bus by handing
// `nova-bus wait` an argv built in busArgs, and that wait writes and pushes a BEAT for
// Johnny. Johnny's own harness loop beats for Johnny too, so the two writers collide on
// from-johnny/BEAT and the serve never reads a note -- #1517's receipt:
//
//	WAKE BUS LINE WAIT NOTE beat push failed: the rebase over what arrived conflicted on
//	from-johnny/BEAT, which this tool will not settle for you
//
// `serve --no-beat` must pass --no-beat through busArgs and change nothing else. The
// control is in the SAME test so that a serve which simply stopped passing the flag (or
// which silently dropped every flag) cannot pass: the default must NOT carry --no-beat.
func TestIssue1517ServeNoBeatReachesTheBusArgv(t *testing.T) {
	polls := func(t *testing.T, busDir string) []string {
		t.Helper()
		var out []string
		for _, c := range calls(t, busDir) {
			if strings.HasPrefix(c, "wait ") {
				out = append(out, c)
			}
		}
		return out
	}
	carries := func(call, arg string) bool {
		for _, tok := range strings.Fields(call) {
			if tok == arg {
				return true
			}
		}
		return false
	}
	run := func(t *testing.T, extra ...string) []string {
		t.Helper()
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
		note, _ := fakeNote(t)
		state := filepath.Join(t.TempDir(), "serve.state")
		args := append([]string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", state, "--hours", "0.02",
			"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}, extra...)
		r := wakeRun(t, args...)
		if r.exit != 0 {
			t.Fatalf("exit = %d; %s", r.exit, r.all())
		}
		got := polls(t, busDir)
		if len(got) == 0 {
			t.Fatalf("serve never polled:\n%s", r.all())
		}
		return got
	}

	// THE FLAG: every poll's argv carries --no-beat.
	for _, c := range run(t, "--no-beat") {
		if !carries(c, "--no-beat") {
			t.Errorf("a serve --no-beat poll did not carry --no-beat: %q", c)
		}
	}

	// THE CONTROL: without the flag, no poll carries it.
	for _, c := range run(t) {
		if carries(c, "--no-beat") {
			t.Errorf("a serve poll without --no-beat carried it: %q", c)
		}
	}
}
