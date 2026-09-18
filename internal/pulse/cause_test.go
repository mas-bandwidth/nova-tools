package pulse

// Class I's red tests (#828). Each signature maps to EXACTLY ONE action on the fixture, a
// second failure of the same kind fails and cuts a packet, and a card that failed once is
// re-cut under a new number with the cause line while the old text is refused at launch.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCauseMapsEachSignatureToOneAction is the rule table itself: four harness signatures,
// four actions, and nothing that maps to two.
func TestCauseMapsEachSignatureToOneAction(t *testing.T) {
	cases := []struct {
		name       string
		log        string
		result     bool
		wantKind   string
		wantAction string
	}{
		{
			name:       "a fence rejection is re-cut with the path named",
			log:        "step 3: writing /etc/hosts\nfence=rejected path=/etc/hosts rule=write-outside-root\nrc=1\n",
			wantKind:   CauseFence,
			wantAction: ActionRecut,
		},
		{
			name:       "a missing toolchain is the bench, not the card",
			log:        "bench space-3: toolchain not available: go1.25\nrc=127\n",
			wantKind:   CauseSignature,
			wantAction: ActionProbe,
		},
		{
			name:       "a missing go is the bench too",
			log:        "sh: command not found: go\nrc=127\n",
			wantKind:   CauseSignature,
			wantAction: ActionProbe,
		},
		{
			name:       "an unresolvable import is the bench too",
			log:        "cannot find package \"github.com/x/y\" in any of:\nrc=1\n",
			wantKind:   CauseSignature,
			wantAction: ActionProbe,
		},
		{
			name:       "a kill at the deadline is an orphan",
			log:        "supervise: deadline exceeded, signalling\nrc=143\n",
			wantKind:   CauseOrphan,
			wantAction: ActionRecut,
		},
		{
			name:       "a clean exit with no RESULT.md is nosha",
			log:        "step 4 done\nharness rc=0\n",
			result:     false,
			wantKind:   CauseNoSHA,
			wantAction: ActionRecut,
		},
		{
			name:       "a clean exit WITH a RESULT.md is not nosha",
			log:        "step 4 done\nharness rc=0\n",
			result:     true,
			wantKind:   CauseUnknown,
			wantAction: ActionTriage,
		},
		{
			name:       "a signature nobody named is a packet, never a rerun",
			log:        "worker said something nobody has a rule for\n",
			wantKind:   CauseUnknown,
			wantAction: ActionTriage,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, action := Cause([]byte(c.log), c.result)
			if kind != c.wantKind || action != c.wantAction {
				t.Fatalf("Cause = (%s, %s), want (%s, %s)", kind, action, c.wantKind, c.wantAction)
			}
		})
	}
}

// TestCauseLineNamesTheFencePath is the half of the rule a worker actually reads: the cause
// line has to say WHICH path was rejected, or the next attempt writes to it again.
func TestCauseLineNamesTheFencePath(t *testing.T) {
	sig := Read([]byte("fence=rejected path=/Users/glenn/secrets rule=write-outside-root\n"), false)
	if !strings.Contains(sig.Line, "/Users/glenn/secrets") {
		t.Errorf("the cause line does not name the path: %q", sig.Line)
	}
	if !strings.Contains(sig.Line, "write only under ./repo and ./scratch") {
		t.Errorf("the cause line does not say what to do instead: %q", sig.Line)
	}
	nosha := Read([]byte("harness rc=0\n"), false)
	if !strings.Contains(nosha.Line, "write RESULT.md as the last step") {
		t.Errorf("the nosha cause line does not name the remedy: %q", nosha.Line)
	}
	orphan := Read([]byte("rc=143\n"), false)
	if !strings.Contains(orphan.Line, "shorter step list") {
		t.Errorf("the orphan cause line does not ask for a shorter step list: %q", orphan.Line)
	}
	if Read([]byte("toolchain not available\n"), false).Line != "" {
		t.Errorf("a bench fault carries a cause line; the card is not what was wrong")
	}
}

// TestSecondFailureOfTheSameKindFails is the rule that ends the 174: a cause that survived
// its own remedy is a decision for the text route and never another launch.
func TestSecondFailureOfTheSameKindFails(t *testing.T) {
	sig := Read([]byte("fence=rejected path=/etc/hosts\n"), false)
	if action, _ := Decide(sig, nil); action != ActionRecut {
		t.Fatalf("first fence failure = %s, want %s", action, ActionRecut)
	}
	action, why := Decide(sig, []string{CauseFence})
	if action != ActionFail {
		t.Fatalf("second fence failure = %s, want %s", action, ActionFail)
	}
	if !strings.Contains(why, "second fence failure") {
		t.Errorf("the failure does not say why: %q", why)
	}
	if a, _ := Decide(sig, []string{CauseOrphan}); a != ActionRecut {
		t.Errorf("a failure of a DIFFERENT kind = %s, want %s: each cause gets its own remedy", a, ActionRecut)
	}
}

