package lifecycle_test

// SPEC-PULSE required tests 1, 2, 3, 4, 7 and 8 for "Durable launch, attempts
// and fleet control", as far as they can be proven without the real control
// store. Stella owns CONTROL; this package uses a fake that linearizes RUN vs
// PAUSE inside WithCoordinator. No check-then-unlock.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/lifecycle"
)

func TestDurableLaunch1_TwoDealersRaceOneReadyCard(t *testing.T) {
	root := t.TempDir()
	a, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	b, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("open B: %v", err)
	}

	type outcome struct {
		attempt string
		rev     int
		err     error
	}
	var ra, rb outcome
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		att, rev, err := a.Claim("card1", "hulk", "local")
		ra = outcome{att, rev, err}
	}()
	go func() {
		defer wg.Done()
		<-start
		att, rev, err := b.Claim("card1", "hulk", "local")
		rb = outcome{att, rev, err}
	}()
	close(start)
	wg.Wait()

	wins := 0
	var win outcome
	for _, o := range []outcome{ra, rb} {
		if o.err == nil {
			wins++
			win = o
			continue
		}
		if !errors.Is(o.err, lifecycle.ErrConflict) {
			t.Errorf("loser error %v, want ErrConflict", o.err)
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one dealer must win the READY->CLAIMED CAS, got %d winners A=%v B=%v", wins, ra, rb)
	}
	if win.attempt == "" || win.attempt == "card1" {
		t.Fatalf("winning CLAIM must mint an unguessable attempt, got %q", win.attempt)
	}
	if !isHex32(win.attempt) {
		t.Errorf("attempt %q is not 32 unguessable hex characters", win.attempt)
	}
	if win.rev != 1 {
		t.Errorf("first claim rev=%d, want 1", win.rev)
	}

	events := readEvents(t, root)
	if len(events) != 1 {
		t.Fatalf("exactly one durable claim must exist, events.jsonl has %d lines", len(events))
	}
	if events[0]["new"] != "CLAIMED" {
		t.Errorf("event new=%v, want CLAIMED", events[0]["new"])
	}
	if events[0]["prior"] != "READY" {
		t.Errorf("event prior=%v, want READY", events[0]["prior"])
	}
	if events[0]["attempt"] != win.attempt {
		t.Errorf("event attempt=%v, want %s", events[0]["attempt"], win.attempt)
	}

	dirs, err := os.ReadDir(filepath.Join(root, "lifecycle", "attempts"))
	if err != nil {
		t.Fatalf("attempts dir: %v", err)
	}
	if len(dirs) != 1 || dirs[0].Name() != win.attempt {
		t.Errorf("attempts/ = %v, want one directory named the minted attempt", names(dirs))
	}

	c, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("reopen after race: %v", err)
	}
	p, ok := c.Lookup("card1")
	if !ok {
		t.Fatal("projection missing after the winning claim")
	}
	if p.State != lifecycle.Claimed {
		t.Errorf("state=%s, want CLAIMED", p.State)
	}
	if p.Attempt == nil || *p.Attempt != win.attempt {
		t.Errorf("projected attempt=%v, want %s", p.Attempt, win.attempt)
	}
}

