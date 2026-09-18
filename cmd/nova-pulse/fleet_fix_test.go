package main

// The verb's half of fix-then-prove-then-escalate: the flags, the two new seams, and the
// waiver. The engine is held by internal/fleet; what is held here is that `--fix` is ON
// without anybody asking for it, that `--no-fix` turns it off, and that the escalation's
// note only leaves when a bus was named.

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// certifyFakeFixer records what the verb asked to apply.
type certifyFakeFixer struct {
	calls   []string
	changed []string
}

func (f *certifyFakeFixer) Apply(machine string, items []string) ([]string, error) {
	f.calls = append(f.calls, machine+":"+strings.Join(items, ","))
	return f.changed, nil
}

// certifyFakeBus keeps the note the verb would have sent.
type certifyFakeBus struct{ posts []string }

func (b *certifyFakeBus) Post(lane, subject, body string) error {
	b.posts = append(b.posts, lane+"|"+subject+"|"+body)
	return nil
}

// withCertifyFixFakes wires the repair seams on top of the remote and forge ones.
func withCertifyFixFakes(t *testing.T, fixer fleet.Fixer, poster fleet.BusPoster) {
	t.Helper()
	oldFixer, oldBus := fleetNewCertifyFixer, fleetNewCertifyBus
	fleetNewCertifyFixer = func(certifyFixer) fleet.Fixer { return fixer }
	fleetNewCertifyBus = func(certifyBusPoster) fleet.BusPoster { return poster }
	t.Cleanup(func() { fleetNewCertifyFixer, fleetNewCertifyBus = oldFixer, oldBus })
}

// failingSpace is the bench with the fault four machines carried on 2026-09-18.
func failingSpace() map[string]string {
	answers := spaceAnswers()
	answers["space|git-identity"] = "GIT IDENTITY name= email=\n"
	return answers
}

// TestTheFixIsOnWithoutAnybodyAskingForIt: mechanized means the fleet does not wait for a
// person to type the same four repairs on four machines again.
func TestTheFixIsOnWithoutAnybodyAskingForIt(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	withCertifyFakes(t, &certifyFakeRemote{answers: failingSpace()}, certifyFakeForge{})
	fixer := &certifyFakeFixer{changed: []string{"git-identity"}}
	bus := &certifyFakeBus{}
	withCertifyFixFakes(t, fixer, bus)
	out, errs, code := runCertifyVerb(t, "--machines", machines, "--machine", "space",
		"--certs", certs, "--standard", standard, "--bus", "/tmp/bus", "--as", "Rowan", "--to", "Glenn")
	all := out + errs
	if code != 1 {
		t.Fatalf("exit = %d, want 1: the fake machine never repairs itself\n%s", code, all)
	}
	if len(fixer.calls) != 1 || fixer.calls[0] != "space:git-identity" {
		t.Fatalf("the verb applied %v, want one apply of git-identity on space", fixer.calls)
	}
	if !strings.Contains(all, "CERTIFY ESCALATE machine=space classes=git-identity") {
		t.Errorf("no escalation:\n%s", all)
	}
	if len(bus.posts) != 1 {
		t.Fatalf("%d notes, want 1", len(bus.posts))
	}
	if !strings.HasPrefix(bus.posts[0], "fleet|") {
		t.Errorf("the note did not go to the fleet lane: %q", bus.posts[0])
	}
}

// TestNoFixWaivesTheRepairAtTheVerb.
func TestNoFixWaivesTheRepairAtTheVerb(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	withCertifyFakes(t, &certifyFakeRemote{answers: failingSpace()}, certifyFakeForge{})
	fixer := &certifyFakeFixer{}
	bus := &certifyFakeBus{}
	withCertifyFixFakes(t, fixer, bus)
	out, errs, code := runCertifyVerb(t, "--machines", machines, "--machine", "space",
		"--certs", certs, "--standard", standard, "--no-fix")
	all := out + errs
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, all)
	}
	if len(fixer.calls) != 0 || len(bus.posts) != 0 {
		t.Errorf("--no-fix applied %v and sent %d notes", fixer.calls, len(bus.posts))
	}
	if !strings.Contains(all, "CERTIFY space git-identity FAIL") {
		t.Errorf("the failure is still reported under --no-fix:\n%s", all)
	}
}

