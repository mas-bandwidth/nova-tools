package pulse

// The pitstop verb's tests, against a miniredis so no test reaches the fleet. The two
// shapes the DONE-WHEN cares about are here:
//
//	set pause --except harvest -> check launch exits 3, check harvest exits 0
//	redis unreachable           -> check exits non-zero, never 0
//
// plus the resume shape and the refusal shapes: unknown subcommand, unknown state, no
// store, an unknown verb the bench passes anyway. Each test owns its own miniredis.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// runPitstop runs one invocation and hands back its exit code and the two streams.
func runPitstop(t *testing.T, in PitstopInput) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	if in.Timeout <= 0 {
		in.Timeout = 2 * time.Second
	}
	code := Pitstop(in)
	return code, out.String(), errb.String()
}

// pitstopDial returns a Dial seam and a StoreOptions that point at the given miniredis.
// A test that wants a refused check passes dialAlwaysFails and a StoreOptions with Addr.
func pitstopDial(mr *miniredis.Miniredis) (func(ctx context.Context, o StoreOptions) (*redis.Client, error), StoreOptions) {
	dial := func(ctx context.Context, o StoreOptions) (*redis.Client, error) {
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		if err := rdb.Ping(ctx).Err(); err != nil {
			return nil, err
		}
		return rdb, nil
	}
	return dial, StoreOptions{Addr: mr.Addr()}
}

// dialAlwaysFails returns a Dial seam that always refuses. The DONE-WHEN's "redis
// unreachable" arm uses this.
func dialAlwaysFails(reason string) func(ctx context.Context, o StoreOptions) (*redis.Client, error) {
	return func(ctx context.Context, o StoreOptions) (*redis.Client, error) {
		return nil, errors.New(reason)
	}
}

// DONE-WHEN set pause --except harvest: check launch exits 3 and check harvest exits 0.
func TestPitstopSetPauseExceptThenCheck(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dial, store := pitstopDial(mr)
	in := PitstopInput{Sub: "set", State: "pause", Except: []string{"harvest"}, Dial: dial, Store: store}

	code, out, errb := runPitstop(t, in)
	if code != 0 {
		t.Fatalf("set pause --except harvest exit = %d, stderr=%s", code, errb)
	}
	if !strings.Contains(out, "PITSTOP SET state=pause") {
		t.Errorf("SET line = %q, want PITSTOP SET state=pause", out)
	}
	// Every verb except harvest should now be in the set. The harvest verb should be
	// absent. This is the DONE-WHEN's contract, asserted directly on the store.
	for _, v := range PitstopVerbs {
		got, err := mr.SIsMember("pitstop", v)
		if err != nil {
			t.Fatalf("SISMEMBER %s: %s", v, err)
		}
		want := v != "harvest"
		if got != want {
			t.Errorf("SISMEMBER pitstop %s = %v, want %v (set pause --except harvest)", v, got, want)
		}
	}

	if code, _, _ := runPitstop(t, PitstopInput{Sub: "check", Verb: "launch", Dial: dial, Store: store}); code != 3 {
		t.Errorf("check launch exit = %d, want 3 (paused)", code)
	}
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "check", Verb: "harvest", Dial: dial, Store: store}); code != 0 {
		t.Errorf("check harvest exit = %d, want 0 (open)", code)
	}
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "check", Verb: "fill", Dial: dial, Store: store}); code != 3 {
		t.Errorf("check fill exit = %d, want 3 (paused)", code)
	}
}

// DONE-WHEN redis unreachable: check exits non-zero, never 0.
func TestPitstopCheckRefusedWhenStoreIsUnreachable(t *testing.T) {
	t.Parallel()
	dial := dialAlwaysFails("connection refused")
	in := PitstopInput{Sub: "check", Verb: "launch", Dial: dial, Store: StoreOptions{Addr: "127.0.0.1:1"}}
	code, out, errb := runPitstop(t, in)
	if code == 0 {
		t.Fatalf("check with an unreachable store exit = 0, the DONE-WHEN says never 0: out=%s err=%s", out, errb)
	}
	if code != 3 {
		// The DONE-WHEN's "non-zero" includes 3; the verb uses 3 so a launcher reading the
		// exit code reads both "paused" and "store down" as the same brake.
		t.Errorf("check with an unreachable store exit = %d, want 3 (the same code a paused check uses)", code)
	}
	if !strings.Contains(errb, "PITSTOP CHECK REFUSED") {
		t.Errorf("refusal line missing: %q", errb)
	}
}

// set resume empties the set; every check is open after.
func TestPitstopSetResumeEmptiesTheSet(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dial, store := pitstopDial(mr)
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "set", State: "pause", Dial: dial, Store: store}); code != 0 {
		t.Fatalf("first set pause exit = %d", code)
	}
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "set", State: "resume", Dial: dial, Store: store}); code != 0 {
		t.Fatalf("set resume exit = %d", code)
	}
	if code, out, _ := runPitstop(t, PitstopInput{Sub: "check", Verb: "launch", Dial: dial, Store: store}); code != 0 {
		t.Errorf("check launch after resume exit = %d, want 0: %s", code, out)
	}
	if exists := mr.Exists("pitstop"); exists {
		t.Errorf("after resume the key still exists; a DEL is the whole reset")
	}
}