func TestDurableLaunch2_NoAcknowledgementBecomesUnknown(t *testing.T) {
	root := t.TempDir()
	s, ctrl, now := openRun(t, root)
	attempt := claimAndAdmit(t, s, ctrl, now, "card2", "hulk", "local")

	stdout := []byte("launcher still writing\n")
	stderr := []byte("deadline passed\n")
	exit := 1
	if err := s.ApplyUnknown(lifecycle.UnknownWhy{
		Attempt: attempt,
		Why:     lifecycle.WhyTimeout,
		Stdout:  stdout,
		Stderr:  stderr,
		Exit:    &exit,
	}); err != nil {
		t.Fatalf("ApplyUnknown: %v", err)
	}

	p, ok := s.Lookup("card2")
	if !ok {
		t.Fatal("card missing after UNKNOWN")
	}
	if p.State != lifecycle.Unknown {
		t.Errorf("state=%s, want UNKNOWN", p.State)
	}
	if !p.Reserved {
		t.Error("UNKNOWN must retain its reservation")
	}
	if p.Attempt == nil || *p.Attempt != attempt {
		t.Errorf("attempt moved off %s", attempt)
	}
	if !p.Raised {
		t.Error("UNKNOWN event must carry raised=true")
	}
	if p.Reason == nil || *p.Reason != lifecycle.WhyTimeout {
		t.Errorf("reason=%v, want timeout", p.Reason)
	}

	gotOut, err := os.ReadFile(filepath.Join(root, "lifecycle", "attempts", attempt, "stdout"))
	if err != nil || string(gotOut) != string(stdout) {
		t.Errorf("stdout retained %q err=%v", gotOut, err)
	}
	gotErr, err := os.ReadFile(filepath.Join(root, "lifecycle", "attempts", attempt, "stderr"))
	if err != nil || string(gotErr) != string(stderr) {
		t.Errorf("stderr retained %q err=%v", gotErr, err)
	}
	gotExit, err := os.ReadFile(filepath.Join(root, "lifecycle", "attempts", attempt, "exit"))
	if err != nil || strings.TrimSpace(string(gotExit)) != "1" {
		t.Errorf("exit retained %q err=%v", gotExit, err)
	}

	if err := s.AdmitStart(context.Background(), now, ctrl, attempt); err == nil {
		t.Fatal("a second invocation must not occur after UNKNOWN")
	}
	if _, _, err := s.Claim("card2", "hulk", "local"); !errors.Is(err, lifecycle.ErrConflict) {
		t.Errorf("retry Claim after UNKNOWN: %v, want conflict (reservation remains)", err)
	}
}

func TestDurableLaunch3_BoundAcknowledgementMakesStarted(t *testing.T) {
	t.Run("correct-binding-makes-started", func(t *testing.T) {
		root := t.TempDir()
		s, ctrl, now := openRun(t, root)
		claimAndAdmit(t, s, ctrl, now, "card3", "hulk", "local")
		p := mustLookup(t, s, "card3")
		receipt := boundReceipt(p, now)
		line := receipt.Line()
		parsed, err := lifecycle.ParseStarted(line)
		if err != nil {
			t.Fatalf("ParseStarted(%q): %v", line, err)
		}
		if err := s.ApplyStarted(parsed); err != nil {
			t.Fatalf("ApplyStarted: %v", err)
		}
		got := mustLookup(t, s, "card3")
		if got.State != lifecycle.Started {
			t.Errorf("state=%s, want STARTED", got.State)
		}
	})

	t.Run("wrong-identity-is-malformed-and-cannot-start", func(t *testing.T) {
		root := t.TempDir()
		s, ctrl, now := openRun(t, root)
		attempt := claimAndAdmit(t, s, ctrl, now, "card3b", "hulk", "local")
		p := mustLookup(t, s, "card3b")
		good := boundReceipt(p, now)
		for _, mutate := range []struct {
			name string
			edit func(*lifecycle.StartedReceipt)
		}{
			{"card", func(r *lifecycle.StartedReceipt) { r.Card = "other-card" }},
			{"attempt", func(r *lifecycle.StartedReceipt) { r.Attempt = "ffffffffffffffffffffffffffffffff" }},
			{"job", func(r *lifecycle.StartedReceipt) { r.Job = "wrong-job" }},
			{"lease", func(r *lifecycle.StartedReceipt) { r.Lease = "wrong-lease" }},
			{"generation", func(r *lifecycle.StartedReceipt) { r.Generation = r.Generation + 1 }},
		} {
			bad := good
			mutate.edit(&bad)
			err := s.ApplyStarted(bad)
			if !errors.Is(err, lifecycle.ErrMalformed) {
				t.Errorf("%s: ApplyStarted=%v, want ErrMalformed", mutate.name, err)
			}
			got := mustLookup(t, s, "card3b")
			if got.State != lifecycle.Starting {
				t.Errorf("%s: state=%s, wrong identity must not start", mutate.name, got.State)
			}
		}
		_ = attempt
	})

	t.Run("late-correct-ack-reconciles-same-attempt", func(t *testing.T) {
		root := t.TempDir()
		s, ctrl, now := openRun(t, root)
		attempt := claimAndAdmit(t, s, ctrl, now, "card3c", "hulk", "local")
		p := mustLookup(t, s, "card3c")
		receipt := boundReceipt(p, now)
		if err := s.ApplyUnknown(lifecycle.UnknownWhy{Attempt: attempt, Why: lifecycle.WhyNoStart}); err != nil {
			t.Fatalf("ApplyUnknown: %v", err)
		}
		if mustLookup(t, s, "card3c").State != lifecycle.Unknown {
			t.Fatal("setup: want UNKNOWN before the late ack")
		}
		if err := s.ApplyStarted(receipt); err != nil {
			t.Fatalf("late ApplyStarted: %v", err)
		}
		got := mustLookup(t, s, "card3c")
		if got.State != lifecycle.Started {
			t.Errorf("late ack state=%s, want STARTED", got.State)
		}
		if got.Attempt == nil || *got.Attempt != attempt {
			t.Errorf("late ack consumed another attempt: %v", got.Attempt)
		}
	})
}

