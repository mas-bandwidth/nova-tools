package tablemodel

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"time"
)

// Finding is one replayed witness or control. Result is "confirmed" when the
// table model's finding reproduces against the table.lua under test (a defect
// that source still has) and "pass" when a control holds.
type Finding struct {
	Name   string
	Result string
	Text   string
}

// ServerOptions names how the disposable store is started.
type ServerOptions struct {
	RedisServer string        // the redis-server program
	TmpDir      string        // where the store's directory goes; the system default when empty
	Startup     time.Duration // how long the store has to create its socket
}

// WithStore starts a disposable store, hands its connection to fn, and stops
// the store. A failed check inside fn is returned as an error.
func WithStore(ctx context.Context, o ServerOptions, fn func(*Store)) error {
	startup := o.Startup
	if startup == 0 {
		startup = 15 * time.Second
	}
	srv, err := StartServer(ctx, o.RedisServer, o.TmpDir, startup)
	if err != nil {
		return err
	}
	defer srv.Close()
	return guard(func() { fn(srv.Store(ctx)) })
}

// RunWitnesses replays the table model's findings against the table.lua at
// source, one after another, in one disposable store. Each finding is an exact
// sequence of calls whose replies and stored state are asserted; a reply that
// is not the one the finding says is a failure, so a source that no longer has
// the defect fails here and the finding is retired or the model updated.
func RunWitnesses(ctx context.Context, source string, o ServerOptions, emit func(Finding)) error {
	code, err := os.ReadFile(source)
	if err != nil {
		return cannotRun(err)
	}
	return WithStore(ctx, o, func(r *Store) {
		r.Cmd("FUNCTION", "LOAD", "REPLACE", "#!lua name=table_model\n"+string(code))
		witnesses(r, emit)
	})
}

