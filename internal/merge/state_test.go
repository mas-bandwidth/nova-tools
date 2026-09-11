package merge

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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

func TestARoundTripPreservesOrder(t *testing.T) {
	lane := t.TempDir()
	if err := Init(lane, "o/n", "main", "nova-merge/l"); err != nil {
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
	if err := Init(lane, "o/n", "main", "nova-merge/l"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(lane, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Init(lane, "o/n", "other", "nova-merge/l"); err == nil {
		t.Fatal("init is creation-only: a lane whose state.json exists is refused")
	}
	after, _ := os.ReadFile(filepath.Join(lane, "state.json"))
	if string(before) != string(after) {
		t.Error("a refused init leaves the state byte-identical")
	}
}

// Demanded test 1: thirty concurrent writers on one lane, and a reader in a tight loop
// that must never see 0 bytes or a partial file.
func TestThirtyConcurrentWritersAllLandAndTheFileAlwaysParses(t *testing.T) {
	lane := t.TempDir()
	if err := Init(lane, "o/n", "main", "nova-merge/l"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	bad := make(chan error, 1)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			raw, err := os.ReadFile(filepath.Join(lane, "state.json"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				select {
				case bad <- err:
				default:
				}
				return
			}
			if _, err := Decode(raw); err != nil {
				select {
				case bad <- err:
				default:
				}
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
	select {
	case err := <-bad:
		t.Fatalf("a reader polling the file saw one that does not parse: %v", err)
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