func TestDurableLaunch4_MissingLeaseAndJobDirLeaveUnknownReserved(t *testing.T) {
	root := t.TempDir()
	s, ctrl, now := openRun(t, root)

	t.Run("before-starting-never-admitted-is-legal", func(t *testing.T) {
		attempt, _, err := s.Claim("card4a", "hulk", "local")
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := s.RecordNeverAdmitted(attempt); err != nil {
			t.Fatalf("never-admitted on CLAIMED: %v", err)
		}
		got := mustLookup(t, s, "card4a")
		if got.State != lifecycle.Claimed {
			t.Errorf("never-admitted must not change CLAIMED, got %s", got.State)
		}
		if got.Execution == nil || *got.Execution != lifecycle.ExecutionNeverAdmitted {
			t.Errorf("execution=%v, want never-admitted", got.Execution)
		}
	})

	attempt := claimAndAdmit(t, s, ctrl, now, "card4", "hulk", "local")
	p := mustLookup(t, s, "card4")
	if p.Job == nil || p.Lease == nil || p.Nonce == nil || p.ExitAttest == nil {
		t.Fatalf("STARTING must bind job, lease, nonce and exit_attest, got %+v", p)
	}

	jobDir := filepath.Join(root, "jobs", *p.Job)
	leasePath := filepath.Join(root, "leases", *p.Lease)
	if _, err := os.Stat(jobDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job directory control must be absent for this test, stat=%v", err)
	}
	if _, err := os.Stat(leasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lease control must be absent for this test, stat=%v", err)
	}

	if err := s.RecordNeverAdmitted(attempt); err == nil {
		t.Fatal("after STARTING, never-admitted must be refused")
	}
	if err := s.RecordExecutorEnded(attempt, lifecycle.ExecutionTerminated, "wrong-attest", "wrong-nonce"); err == nil {
		t.Fatal("mismatched exit_attest/nonce must not reconcile")
	}

	if err := s.ApplyUnknown(lifecycle.UnknownWhy{Attempt: attempt, Why: lifecycle.WhyLostReply}); err != nil {
		t.Fatalf("ApplyUnknown: %v", err)
	}
	got := mustLookup(t, s, "card4")
	if got.State != lifecycle.Unknown {
		t.Errorf("missing lease/job-dir must leave UNKNOWN, got %s", got.State)
	}
	if !got.Reserved {
		t.Error("UNKNOWN must remain reserved")
	}

	if err := s.RecordExecutorEnded(attempt, lifecycle.ExecutionTerminated, *p.ExitAttest, *p.Nonce); err != nil {
		t.Fatalf("matching executor receipt after STARTING: %v", err)
	}

	if _, err := s.AdvanceFence("card4"); err != nil {
		t.Fatalf("AdvanceFence: %v", err)
	}
	if _, _, err := s.Claim("card4", "hulk", "local"); !errors.Is(err, lifecycle.ErrConflict) {
		t.Errorf("fence advancement alone must forbid retry, Claim err=%v", err)
	}
}