// TestRecutTakesANewNumberAndCarriesTheCause is class I's contract test: a card that failed
// once is re-cut under a new number with the cause line, and the old text is refused at
// launch.
func TestRecutTakesANewNumberAndCarriesTheCause(t *testing.T) {
	queue := t.TempDir()
	mustWrite(t, filepath.Join(queue, "state.tsv"), "next_card\t900\n")
	old := Stamp("RESULT: CARD-899 nova-tools #1 fixed with its red test first: a thing\n"+
		"SOURCE: mas-bandwidth/nova-tools mas-bandwidth/nova-tools#1\n"+
		"1. clone the repo\n2. write the test\n", "test")
	oldPath := filepath.Join(queue, "launched", "card-899.md")
	mustWrite(t, oldPath, old)

	sig := Read([]byte("fence=rejected path=/etc/hosts\n"), false)
	out, err := Recut(RecutInput{Queue: queue, CardPath: oldPath, Sig: sig, Version: "test",
		Now: func() time.Time { return time.Unix(0, 0).UTC() }})
	if err != nil {
		t.Fatalf("Recut: %v", err)
	}
	if filepath.Base(out) != "card-900.md" {
		t.Fatalf("the re-cut is %s, want card-900.md: a re-cut takes the NEXT number", filepath.Base(out))
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	card := string(body)
	if !strings.Contains(card, "Prior attempt: fence rejected /etc/hosts") {
		t.Errorf("the re-cut card carries no cause line:\n%s", card)
	}
	if !strings.HasPrefix(card, "RESULT: CARD-900 ") {
		t.Errorf("line 1 still carries the old number:\n%s", card)
	}
	if !CheckStamp(card).Valid {
		t.Errorf("the re-cut card is not stamped: %s", CheckStamp(card).Reason)
	}
	// The cause line sits above the steps, where it is read first.
	if strings.Index(card, "Prior attempt:") > strings.Index(card, "1. clone the repo") {
		t.Errorf("the cause line is below the steps:\n%s", card)
	}

	// THE OLD TEXT IS REFUSED AT LAUNCH.
	recuts := ReadRecuts(queue)
	if len(recuts) != 1 || recuts[0].To != "card-900.md" || recuts[0].Kind != CauseFence {
		t.Fatalf("RECUT.tsv = %+v, want one row naming card-900.md and %s", recuts, CauseFence)
	}
	ok, refusal, _ := CardGate(old, recuts, false)
	if ok {
		t.Fatal("the superseded card text was admitted; the same card text is never launched twice")
	}
	if !strings.Contains(refusal, "card-900.md") {
		t.Errorf("the refusal does not name the card that replaced it: %q", refusal)
	}
	if ok, _, _ := CardGate(card, recuts, false); !ok {
		t.Error("the re-cut card was refused; it is the one that should launch")
	}

	// And the history came with it, so a SECOND fence failure is seen under the new number.
	if got := PriorKinds(queue, "card-900.md"); len(got) != 1 || got[0] != CauseFence {
		t.Errorf("the new card's prior kinds = %v, want [%s]", got, CauseFence)
	}
	if line := PriorLine(queue, "card-900.md"); !strings.Contains(line, "fence rejected") {
		t.Errorf("the prior-attempts line a refill would pass = %q", line)
	}
}

// TestReapDisposesEachCauseOnce drives the reaper over four launched cards, one per cause,
// and checks the one line it prints and where each card went.
func TestReapDisposesEachCauseOnce(t *testing.T) {
	queue := t.TempDir()
	root := t.TempDir()
	mustWrite(t, filepath.Join(queue, "state.tsv"), "next_card\t500\n")
	old := time.Now().Add(-2 * time.Hour)

	write := func(card, log string, result bool) {
		path := filepath.Join(queue, "launched", card+".md")
		mustWrite(t, path, Stamp("RESULT: CARD-1 nova-tools #1 fixed with its red test first: x\nSOURCE: a b\n1. do it\n", "test"))
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(queue, "harness", card+".log"), log)
		if result {
			mustWrite(t, filepath.Join(queue, "harness", card+".RESULT.md"), "RESULT\n")
		}
	}
	write("card-1", "fence=rejected path=/etc/hosts\n", false)
	write("card-2", "toolchain not available: go\n", false)
	write("card-3", "rc=143\n", false)
	write("card-4", "nothing anybody has a rule for\n", true)

	var out, errs strings.Builder
	code := Reap(ReapInput{
		Roots: root, Queue: queue, Deadline: time.Minute, Version: "test",
		Procs: noProcs{}, TempGlob: filepath.Join(t.TempDir(), "none-*"),
		Now: func() time.Time { return time.Now().UTC() }, Stdout: &out, Stderr: &errs,
	})
	if code != 0 {
		t.Fatalf("Reap = %d, stderr=%s", code, errs.String())
	}
	line := lastLine(out.String())
	for _, want := range []string{"requeued=3", "recut=2", "probe=1", "failed=1", "triaged=1"} {
		if !strings.Contains(line, want) {
			t.Errorf("the REAP line has no %s:\n%s", want, line)
		}
	}
	// The fence and the orphan were re-cut under new numbers.
	for _, n := range []string{"card-500.md", "card-501.md"} {
		if _, err := os.Stat(filepath.Join(queue, "pending", n)); err != nil {
			t.Errorf("no re-cut %s in pending: %v", n, err)
		}
	}
	// The bench fault went back to pending with its own text and left a probe row.
	if _, err := os.Stat(filepath.Join(queue, "pending", "card-2.md")); err != nil {
		t.Errorf("the bench-fault card was not requeued unchanged: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(queue, "PROBE.tsv")); err != nil || !strings.Contains(string(raw), "card-2.md") {
		t.Errorf("no probe row for the bench fault: %v %q", err, string(raw))
	}
	// The unreadable signature failed and left a packet.
	if _, err := os.Stat(filepath.Join(queue, "failed", "card-4.md")); err != nil {
		t.Errorf("the unreadable signature was not failed: %v", err)
	}
	packet := filepath.Join(queue, "triage", "triage-signature-card-4.md")
	raw, err := os.ReadFile(packet)
	if err != nil {
		t.Fatalf("no triage packet: %v", err)
	}
	if len(raw) > PacketMax {
		t.Errorf("the packet is %d bytes, over PacketMax %d", len(raw), PacketMax)
	}
}

// TestReapFailsTheSecondFailureOfACause runs the reaper twice over the same cause and
// checks that the second time it fails the card and cuts a packet instead of re-cutting.
func TestReapFailsTheSecondFailureOfACause(t *testing.T) {
	queue := t.TempDir()
	root := t.TempDir()
	mustWrite(t, filepath.Join(queue, "state.tsv"), "next_card\t700\n")
	old := time.Now().Add(-2 * time.Hour)

	launch := func(card string) {
		path := filepath.Join(queue, "launched", card+".md")
		mustWrite(t, path, Stamp("RESULT: CARD-1 nova-tools #1 fixed with its red test first: x\nSOURCE: a b\n1. do it\n", "test"))
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(queue, "harness", card+".log"), "fence=rejected path=/etc/hosts\n")
	}
	run := func() string {
		var out, errs strings.Builder
		if code := Reap(ReapInput{
			Roots: root, Queue: queue, Deadline: time.Minute, Version: "test",
			Procs: noProcs{}, TempGlob: filepath.Join(t.TempDir(), "none-*"),
			Now: func() time.Time { return time.Now().UTC() }, Stdout: &out, Stderr: &errs,
		}); code != 0 {
			t.Fatalf("Reap = %d: %s", code, errs.String())
		}
		return out.String()
	}

	launch("card-1")
	if line := lastLine(run()); !strings.Contains(line, "recut=1") {
		t.Fatalf("the first fence failure was not re-cut:\n%s", line)
	}
	// The re-cut card is card-700; fail it the same way, and the cause has had its remedy.
	if err := os.Rename(filepath.Join(queue, "pending", "card-700.md"), filepath.Join(queue, "launched", "card-700.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(queue, "launched", "card-700.md"), old, old); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(queue, "harness", "card-700.log"), "fence=rejected path=/etc/hosts\n")

	line := lastLine(run())
	if !strings.Contains(line, "failed=1") || !strings.Contains(line, "triaged=1") || strings.Contains(line, "recut=1") {
		t.Fatalf("a second failure of the same cause was not failed with a packet:\n%s", line)
	}
	if _, err := os.Stat(filepath.Join(queue, "failed", "card-700.md")); err != nil {
		t.Errorf("the twice-failed card is not in failed/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queue, "triage", "triage-fence-card-700.md")); err != nil {
		t.Errorf("no fence packet for the twice-failed card: %v", err)
	}
}

// noProcs is a process table with nothing in it: these tests are about cards, and no test in
// this package signals any pid.
type noProcs struct{}

func (noProcs) List() ([]Proc, error) { return nil, nil }
func (noProcs) Alive(int) bool        { return true }
func (noProcs) Kill(int) error        { return nil }

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
