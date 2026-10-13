package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/require"
)

// refusingRedis is the harness's fake Redis with a refusal per row name:
// a *config.RefusedError the runtime would answer (an actor without
// the role, a row in use), or a plain error, a Redis that could not answer.
type refusingRedis struct {
	*fakeRedis
	refuse map[string]error
}

func (r *refusingRedis) Write(ctx context.Context, kind string, row config.Row, prev config.View, actor, idem string) error {
	if err := r.refuse[row.Name]; err != nil {
		return err
	}
	return r.fakeRedis.Write(ctx, kind, row, prev, actor, idem)
}

// everyKindHarness is a store with one machine, three friends, the fleet and
// a sprint row whose git_identity and max_attempts differ from Redis's, and
// a Redis that answers each write of a name in refuse with that error.
func everyKindHarness(t *testing.T, refuse map[string]error) (*harness, *refusingRedis, deps) {
	t.Helper()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "rowan"
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	for _, args := range [][]string{
		{"machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64"},
		{"friend", "add", "rowan", "--width", "8", "--tiers", "frontier", "--roles", "builder"},
		{"friend", "add", "stella", "--width", "16", "--tiers", "frontier,pro"},
		{"friend", "add", "zara", "--width", "8", "--tiers", "pro"},
		{"fleet", "set", "--coordinator", "studio", "--redis_port", "6380", "--pg_dsn", dsn},
		{"sprint", "set", "--coordinator", "rowan", "--git_identity", "Rowan <rowan@mas-bandwidth.com>", "--max_attempts", "6"},
	} {
		code, out, errs := h.run(t, args...)
		require.Equal(t, 0, code, "%v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errs)
	}
	rr := &refusingRedis{fakeRedis: h.redis, refuse: refuse}
	d := h.deps()
	d.openRedis = func(context.Context, string) (redisSide, error) { return rr, nil }
	return h, rr, d
}

func runEveryKind(d deps, args ...string) (int, string, string) {
	var out, errb strings.Builder
	code := run(args, &out, &errb, d)
	return code, out.String(), errb.String()
}

// TestApplyAppliesEveryKindPastARefusalAndNamesIt is the v1.2.16 incident:
// a friend refused (then by the per-machine CEILING, since removed: a friend
// row has only width; here an actor without the role) stopped the apply
// before the sprint kind, so the
// sprint row's git_identity never reached Redis. A refusal now names the row
// on one line, the kind's later rows are still written, the refused kind is
// not stamped, and every later kind applies (tla/ConfigApply.tla,
// EveryPlannedKindIsAttemptedUnlessCrashed and StampedKindHasNoRefusal).
func TestApplyAppliesEveryKindPastARefusalAndNamesIt(t *testing.T) {
	t.Parallel()

	refusal := &config.RefusedError{Err: config.ErrActor, Detail: "roles of stella: --as rowan does not hold the coordinator role in Redis"}
	h, rr, d := everyKindHarness(t, map[string]error{"stella": refusal})
	code, out, errs := runEveryKind(d, "apply")
	require.Equal(t, 1, code, "a refused row is exit 1\nstdout: %s\nstderr: %s", out, errs)

	// Exactly one refusal line, naming the friend, the reason and the remedy.
	var refusals []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "CONFIG REFUSED ") {
			refusals = append(refusals, line)
		}
	}
	require.Len(t, refusals, 1, "stdout:\n%s", out)
	require.True(t, strings.HasPrefix(refusals[0], "CONFIG REFUSED kind=friend name=stella why=roles of stella: --as rowan does not hold the coordinator role in Redis"), "refusal line %q", refusals[0])
	require.Contains(t, refusals[0], " run: nova-config apply --as <a friend that holds the coordinator role>", "the remedy: %q", refusals[0])

	// The friend after the refused one is written; the friend kind is not stamped.
	require.Contains(t, rr.log, "write friend rowan", "log %v", rr.log)
	require.Contains(t, rr.log, "write friend zara", "a friend after the refused one is still written: %v", rr.log)
	require.NotContains(t, rr.log, "write friend stella", "log %v", rr.log)
	require.Zero(t, rr.revs[config.KindFriend], "a kind with a refused row was stamped: %v", rr.revs)
	require.NotContains(t, out, "CONFIG APPLY kind=friend ", "a refused kind does not say it applied:\n%s", out)

	// The sprint kind, after the refusal, is written and stamped.
	sprint := rr.views[config.KindSprint][config.KindSprint]
	require.Equal(t, "Rowan <rowan@mas-bandwidth.com>", sprint[config.FieldGitIdentity], "sprint view %v", sprint)
	require.Equal(t, "6", sprint[config.FieldMaxAttempts], "sprint view %v", sprint)
	sprintRev, err := h.store.Rev(context.Background(), config.KindSprint)
	require.NoError(t, err)
	require.Equal(t, sprintRev, rr.revs[config.KindSprint], "the sprint kind is stamped: %v", rr.revs)
	require.Contains(t, out, "CONFIG APPLY kind=sprint ", "stdout:\n%s", out)
	require.Contains(t, rr.log, "write tier flash", "the last kind applied: %v", rr.log)
	for _, kn := range []string{config.KindMachine, config.KindFleet} {
		require.NotZero(t, rr.revs[kn], "kind %s was not stamped: %v", kn, rr.revs)
	}

	// The end line names the refused kind and the kinds that applied.
	require.Contains(t, out, "CONFIG APPLY REFUSED kinds=friend:1 applied=machine,fleet,sprint,loop,provider,route,tier; run: ", "end line:\n%s", out)
	require.NotContains(t, out, "CONFIG APPLY OK", "stdout:\n%s", out)
	require.Empty(t, errs, "stderr %q", errs)

	// The next apply, with the refusal gone, writes only the diff and says OK.
	delete(rr.refuse, "stella")
	rr.log = nil
	code, out, errs = runEveryKind(d, "apply")
	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", out, errs)
	require.Equal(t, []string{"write friend stella"}, rr.log, "the retry writes only the refused row")
	require.True(t, strings.HasSuffix(out, "CONFIG APPLY OK kinds=8\n"), "stdout:\n%s", out)
}

