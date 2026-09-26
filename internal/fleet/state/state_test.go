package state_test

// The fleet-state engine's check: UP, DOWN and HELD by a heartbeat key with a TTL.
//
// The #2161 case is the reason this package decides by a key and not by a probe:
// a bench at load 21 on 16 cores -- load over cores, the shape that used to read
// DOWN because everything asked of it timed out -- that still writes its
// heartbeat key is UP. The key is the bench's own word that it is there
// (SPEC-STATE ## Presence: keys with TTL); load is a fact on the row beside it
// and never a say in the state.
//
// DONE-WHEN, pinned here as three checks:
//
//  1. TestLoadedBenchWithHeartbeatIsUp -- the #2161 case replayed.
//  2. TestAnExpiredHeartbeatKeyIsDownWithinTTLPlusFiveSeconds -- a bench whose
//     key expires is DOWN within TTL+5 s.
//  3. TestNoReaderOfFleetStateTSV -- a CI grep finds no reader of
//     queue/control/fleet-state.tsv. The file bin/fleet-state writes is a page,
//     never authority: anything that needs the truth asks the keys.
//
// No clock runs here. Every wall is a time.Time handed to the decision, so the
// TTL boundary is tested at the boundary and not against a bench's load.

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet/state"
)

// TestLoadedBenchWithHeartbeatIsUp is the #2161 case replayed: a bench at load
// 21 on 16 cores that still writes its heartbeat key is UP.
func TestLoadedBenchWithHeartbeatIsUp(t *testing.T) {
	t.Parallel()

	const ttl = 30 * time.Second
	start := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	b := state.Bench{
		Name:  "space",
		Load:  21,
		Cores: 16,
		Key:   &state.Key{Written: start, TTL: ttl},
	}

	// One second inside the first TTL the key counts: UP, load notwithstanding.
	if got := b.State(start.Add(ttl - time.Second)); got != state.Up {
		t.Fatalf("a bench at load 21 on 16 cores whose key is %v old is %s, want UP", ttl-time.Second, got)
	}

	// The bench keeps writing: a second beat a TTL after the first, while the
	// load stays at 21 on 16 cores.
	b.Key = &state.Key{Written: start.Add(ttl), TTL: ttl}

	// Half a TTL after that second beat the first write is a TTL and a half
	// stale -- an unrefreshed key would have expired long ago -- and the bench
	// is UP on the write it still makes.
	at := start.Add(ttl + ttl/2)
	if got := b.State(at); got != state.Up {
		t.Fatalf("a bench at load 21 on 16 cores that still writes its heartbeat key is %s at %s, want UP (the #2161 case)", got, at.Format(time.RFC3339))
	}

	// The contrast that makes it a replay: the same loaded bench, its key left
	// at the first write with no beat since, is DOWN at the same wall. What
	// makes it UP is the key being written, never the load falling.
	b.Key = &state.Key{Written: start, TTL: ttl}
	if got := b.State(at); got != state.Down {
		t.Fatalf("the same bench with no beat since %s is %s at %s, want DOWN", start.Format(time.RFC3339), got, at.Format(time.RFC3339))
	}

	// Load is a fact on the row and never a say in the state: the identical key
	// reads the same at load 0 and at load 21 on 16 cores.
	for _, load := range []float64{0, 21} {
		for _, cores := range []int{1, 16} {
			lb := state.Bench{Name: "space", Load: load, Cores: cores,
				Key: &state.Key{Written: start.Add(ttl), TTL: ttl}}
			if got := lb.State(at); got != state.Up {
				t.Fatalf("load %v on %d cores turned a fresh key into %s, want UP", load, cores, got)
			}
		}
	}
}

// TestAnExpiredHeartbeatKeyIsDownWithinTTLPlusFiveSeconds is the other half of
// the contract: a bench whose key expires is DOWN within TTL+5 s. The window is
// the key's own TTL, not less and not more -- the +5 s is the clause's whole
// allowance for anyone observing the expiry late, so the decision is DOWN at
// the TTL and certainly by the TTL plus five seconds.
func TestAnExpiredHeartbeatKeyIsDownWithinTTLPlusFiveSeconds(t *testing.T) {
	t.Parallel()

	const ttl = 30 * time.Second
	written := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	b := state.Bench{Name: "hulk", Key: &state.Key{Written: written, TTL: ttl}}

	// The key counts right up to its TTL...
	if got := b.State(written.Add(ttl - time.Second)); got != state.Up {
		t.Fatalf("a key %v old (TTL %v) reads %s, want UP", ttl-time.Second, ttl, got)
	}
	// ...and a bench whose key has expired is DOWN inside the clause's
	// TTL+5 s allowance.
	if got := b.State(written.Add(ttl + 5*time.Second)); got != state.Down {
		t.Fatalf("a bench whose key expired at %s is %s at the TTL+5s, want DOWN",
			written.Add(ttl).Format(time.RFC3339), got)
	}

	// A key that was never written is as down as one that expired: a bench that
	// has said nothing is not up.
	never := state.Bench{Name: "vision"}
	if got := never.State(written); got != state.Down {
		t.Fatalf("a bench that never wrote its heartbeat key is %s, want DOWN", got)
	}
}

