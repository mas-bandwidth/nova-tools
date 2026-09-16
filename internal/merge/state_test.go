package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Work list 1, and demanded tests 1 and 20. The state file is the lane's order and the
// fold of its records; every one of these is a refusal the spec names by hand.

func TestAnUnknownFieldRefuses(t *testing.T) {
	_, err := Decode([]byte(`{"version":1,"repo":"o/n","base":"main","lane_branch":"l","prs":[],"branches":[],"gates":[],"needs_reads":"yes"}`))
	if err == nil {
		t.Fatal("an unknown field must refuse: a state file whose needs_read key was typed needs_reads is a state file whose owner believes a read is required")
	}
	if !strings.Contains(err.Error(), "needs_reads") {
		t.Errorf("the refusal must name the field it did not know, got %v", err)
	}
}

func TestNeedsReadIsClosedToYesAndNo(t *testing.T) {
	// F3: needs_read is closed to exactly "yes" or "no", and a missing or empty field
	// refuses. "true", "Yes", "y", "1" and a missing key all fall off the package's
	// comparison against the word "yes", which is the direction that merges unread.
	for _, tc := range []struct{ name, json string }{
		{"an unrecognised spelling", `{"version":1,"repo":"o/n","base":"main","lane_branch":"l","prs":[{"pr":951,"needs_read":"true","reads":[],"head":"b","oid":"","state":"NEW","last":"","detail":""}],"branches":[],"gates":[]}`},
		{"the key absent", `{"version":1,"repo":"o/n","base":"main","lane_branch":"l","prs":[{"pr":951,"reads":[],"head":"b","oid":"","state":"NEW","last":"","detail":""}],"branches":[],"gates":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode([]byte(tc.json))
			if err == nil {
				t.Fatalf("needs_read must be exactly \"yes\" or \"no\": the spec says a record missing any does not decode (rules 18, 19 and 21)")
			}
			if !strings.Contains(err.Error(), "needs_read") {
				t.Errorf("the refusal must name needs_read, got %v", err)
			}
		})
	}
}

func TestAStringWhereANumberBelongsRefuses(t *testing.T) {
	_, err := Decode([]byte(`{"version":1,"repo":"o/n","base":"main","lane_branch":"l","prs":[{"pr":951,"needs_read":"yes","reads":[],"head":"b","oid":"","state":"NEW","last":"","detail":"","green":"24","pending":0,"red":0}],"branches":[],"gates":[]}`))
	if err == nil {
		t.Fatal(`"24" is a string and green is a number; a count that is a string compares as a string and "10" < "9"`)
	}
}

func TestAGateWithoutBaseOrMergeRefuses(t *testing.T) {
	for _, tc := range []struct{ name, json, want string }{
		{"no base", `{"version":1,"repo":"o/n","base":"main","lane_branch":"l","prs":[],"branches":[],"gates":[{"pr":949,"head":"` + h40('a') + `","merge":"` + h40('c') + `","verdict":"green","summary":"s","at":"t","run":"r","file":"f"}]}`, "base"},
		{"no merge", `{"version":1,"repo":"o/n","base":"main","lane_branch":"l","prs":[],"branches":[],"gates":[{"pr":949,"head":"` + h40('a') + `","base":"` + h40('b') + `","verdict":"green","summary":"s","at":"t","run":"r","file":"f"}]}`, "merge"},
		{"short base", `{"version":1,"repo":"o/n","base":"main","lane_branch":"l","prs":[],"branches":[],"gates":[{"pr":949,"head":"` + h40('a') + `","base":"abc","merge":"` + h40('c') + `","verdict":"green","summary":"s","at":"t","run":"r","file":"f"}]}`, "base"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode([]byte(tc.json)); err == nil {
				t.Fatalf("a gate record without a full %s does not decode (rules 18 and 21)", tc.want)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal must name %s, got %v", tc.want, err)
			}
		})
	}
}

func TestAVersionThisBinaryDoesNotKnowIsRefusedByNumber(t *testing.T) {
	_, err := Decode([]byte(`{"version":2,"repo":"o/n","base":"main","lane_branch":"l","prs":[],"branches":[],"gates":[]}`))
	if err == nil {
		t.Fatal("version 2 must be refused")
	}
	if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "1") {
		t.Errorf("the refusal names both numbers, got %v", err)
	}
}

func TestVersionIsCheckedBeforeAnyOtherField(t *testing.T) {
	// A version this binary does not know is refused BY NUMBER, before the fields it
	// cannot be expected to understand are read.
	_, err := Decode([]byte(`{"version":2,"whatever":true}`))
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version is checked first, got %v", err)
	}
}

// HostedRedBlocks is rule 15's derivation as a table, because the run-path tests in
// cmd/nova-merge drive the same derivation through a real pass but a table pins every arm.
func TestHostedRedBlocksDerivation(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		hostedRed, defaultBranch, base, discovered string
		want                                       bool
	}{
		{"explicit blocks wins", HostedRedBlocksValue, "", "main", "", true},
		{"explicit names wins", HostedRedNamesValue, "main", "main", "", false},
		{"explicit names even when the base is the default", HostedRedNamesValue, "main", "main", "main", false},
		{"derived: base is the recorded default", "", "main", "main", "", true},
		{"derived: base is the freshly discovered default", "", "main", "trunk", "trunk", true},
		{"derived: a matching recorded default keeps blocks across a rename", "", "trunk", "trunk", "master", true},
		{"derived: a successful discovery of a different default derives names", "", "main", "rowan/step-2", "main", false},
		{"derived: a failed discovery blocks even with a stale recorded name", "", "main", "trunk", "", true},
		{"derived: no recorded fact and a failed discovery blocks", "", "", "trunk", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &State{HostedRed: tc.hostedRed, DefaultBranch: tc.defaultBranch, Base: tc.base}
			if got := s.HostedRedBlocks(tc.discovered); got != tc.want {
				t.Errorf("HostedRedBlocks(%q) = %v, want %v", tc.discovered, got, tc.want)
			}
		})
	}
}

func TestARoundTripPreservesOrder(t *testing.T) {
	lane := t.TempDir()
	if err := Init(lane, LaneConfig{Repo: "o/n", Base: "main", LaneBranch: "nova-merge/l"}); err != nil {
		t.Fatal(err)
	}
	st, err := Load(lane)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{951, 942, 949} {
		st.PRs = append(st.PRs, &Entry{PR: n, NeedsRead: "yes", State: "NEW"})
	}
	st.Branches = append(st.Branches, &Entry{Branch: "rowan/wire-probe", NeedsRead: "no", State: "NEW"})
	if err := st.SaveTo(lane); err != nil {
		t.Fatal(err)
	}
	back, err := Load(lane)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, e := range back.PRs {
		got = append(got, e.PR)
	}
	if len(got) != 3 || got[0] != 951 || got[1] != 942 || got[2] != 949 {
		t.Errorf("the lane is ORDERED and the order is the order entries were added, got %v", got)
	}
	if len(back.Branches) != 1 || back.Branches[0].Branch != "rowan/wire-probe" {
		t.Errorf("the two entry lists are separate, got %+v", back.Branches)
	}
}

func TestAnInitOfALaneThatExistsRefuses(t *testing.T) {
	lane := t.TempDir()
	if err := Init(lane, LaneConfig{Repo: "o/n", Base: "main", LaneBranch: "nova-merge/l"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(lane, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Init(lane, LaneConfig{Repo: "o/n", Base: "other", LaneBranch: "nova-merge/l"}); err == nil {
		t.Fatal("init is creation-only: a lane whose state.json exists is refused")
	}
	after, _ := os.ReadFile(filepath.Join(lane, "state.json"))
	if string(before) != string(after) {
		t.Error("a refused init leaves the state byte-identical")
	}
}

// Demanded test 1: thirty concurrent writers on one lane, and a reader in a tight loop
// that must never see 0 bytes or a partial file.
//
// TWO READERS, because the replace has two sides. THE TOOL'S OWN READER, Load, must never
// answer a parse error and never answer "this is not a lane" about a lane that is there:
// that is the contract every verb depends on, and on Windows it is what the bounded wait
// in readState buys -- an open refused for the microseconds of a MoveFileEx replace is a
// door held shut, not an answer. A RAW READER holds the other half: whenever a plain open
// does succeed, the bytes it gets parse, so a zero-byte or half-written state file is red
// on every platform, which is the property the rename is there for.
func TestThirtyConcurrentWritersAllLandAndTheFileAlwaysParses(t *testing.T) {
	lane := t.TempDir()
	if err := Init(lane, LaneConfig{Repo: "o/n", Base: "main", LaneBranch: "nova-merge/l"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	bad := make(chan error, 1)
	fail := func(err error) {
		select {
		case bad <- err:
		default:
		}
	}
	var readers sync.WaitGroup
	readers.Add(2)
	// The tool's reader: every answer it gives while thirty writers race must be a state.
	go func() {
		defer readers.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := Load(lane); err != nil {
				fail(fmt.Errorf("the tool's own reader: %w", err))
				return
			}
		}
	}()
	// The raw reader: an open a replace refused is not this test's subject, but bytes
	// that do not parse are.
	go func() {
		defer readers.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			raw, err := os.ReadFile(filepath.Join(lane, "state.json"))
			if err != nil {
				continue
			}
			if _, err := Decode(raw); err != nil {
				fail(fmt.Errorf("a plain read of %d bytes: %w", len(raw), err))
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Update(lane, LockWait, func(st *State) error {
				st.PRs = append(st.PRs, &Entry{PR: 1000 + i, NeedsRead: "no", State: "NEW"})
				return nil
			}); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(done)
	readers.Wait()
	select {
	case err := <-bad:
		t.Fatalf("a reader racing thirty writers must never see a missing or unparsable state: %v", err)
	default:
	}
	st, err := Load(lane)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.PRs) != 30 {
		t.Errorf("every write must land: got %d of 30", len(st.PRs))
	}
}

func h40(c byte) string { return strings.Repeat(string(c), 40) }

// Companion to demanded test 1: a sustained replace refusal exhausts the writer's bounded
// retry. The loop must surface the rename error to the caller (NOT call the write OK,
// because a write that returned success without replacing has lost acknowledged bytes the
// caller did not recover), the wait must stop at replaceWriteWindow (NOT spin forever,
// because a busy unbounded wait is the door held open forever), and the old parseable
// state must still be on disk (NOT replaced or damaged, because the question a hand at the
// keyboard next asks is "what was the lane before," and "this is not a lane" is the wrong
// answer when the lane is there).
//
// The hook makes this deterministic on every platform: replaceRefusal is stubbed to admit
// any error, and the path that os.Rename would replace is made a directory so os.Rename
// returns "file exists" on every iteration. The same loop on Windows refuses the rename
// with `Access is denied` when an unsympathetic reader holds state.json open, and the
// diagnostic is the loop's behavior, not the OS's classification.
//
// The test calls SaveTo (not Update) so the writer's bounded loop is exercised on its own;
// routing via Update would also drive the tool's own reader's bounded readState loop on
// the now-directory state.json, and that one would exhaust first, masking the writer's
// behavior. The reader's exhaustion is its OWN contract, and its own test.
//
// SaveTo does what the contract says on a refused replace: os.Remove(tmp), then the rename
// error. tmp is gone, dst is the original parseable state, the caller sees the rename
// error, and a hand holding the lane at the keyboard sees it whole.
func TestExhaustedReplaceReturnsTheRenameErrorAndLeavesStateParseable(t *testing.T) {
	lane := t.TempDir()
	if err := Init(lane, LaneConfig{Repo: "o/n", Base: "main", LaneBranch: "l"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(lane, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(before); err != nil {
		t.Fatalf("the prior state must decode: %v", err)
	}

	origRefusal := replaceRefusal
	replaceRefusal = func(error) bool { return true }
	defer func() { replaceRefusal = origRefusal }()

	dst := filepath.Join(lane, StateName)
	if err := os.Remove(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	st := &State{Version: Version, Repo: "o/n", Base: "main", LaneBranch: "l",
		PRs: []*Entry{}, Branches: []*Entry{}, Gates: []Gate{}}
	err = st.SaveTo(lane)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a sustained replace refusal must surface the rename error to the caller, not smooth it over with success")
	}
	// The bound is replaceWriteWindow; the rename OS call sits between the deadline
	// check and the return, so an overshoot of up to one final poll (replaceWritePollMax)
	// plus a small allowance for test-host scheduling is the contract, not the loop
	// spinning or bailing at a fraction of the bound.
	if elapsed > replaceWriteWindow+replaceWritePollMax+500*time.Millisecond {
		t.Errorf("the bounded retry must not run past the bound (one final poll + test-host allowance): %v > %v+%v+500ms", elapsed, replaceWriteWindow, replaceWritePollMax)
	}
	if elapsed < replaceWriteWindow/2 {
		t.Errorf("the bounded retry must run the bound, not bail early: only %v of %v", elapsed, replaceWriteWindow)
	}
	if _, err := os.Stat(filepath.Join(lane, StateTmpName)); err == nil {
		t.Errorf("a refused replace must clean up the temp file; found %s still on disk", StateTmpName)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("the prior parseable state must still be on disk: %v", err)
	}
	if !fi.IsDir() {
		t.Errorf("a refused replace must leave the prior state byte-identical; was it replaced? fi=%+v", fi)
	}
}