// TestApplyRefusesARowThatNamesARowRefusedEarlier: the sprint row names the
// coordinator friend; when that friend's write is refused, the sprint row is
// refused with that reason and never written half-referenced, and the kinds
// after it still apply (ConfigApply.tla RefRefused).
func TestApplyRefusesARowThatNamesARowRefusedEarlier(t *testing.T) {
	t.Parallel()

	actor := &config.RefusedError{Err: config.ErrActor, Detail: "roles of rowan: --as rowan is not a registered friend; apply as a friend that holds the coordinator role"}
	_, rr, d := everyKindHarness(t, map[string]error{"rowan": actor})
	code, out, errs := runEveryKind(d, "apply")
	require.Equal(t, 1, code, "stdout: %s\nstderr: %s", out, errs)
	require.Contains(t, out, "CONFIG REFUSED kind=friend name=rowan why=roles of rowan", "stdout:\n%s", out)
	require.Contains(t, out, "CONFIG REFUSED kind=sprint name=sprint why=sprint sprint: its coordinator names friend rowan, refused earlier in this run", "stdout:\n%s", out)
	require.NotContains(t, rr.log, "write sprint sprint", "a row naming a refused row was written: %v", rr.log)
	require.Zero(t, rr.revs[config.KindSprint], "the sprint kind was stamped: %v", rr.revs)
	require.Contains(t, out, "CONFIG APPLY REFUSED kinds=friend:1,sprint:1 applied=machine,fleet,loop,provider,route,tier; run: ", "stdout:\n%s", out)
}

// TestApplyStopsAtOnceOnAStoreError: a Redis that could not answer is not a
// refusal; the run stops there with exit 2 and writes no later kind.
func TestApplyStopsAtOnceOnAStoreError(t *testing.T) {
	t.Parallel()

	_, rr, d := everyKindHarness(t, map[string]error{"studio": errors.New("redis at 127.0.0.1:6379: connection refused")})
	code, out, errs := runEveryKind(d, "apply")
	require.Equal(t, 2, code, "stdout: %s\nstderr: %s", out, errs)
	require.Contains(t, errs, "nova-config apply REFUSED: redis at 127.0.0.1:6379: connection refused", "stderr %q", errs)
	require.Empty(t, rr.log, "a write after the connection error: %v", rr.log)
	require.Empty(t, rr.revs, "a stamp after the connection error: %v", rr.revs)
	require.NotContains(t, out, "CONFIG APPLY OK", "stdout:\n%s", out)
	require.NotContains(t, out, "CONFIG APPLY REFUSED", "stdout:\n%s", out)
}

// TestApplyConflictStopsOnlyItsKind: a kind whose stamp is ahead is refused
// CONFLICT and writes nothing; every later kind still applies, and the run
// ends exit 1 naming it.
func TestApplyConflictStopsOnlyItsKind(t *testing.T) {
	t.Parallel()

	_, rr, d := everyKindHarness(t, nil)
	rr.revs[config.KindFleet] = 99
	code, out, errs := runEveryKind(d, "apply")
	require.Equal(t, 1, code, "stdout: %s\nstderr: %s", out, errs)
	require.Contains(t, errs, "nova-config apply REFUSED: CONFLICT fleet: Redis holds rev 99", "stderr %q", errs)
	require.NotContains(t, rr.log, "write fleet fleet", "a CONFLICT kind wrote: %v", rr.log)
	require.Contains(t, rr.log, "write sprint sprint", "a kind after the CONFLICT was not applied: %v", rr.log)
	require.Contains(t, out, "CONFIG APPLY REFUSED kinds=fleet:1 applied=machine,friend,sprint,loop,provider,route,tier; run: ", "stdout:\n%s", out)
}

