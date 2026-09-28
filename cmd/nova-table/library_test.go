package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/redisfn"
)

// reply is an error the store itself replied with.
type reply string

func (e reply) Error() string { return string(e) }

func (reply) RedisError() {}

const notFound = reply("ERR Function not found")

// fakeStore stands behind the hook: next answers each command from the
// library it holds, and load is LoadMissing over it. Nothing is dialled.
type fakeStore struct {
	holds    bool  // the store holds a library that registers every function
	loadErr  error // the load fails with this
	outcome  redisfn.Outcome
	sends    int
	loads    int
	commands []string
}

func (s *fakeStore) answer(cmd redis.Cmder) error {
	s.sends++
	s.commands = append(s.commands, cmd.Name())
	if (cmd.Name() == "fcall" || cmd.Name() == "fcall_ro") && !s.holds {
		cmd.SetErr(notFound)
		return notFound
	}
	cmd.SetErr(nil)
	return nil
}

func (s *fakeStore) next(_ context.Context, cmd redis.Cmder) error { return s.answer(cmd) }

func (s *fakeStore) pipeline(_ context.Context, cmds []redis.Cmder) error {
	var first error
	for _, cmd := range cmds {
		if err := s.answer(cmd); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *fakeStore) load(context.Context) (redisfn.Receipt, error) {
	s.loads++
	if s.loadErr != nil {
		return redisfn.Receipt{}, s.loadErr
	}
	r := redisfn.Receipt{Library: "nova_sprint", Outcome: s.outcome}
	if r.Outcome == 0 {
		r.Outcome = redisfn.Loaded
	}
	if r.Outcome == redisfn.Loaded {
		s.holds = true
	}
	if r.Outcome == redisfn.Skipped {
		r.Why = "NOPERM this user has no permissions to run the 'function|load' command"
	}
	return r, nil
}

func fcall(name string) redis.Cmder {
	return redis.NewCmd(context.Background(), "fcall", name, 0)
}

// TestFirstContactLoadsOnlyWhenMissingAndOnce: a store that holds the
// library is sent each verb once and no load; a store that holds none
// answers the first FCALL "Function not found", the library is loaded once
// and the FCALL sent again once; later misses in the same process load
// nothing and are refused with the deployer's remedy.
func TestFirstContactLoadsOnlyWhenMissingAndOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	present := &fakeStore{holds: true}
	hook := libraryHook{state: &firstContact{}, load: present.load}
	for i := 0; i < 3; i++ {
		if err := hook.ProcessHook(present.next)(ctx, fcall("ns_table_create")); err != nil {
			t.Fatal(err)
		}
	}
	if present.loads != 0 || present.sends != 3 {
		t.Fatalf("a store holding the library: %d loads, %d sends; want 0 loads and one send a verb", present.loads, present.sends)
	}

	// Not an FCALL, or an FCALL refused for another reason: no load.
	other := &fakeStore{}
	hook = libraryHook{state: &firstContact{}, load: other.load}
	if err := hook.ProcessHook(other.next)(ctx, redis.NewCmd(ctx, "hget", "k", "f")); err != nil || other.loads != 0 {
		t.Fatalf("hget: err %v, %d loads; want none", err, other.loads)
	}
	refusal := reply("ERR stale epoch")
	stale := func(_ context.Context, cmd redis.Cmder) error { cmd.SetErr(refusal); return refusal }
	if err := hook.ProcessHook(stale)(ctx, fcall("ns_table_set")); !errors.Is(err, refusal) || other.loads != 0 {
		t.Fatalf("an FCALL the store refused for its own reason: err %v, %d loads; want the refusal as it is and no load", err, other.loads)
	}

	fresh := &fakeStore{}
	state := &firstContact{}
	hook = libraryHook{state: state, load: fresh.load}
	if err := hook.ProcessHook(fresh.next)(ctx, fcall("ns_table_create")); err != nil {
		t.Fatalf("first contact with a store that holds no library: %v", err)
	}
	if fresh.loads != 1 || fresh.sends != 2 {
		t.Fatalf("first contact: %d loads, %d sends; want one load and the FCALL sent twice", fresh.loads, fresh.sends)
	}
	for i := 0; i < 3; i++ {
		if err := hook.ProcessHook(fresh.next)(ctx, fcall("ns_table_row_add")); err != nil {
			t.Fatal(err)
		}
	}
	if fresh.loads != 1 || fresh.sends != 5 || state.loads != 1 {
		t.Fatalf("after the load: %d loads (%d attempts), %d sends; want 1 load and one send a verb", fresh.loads, state.loads, fresh.sends)
	}

	// The library vanishes (another tool deleted it): this process has
	// loaded once, so it loads nothing more and says so.
	fresh.holds = false
	err := hook.ProcessHook(fresh.next)(ctx, fcall("ns_table_drop"))
	if err == nil || fresh.loads != 1 || !strings.Contains(err.Error(), "ERR Function not found: function ns_table_drop") ||
		!strings.Contains(err.Error(), "; run: nova-redis fn load --addr <host:port>") || !remedied(err.Error()) {
		t.Fatalf("a miss after the load: err %v, %d loads; want the remedy and no second load", err, fresh.loads)
	}
	if redisconn.Classify(err) != redisconn.Other || lost(err) {
		t.Fatalf("a miss is the store's refusal (exit 1), got class %s", redisconn.Classify(err))
	}
}