func TestDurableLaunch7_CrashReconstructsClaimedNotReady(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	ctrl := newFake(now)

	s, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	attempt, _, err := s.Claim("card7", "hulk", "local")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	cardPath := filepath.Join(root, "lifecycle", "cards", "card7.json")
	if err := os.Remove(cardPath); err != nil {
		t.Fatalf("simulate crash before projection: %v", err)
	}

	recovered, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("replay after CLAIM crash: %v", err)
	}
	got := mustLookup(t, recovered, "card7")
	if got.State != lifecycle.Claimed {
		t.Errorf("crash after CLAIM reconstructed %s, want CLAIMED", got.State)
	}
	if got.Attempt == nil || *got.Attempt != attempt {
		t.Errorf("reconstructed attempt=%v, want %s", got.Attempt, attempt)
	}
	if err := recovered.RecordNeverAdmitted(attempt); err != nil {
		t.Fatalf("reconcile reconstructed CLAIMED: %v", err)
	}

	s2, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("open for STARTING path: %v", err)
	}
	attempt2, _, err := s2.Claim("card7b", "hulk", "local")
	if err != nil {
		t.Fatalf("Claim 7b: %v", err)
	}
	if err := s2.AdmitStart(context.Background(), now, ctrl, attempt2); err != nil {
		t.Fatalf("AdmitStart: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "lifecycle", "cards", "card7b.json")); err != nil {
		t.Fatalf("simulate crash after STARTING: %v", err)
	}

	afterStart, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("replay after STARTING crash: %v", err)
	}
	got = mustLookup(t, afterStart, "card7b")
	if got.State != lifecycle.Starting {
		t.Errorf("crash after STARTING reconstructed %s, want STARTING", got.State)
	}
	if got.State == lifecycle.Ready {
		t.Fatal("absence must not return READY")
	}
	if err := afterStart.ApplyUnknown(lifecycle.UnknownWhy{Attempt: attempt2, Why: lifecycle.WhyNoStart}); err != nil {
		t.Fatalf("UNKNOWN after STARTING crash: %v", err)
	}
	got = mustLookup(t, afterStart, "card7b")
	if got.State != lifecycle.Unknown {
		t.Errorf("after STARTING crash, reconciliation became %s, want UNKNOWN", got.State)
	}
	if _, _, err := afterStart.Claim("card7b", "hulk", "local"); !errors.Is(err, lifecycle.ErrConflict) {
		t.Errorf("absence must not return READY: Claim err=%v", err)
	}
}