// TestApplyCheckNamesAKnownRefusalAndGoesOn: --check reports the ops it
// would write and the ones it already knows would be refused, never stops
// early, and writes nothing.
func TestApplyCheckNamesAKnownRefusalAndGoesOn(t *testing.T) {
	t.Parallel()

	_, rr, d := everyKindHarness(t, nil)
	rr.revs[config.KindFriend] = 99 // the friend kind is CONFLICT: its pending rows count as refused
	code, out, errs := runEveryKind(d, "apply", "--check")
	require.Equal(t, 1, code, "stdout: %s\nstderr: %s", out, errs)
	require.Contains(t, out, "CONFIG CHECK REFUSED kind=sprint name=sprint why=sprint sprint: its coordinator names friend rowan, refused earlier in this run (CONFLICT friend: Redis holds rev 99", "stdout:\n%s", out)
	require.Contains(t, out, "CONFIG CHECK kind=tier ", "the check went on past the refusals:\n%s", out)
	require.Contains(t, out, "CONFIG CHECK REFUSED kinds=friend:1,sprint:1 applied=machine,fleet,loop,provider,route,tier; run: ", "stdout:\n%s", out)
	require.Empty(t, rr.log, "--check wrote: %v", rr.log)
}

// TestApplyStopsAtAStoreErrorAfterARefusalInTheSameKind: a connection error
// after a refusal in the same kind is never swallowed as one more refusal:
// the run says the refusal, then stops with exit 2 and the error on stderr,
// and no later kind is written (tla/ConfigApply.tla, StoreErrorStopsTheRun).
func TestApplyStopsAtAStoreErrorAfterARefusalInTheSameKind(t *testing.T) {
	t.Parallel()

	refusal := &config.RefusedError{Err: config.ErrActor, Detail: "roles of rowan: --as rowan does not hold the coordinator role in Redis"}
	_, rr, d := everyKindHarness(t, map[string]error{"rowan": refusal, "stella": errors.New("redis at 127.0.0.1:6379: connection refused")})
	code, out, errs := runEveryKind(d, "apply")
	require.Equal(t, 2, code, "stdout: %s\nstderr: %s", out, errs)
	require.Contains(t, errs, "nova-config apply REFUSED: redis at 127.0.0.1:6379: connection refused", "the error is said: %q", errs)
	require.Contains(t, out, "CONFIG REFUSED kind=friend name=rowan ", "the refusal before it is still said:\n%s", out)
	require.Contains(t, out, "run: nova-config apply --as <a friend that holds the coordinator role>", "the remedy:\n%s", out)
	require.NotContains(t, rr.log, "write friend zara", "a write after the store error: %v", rr.log)
	require.NotContains(t, rr.log, "write sprint sprint", "a later kind was written against a failed Redis: %v", rr.log)
	require.Zero(t, rr.revs[config.KindFriend], "%v", rr.revs)
	require.NotContains(t, out, "CONFIG APPLY OK", "stdout:\n%s", out)
	require.NotContains(t, out, "CONFIG APPLY REFUSED", "stdout:\n%s", out)
}

// TestApplyWritesTheSprintRowWhenTheCoordinatorsSetIsRefused: the ref rule
// does not bring the incident back. The coordinator friend is in Redis and
// her set is refused; the sprint row, whose coordinator does not change, is
// written with its new git_identity and stamped.
func TestApplyWritesTheSprintRowWhenTheCoordinatorsSetIsRefused(t *testing.T) {
	t.Parallel()

	h, rr, d := everyKindHarness(t, map[string]error{})
	code, out, errs := runEveryKind(d, "apply")
	require.Equal(t, 0, code, "first apply\nstdout: %s\nstderr: %s", out, errs)
	for _, args := range [][]string{
		{"friend", "set", "rowan", "--width", "16"},
		{"sprint", "set", "--git_identity", "Nova Sprint <sprint@mas-bandwidth.com>"},
	} {
		code, out, errs := h.run(t, args...)
		require.Equal(t, 0, code, "%v\nstdout: %s\nstderr: %s", args, out, errs)
	}
	rr.refuse["rowan"] = &config.RefusedError{Err: config.ErrActor, Detail: "roles of rowan: --as rowan does not hold the coordinator role in Redis"}
	rr.log = nil
	code, out, errs = runEveryKind(d, "apply")
	require.Equal(t, 1, code, "stdout: %s\nstderr: %s", out, errs)
	require.Contains(t, out, "CONFIG REFUSED kind=friend name=rowan ", "stdout:\n%s", out)
	require.NotContains(t, out, "kind=sprint name=sprint why=", "the sprint row was refused by the ref rule:\n%s", out)
	require.Contains(t, rr.log, "write sprint sprint", "log %v", rr.log)
	require.Equal(t, "Nova Sprint <sprint@mas-bandwidth.com>", rr.views[config.KindSprint][config.KindSprint][config.FieldGitIdentity])
	sprintRev, err := h.store.Rev(context.Background(), config.KindSprint)
	require.NoError(t, err)
	require.Equal(t, sprintRev, rr.revs[config.KindSprint], "the sprint kind is stamped: %v", rr.revs)
	require.Contains(t, out, "CONFIG APPLY REFUSED kinds=friend:1 applied=machine,fleet,sprint,loop,provider,route,tier; run: ", "stdout:\n%s", out)
}

