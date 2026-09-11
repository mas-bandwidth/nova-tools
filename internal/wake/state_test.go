package wake

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The second lesson of 2026-09-11, at the layer that bought it: a state value
// round-trips or it is not state. The prototype joined a value with tabs, the
// reload ate the trailing empty field, and every poll thereafter reported a
// change over a bus that had not moved.
func TestAValueRoundTripsByteForByte(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"a trailing empty field", Compose("OPEN", "0", "0", "3", "")},
		{"every field empty", Compose("", "", "")},
		{"a literal pipe", Compose("report:/a|b/RESULT.md", "17:42")},
		{"a literal percent-7C", Compose("%7C", "x")},
		{"a literal percent-25", Compose("%25", "x")},
		{"a tab and a newline", Compose("a\tb", "c\nd")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state")
			s, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			s.Observe("entry:x#1", tc.value, "id0")
			for _, r := range s.Queue() {
				s.MarkPrinted(r)
			}
			if err := s.Save(path); err != nil {
				t.Fatal(err)
			}
			back, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := back.Newest("entry:x#1"); got != tc.value {
				t.Fatalf("reloaded value %q, stored %q", got, tc.value)
			}
			// The poll that follows the reload observes exactly what was
			// stored, and must be quiet.
			if back.Observe("entry:x#1", tc.value, "id0") {
				t.Errorf("a second poll of an unchanged value reported a change: this is the false wake of 2026-09-11")
			}
			if back.Pending() != 0 {
				t.Errorf("pending = %d after a quiet poll, want 0", back.Pending())
			}
		})
	}
}

// Compose and Decompose are inverse, and the escape is applied % first so a
// value holding a literal %7C is not a pipe when it comes back.
func TestComposeDecomposeAreInverse(t *testing.T) {
	parts := []string{"a|b", "%7C", "%25", "", "plain", "%"}
	got := Decompose(Compose(parts...))
	if len(got) != len(parts) {
		t.Fatalf("decomposed %d parts, composed %d: %q", len(got), len(parts), got)
	}
	for i := range parts {
		if got[i] != parts[i] {
			t.Errorf("part %d round-tripped as %q, was %q", i, got[i], parts[i])
		}
	}
}

// Rule 11: an observation is APPENDED behind an unprinted one and never over
// it. Red then green with the red unprinted is two records, red first.
func TestAnObservationIsAppendedBehindAnUnprintedOne(t *testing.T) {
	s := newState()
	if !s.Observe("entry:r#1", "red", "id-red") {
		t.Fatal("the first observation of a key is a change")
	}
	if !s.Observe("entry:r#1", "green", "id-green") {
		t.Fatal("a second, different observation is a change")
	}
	q := s.Queue()
	if len(q) != 2 {
		t.Fatalf("queue holds %d records, want 2 (nothing coalesces)", len(q))
	}
	if q[0].Value != "red" || q[1].Value != "green" {
		t.Fatalf("queue is %q then %q, want red then green", q[0].Value, q[1].Value)
	}
	if s.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", s.Pending())
	}
	if !s.IsPending("entry:r#1") {
		t.Error("a key a queue record names is pending")
	}
	s.MarkPrinted(q[0])
	if got := s.PrintedID("entry:r#1"); got != "id-red" {
		t.Errorf("printed id = %q after printing the red line, want id-red", got)
	}
	s.MarkPrinted(q[1])
	if s.Pending() != 0 || s.IsPending("entry:r#1") {
		t.Errorf("pending = %d after both were printed, want 0", s.Pending())
	}
}

// The queue numbering is never reused, so a record printed by one call cannot
// be confused with a record appended by the next.
func TestQueueNumbersAreNeverReused(t *testing.T) {
	s := newState()
	s.Observe("report:a", "1", "a1")
	first := s.Queue()[0].N
	s.MarkPrinted(s.Queue()[0])
	s.Observe("report:a", "2", "a2")
	if got := s.Queue()[0].N; got == first {
		t.Fatalf("the second record reused number %d", got)
	}
}

// The LRU is bounded where it grows with events and exempt where a bound would
// lose a delivery: a pending record survives 300 newer delivered ones.
func TestEvictionKeepsPendingAndTakesTheOldestDelivered(t *testing.T) {
	s := newState()
	// One pending note, observed first and never printed.
	s.Observe("bus:note:pending", "p", "pending")
	for i := 0; i < LRUMax+50; i++ {
		key := "bus:note:" + strings.Repeat("0", 3) + itoa(i)
		s.Observe(key, "v", "id"+itoa(i))
		for _, r := range s.Queue() {
			if r.Key == key {
				s.MarkPrinted(r)
			}
		}
	}
	s.Evict()
	if _, ok := s.Newest("bus:note:pending"); !ok {
		t.Error("the pending note was evicted; a record leaves the state only by being printed")
	}
	if s.Pending() != 1 {
		t.Errorf("pending = %d, want 1", s.Pending())
	}
	delivered := 0
	for _, k := range s.Keys() {
		if strings.HasPrefix(k, "bus:note:") {
			delivered++
		}
	}
	if delivered > LRUMax+1 {
		t.Errorf("%d bus:note: keys after eviction, want at most %d (the LRU plus the exempt pending one)", delivered, LRUMax+1)
	}
	// The oldest delivered ones go first.
	if _, ok := s.Newest("bus:note:0000"); ok {
		t.Error("the oldest delivered note survived while newer ones were evicted")
	}
	if _, ok := s.Newest("bus:note:000" + itoa(LRUMax+49)); !ok {
		t.Error("the newest delivered note was evicted")
	}
}

// An unparsable state file is an error and NEVER a silent cold start: a run
// that decides to start cold has swallowed everything that moved while no
// watcher was running, and has a correct-looking first poll.
func TestAnUnparsableStateFileIsAnErrorAndNotAColdStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("this is not a state file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("an unparsable state file loaded quietly; it must refuse and name the parse error")
	}
}

// A missing state file is a cold start, and says so.
func TestAMissingStateFileIsCold(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nothing-here"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Cold() {
		t.Error("a state file that does not exist is a cold start")
	}
}

// A write that cannot land leaves the OLD file intact: the temp file and the
// rename are there so a call killed mid-poll leaves one whole state or the
// other and never half of either.
func TestAKilledWriteLeavesTheOldFileIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	s := newState()
	s.Observe("report:a", "first", "a1")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Observe("report:a", "second", "a2")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make the directory unwritable here: %v", err)
	}
	defer os.Chmod(dir, 0o700)
	if err := s.Save(path); err == nil {
		t.Skip("the directory is still writable on this filesystem")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("a failed write changed the state file; the rename is there so it cannot")
	}
}

// The failure streak spans calls: it is written on every failed poll, read at
// the next call's start, and cleared by the first success.
func TestTheFailureStreakSpansCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	at := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	s := newState()
	n, since, _ := s.Fail("bus", "nova-bus exit=1", at)
	if n != 1 || since != Stamp(at) {
		t.Fatalf("first failure is %d since %q", n, since)
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	n, since, _ = back.Fail("bus", "a different reason", at.Add(time.Minute))
	if n != 2 {
		t.Errorf("the streak did not span the reload: n = %d, want 2", n)
	}
	if since != Stamp(at) {
		t.Errorf("since = %q, want the first failure's stamp %q", since, Stamp(at))
	}
	back.ClearFail("bus")
	if n, _, _ := back.Fail("bus", "again", at.Add(2*time.Minute)); n != 1 {
		t.Errorf("a success did not clear the streak: n = %d, want 1", n)
	}
}