// TestFirstContactOlderLibraryAndSkippedLoad: a store whose library of the
// name lacks the function (UNCHANGED) and a login the store will not let
// load (SKIPPED) each end in one refusal naming the deployer's remedy, after
// one load attempt; the FCALL is sent again only after UNCHANGED.
func TestFirstContactOlderLibraryAndSkippedLoad(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct {
		outcome redisfn.Outcome
		sends   int
		words   string
	}{
		{redisfn.Unchanged, 2, "does not register it: it is older than this nova-table"},
		{redisfn.Skipped, 1, "would not let this login load it (NOPERM"},
	} {
		s := &fakeStore{outcome: c.outcome}
		hook := libraryHook{state: &firstContact{}, load: s.load}
		err := hook.ProcessHook(s.next)(ctx, fcall("ns_table_create"))
		if err == nil || s.loads != 1 || s.sends != c.sends || !strings.Contains(err.Error(), c.words) || !strings.HasSuffix(err.Error(), deployRemedy) {
			t.Errorf("%s: err %v, %d loads, %d sends; want one load, %d sends and %q", c.outcome, err, s.loads, s.sends, c.sends, c.words)
		}
	}
}

// TestFirstContactFailedLoadIsTriedAgain: a load that failed (the store
// stopped answering) is the verb's error in the load's class, and leaves the
// process not done, so the next verb that misses loads.
func TestFirstContactFailedLoadIsTriedAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &fakeStore{loadErr: errors.New("redisfn: check nova_sprint: the store did not answer: dial tcp 127.0.0.1:1: connect: connection refused; nothing was changed")}
	state := &firstContact{}
	hook := libraryHook{state: state, load: s.load}
	err := hook.ProcessHook(s.next)(ctx, fcall("ns_table_create"))
	if err == nil || !strings.Contains(err.Error(), "loading it failed") || redisconn.Classify(err) != redisconn.Unreachable || state.done {
		t.Fatalf("a failed load: err %v class %s done %v; want the load's error, unreachable, not done", err, redisconn.Classify(err), state.done)
	}
	s.loadErr = nil
	if err := hook.ProcessHook(s.next)(ctx, fcall("ns_table_create")); err != nil || s.loads != 2 || !state.done {
		t.Fatalf("the next verb: err %v, %d loads, done %v; want it loaded and sent", err, s.loads, state.done)
	}
}

// TestFirstContactPipelines: a pipeline of FCALLs that all missed is sent
// again after the one load; a pipeline holding any other command, or a
// transaction, is loaded for but never sent again, and its missed FCALLs
// carry the remedy.
func TestFirstContactPipelines(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	s := &fakeStore{}
	hook := libraryHook{state: &firstContact{}, load: s.load}
	cmds := []redis.Cmder{redis.NewCmd(ctx, "fcall_ro", "ns_table_read", 1, "k"), redis.NewCmd(ctx, "fcall_ro", "ns_table_read", 1, "j")}
	if err := hook.ProcessPipelineHook(s.pipeline)(ctx, cmds); err != nil || s.loads != 1 || s.sends != 4 {
		t.Fatalf("all FCALLs missed: err %v, %d loads, %d sends; want one load and the pipeline sent twice", err, s.loads, s.sends)
	}
	for _, cmd := range cmds {
		if cmd.Err() != nil {
			t.Fatalf("%v after the load: %v", cmd.Args(), cmd.Err())
		}
	}

	for _, first := range []string{"multi", "hset"} {
		s := &fakeStore{}
		hook := libraryHook{state: &firstContact{}, load: s.load}
		cmds := []redis.Cmder{redis.NewCmd(ctx, first), fcall("ns_table_set"), redis.NewCmd(ctx, "exec")}
		err := hook.ProcessPipelineHook(s.pipeline)(ctx, cmds)
		if err == nil || s.loads != 1 || s.sends != 3 || !strings.Contains(cmds[1].Err().Error(), deployRemedy) || cmds[0].Err() != nil {
			t.Fatalf("a pipeline with %s: err %v, %d loads, %d sends, fcall err %v; want the load, one send and the remedy", first, err, s.loads, s.sends, cmds[1].Err())
		}
	}
}