func TestDurableLaunch8_PauseRacingLaunchTwoLegalHistories(t *testing.T) {
	now := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)

	t.Run("starting-linearizes-first-then-pause-outstanding", func(t *testing.T) {
		root := t.TempDir()
		s, ctrl, _ := openRunAt(t, root, now)
		attempt := claimOnly(t, s, "card8a", "hulk", "local")
		if err := s.AdmitStart(context.Background(), now, ctrl, attempt); err != nil {
			t.Fatalf("AdmitStart: %v", err)
		}
		ctrl.Pause()
		got := mustLookup(t, s, "card8a")
		if got.State != lifecycle.Starting {
			t.Errorf("admitted start after later PAUSE is %s, want STARTING (outstanding)", got.State)
		}
		if got.Generation == nil || *got.Generation != 1 {
			t.Errorf("generation=%v, want the RUN generation 1, not PAUSE's n+1", got.Generation)
		}
	})

	t.Run("pause-linearizes-first-admission-refused", func(t *testing.T) {
		root := t.TempDir()
		s, ctrl, _ := openRunAt(t, root, now)
		attempt := claimOnly(t, s, "card8b", "hulk", "local")
		ctrl.Pause()
		err := s.AdmitStart(context.Background(), now, ctrl, attempt)
		if !errors.Is(err, lifecycle.ErrRefused) {
			t.Fatalf("AdmitStart after PAUSE: %v, want ErrRefused", err)
		}
		got := mustLookup(t, s, "card8b")
		if got.State != lifecycle.Claimed {
			t.Errorf("state=%s, PAUSE first must leave CLAIMED", got.State)
		}
	})

	t.Run("issued-unconsumed-token-cannot-start", func(t *testing.T) {
		root := t.TempDir()
		s, ctrl, _ := openRunAt(t, root, now)
		attempt := claimOnly(t, s, "card8c", "hulk", "local")
		_ = ctrl.Issue(attempt)
		ctrl.Pause()
		err := s.AdmitStart(context.Background(), now, ctrl, attempt)
		if !errors.Is(err, lifecycle.ErrRefused) {
			t.Fatalf("unconsumed issued token started: %v", err)
		}
		if mustLookup(t, s, "card8c").State != lifecycle.Claimed {
			t.Errorf("state=%s, issued-but-unconsumed must not admit", mustLookup(t, s, "card8c").State)
		}
	})

	t.Run("offline-stale-missing-ack-or-paused-bench-cannot-start", func(t *testing.T) {
		for _, flag := range []string{"offline", "stale", "missing-ack", "paused-bench"} {
			root := t.TempDir()
			s, ctrl, _ := openRunAt(t, root, now)
			attempt := claimOnly(t, s, "card8d", "hulk", "local")
			switch flag {
			case "offline":
				ctrl.Offline = true
			case "stale":
				ctrl.StaleAck = true
			case "missing-ack":
				ctrl.MissingAck = true
			case "paused-bench":
				ctrl.PausedBench = true
			}
			err := s.AdmitStart(context.Background(), now, ctrl, attempt)
			if !errors.Is(err, lifecycle.ErrRefused) {
				t.Errorf("%s: AdmitStart=%v, want ErrRefused", flag, err)
			}
			if mustLookup(t, s, "card8d").State != lifecycle.Claimed {
				t.Errorf("%s: state=%s, want CLAIMED", flag, mustLookup(t, s, "card8d").State)
			}
		}
	})

	t.Run("concurrent-race-is-one-of-the-two-legal-histories", func(t *testing.T) {
		root := t.TempDir()
		s, ctrl, _ := openRunAt(t, root, now)
		attempt := claimOnly(t, s, "card8e", "hulk", "local")
		start := make(chan struct{})
		var wg sync.WaitGroup
		var admitErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			admitErr = s.AdmitStart(context.Background(), now, ctrl, attempt)
		}()
		go func() {
			defer wg.Done()
			<-start
			ctrl.Pause()
		}()
		close(start)
		wg.Wait()

		got := mustLookup(t, s, "card8e")
		switch {
		case admitErr == nil:
			if got.State != lifecycle.Starting {
				t.Errorf("history A: state=%s, want STARTING", got.State)
			}
			if got.Generation == nil || *got.Generation != 1 {
				t.Errorf("history A: generation=%v, want RUN generation 1 (PAUSE must not be the admission generation)", got.Generation)
			}
		case errors.Is(admitErr, lifecycle.ErrRefused):
			if got.State != lifecycle.Claimed {
				t.Errorf("history B: state=%s, want CLAIMED", got.State)
			}
		default:
			t.Fatalf("third history: AdmitStart=%v state=%s", admitErr, got.State)
		}
	})
}

