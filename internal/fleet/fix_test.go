package fleet

// Red tests first, for the second half of mechanized certification: a FAIL that a
// provisioning-standard item can repair is repaired, certified ONCE more, and -- when it
// still fails -- escalated by name and left uncertified.
//
// The hurt this closes: on 2026-09-18 every Linux machine in the fleet failed the same four
// ways (no non-interactive PATH, eighteen `~/go/bin` shadows, no git identity, no Go on a
// runner's `.path`), a person fixed all four BY HAND on four machines, and nothing in the
// tools remembered how. The repair is now a table, the loop runs it, and the only thing a
// person is asked to look at is what the repair could not fix.
//
// Everything here is fakes: a remote that answers from a table, a fixer that records what it
// was asked to apply and changes the table the way a real repair changes a machine, a bus
// poster that keeps the note in memory. No test opens a socket or starts a program.

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// fakes
// ---------------------------------------------------------------------------

// fakeFixer is the apply seam: it records every (machine, items) it was asked for, answers
// which items it changed, and runs `after` -- which is how a test says what the repair did
// to the machine, the way a real apply changes what the next ssh sees.
type fakeFixer struct {
	changed map[string][]string // item -> nothing; the items this fixer reports changed
	err     error
	after   func()

	calls []fixerCall
}

type fixerCall struct {
	machine string
	items   []string
}

func (f *fakeFixer) Apply(machine string, items []string) ([]string, error) {
	f.calls = append(f.calls, fixerCall{machine: machine, items: append([]string(nil), items...)})
	if f.err != nil {
		return nil, f.err
	}
	var out []string
	for _, item := range items {
		if _, ok := f.changed[item]; ok {
			out = append(out, item)
		}
	}
	if f.after != nil {
		f.after()
	}
	return out, nil
}

// fakeBus keeps every note in memory. It is STRICT about the lane: a note with no lane is
// the failure this seam exists to prevent -- a note nobody is reading.
type fakeBus struct {
	notes []busNote
	err   error
}

type busNote struct{ lane, subject, body string }

func (b *fakeBus) Post(lane, subject, body string) error {
	if b.err != nil {
		return b.err
	}
	if strings.TrimSpace(lane) == "" {
		return errors.New("a note with no lane reaches nobody")
	}
	b.notes = append(b.notes, busNote{lane: lane, subject: subject, body: body})
	return nil
}

// failing turns one class of one machine into the answer a broken machine gives.
func failing(answers map[string]remoteAnswer, machine, class, said string) {
	answers[machine+"|"+class] = remoteAnswer{out: said + "\n", err: errors.New("exit status 1")}
}

// ---------------------------------------------------------------------------
// the mapping
// ---------------------------------------------------------------------------

// TestTheFixMappingNamesAStandardItemPerRepairableClass holds the table itself. It is a
// table in code, and not a guess in a shell, because every entry is a fault the hand pass of
// 2026-09-18 found and repaired on four machines in a row.
func TestTheFixMappingNamesAStandardItemPerRepairableClass(t *testing.T) {
	want := map[string][]string{
		"path-resolves": {ItemGobinShadow, ItemPathNonInteractive},
		"go-on-path":    {ItemPathNonInteractive},
		"release-path":  {ItemNovaStamp, ItemPathNonInteractive},
		"git-identity":  {ItemGitIdentity},
		"runner-path":   {ItemRunnerPathGo},
	}
	for class, items := range want {
		got := ItemsForClass(class)
		if strings.Join(got, ",") != strings.Join(items, ",") {
			t.Errorf("%s maps to %v, want %v", class, got, items)
		}
	}
	// A class no standard item repairs must map to NOTHING rather than to something
	// plausible: a repair that cannot work is a round of load on a machine and a lie in the
	// record.
	for _, class := range []string{"go-test", "c-build", "sbcl", "git-push", "registry-truth", "diag-size"} {
		if items := ItemsForClass(class); len(items) != 0 {
			t.Errorf("%s maps to %v; no standard item repairs it", class, items)
		}
	}
	// Every item the table names is an item apply can be asked for.
	for class, items := range want {
		for _, item := range items {
			if !KnownItem(item) {
				t.Errorf("%s names %q, which is no item of the provisioning standard", class, item)
			}
		}
	}
	// The items the mapping asks for, over a set of classes, are sorted and unique: they go
	// on a line and into an argv, and two spellings of one repair is two repairs.
	items, byHand := ItemsForClasses([]string{"go-on-path", "path-resolves", "release-path"})
	if strings.Join(items, ",") != strings.Join([]string{ItemGobinShadow, ItemNovaStamp, ItemPathNonInteractive}, ",") {
		t.Errorf("items = %v, want them sorted and each once", items)
	}
	// nova-stamp is the one item apply never runs itself.
	if strings.Join(byHand, ",") != ItemNovaStamp {
		t.Errorf("by-hand items = %v, want %s: a stale binary is `nova-update release adopt`, which apply never runs", byHand, ItemNovaStamp)
	}
}