// TestHeldRidesTheKeyAndExpiresWithIt pins the third state. HELD is not a
// tombstone and not a liveness of its own: it is the hold the key carries, so a
// held bench whose key still counts is HELD (there, and taking no work), and a
// held bench whose key expires is DOWN like any other.
func TestHeldRidesTheKeyAndExpiresWithIt(t *testing.T) {
	t.Parallel()

	const ttl = 30 * time.Second
	written := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	b := state.Bench{Name: "hulk", Key: &state.Key{Written: written, TTL: ttl, Held: true}}

	if got := b.State(written.Add(time.Second)); got != state.Held {
		t.Fatalf("a fresh key carrying a hold reads %s, want HELD", got)
	}
	if got := b.State(written.Add(ttl + 5*time.Second)); got != state.Down {
		t.Fatalf("a hold on an expired key reads %s, want DOWN: a hold rides the key and expires with it", got)
	}
}

// TestLiveAndRowAreTheBinsEngines pins the production path this decision sits
// on: bin/fleet-live lists presence (the benches whose key still counts -- a
// HELD bench is here, a DOWN bench is not) and bin/fleet-state writes one
// tab-separated row per bench. Both reach Bench.State; neither reads a file.
func TestLiveAndRowAreTheBinsEngines(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	const ttl = 30 * time.Second
	benches := []state.Bench{
		{Name: "hulk", Load: 21, Cores: 16, Key: &state.Key{Written: now.Add(-time.Second), TTL: ttl}},
		{Name: "vision", Key: &state.Key{Written: now.Add(-time.Second), TTL: ttl, Held: true}},
		{Name: "threadripper-wsl", Key: &state.Key{Written: now.Add(-2 * ttl), TTL: ttl}},
	}

	live := strings.Join(state.Live(benches, now), ",")
	if want := "hulk,vision"; live != want {
		t.Fatalf("Live = %q, want %q (presence: a held bench is here, an expired one is not)", live, want)
	}

	var rows []string
	for _, b := range benches {
		rows = append(rows, state.Row(b, now))
	}
	want := strings.Join([]string{"hulk\tUP", "vision\tHELD", "threadripper-wsl\tDOWN"}, "\n")
	if got := strings.Join(rows, "\n"); got != want {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

// TestNoReaderOfFleetStateTSV is the CI grep DONE-WHEN names. The artifact
// bin/fleet-state writes -- queue/control/fleet-state.tsv -- is a page and never
// authority: a stale projection read back is how a fleet page becomes a lie, so
// NOTHING in the tree may read it. The check is the grep itself, run over the
// repository's code: every mention of the file's name must be a comment (a
// human's note about the page), and no code line may name it at all -- which is
// a stronger shape than "no reader", and greps as one.
//
// This checker is the one file allowed to hold the name in code, so it skips
// itself; the needle is split so this source line is not itself a mention.
func TestNoReaderOfFleetStateTSV(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this checker to skip it")
	}
	needle := "fleet-state" + ".tsv"

	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if abs == self {
			return nil
		}
		if !scansForReaders(d.Name()) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		for i, line := range strings.Split(string(raw), "\n") {
			at := strings.Index(line, needle)
			if at < 0 {
				continue
			}
			if isCommentMention(line, at) {
				continue
			}
			violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("a CI grep found code naming fleet-state.tsv -- the page bin/fleet-state writes is never read:\n%s",
			strings.Join(violations, "\n"))
	}
}

// scansForReaders says whether a file is code this rule reads: the languages
// that run on the CI path and beside the tools. A spec or a note may name the
// page freely; a reader is code.
func scansForReaders(name string) bool {
	switch {
	case strings.HasSuffix(name, ".go"),
		strings.HasSuffix(name, ".sh"),
		strings.HasSuffix(name, ".yml"),
		strings.HasSuffix(name, ".yaml"),
		strings.HasSuffix(name, ".lisp"),
		name == "Makefile":
		return true
	}
	return false
}

// isCommentMention says whether the needle occurrence at at on line is inside a
// comment: a whole-line comment, or a trailing comment that begins before it.
func isCommentMention(line string, at int) bool {
	trimmed := strings.TrimSpace(line)
	for _, marker := range []string{"//", "#", "*", "/*", "<!--", ";"} {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	if i := strings.Index(line, "//"); i >= 0 && i < at {
		return true
	}
	if i := strings.Index(line, "#"); i >= 0 && i < at {
		return true
	}
	return false
}

// repoRoot walks up from this package's directory to the module root: the one
// tree this rule reads, found rather than guessed (SPEC.md: no guessed paths).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("no go.mod above this package; refusing to guess the tree to grep")
	return ""
}