func TestReplayRefusesClaimedToReady(t *testing.T) {
	root := t.TempDir()
	s, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, _, err := s.Claim("card-hold1", "hulk", "local"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if mustLookup(t, s, "card-hold1").State != lifecycle.Claimed {
		t.Fatal("setup: want CLAIMED")
	}

	illegal := `{"card":"card-hold1","attempt":null,"prior":"CLAIMED","new":"READY","rev":2,"generation":null,"bench":"hulk","route":"local","source":"card-hold1","job":null,"lease":null,"limits":{"attempts":1,"max":2},"at":"2026-09-20T14:00:00Z","idempotency":"illegal-ready"}` + "\n"
	f, err := os.OpenFile(filepath.Join(root, "lifecycle", "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := f.WriteString(illegal); err != nil {
		t.Fatalf("append illegal event: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	_, err = lifecycle.Open(root)
	if !errors.Is(err, lifecycle.ErrMalformed) {
		t.Fatalf("Open accepted CLAIMED->READY (reservation would be released): %v", err)
	}
	_, _, err = s.Claim("card-hold1", "hulk", "local")
	if err == nil {
		t.Fatal("Claim succeeded after CLAIMED->READY; replay released the reservation")
	}
	if !errors.Is(err, lifecycle.ErrMalformed) && !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("Claim after illegal READY: %v, want fail-closed (malformed or still claimed)", err)
	}
}

func TestApplyStartedIdenticalRetryIsIdempotent(t *testing.T) {
	root := t.TempDir()
	s, ctrl, now := openRun(t, root)
	attempt := claimAndAdmit(t, s, ctrl, now, "card-hold2", "hulk", "local")
	p := mustLookup(t, s, "card-hold2")
	receipt := boundReceipt(p, now)
	if err := s.ApplyStarted(receipt); err != nil {
		t.Fatalf("first ApplyStarted: %v", err)
	}
	if err := s.ApplyStarted(receipt); err != nil {
		t.Fatalf("identical bound STARTED retry: %v", err)
	}
	got := mustLookup(t, s, "card-hold2")
	if got.State != lifecycle.Started {
		t.Errorf("state=%s, want STARTED", got.State)
	}
	started := 0
	for _, ev := range readEvents(t, root) {
		if ev["new"] == "STARTED" {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("identical retry wrote %d STARTED events, want 1", started)
	}

	receiptPath := filepath.Join(root, "lifecycle", "attempts", attempt, "started")
	if err := os.Remove(receiptPath); err != nil {
		t.Fatalf("remove receipt: %v", err)
	}
	if err := s.ApplyStarted(receipt); err != nil {
		t.Fatalf("STARTED replay must restore the receipt: %v", err)
	}
	body, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("receipt not restored: %v", err)
	}
	if strings.TrimSpace(string(body)) != receipt.Line() {
		t.Errorf("restored receipt %q, want %q", body, receipt.Line())
	}

	bad := receipt
	bad.Worker = "other-worker"
	err = s.ApplyStarted(bad)
	if err == nil {
		t.Fatal("different STARTED payload under the same attempt was accepted")
	}
	if !errors.Is(err, lifecycle.ErrRefused) && !errors.Is(err, lifecycle.ErrMalformed) {
		t.Errorf("different payload: %v, want refused or malformed", err)
	}
}

func openRun(t *testing.T, root string) (*lifecycle.Store, *fakeControl, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 20, 14, 0, 0, 0, time.UTC)
	return openRunAt(t, root, now)
}

func openRunAt(t *testing.T, root string, now time.Time) (*lifecycle.Store, *fakeControl, time.Time) {
	t.Helper()
	s, err := lifecycle.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, newFake(now), now
}

func claimOnly(t *testing.T, s *lifecycle.Store, card, bench, route string) string {
	t.Helper()
	attempt, _, err := s.Claim(card, bench, route)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return attempt
}

func claimAndAdmit(t *testing.T, s *lifecycle.Store, ctrl *fakeControl, now time.Time, card, bench, route string) string {
	t.Helper()
	attempt := claimOnly(t, s, card, bench, route)
	if err := s.AdmitStart(context.Background(), now, ctrl, attempt); err != nil {
		t.Fatalf("AdmitStart: %v", err)
	}
	return attempt
}

func mustLookup(t *testing.T, s *lifecycle.Store, card string) lifecycle.Projection {
	t.Helper()
	p, ok := s.Lookup(card)
	if !ok {
		t.Fatalf("Lookup(%q) missing", card)
	}
	return p
}

func boundReceipt(p lifecycle.Projection, now time.Time) lifecycle.StartedReceipt {
	gen := 0
	if p.Generation != nil {
		gen = *p.Generation
	}
	return lifecycle.StartedReceipt{
		Card:       p.Card,
		Attempt:    deref(p.Attempt),
		Job:        deref(p.Job),
		Lease:      deref(p.Lease),
		Bench:      deref(p.Bench),
		Route:      deref(p.Route),
		Generation: gen,
		Worker:     "worker-1",
		At:         now,
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func readEvents(t *testing.T, root string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "lifecycle", "events.jsonl"))
	if err != nil {
		t.Fatalf("events.jsonl: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("event %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func names(ds []os.DirEntry) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Name())
	}
	return out
}

func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// fakeControl linearizes RUN vs PAUSE: WithCoordinator holds the same mutex
// Pause takes, and ConsumeStartToken is the only admission check. A desired
// read outside that lock would be check-then-unlock.
type fakeControl struct {
	mu          sync.Mutex
	desired     string
	generation  int
	expires     time.Time
	consumed    map[string]lifecycle.StartToken
	issued      map[string]lifecycle.StartToken
	now         time.Time
	Offline     bool
	StaleAck    bool
	MissingAck  bool
	PausedBench bool
}

func newFake(now time.Time) *fakeControl {
	return &fakeControl{
		desired:    "RUN",
		generation: 1,
		expires:    now.Add(time.Hour),
		consumed:   map[string]lifecycle.StartToken{},
		issued:     map[string]lifecycle.StartToken{},
		now:        now,
	}
}

func (f *fakeControl) WithCoordinator(ctx context.Context, now time.Time, fn func(lifecycle.Admission) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	f.now = now
	desired := f.desired
	generation := f.generation
	consumed := cloneTokens(f.consumed)
	issued := cloneTokens(f.issued)
	err := fn(f)
	if err != nil {
		f.desired = desired
		f.generation = generation
		f.consumed = consumed
		f.issued = issued
	}
	return err
}

func (f *fakeControl) ConsumeStartToken(attempt string) (lifecycle.StartToken, error) {
	if f.Offline || f.StaleAck || f.MissingAck || f.PausedBench {
		return lifecycle.StartToken{}, lifecycle.ErrRefused
	}
	if f.desired != "RUN" {
		return lifecycle.StartToken{}, lifecycle.ErrRefused
	}
	if !f.now.Before(f.expires) {
		return lifecycle.StartToken{}, lifecycle.ErrRefused
	}
	tok := lifecycle.StartToken{Generation: f.generation, Scope: "fleet", Expires: f.expires}
	f.consumed[attempt] = tok
	return tok, nil
}

func (f *fakeControl) Pause() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
	f.desired = "PAUSE"
}

func (f *fakeControl) Issue(attempt string) lifecycle.StartToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := lifecycle.StartToken{Generation: f.generation, Scope: "fleet", Expires: f.expires}
	f.issued[attempt] = tok
	return tok
}

func cloneTokens(in map[string]lifecycle.StartToken) map[string]lifecycle.StartToken {
	out := make(map[string]lifecycle.StartToken, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