// ---------------------------------------------------------------------------
// fix, then certify once more
// ---------------------------------------------------------------------------

// TestAFailedClassIsRepairedAndCertifiedAgain is the whole point: the machine failed, the
// standard item behind the failure was applied, the class was certified ONE more time, and
// the run is green with a certificate that says so.
func TestAFailedClassIsRepairedAndCertifiedAgain(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "path-resolves", "PATH RESOLVES nova-merge=- shadows=18")
	remote := &fakeRemote{answers: answers}
	fixer := &fakeFixer{
		changed: map[string][]string{ItemPathNonInteractive: nil, ItemGobinShadow: nil},
		// what the repair did to the machine: the next ssh finds the tools on PATH.
		after: func() { answers["space|path-resolves"] = benchOK()["space|path-resolves"] },
	}
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: remote, Hash: "h", Now: fixedNow, Fix: true, Fixer: fixer,
	})
	all := out + errs
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- the repair worked\n%s", code, all)
	}
	if len(fixer.calls) != 1 {
		t.Fatalf("the fixer was called %d times, want 1", len(fixer.calls))
	}
	if fixer.calls[0].machine != "space" {
		t.Errorf("the repair was applied to %q, not the machine that failed", fixer.calls[0].machine)
	}
	if strings.Join(fixer.calls[0].items, ",") != ItemGobinShadow+","+ItemPathNonInteractive {
		t.Errorf("the repair applied %v, want the items the failed class maps to", fixer.calls[0].items)
	}
	if !strings.Contains(all, "CERTIFY FIX machine=space") {
		t.Errorf("the repair is not on a line:\n%s", all)
	}
	if !strings.Contains(all, "CERTIFY space path-resolves OK") {
		t.Errorf("the class was not certified again after the repair:\n%s", all)
	}
	if !Certified(mustRead(t, certs), "space", "path-resolves", "v0.17.0", "h") {
		t.Error("the repaired class holds no current certificate; a fix that is not certified is a fix nobody can trust")
	}
	if strings.Contains(all, "CERTIFY ESCALATE") {
		t.Errorf("a repaired class escalated:\n%s", all)
	}
}

// TestTheRepairIsBoundedByMaxFixRounds: one round by default. A loop that repairs and
// re-certifies until it gives up is a loop that puts a machine's whole load into a hole it
// cannot climb out of (memory: every wait has a deadline).
func TestTheRepairIsBoundedByMaxFixRounds(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "git-identity", "GIT IDENTITY name=- email=-")
	fixer := &fakeFixer{changed: map[string][]string{ItemGitIdentity: nil}}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
		Fix: true, Fixer: fixer,
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1: the class still fails\n%s%s", code, out, errs)
	}
	if len(fixer.calls) != 1 {
		t.Errorf("the fixer ran %d times under the default --max-fix-rounds, want 1", len(fixer.calls))
	}
}