// TestABusWithNoSenderOrNoRecipientIsRefusedBeforeAnyMachineIsTouched.
func TestABusWithNoSenderOrNoRecipientIsRefusedBeforeAnyMachineIsTouched(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	remote := &certifyFakeRemote{answers: spaceAnswers()}
	withCertifyFakes(t, remote, certifyFakeForge{})
	for _, args := range [][]string{
		{"--bus", "/tmp/bus"},
		{"--bus", "/tmp/bus", "--as", "Rowan"},
		{"--bus", "/tmp/bus", "--to", "Glenn"},
	} {
		_, errs, code := runCertifyVerb(t, append([]string{
			"--machines", machines, "--machine", "space", "--certs", certs, "--standard", standard}, args...)...)
		if code != 2 {
			t.Fatalf("%v exited %d, want 2\n%s", args, code, errs)
		}
		if len(remote.scripts) != 0 {
			t.Errorf("a refusal reached the machine %d times", len(remote.scripts))
		}
	}
}

// TestMaxFixRoundsZeroIsTheSameWaiver: a number nobody has to remember the meaning of.
func TestMaxFixRoundsZeroIsTheSameWaiver(t *testing.T) {
	machines, certs, standard := certifyFiles(t)
	withCertifyFakes(t, &certifyFakeRemote{answers: failingSpace()}, certifyFakeForge{})
	fixer := &certifyFakeFixer{}
	withCertifyFixFakes(t, fixer, &certifyFakeBus{})
	_, errs, code := runCertifyVerb(t, "--machines", machines, "--machine", "space",
		"--certs", certs, "--standard", standard, "--max-fix-rounds", "-1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a negative round count\n%s", code, errs)
	}
	if !strings.Contains(errs, "--max-fix-rounds") {
		t.Errorf("the refusal does not name the flag:\n%s", errs)
	}
}

// TestTheApplyVerbIsTheOneMutatingFleetVerbAndRefusesToGuess: `fleet standard --apply`
// through its own flags. It never takes --bench, because the file that decides where cards
// go is the registry.
func TestTheApplyVerbIsTheOneMutatingFleetVerbAndRefusesToGuess(t *testing.T) {
	machines, _, _ := certifyFiles(t)
	for _, tc := range []struct {
		name  string
		args  []string
		wants string
	}{
		{"no machine", []string{"--apply", "--machines", machines}, "--machine"},
		{"no registry", []string{"--apply", "--machine", "space"}, "--machines"},
		{"a bench", []string{"--apply", "--machines", machines, "--machine", "space", "--bench", "space"}, "--bench"},
		{"items without apply", []string{"--benches", machines, "--bench", "space", "--items", "git-identity"}, "--items"},
		{"dry-run without apply", []string{"--benches", machines, "--bench", "space", "--dry-run"}, "--dry-run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errs strings.Builder
			code := cmdFleetStandard(tc.args, &out, &errs)
			if code != 2 {
				t.Fatalf("exit = %d, want 2\n%s%s", code, out.String(), errs.String())
			}
			if !strings.Contains(errs.String(), tc.wants) {
				t.Errorf("the refusal does not name %s:\n%s", tc.wants, errs.String())
			}
		})
	}
}

// TestTheFixerAndThePosterAreTheRealOnesWhenNoTestReplacesThem holds the production wiring
// itself: the verb builds an apply that is `fleet standard --apply` in process, and a poster
// that is `nova-bus send`, and neither is a second copy of that path.
func TestTheFixerAndThePosterAreTheRealOnesWhenNoTestReplacesThem(t *testing.T) {
	var fixer fleet.Fixer = fleetNewCertifyFixer(certifyFixer{Machines: "m.tsv", SSH: "ssh", Timeout: time.Minute})
	if _, ok := fixer.(certifyFixer); !ok {
		t.Errorf("the production fixer is %T", fixer)
	}
	var poster fleet.BusPoster = fleetNewCertifyBus(certifyBusPoster{Bus: "/tmp/bus", As: "Rowan", To: "Glenn"})
	if _, ok := poster.(certifyBusPoster); !ok {
		t.Errorf("the production poster is %T", poster)
	}
}
