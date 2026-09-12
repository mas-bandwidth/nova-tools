package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// Emma, dogfooding v0.12.0 (nova-tools #104, 2026-09-12): "nova-wake refuses to
// run against the nova-bus binary built from its own monorepo release" --
// `WAKE REFUSED: nova-bus v0.12.0; this tool is written against v0.10.3 and its
// fetch is a property of the push`, from a literal in internal/wake/bus.go that
// no release could follow.
//
// The pin is not deleted here: it is the guard on the two-poll freshness
// promise, which is a property of nova-bus's PUSH (docs/SPEC-WAKE.md, "How the
// checkout receives mail"). It is made TRUE instead -- the nova-bus this tool
// is written against is the one from its own release, so the pin is this
// build's own version, which a release stamps into every binary.
//
// This test is the one that would have caught it: a nova-wake stamped at a
// release version, handed a nova-bus answering THAT version, must run.
func TestAWakeBuiltAtAVersionRunsAgainstTheBusOfTheSameRelease(t *testing.T) {
	const released = "v0.12.0"
	stampRelease(t, released)

	t.Run("watch", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "version"), "nova-bus "+released+" darwin/arm64 go1.27.1\n")
		write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
		r := wakeRun(t, "watch", "--state", filepath.Join(t.TempDir(), "wake.state"),
			"--max", "5s", "--on-deadline", "report", "--interval", "5s",
			"--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
		if r.exit != 0 {
			t.Fatalf("exit = %d: a nova-wake %s refused the nova-bus of its own release\n%s", r.exit, released, r.all())
		}
		if !strings.Contains(r.stdout, "nova-bus="+released) {
			t.Errorf("the opening line does not carry the version it measured against:\n%s", r.stdout)
		}
	})

	t.Run("serve", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "version"), "nova-bus "+released+" darwin/arm64 go1.27.1\n")
		write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
		note, _ := fakeNote(t)
		r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", filepath.Join(t.TempDir(), "serve.state"),
			"--hours", "0.02", "--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
		if r.exit != 0 {
			t.Fatalf("exit = %d: a nova-wake %s refused the nova-bus of its own release\n%s", r.exit, released, r.all())
		}
	})

	// The guard is still a guard: a nova-bus from another release is refused by
	// name, before the opening line, exactly as the spec says.
	t.Run("a nova-bus from another release is still refused", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "version"), "nova-bus v0.10.3 darwin/arm64 go1.27.1\n")
		r := wakeRun(t, "watch", "--state", filepath.Join(t.TempDir(), "wake.state"),
			"--max", "5s", "--on-deadline", "report", "--interval", "5s",
			"--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40")
		if r.exit != 2 {
			t.Fatalf("exit = %d, want 2: an older nova-bus is what the pin is for\n%s", r.exit, r.all())
		}
		for _, want := range []string{"v0.10.3", released, "fetch is a property of the push"} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("the refusal must name both versions and the reason; %q is missing:\n%s", want, r.stderr)
			}
		}
		if strings.Contains(r.stdout, "WAKE at=") {
			t.Errorf("the version is checked BEFORE the opening line:\n%s", r.stdout)
		}
	})
}

// A build with no stamp at all still refuses rather than accepting anything: an
// empty tool version is not a wildcard.
func TestAnUnstampedVersionIsNotAWildcard(t *testing.T) {
	if wake.AcceptBus("", "v0.12.0") {
		t.Error("a nova-wake that cannot say what it is accepted a nova-bus anyway")
	}
	if !wake.AcceptBus("devel", "devel") {
		t.Error("two halves of one unstamped tree must still be a pair")
	}
	if wake.AcceptBus("v0.12.0", "v0.12.1") {
		t.Error("a nova-bus from another release was accepted")
	}
}

// stampRelease makes this test binary answer the way a released one does: the
// release stamps -ldflags "-X main.version=<tag>" and buildVersion reads it.
func stampRelease(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}