// TestNoFixWaivesTheRepairAndReachesTheStandardNotAtAll: `--no-fix` is a waiver a person
// says out loud, and under it certification only ever reads.
func TestNoFixWaivesTheRepairAndReachesTheStandardNotAtAll(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "git-identity", "GIT IDENTITY name=- email=-")
	fixer := &fakeFixer{changed: map[string][]string{ItemGitIdentity: nil}}
	bus := &fakeBus{}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
		Fix: false, Fixer: fixer, Bus: bus, Lane: "fleet",
	})
	all := out + errs
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, all)
	}
	if len(fixer.calls) != 0 {
		t.Errorf("--no-fix applied %d repairs; it waives every one", len(fixer.calls))
	}
	if len(bus.notes) != 0 {
		t.Errorf("--no-fix sent %d bus notes; the waiver is the person saying they will look", len(bus.notes))
	}
	if !strings.Contains(all, "CERTIFY space git-identity FAIL") {
		t.Errorf("the failure is still reported under --no-fix:\n%s", all)
	}
}

// ---------------------------------------------------------------------------
// escalation
// ---------------------------------------------------------------------------

// TestAClassThatStillFailsEscalatesOnceAndStaysUncertified: one line, one note, and the
// class left uncertified so `fill` keeps refusing cards for it. A repair that reports
// success and leaves the machine broken is the survey's own lie in a new place.
func TestAClassThatStillFailsEscalatesOnceAndStaysUncertified(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "path-resolves", "PATH RESOLVES nova-merge=- shadows=18")
	fixer := &fakeFixer{changed: map[string][]string{ItemPathNonInteractive: nil}}
	bus := &fakeBus{}
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
		Fix: true, Fixer: fixer, Bus: bus, Lane: "fleet",
	})
	all := out + errs
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, all)
	}
	line := ""
	for _, l := range strings.Split(all, "\n") {
		if strings.HasPrefix(l, "CERTIFY ESCALATE") {
			if line != "" {
				t.Fatalf("two escalations for one machine:\n%s", all)
			}
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no escalation line:\n%s", all)
	}
	for _, want := range []string{"machine=space", "classes=path-resolves", "remedy="} {
		if !strings.Contains(line, want) {
			t.Errorf("the escalation line does not carry %s:\n%s", want, line)
		}
	}
	if len(bus.notes) != 1 {
		t.Fatalf("%d bus notes, want exactly 1", len(bus.notes))
	}
	note := bus.notes[0]
	if note.lane != "fleet" {
		t.Errorf("the note went to lane %q, want fleet", note.lane)
	}
	if !strings.Contains(note.subject, "space") {
		t.Errorf("the note's subject does not name the machine: %q", note.subject)
	}
	for _, want := range []string{"path-resolves", ItemPathNonInteractive, "shadows=18"} {
		if !strings.Contains(note.body, want) {
			t.Errorf("the note's body does not carry %q:\n%s", want, note.body)
		}
	}
	if Certified(mustRead(t, certs), "space", "path-resolves", "v0.17.0", "h") {
		t.Error("an escalated class is certified; it must stay uncertified so fill keeps refusing it")
	}
	if !Certified(mustRead(t, certs), "space", "c-build", "v0.17.0", "h") {
		t.Error("an escalation took the passing classes with it")
	}
}

// TestAFailureNoStandardItemRepairsEscalatesWithoutTouchingTheMachine: go-test failing is a
// broken toolchain inside the wall, and no line of ~/.bashrc fixes that. The escalation says
// so rather than applying something and hoping.
func TestAFailureNoStandardItemRepairsEscalatesWithoutTouchingTheMachine(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "go-test", "go: go.mod requires go >= 1.26.5 (running go 1.22.2)")
	fixer := &fakeFixer{}
	bus := &fakeBus{}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
		Fix: true, Fixer: fixer, Bus: bus, Lane: "fleet",
	})
	all := out + errs
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, all)
	}
	if len(fixer.calls) != 0 {
		t.Errorf("a class no item repairs was 'repaired' anyway: %v", fixer.calls)
	}
	if !strings.Contains(all, "CERTIFY ESCALATE machine=space classes=go-test") {
		t.Errorf("no escalation for the unrepairable class:\n%s", all)
	}
	if len(bus.notes) != 1 {
		t.Fatalf("%d bus notes, want 1", len(bus.notes))
	}
	if !strings.Contains(bus.notes[0].body, "go.mod requires go >= 1.26.5") {
		t.Errorf("the note does not carry what the machine said:\n%s", bus.notes[0].body)
	}
}