// unknown subcommand is a refusal; the verb refuses before it touches the store.
func TestPitstopRejectsUnknownSubcommand(t *testing.T) {
	t.Parallel()
	in := PitstopInput{Sub: "stop", Dial: dialAlwaysFails("should not be called")}
	code, _, errb := runPitstop(t, in)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb, "PITSTOP REFUSED") {
		t.Errorf("refusal line = %q", errb)
	}
}

// unknown state under set is a refusal that names the two states.
func TestPitstopSetRejectsUnknownState(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dial, store := pitstopDial(mr)
	in := PitstopInput{Sub: "set", State: "wait", Dial: dial, Store: store}
	code, _, errb := runPitstop(t, in)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb, "pause or resume") {
		t.Errorf("refusal = %q, want it to name the two states", errb)
	}
}

// no --store is a refusal before the dial; the verb refuses rather than guess.
func TestPitstopRefusesWithoutStore(t *testing.T) {
	t.Parallel()
	in := PitstopInput{Sub: "check", Verb: "launch"}
	code, _, errb := runPitstop(t, in)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb, "--store is required") {
		t.Errorf("refusal = %q, want it to name --store", errb)
	}
}

// check with no verb is a refusal that names what is missing.
func TestPitstopCheckRequiresAVerb(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dial, store := pitstopDial(mr)
	in := PitstopInput{Sub: "check", Dial: dial, Store: store}
	code, _, errb := runPitstop(t, in)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb, "verb") {
		t.Errorf("refusal = %q, want it to name the verb", errb)
	}
}

// a check the fleet does not own still answers "open"; a misspelling on the bench reads as
// the bench proceeding. That is the safer failure than a refusal the launcher did not ask
// for.
func TestPitstopCheckOnAnUnknownVerbIsOpen(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dial, store := pitstopDial(mr)
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "set", State: "pause", Dial: dial, Store: store}); code != 0 {
		t.Fatal("set pause failed")
	}
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "check", Verb: "sidecar", Dial: dial, Store: store}); code != 0 {
		t.Errorf("check sidecar exit = %d, want 0 (an unknown verb reads as open)", code)
	}
}

// a --except that names a verb outside the closed set is silently skipped; the closed set
// is the whole state, so a typo on the bench pauses nothing extra and nothing less.
func TestPitstopSetPauseSkipsUnknownExceptNames(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dial, store := pitstopDial(mr)
	in := PitstopInput{Sub: "set", State: "pause", Except: []string{"harvest", "sidecar"}, Dial: dial, Store: store}
	if code, _, _ := runPitstop(t, in); code != 0 {
		t.Fatal("set pause failed")
	}
	for _, v := range PitstopVerbs {
		got, err := mr.SIsMember("pitstop", v)
		if err != nil {
			t.Fatalf("SISMEMBER %s: %s", v, err)
		}
		want := v != "harvest"
		if got != want {
			t.Errorf("SISMEMBER pitstop %s = %v, want %v", v, got, want)
		}
	}
}

// --key overrides the default; a fleet that runs more than one stop at once reads and
// writes the named key, never the default.
func TestPitstopCheckHonoursKeyOverride(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dial, store := pitstopDial(mr)
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "set", State: "pause", Except: []string{"harvest"}, Key: "sprint:stop", Dial: dial, Store: store}); code != 0 {
		t.Fatal("set pause failed")
	}
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "check", Verb: "launch", Key: "sprint:stop", Dial: dial, Store: store}); code != 3 {
		t.Errorf("check launch on the override key exit = %d, want 3", code)
	}
	// The default key is unaffected; an absent set is open.
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "check", Verb: "launch", Dial: dial, Store: store}); code != 0 {
		t.Errorf("check launch on the default key exit = %d, want 0 (the default key was not written)", code)
	}
}

// the verb prints one PITSTOP CHECK line per call: state=open or state=paused. A bench that
// reads the line and the exit code in parallel reads the same answer.
func TestPitstopCheckPrintsOneLine(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dial, store := pitstopDial(mr)
	if code, _, _ := runPitstop(t, PitstopInput{Sub: "set", State: "pause", Except: []string{"harvest"}, Dial: dial, Store: store}); code != 0 {
		t.Fatal("set pause failed")
	}
	code, out, _ := runPitstop(t, PitstopInput{Sub: "check", Verb: "launch", Dial: dial, Store: store})
	if code != 3 {
		t.Fatalf("check launch exit = %d, want 3", code)
	}
	if n := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); n != 1 {
		t.Errorf("check printed %d lines, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "PITSTOP CHECK verb=launch state=paused key=pitstop") {
		t.Errorf("CHECK line = %q, want verb=launch state=paused", out)
	}
	code, out, _ = runPitstop(t, PitstopInput{Sub: "check", Verb: "harvest", Dial: dial, Store: store})
	if code != 0 {
		t.Fatalf("check harvest exit = %d, want 0", code)
	}
	if !strings.Contains(out, "PITSTOP CHECK verb=harvest state=open key=pitstop") {
		t.Errorf("CHECK line = %q, want verb=harvest state=open", out)
	}
}