func witnesses(r *Store, emit func(Finding)) {
	fields := map[string]string{"order": "c1,c2,c3", "footer": "total", "created_at": "2026-09-27T00:00:00Z"}
	for _, c := range []string{"c1", "c2", "c3"} {
		fields["col:"+c] = "members:none:10:" + c
	}
	call := func(name string, args ...any) any {
		return r.Cmd(append([]any{"FCALL", fcallName(name), 0}, args...)...)
	}
	jsonOf := func(v any) string {
		raw, err := json.Marshal(v)
		assert(err == nil, "cannot encode %v", v)
		return string(raw)
	}
	head := func(v any) any { return list(v)[0] }
	first2 := func(v any) any {
		l := list(v)
		assert(len(l) >= 2, "reply %v has fewer than two elements", l)
		return l[:2]
	}
	score := func(key, member string) any { return r.Cmd("ZSCORE", key, member) }
	setup := func() {
		r.Cmd("FLUSHDB")
		for _, t := range []string{"t1", "t2"} {
			assert(reflect.DeepEqual(call("create", t, jsonOf(fields)), []any{"OK"}), "create %s did not reply OK", t)
			for _, row := range []string{"r1", "r2"} {
				assert(head(call("row_add", t, row, "{}")) == "ROW", "row_add %s %s did not reply ROW", t, row)
			}
		}
		assert(head(call("cell_add", "t1", "r1", "c1", "m1", 1)) == "OK", "the seed cell_add did not reply OK")
		r.Cmd("ZADD", "external", 2, "m2")
	}
	binding := func(rows []any) string { return jsonOf(map[string]any{"fields": fields, "rows": rows}) }
	rowBinds := func(binds map[string]string) string { return jsonOf(map[string]any{"binds": binds}) }
	is := func(v any, want string) bool { s, ok := v.(string); return ok && s == want }
	same := func(v any, want ...any) bool { return reflect.DeepEqual(v, want) }
	report := func(result, name, text string) { emit(Finding{Name: name, Result: result, Text: text}) }
	const c1, c2 = "table:t1:cell:r1:c1", "table:t1:cell:r1:c2"

	setup()
	assert(head(call("cell_add", "t1", "r1", "c2", "m1", 1)) == "OK", "cell_add of m1 into a second cell did not reply OK")
	assert(is(score(c1, "m1"), "1") && is(score(c2, "m1"), "1"), "m1 is not in both owned cells")
	report("confirmed", "OnePlacePerTable", "OnePlacePerTable fails: add permits m1 in two owned cells")

	setup()
	assert(head(call("cell_add", "t2", "r1", "c1", "m1", 1)) == "OK", "cell_add into the other table did not reply OK")
	assert(is(score(c1, "m1"), "1") && is(score("table:t2:cell:r1:c1", "m1"), "1"), "m1 is not in both tables")
	report("pass", "scope-control", "scope control: same member can be in both tables, as intended by the per-table placement rule")

	setup()
	assert(same(call("bind", "t1", binding([]any{})), "OK"), "bind with no rows did not reply OK")
	assert(score(c1, "m1") == nil, "m1 survived the removal of r1")
	report("confirmed", "BindPreservesOwned-removal", "BindPreservesOwned fails: removing r1 deletes its owned m1")

	setup()
	rows := []any{map[string]any{"key": "r1", "binds": map[string]string{"c1": "external"}}, map[string]any{"key": "r2"}}
	assert(same(call("bind", "t1", binding(rows)), "OK"), "bind keeping r1 behind a binding did not reply OK")
	assert(is(score(c1, "m1"), "1"), "m1 is gone from the retained r1")
	assert(reflect.DeepEqual(call("members", "t1", "r1", "c1"), []any{"MEMBERS", []any{"m2", "2"}}), "the bound cell does not show the external set")
	report("confirmed", "BindPreservesOwned-retained", "BindPreservesOwned fails: retained r1 can hide m1 behind a binding")

	setup()
	assert(head(call("row_add", "t1", "r1", rowBinds(map[string]string{"c2": c1}))) == "ROW", "row_add with an alias did not reply ROW")
	assert(reflect.DeepEqual(call("members", "t1", "r1", "c2"), []any{"MEMBERS", []any{"m1", "1"}}), "the alias does not show m1")
	assert(head(call("drop", "t1")) == "OK", "drop did not reply OK")
	assert(score(c1, "m1") == nil, "m1 survived the drop")
	report("confirmed", "DropPreservesBound-alias", "DropPreservesBound fails with alias: unbound owner deletion removes bound view target")

	setup()
	assert(head(call("row_add", "t1", "r1", rowBinds(map[string]string{"c1": c2}))) == "ROW", "row_add with an owned-key alias did not reply ROW")
	assert(head(call("cell_add", "t1", "r1", "c2", "m1", 1)) == "OK", "cell_add through the owner did not reply OK")
	assert(reflect.DeepEqual(call("members", "t1", "r1", "c1"), []any{"MEMBERS", []any{"m1", "1"}}), "the alias does not show m1")
	report("confirmed", "CellWritesPreserveBoundSets-alias", "CellWritesPreserveBoundSets fails for owned-key alias: owner may still add member")

	setup()
	assert(head(call("cell_move", "t1", "r1", "c1", "m1", "c2")) == "OK", "cell_move did not reply OK")
	assert(score(c1, "m1") == nil && is(score(c2, "m1"), "1"), "cell_move did not move m1 with its score")
	r.Cmd("ZADD", "external", 2, "m2")
	assert(head(call("row_add", "t1", "r2", rowBinds(map[string]string{"c1": "external"}))) == "ROW", "row_add with a binding did not reply ROW")
	before := r.Cmd("ZRANGE", "external", 0, -1, "WITHSCORES")
	refused := func(v any) bool { return reflect.DeepEqual(first2(v), []any{"REFUSED", "BOUND"}) }
	assert(refused(call("cell_add", "t1", "r2", "c1", "m1", 1)), "cell_add into a bound cell was not refused BOUND")
	assert(refused(call("cell_remove", "t1", "r2", "c1", "m2")), "cell_remove from a bound cell was not refused BOUND")
	assert(refused(call("cell_move", "t1", "r2", "c1", "m2", "c2")), "cell_move out of a bound cell was not refused BOUND")
	assert(refused(call("cell_move", "t1", "r2", "c2", "m2", "c1")), "cell_move into a bound cell was not refused BOUND")
	assert(refused(call("clear", "t1")), "clear with a bound cell was not refused BOUND")
	assert(is(score(c2, "m1"), "1"), "a refusal changed the other owned member")
	assert(head(call("drop", "t1")) == "OK", "drop did not reply OK")
	assert(reflect.DeepEqual(r.Cmd("ZRANGE", "external", 0, -1, "WITHSCORES"), before), "drop changed the disjoint external set")
	report("pass", "controls", "controls: move keeps score; bound add/remove/move/clear refuse; drop leaves disjoint external set")

	setup()
	assert(head(call("clear", "t1")) == "OK", "clear did not reply OK")
	assert(integer(r.Cmd("EXISTS", "table:t1")) == 1, "clear removed the definition")
	assert(integer(r.Cmd("ZCARD", "table:t1:rows")) == 0, "clear left rows")
	assert(integer(r.Cmd("ZCARD", "table:t1:cell:r1:c1")) == 0, "clear left members")
	report("pass", "clear-control", "control: successful clear removes owned rows/members and keeps definition")
}