// TestAnEscalationIsAnEventThroughTheEmitter: the dashboard sees an escalation without
// anyone reading a terminal. One event, kind certify, at ERROR.
func TestAnEscalationIsAnEventThroughTheEmitter(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "git-identity", "GIT IDENTITY name=- email=-")
	var events strings.Builder
	_, _, _ = runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow, Log: &events,
		Fix: true, Fixer: &fakeFixer{}, Bus: &fakeBus{}, Lane: "fleet",
	})
	var escalations []string
	for _, line := range strings.Split(events.String(), "\n") {
		if strings.Contains(line, "escalate") {
			escalations = append(escalations, line)
		}
	}
	if len(escalations) != 1 {
		t.Fatalf("%d escalation events, want 1:\n%s", len(escalations), events.String())
	}
	for _, want := range []string{`"verb":"certify"`, `"level":"ERROR"`, `"bench":"space"`, "git-identity"} {
		if !strings.Contains(escalations[0], want) {
			t.Errorf("the escalation event does not carry %s:\n%s", want, escalations[0])
		}
	}
}

// TestAFixerThatCannotRunIsNamedAndEscalated: the apply itself failing is not a silent pass.
func TestAFixerThatCannotRunIsNamedAndEscalated(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "git-identity", "GIT IDENTITY name=- email=-")
	bus := &fakeBus{}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
		Fix: true, Fixer: &fakeFixer{err: errors.New("ssh: connect to host space port 22: Connection refused")},
		Bus: bus, Lane: "fleet",
	})
	all := out + errs
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, all)
	}
	if !strings.Contains(all, "Connection refused") {
		t.Errorf("the apply's own failure is not on a line:\n%s", all)
	}
	if !strings.Contains(all, "CERTIFY ESCALATE machine=space") {
		t.Errorf("an apply that could not run did not escalate:\n%s", all)
	}
}

// TestADryRunNeverRepairsAndNeverEscalates: --dry-run reaches nothing, and that includes the
// repair.
func TestADryRunNeverRepairsAndNeverEscalates(t *testing.T) {
	fixer := &fakeFixer{}
	bus := &fakeBus{}
	out, _, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: benchOK()}, Hash: "h", Now: fixedNow, DryRun: true,
		Fix: true, Fixer: fixer, Bus: bus, Lane: "fleet",
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if len(fixer.calls) != 0 || len(bus.notes) != 0 {
		t.Errorf("--dry-run applied %d repairs and sent %d notes", len(fixer.calls), len(bus.notes))
	}
}

// TestTheEscalationNamesEveryFailedClassOfOneMachineOnOneLine: one line per machine, never
// one per class -- the fleet has twenty machines and a person reading twenty lines reads
// none of them.
func TestTheEscalationNamesEveryFailedClassOfOneMachineOnOneLine(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "go-test", "go: go.mod requires go >= 1.26.5")
	failing(answers, "space", "sbcl", "/home/nova/.local/bin/sbcl: Permission denied")
	out, errs, _ := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
		Fix: true, Fixer: &fakeFixer{}, Bus: &fakeBus{}, Lane: "fleet",
	})
	all := out + errs
	want := "CERTIFY ESCALATE machine=space classes=go-test,sbcl"
	if !strings.Contains(all, want) {
		t.Errorf("no %q:\n%s", want, all)
	}
	if n := strings.Count(all, "CERTIFY ESCALATE"); n != 1 {
		t.Errorf("%d escalation lines for one machine, want 1", n)
	}
}