// TestApplyWritesTheSprintRowWhenTheLiveSeatIsHeld: the live seat in Redis is
// stella and the stored row names rowan, whose set is refused. The row
// written holds stella, so the ref rule does not refuse it: the held line is
// said, git_identity lands and the sprint kind is stamped; --check names no
// sprint refusal either (tla/ConfigApply.tla,
// SprintRefusedOnlyForItsWrittenRef).
func TestApplyWritesTheSprintRowWhenTheLiveSeatIsHeld(t *testing.T) {
	t.Parallel()

	h, rr, d := everyKindHarness(t, map[string]error{})
	code, out, errs := runEveryKind(d, "apply")
	require.Equal(t, 0, code, "first apply\nstdout: %s\nstderr: %s", out, errs)
	rr.views[config.KindSprint][config.KindSprint]["coordinator"] = "stella"
	for _, args := range [][]string{
		{"friend", "set", "rowan", "--width", "16"},
		{"sprint", "set", "--git_identity", "Nova Sprint <sprint@mas-bandwidth.com>"},
	} {
		code, out, errs := h.run(t, args...)
		require.Equal(t, 0, code, "%v\nstdout: %s\nstderr: %s", args, out, errs)
	}
	rr.refuse["rowan"] = &config.RefusedError{Err: config.ErrActor, Detail: "roles of rowan: --as rowan does not hold the coordinator role in Redis"}
	code, out, errs = runEveryKind(d, "apply", "--check")
	require.Equal(t, 0, code, "--check knows no refusal (the runtime's role refusal is not --check's)\nstdout: %s\nstderr: %s", out, errs)
	require.NotContains(t, out, "REFUSED kind=sprint", "stdout:\n%s", out)
	rr.log = nil
	code, out, errs = runEveryKind(d, "apply")
	require.Equal(t, 1, code, "stdout: %s\nstderr: %s", out, errs)
	require.Contains(t, out, "APPLY HELD kind=sprint field=coordinator live=stella row=rowan", "stdout:\n%s", out)
	require.Contains(t, out, "CONFIG REFUSED kind=friend name=rowan ", "stdout:\n%s", out)
	require.NotContains(t, out, "kind=sprint name=sprint why=", "the sprint row was refused with the seat held:\n%s", out)
	require.Contains(t, rr.log, "write sprint sprint", "log %v", rr.log)
	sprint := rr.views[config.KindSprint][config.KindSprint]
	require.Equal(t, "Nova Sprint <sprint@mas-bandwidth.com>", sprint[config.FieldGitIdentity], "%v", sprint)
	require.Equal(t, "stella", sprint["coordinator"], "the live seat is held: %v", sprint)
	sprintRev, err := h.store.Rev(context.Background(), config.KindSprint)
	require.NoError(t, err)
	require.Equal(t, sprintRev, rr.revs[config.KindSprint], "the sprint kind is stamped: %v", rr.revs)
}

// TestApplyJSONKeepsTheRefusalsBeforeAStoreError: under --json the one object
// carries the row refused before a store error, at exit 2.
func TestApplyJSONKeepsTheRefusalsBeforeAStoreError(t *testing.T) {
	t.Parallel()

	refusal := &config.RefusedError{Err: config.ErrActor, Detail: "roles of rowan: --as rowan does not hold the coordinator role in Redis"}
	_, _, d := everyKindHarness(t, map[string]error{"rowan": refusal, "stella": errors.New("redis at 127.0.0.1:6379: connection refused")})
	code, out, errs := runEveryKind(d, "apply", "--json")
	require.Equal(t, 2, code, "stdout: %s\nstderr: %s", out, errs)
	require.Equal(t, 1, strings.Count(out, `"result"`), "one object: %s", out)
	require.Contains(t, out, `"rowan"`, "the refused row is in the object: %s", out)
	require.Contains(t, out, "connection refused", "the error is in the object: %s", out)
	require.Contains(t, out, `"exit":2`, "%s", out)
}
