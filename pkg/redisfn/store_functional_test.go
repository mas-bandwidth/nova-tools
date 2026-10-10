//go:build functional

package redisfn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/pkg/testredis"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What the package says of a store, held against a redis-server: every test
// starts a throwaway one of its own, so no library of one test is on the
// store of another.

// store is a client of a redis-server only this test uses.
func store(t *testing.T) *redis.Client {
	t.Helper()
	return client(t, &redis.Options{Addr: testredis.Start(t)})
}

func client(t *testing.T, options *redis.Options) *redis.Client {
	t.Helper()
	c := redis.NewClient(options)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// call is what the function answers.
func call(t *testing.T, c *redis.Client, function string) string {
	t.Helper()
	answer, err := c.FCall(context.Background(), function, nil).Text()
	if err != nil {
		require.NoError(t, err, "FCALL %s: %v", function, err)
	}
	return answer
}

// put loads code with no loader: what another writer does to the store.
func put(t *testing.T, c *redis.Client, code string) {
	t.Helper()
	if err := c.FunctionLoadReplace(context.Background(), code).Err(); err != nil {
		require.NoError(t, err, "FUNCTION LOAD REPLACE: %v\n%s", err, code)
	}
}

// held is what the store holds: every library's name with its functions'
// names, sorted.
func held(t *testing.T, c *redis.Client) string {
	t.Helper()
	libs, err := c.FunctionList(context.Background(), redis.FunctionListQuery{}).Result()
	if err != nil {
		require.NoError(t, err, err)
	}
	var out []string
	for _, lib := range libs {
		var names []string
		for _, f := range lib.Functions {
			names = append(names, f.Name)
		}
		slices.Sort(names)
		out = append(out, lib.Name+"("+strings.Join(names, ",")+")")
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

// answering is one line of Lua that registers a function which answers the text.
func answering(name, text string) string {
	return "redis.register_function('" + name + "', function(keys, args) return '" + text + "' end)\n"
}

func TestTheLoadPutsTheSourceOnTheStoreAndItsFunctionsAnswer(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	receipt, err := two().Ensure(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Loaded, Digest: twoDigest}) {
		require.Failf(t, "", "Ensure = %+v %v", receipt, err)
	}
	if fa, fb := call(t, c, "fa"), call(t, c, "fb"); fa != "fa" || fb != "fb" {
		require.Failf(t, "", "fa answers %q and fb %q", fa, fb)
	}
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{WithCode: true}).Result()
	if err != nil || len(libs) != 1 || libs[0].Name != "lib_one" || libs[0].Code != twoSource {
		require.Failf(t, "", "the store holds %+v (%v), want lib_one with the source byte for byte", libs, err)
	}
}

func TestTheLoadTwiceLeavesWhatOneLoadLeft(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := two()
	first, err := lib.Ensure(ctx, c)
	if err != nil || first.Outcome != Loaded {
		require.Failf(t, "", "the first Ensure = %+v %v", first, err)
	}
	after := held(t, c)
	second, err := lib.Ensure(ctx, c)
	if err != nil || second.Outcome != Unchanged || second.Digest != first.Digest {
		require.Failf(t, "", "the second Ensure = %+v %v, the first gave %+v", second, err, first)
	}
	if now := held(t, c); now != after || now != "lib_one(fa,fb)" {
		require.Failf(t, "", "after two loads the store holds %s, after one %s", now, after)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "Check = %v %v", state, err)
	}
	if fa := call(t, c, "fa"); fa != "fa" {
		require.Equal(t, "fa", fa, "fa answers %q", fa)
	}
}

func TestALoadOfChangedSourceChangesWhatTheStoreDoesAndTheDigest(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	one := Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": answering("fa", "one"), "b.lua": answering("fb", "b")}), Glob: "*.lua"}
	two := Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": answering("fa", "two"), "c.lua": answering("fc", "c")}), Glob: "*.lua"}
	first, err := one.Ensure(ctx, c)
	if err != nil || first.Outcome != Loaded || call(t, c, "fa") != "one" {
		require.Failf(t, "", "the load of the first = %+v %v", first, err)
	}
	second, err := two.Ensure(ctx, c)
	if err != nil || second.Outcome != Replaced || second.Digest == first.Digest || second.Was != first.Digest {
		require.Failf(t, "", "the load of the second = %+v %v, the first was %+v", second, err, first)
	}
	if fa, fc := call(t, c, "fa"), call(t, c, "fc"); fa != "two" || fc != "c" {
		require.Failf(t, "", "after the second, fa answers %q and fc %q", fa, fc)
	}
	// The whole library was replaced: what only the first registered is gone.
	if err := c.FCall(ctx, "fb", nil).Err(); err == nil || !strings.Contains(err.Error(), "Function not found") {
		require.Failf(t, "", "fb, which the second does not register, answers: %v", err)
	}
	if now := held(t, c); now != "lib_one(fa,fc)" {
		require.Equal(t, "lib_one(fa,fc)", now, "the store holds %s", now)
	}
	state, err := one.Check(ctx, c)
	var mismatch *MismatchError
	if state != Different || !errors.As(err, &mismatch) || mismatch.Loaded != second.Digest || mismatch.Want != first.Digest {
		require.Failf(t, "", "Check of the first = %v %v, want different with loaded=%s want=%s", state, err, second.Digest, first.Digest)
	}
	if state, err := two.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "Check of the second = %v %v", state, err)
	}
}

func TestCheckSaysAbsentSameAndDifferentAndChangesNothing(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := two()

	state, err := lib.Check(ctx, c)
	var mismatch *MismatchError
	if state != Absent || !errors.As(err, &mismatch) || *mismatch != (MismatchError{Library: "lib_one", Want: twoDigest, Remedy: mismatch.Remedy}) {
		require.Failf(t, "", "on an empty store Check = %v %v", state, err)
	}
	if line := err.Error(); line != "redisfn: library lib_one is absent from the store: loaded=none want="+twoDigest+"; remedy: load this binary's library (Ensure)" {
		require.Failf(t, "", "the error reads %q", line)
	}
	if now := held(t, c); now != "" {
		require.Empty(t, now, "after Check the store holds %s", now)
	}

	if _, err := lib.Ensure(ctx, c); err != nil {
		require.NoError(t, err, err)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "after the load, Check = %v %v", state, err)
	}

	other := strings.Replace(twoSource, "local b = 1", "local b = 2", 1)
	put(t, c, other)
	state, err = lib.Check(ctx, c)
	if state != Different || !errors.As(err, &mismatch) || *mismatch != (MismatchError{Library: "lib_one", Want: twoDigest, Loaded: DigestOf(other), Remedy: mismatch.Remedy}) {
		require.Failf(t, "", "over other code Check = %v %v", state, err)
	}
	if line := err.Error(); line != "redisfn: library lib_one on the store is not the one this binary was built with: loaded="+DigestOf(other)+" want="+twoDigest+
		"; remedy: load this binary's library over it (Ensure), or run the binary the store's library came from" {
		require.Failf(t, "", "the error reads %q", line)
	}
	if libs, err := c.FunctionList(ctx, lib.Query()).Result(); err != nil || len(libs) != 1 || libs[0].Code != other {
		require.Failf(t, "", "after Check the store holds %+v (%v), want the other code as it was", libs, err)
	}
}

// The store matches a library's name as a pattern and without case, so what
// it returns for one name may be another library. Check takes the library of
// exactly its name.
func TestCheckIsNotMisledByALibraryWhoseNameTheStoreMatches(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := two()
	put(t, c, "#!lua name=LIB_ONE\n"+answering("upper", "upper"))
	put(t, c, "#!lua name=Lib_One\n"+answering("mixed", "mixed"))

	// The control: asked for lib_one, the store returns the two that are not it.
	libs, err := c.FunctionList(ctx, lib.Query()).Result()
	if err != nil || len(libs) != 2 {
		require.Failf(t, "", "FUNCTION LIST LIBRARYNAME lib_one returns %+v (%v), want LIB_ONE and Lib_One", libs, err)
	}
	if state, err := lib.Check(ctx, c); state != Absent || !isMismatch(err) {
		require.Failf(t, "", "Check = %v %v, want absent: no library is named lib_one", state, err)
	}
	receipt, err := lib.LoadMissing(ctx, c)
	if err != nil || receipt.Outcome != Loaded || receipt.Was != "" {
		require.Failf(t, "", "LoadMissing = %+v %v, want loaded", receipt, err)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "after the load Check = %v %v", state, err)
	}
	if now := held(t, c); now != "LIB_ONE(upper) Lib_One(mixed) lib_one(fa,fb)" {
		require.Equal(t, "LIB_ONE(upper) Lib_One(mixed) lib_one(fa,fb)", now, "the store holds %s", now)
	}
}

// The identity Check reads is the library and nothing beside it: no key of
// the store holds it, so emptying the keys leaves it, and only a change of
// the library changes it.
func TestCheckReadsTheLibraryAndNoKey(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := two()
	if _, err := lib.Ensure(ctx, c); err != nil {
		require.NoError(t, err, err)
	}
	if keys, err := c.Keys(ctx, "*").Result(); err != nil || len(keys) != 0 {
		require.Failf(t, "", "after the load the store has the keys %q (%v), want none", keys, err)
	}
	for _, key := range []string{"lib_one", "lib_one:sha", "redisfn:lib_one", twoDigest} {
		if err := c.Set(ctx, key, "0000000000000000", 0).Err(); err != nil {
			require.NoError(t, err, err)
		}
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "with keys another writer set, Check = %v %v", state, err)
	}
	if err := c.FlushAll(ctx).Err(); err != nil {
		require.NoError(t, err, err)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "after FLUSHALL, Check = %v %v", state, err)
	}
	if err := c.FunctionDelete(ctx, "lib_one").Err(); err != nil {
		require.NoError(t, err, err)
	}
	if state, err := lib.Check(ctx, c); state != Absent || !isMismatch(err) {
		require.Failf(t, "", "after FUNCTION DELETE, Check = %v %v", state, err)
	}
}

func TestEnsureLoadsWhenAbsentOrDifferentAndSaysWhich(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := two()

	receipt, err := lib.Ensure(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Loaded, Digest: twoDigest}) {
		require.Failf(t, "", "on an empty store Ensure = %+v %v", receipt, err)
	}
	if line := receipt.String(); line != "LOADED lib_one sha="+twoDigest {
		require.Equal(t, "LOADED lib_one sha="+twoDigest, line, "the receipt reads %q", line)
	}
	if fa := call(t, c, "fa"); fa != "fa" {
		require.Equal(t, "fa", fa, "fa answers %q", fa)
	}

	receipt, err = lib.Ensure(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Unchanged, Digest: twoDigest}) {
		require.Failf(t, "", "over itself Ensure = %+v %v", receipt, err)
	}

	other := "#!lua name=lib_one\n" + answering("fa", "other")
	put(t, c, other)
	receipt, err = lib.Ensure(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Replaced, Digest: twoDigest, Was: DigestOf(other)}) {
		require.Failf(t, "", "over other code Ensure = %+v %v", receipt, err)
	}
	if line := receipt.String(); line != "REPLACED lib_one sha="+twoDigest+" was="+DigestOf(other) {
		require.Equal(t, "REPLACED lib_one sha="+twoDigest+" was="+DigestOf(other), line, "the receipt reads %q", line)
	}
	if fa := call(t, c, "fa"); fa != "fa" {
		require.Equal(t, "fa", fa, "after the replace fa answers %q", fa)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "Check = %v %v", state, err)
	}
}

// LoadMissing loads only when the store holds no library of the name, with
// FUNCTION LOAD and never REPLACE: over other code under the name, over
// itself, and when another loader is there first, the store is left as it
// was and the call answers with a nil error (nova-tools #3620).
func TestLoadMissingNeverReplacesALibraryOnTheStore(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := two()

	receipt, err := lib.LoadMissing(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Loaded, Digest: twoDigest}) {
		require.Failf(t, "", "on an empty store LoadMissing = %+v %v", receipt, err)
	}
	if fa := call(t, c, "fa"); fa != "fa" {
		require.Equal(t, "fa", fa, "fa answers %q", fa)
	}
	receipt, err = lib.LoadMissing(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Unchanged, Digest: twoDigest}) {
		require.Failf(t, "", "over itself LoadMissing = %+v %v", receipt, err)
	}

	// Other code under the name: a deployed build this binary did not bring.
	other := "#!lua name=lib_one\n" + answering("fa", "deployed") + answering("fnew", "deployed")
	put(t, c, other)
	receipt, err = lib.LoadMissing(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Unchanged, Digest: twoDigest}) {
		require.Failf(t, "", "over other code LoadMissing = %+v %v", receipt, err)
	}
	if fa, fnew := call(t, c, "fa"), call(t, c, "fnew"); fa != "deployed" || fnew != "deployed" {
		require.Failf(t, "", "after LoadMissing over the deployed build fa answers %q and fnew %q", fa, fnew)
	}
	if state, err := lib.Check(ctx, c); state != Different || !isMismatch(err) {
		require.Failf(t, "", "Check = %v %v, want the deployed build left as it was", state, err)
	}

	// The race, as the store answers it: a FUNCTION LOAD of a name the store
	// holds (another loader's, put there after LoadMissing's read) is refused
	// in the words LoadMissing takes for Unchanged, and writes nothing.
	b, err := lib.build()
	if err != nil {
		require.NoError(t, err, err)
	}
	err = c.FunctionLoad(ctx, b.source).Err()
	if err == nil || !isReply(err) || !libraryExists.MatchString(err.Error()) {
		require.Failf(t, "", "FUNCTION LOAD over a library of the name: %v, want the store's refusal that libraryExists reads", err)
	}
	if fa := call(t, c, "fa"); fa != "deployed" {
		require.Equal(t, "deployed", fa, "after the refused FUNCTION LOAD fa answers %q", fa)
	}

	// A function name another library holds: skipped, with the store's
	// words, and nothing written.
	fresh := Library{Name: "lib_new", Files: tree(map[string]string{"a.lua": answering("fnew", "new")}), Glob: "*.lua"}
	receipt, err = fresh.LoadMissing(ctx, c)
	if err != nil || receipt.Outcome != Skipped || receipt.Why != "ERR Function fnew already exists" {
		require.Failf(t, "", "a function another library holds: LoadMissing = %+v %v", receipt, err)
	}
	if now := held(t, c); now != "lib_one(fa,fnew)" {
		require.Equal(t, "lib_one(fa,fnew)", now, "after the skipped load the store holds %s", now)
	}
}

// Whatever the store refuses a library for, it holds afterwards the whole
// library it held before: the functions answer as they did and Check says so.
func TestALoadTheStoreRefusesLeavesTheLibraryItHeld(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	good := Library{Name: "lib_one", Files: tree(map[string]string{"lua/a.lua": answering("fa", "good"), "lua/b.lua": answering("fb", "good")}), Glob: "lua/*.lua"}
	if _, err := good.Ensure(ctx, c); err != nil {
		require.NoError(t, err, err)
	}
	for _, bad := range []struct {
		name string
		b    string // lua/b.lua of the library the store refuses
		want []string
	}{
		{"a syntax error", answering("fb", "bad") + "local x = = 3\n",
			[]string{"redisfn: load lib_one: the store refused: ERR Error compiling function: user_function:", "unexpected symbol near '='", " = lua/b.lua:2]; the store holds what it held before"}},
		{"a statement cut off at the file's end", answering("fb", "bad") + "local x =",
			[]string{"ERR Error compiling function: user_function:", "near 'end'", " = the loader's lines around lua/b.lua]; the store holds what it held before"}},
		{"an error when the library's own text runs", answering("fb", "bad") + "\nlocal t = nil\nlocal x = t.field\n",
			[]string{"ERR Error registering functions: ERR user_function:", "attempt to index local 't'", " = lua/b.lua:4]; the store holds what it held before"}},
		{"a function name the store does not take", answering("f-b", "bad"),
			[]string{"the store refused: ERR Error registering functions:", "; the store holds what it held before"}},
		{"a function registered twice under a name the loader cannot read", "local name = 'fb'\nredis.register_function(name, function() end)\nredis.register_function(name, function() end)\n",
			[]string{"ERR Error registering functions: ERR Function already exists in the library", "; the store holds what it held before"}},
	} {
		lib := Library{Name: "lib_one", Files: tree(map[string]string{"lua/a.lua": answering("fa", "bad"), "lua/b.lua": bad.b}), Glob: "lua/*.lua"}
		receipt, err := lib.Ensure(ctx, c)
		if err == nil || receipt.Outcome != Failed {
			require.Failf(t, "", "%s: Ensure = %+v %v, want the store's refusal", bad.name, receipt, err)
		}
		line := err.Error()
		if strings.ContainsAny(line, "\n\r") {
			assert.False(t, strings.ContainsAny(line, "\n\r"), "%s: the error is more than one line: %q", bad.name, line)
		}
		for _, want := range bad.want {
			if !strings.Contains(line, want) {
				assert.Contains(t, line, want, "%s: the error does not say %q: %s", bad.name, want, line)
			}
		}
		var reply redis.Error
		if !errors.As(err, &reply) {
			assert.ErrorAs(t, err, &reply, "%s: the error does not wrap the store's reply: %v", bad.name, err)
		}
		if fa, fb := call(t, c, "fa"), call(t, c, "fb"); fa != "good" || fb != "good" {
			assert.Failf(t, "", "%s: after the refusal fa answers %q and fb %q", bad.name, fa, fb)
		}
		if state, err := good.Check(ctx, c); state != Same || err != nil {
			assert.Failf(t, "", "%s: after the refusal Check of the library held = %v %v", bad.name, state, err)
		}
		if receipt, err := lib.Ensure(ctx, c); err == nil || receipt.Outcome != Failed || receipt.Was == "" {
			assert.Failf(t, "", "%s: Ensure = %+v %v, want the store's refusal", bad.name, receipt, err)
		}
		if receipt, err := lib.LoadMissing(ctx, c); err != nil || receipt.Outcome != Unchanged {
			assert.Failf(t, "", "%s: LoadMissing = %+v %v, want the library held left as it is", bad.name, receipt, err)
		}
		if now := held(t, c); now != "lib_one(fa,fb)" {
			assert.Equal(t, "lib_one(fa,fb)", now, "%s: after the refusal the store holds %s", bad.name, now)
		}
	}

	// A library that registers nothing is refused as well, and an empty store
	// stays empty.
	empty := store(t)
	none := Library{Name: "lib_none", Files: tree(map[string]string{"a.lua": "local x = 1\n"}), Glob: "*.lua"}
	if _, err := none.Ensure(ctx, empty); err == nil || !strings.Contains(err.Error(), "the store refused: ERR No functions registered") {
		assert.Failf(t, "", "a library that registers nothing: %v", err)
	}
	if now := held(t, empty); now != "" {
		assert.Empty(t, now, "after the refusal the empty store holds %s", now)
	}
}

// The line the store names in an error of a running function is a line of
// the source; the note a failed call adds and the line mapping say which
// file's.
func TestALineTheStoreNamesMapsToItsFile(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := Library{
		Name: "lib_one",
		Files: tree(map[string]string{
			"lua/a.lua": answering("fa", "a") + "\n\n" + answering("fa2", "a"),
			"lua/b.lua": "-- two lines\n-- of comment\nredis.register_function('fb', function(keys, args)\n  local t = nil\n  return t.field\nend)",
			"lua/c.lua": "redis.register_function('fc', function(keys, args)\n  return NS.fail('from c')\nend)\n" +
				"redis.register_function('fd', function(keys, args)\n  return redis.call('INCR', keys[1])\nend)\n",
		}),
		Glob:    "lua/*.lua",
		Prelude: "local NS = {}\nfunction NS.fail(why)\n  error('failed ' .. why)\nend\n",
	}
	b, err := lib.build()
	require.NoError(t, err, err)
	if _, err := lib.Ensure(ctx, c); err != nil {
		require.NoError(t, err, err)
	}

	err = c.FCall(ctx, "fb", nil).Err()
	if err == nil || !strings.Contains(err.Error(), "attempt to index local 't'") {
		require.Failf(t, "", "fb: %v", err)
	}
	if note := b.explain(err.Error()); !strings.HasSuffix(note, " = lua/b.lua:5]") {
		require.Failf(t, "", "the note of fb's error: %s", note)
	}
	var n int
	if _, serr := fmt.Sscanf(err.Error()[strings.Index(err.Error(), "user_function:"):], "user_function:%d", &n); serr != nil {
		require.NoError(t, serr, serr)
	}
	if origin, ok := b.locate(n); !ok || origin != (Origin{File: "lua/b.lua", Line: 5}) {
		require.Failf(t, "", "line %d = %+v (%v), want line 5 of lua/b.lua", n, origin, ok)
	}
	// The line is the source's own: the text there is the file's line.
	if line := strings.Split(b.source, "\n")[n-1]; line != "  return t.field" {
		require.Equal(t, "  return t.field", line, "line %d of the source is %q", n, line)
	}

	// An error raised in the prelude, by a function of c.
	err = c.FCall(ctx, "fc", nil).Err()
	if err == nil || !strings.Contains(err.Error(), "failed from c") {
		require.Failf(t, "", "fc: %v", err)
	}
	if note := b.explain(err.Error()); !strings.Contains(note, " = prelude:3") {
		require.Contains(t, note, " = prelude:3", "the note of fc's error: %s", note)
	}

	// An error of a command the function ran names the line of the call.
	if err := c.Set(ctx, "k", "not a number", 0).Err(); err != nil {
		require.NoError(t, err, err)
	}
	err = c.FCall(ctx, "fd", []string{"k"}).Err()
	if err == nil || !strings.Contains(err.Error(), "not an integer") {
		require.Failf(t, "", "fd: %v", err)
	}
	if note := b.explain(err.Error()); !strings.HasSuffix(note, " = lua/c.lua:5]") {
		require.True(t, strings.HasSuffix(note, " = lua/c.lua:5]"), "the note of fd's error: %s", note)
	}
}

// many is a file of that many top-level locals and one function that answers
// the last of them.
func many(prefix string, locals int) string {
	var b strings.Builder
	for i := 1; i <= locals; i++ {
		fmt.Fprintf(&b, "local %s%d = '%s%d'\n", prefix, i, prefix, i)
	}
	fmt.Fprintf(&b, "redis.register_function('%s', function(keys, args) return %s%d .. NS.shared end)\n", prefix, prefix, locals)
	return b.String()
}

// Lua refuses a function of more than 200 locals, and a library's text is
// one function. Three files of 90 locals each load because each file is a
// block of its own; the same three with no blocks do not.
func TestEachFilesLocalsLeaveScopeAtItsEnd(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	texts := map[string]string{
		"a.lua": many("a", 90),
		"b.lua": many("b", 90),
		"c.lua": many("c", 90) + "redis.register_function('reach', function(keys, args) return a1 end)\n",
	}
	lib := Library{Name: "lib_one", Files: tree(texts), Glob: "*.lua", Prelude: "local NS = {shared = '!'}\n"}

	// The control: one text with no blocks is over Lua's limit.
	flat := "#!lua name=lib_flat\n" + lib.Prelude + texts["a.lua"] + texts["b.lua"] + texts["c.lua"]
	if err := c.FunctionLoadReplace(ctx, flat).Err(); err == nil || !strings.Contains(err.Error(), "more than 200 local variables") {
		require.Failf(t, "", "the files with no blocks: %v, want Lua's refusal of over 200 locals", err)
	}

	if _, err := lib.Ensure(ctx, c); err != nil {
		require.NoError(t, err, "the files each in its block: %v", err)
	}
	// The prelude's local is in scope in every file.
	if a, b, cc := call(t, c, "a"), call(t, c, "b"), call(t, c, "c"); a != "a90!" || b != "b90!" || cc != "c90!" {
		require.Failf(t, "", "a, b and c answer %q %q %q", a, b, cc)
	}
	// A file's local is not in scope in a later file.
	if err := c.FCall(ctx, "reach", nil).Err(); err == nil || !strings.Contains(err.Error(), "nonexistent global variable 'a1'") {
		require.Failf(t, "", "a function of c that reads a local of a: %v", err)
	}
}

// The loader's count of locals is Lua's: a text the count says holds 200 at
// once is taken by the store, and one it says holds 201 is refused with
// Lua's words. The texts mix every form the count knows, so a form counted
// wrong moves the edge.
func TestTheLocalsCountIsLuas(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	body := func(plain int) string {
		var b strings.Builder
		b.WriteString("local function helper() local inner = 1 return inner end\n") // 1
		b.WriteString("local a, b2, c3 = 1, 2, 3\n")                                // 3
		b.WriteString("if a then local x, y = 1, 2 else local z = 3 end\n")         // in scope only inside
		b.WriteString(locals("p", plain))
		b.WriteString("for i = 1, 2 do\n  for k, v in function() return nil end do local w, u = k, v end\nend\n") // 4 + 3 + 2 + 2
		b.WriteString(answering("edge", "edge"))
		return b.String()
	}
	for _, c2 := range []struct {
		plain int
		count int
		taken bool
	}{{200 - 4 - 11, 200, true}, {200 - 4 - 11 + 1, 201, false}} {
		text := body(c2.plain)
		read, bad := scan("edge.lua", text)
		if bad != nil || read.peak != c2.count {
			require.Failf(t, "", "the loader counts %d (%v), want %d", read.peak, bad, c2.count)
		}
		err := c.FunctionLoadReplace(ctx, "#!lua name=lib_edge\n"+text).Err()
		switch {
		case c2.taken && err != nil:
			require.Failf(t, "", "a text the loader counts %d: %v, want it taken", c2.count, err)
		case !c2.taken && (err == nil || !strings.Contains(err.Error(), "more than 200 local variables")):
			require.Failf(t, "", "a text the loader counts %d: %v, want Lua's refusal of over 200 locals", c2.count, err)
		}
	}
}

// A for's locals held against Lua where a do inside a function of the for's
// expressions once took them (Stella, #4486): the loader counts what Lua
// counts, the store takes 199 and refuses 204, and the library refuses both
// as over MaxLocals.
func TestAForsLocalsAreLuasWhenItsExpressionsHoldADo(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	for _, edge := range []struct {
		depth, count int
		taken        bool
	}{{5, 199, true}, {6, 204, false}} {
		text := forInFunction(174, edge.depth)
		if read, bad := scan("scope.lua", text); bad != nil || read.peak != edge.count {
			require.Failf(t, "", "depth %d: the loader counts %d (%v), want %d", edge.depth, read.peak, bad, edge.count)
		}
		lib := Library{Name: "scope_probe", Files: tree(map[string]string{"scope.lua": text}), Glob: "*.lua"}
		if _, err := lib.Digest(); !errors.Is(err, ErrRefused) {
			require.ErrorIs(t, err, ErrRefused, "depth %d: the library = %v, want the refusal over MaxLocals", edge.depth, err)
		}
		err := c.FunctionLoadReplace(ctx, "#!lua name=scope_probe\ndo\n"+text+"\nend\n").Err()
		switch {
		case edge.taken && err != nil:
			require.Failf(t, "", "depth %d, %d locals: %v, want the store to take it", edge.depth, edge.count, err)
		case !edge.taken && (err == nil || !strings.Contains(err.Error(), "more than 200 local variables")):
			require.Failf(t, "", "depth %d, %d locals: %v, want Lua's refusal of over 200 locals", edge.depth, edge.count, err)
		}
	}
}

// racing is a client that, once, runs between after a FUNCTION LIST has
// been answered and before its caller sees the answer: another loader acting
// between LoadMissing's read and its load, on the real store.
type racing struct {
	redis.UniversalClient
	between func() error
}

func (r *racing) FunctionList(ctx context.Context, q redis.FunctionListQuery) *redis.FunctionListCmd {
	cmd := r.UniversalClient.FunctionList(ctx, q)
	if cmd.Err() == nil && r.between != nil {
		between := r.between
		r.between = nil
		if err := between(); err != nil {
			cmd.SetErr(err)
		}
	}
	return cmd
}

// A deployer puts newer code on the store after LoadMissing read the name
// free (Stella, #4486): LoadMissing's FUNCTION LOAD is refused by the store,
// it answers Unchanged, and the newer code is what the store holds. A
// LoadMissing that sent REPLACE would answer LOADED over it.
func TestLoadMissingLeavesWhatADeployerPutsThereAfterItsRead(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	peer := client(t, &redis.Options{Addr: c.Options().Addr})
	deployed := "#!lua name=lib_one\n" + answering("fa", "deployed") + answering("newer", "newer")
	raced := &racing{UniversalClient: c, between: func() error { return peer.FunctionLoadReplace(ctx, deployed).Err() }}
	receipt, err := two().LoadMissing(ctx, raced)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Unchanged, Digest: twoDigest}) {
		require.Failf(t, "", "LoadMissing = %s %v, want UNCHANGED", receipt, err)
	}
	if raced.between != nil {
		require.Nil(t, raced.between, "the deployer never ran: the race was not made")
	}
	libs, err := peer.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: "lib_one", WithCode: true}).Result()
	if err != nil || len(libs) != 1 || libs[0].Code != deployed {
		require.Failf(t, "", "after LoadMissing the store holds %+v (%v), want the deployer's code", libs, err)
	}
	if fa, newer := call(t, c, "fa"), call(t, c, "newer"); fa != "deployed" || newer != "newer" {
		require.Failf(t, "", "fa answers %q and newer %q", fa, newer)
	}
}

// The names the loader reads from the files are the names the store
// registers, however a file spells them.
func TestTheNamesTheLoaderReadsAreTheNamesTheStoreRegisters(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := Library{Name: "lib_one", Files: tree(map[string]string{
		"a.lua": "redis.register_function('plain', function() return 1 end)\n" +
			"redis.register_function(\"double\", function() return 1 end)\n" +
			"redis.register_function('\\101scaped\\95\\049', function() return 1 end)\n" +
			"redis . register_function --[[ between ]] ( -- and\n [[long]] , function() return 1 end)\n" +
			"redis.register_function{function_name = 'table_form', callback = function(keys) return {1, {2}} end, flags = {'no-writes'}}\n" +
			"redis.register_function({callback = function() return 'end' end; function_name = [==[table_long]==]})\n" +
			"-- redis.register_function('in_a_comment', function() return 1 end)\n" +
			"local s = \"redis.register_function('in_a_string', f)\" .. [[ redis.register_function('in_a_long_string', f) ]]\n" +
			"local name = 'comp' .. 'uted'\nredis.register_function(name, function() return s end)\n",
	}), Glob: "*.lua"}
	b, err := lib.build()
	if err != nil {
		require.NoError(t, err, err)
	}
	if _, err := lib.Ensure(ctx, c); err != nil {
		require.NoError(t, err, err)
	}
	var read []string
	for _, name := range b.names {
		read = append(read, name)
	}
	slices.Sort(read)
	// computed is the one name the loader cannot read; the store has it.
	if got, want := strings.Join(read, ","), "double,escaped_1,long,plain,table_form,table_long"; got != want {
		require.Equal(t, want, got, "the loader reads %s, want %s", got, want)
	}
	if now := held(t, c); now != "lib_one(computed,double,escaped_1,long,plain,table_form,table_long)" {
		require.Equal(t, "lib_one(computed,double,escaped_1,long,plain,table_form,table_long)", now, "the store holds %s", now)
	}
}

// Requirement of a migration: while two libraries carry the same function,
// the one loaded second is refused, the error names the function and its
// holder, and the store holds what it held. When the holder lets go, the
// load is taken.
func TestAFunctionAnotherLibraryHoldsIsNamedWithItsHolder(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	old := Library{Name: "lib_old", Files: tree(map[string]string{
		"a.lua": answering("kept", "old") + answering("moved_one", "old") + answering("MOVED_TWO", "old"),
	}), Glob: "*.lua"}
	less := Library{Name: "lib_old", Files: tree(map[string]string{"a.lua": answering("kept", "old")}), Glob: "*.lua"}
	fresh := Library{Name: "lib_new", Files: tree(map[string]string{
		"a.lua": answering("fresh", "new") + answering("moved_one", "new") + answering("moved_two", "new"),
	}), Glob: "*.lua"}
	if _, err := old.Ensure(ctx, c); err != nil {
		require.NoError(t, err, err)
	}

	_, err := fresh.Ensure(ctx, c)
	var collision *CollisionError
	if !errors.As(err, &collision) {
		require.ErrorAs(t, err, &collision, "the load of lib_new = %v, want a *CollisionError", err)
	}
	if want := []Held{{"moved_one", "lib_old"}, {"moved_two", "lib_old"}}; collision.Library != "lib_new" || !slices.Equal(collision.Held, want) || collision.Unread != nil {
		require.Failf(t, "", "the collision is %+v, want lib_new and %+v", *collision, want)
	}
	if line := err.Error(); line != "redisfn: load lib_new: the store refused it and holds what it held before: "+
		"function moved_one is registered by library lib_old, function moved_two is registered by library lib_old; "+
		"remedy: a function name belongs to one library: load the version of the other library that no longer registers it, then load lib_new again" {
		require.Failf(t, "", "the error reads %q", line)
	}
	if now := held(t, c); now != "lib_old(MOVED_TWO,kept,moved_one)" {
		require.Equal(t, "lib_old(MOVED_TWO,kept,moved_one)", now, "after the refusal the store holds %s", now)
	}
	if one, two := call(t, c, "moved_one"), call(t, c, "moved_two"); one != "old" || two != "old" {
		require.Failf(t, "", "after the refusal moved_one answers %q and moved_two %q", one, two)
	}
	if state, err := old.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "after the refusal Check of lib_old = %v %v", state, err)
	}
	if state, err := fresh.Check(ctx, c); state != Absent || !isMismatch(err) {
		require.Failf(t, "", "after the refusal Check of lib_new = %v %v", state, err)
	}

	// LoadMissing is not the deployer: the store's refusal is its answer.
	if receipt, err := fresh.LoadMissing(ctx, c); err != nil || receipt.Outcome != Skipped || !named.MatchString(receipt.Why) {
		require.Failf(t, "", "LoadMissing of lib_new = %+v %v, want skipped with the store's words", receipt, err)
	}
	if now := held(t, c); now != "lib_old(MOVED_TWO,kept,moved_one)" {
		require.Equal(t, "lib_old(MOVED_TWO,kept,moved_one)", now, "after the skipped load the store holds %s", now)
	}

	// The migration: the old library lets the functions go, the new one takes them.
	if _, err := less.Ensure(ctx, c); err != nil {
		require.NoError(t, err, err)
	}
	if receipt, err := fresh.LoadMissing(ctx, c); err != nil || receipt.Outcome != Loaded {
		require.Failf(t, "", "after lib_old let go, LoadMissing of lib_new = %+v %v", receipt, err)
	}
	if now := held(t, c); now != "lib_new(fresh,moved_one,moved_two) lib_old(kept)" {
		require.Equal(t, "lib_new(fresh,moved_one,moved_two) lib_old(kept)", now, "the store holds %s", now)
	}
	if one, two := call(t, c, "moved_one"), call(t, c, "MOVED_TWO"); one != "new" || two != "new" {
		require.Failf(t, "", "moved_one answers %q and MOVED_TWO %q", one, two)
	}
	// And the other way round now: the old library, as it was, is the one refused.
	_, err = old.Ensure(ctx, c)
	if !errors.As(err, &collision) || !slices.Equal(collision.Held, []Held{{"MOVED_TWO", "lib_new"}, {"moved_one", "lib_new"}}) {
		require.Failf(t, "", "the load of lib_old as it was = %v, want a collision with lib_new", err)
	}
	if state, err := less.Check(ctx, c); state != Same || err != nil {
		require.Failf(t, "", "Check of lib_old = %v %v", state, err)
	}
}

// A name the loader cannot read from the files is named all the same: the
// store names it, and the error carries its holder.
func TestAComputedNameAnotherLibraryHoldsIsNamedWithItsHolder(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	put(t, c, "#!lua name=lib_old\n"+answering("moved", "old"))
	lib := Library{Name: "lib_new", Files: tree(map[string]string{
		"a.lua": "local name = 'mo' .. 'ved'\nredis.register_function(name, function() return 'new' end)\n",
	}), Glob: "*.lua"}
	_, err := lib.Ensure(ctx, c)
	var collision *CollisionError
	if !errors.As(err, &collision) || !slices.Equal(collision.Held, []Held{{"moved", "lib_old"}}) {
		require.Failf(t, "", "Load = %v, want a collision on moved with lib_old", err)
	}
}

// A seat that may load and may not list: Check and Ensure say why they
// cannot answer, and LoadMissing takes the store's refusal for an answer.
func TestASeatThatMayNotListIsToldSo(t *testing.T) {
	t.Parallel()
	admin := store(t)
	ctx := context.Background()
	if err := admin.Do(ctx, "ACL", "SETUSER", "seat", "reset", "on", ">pw", "~*", "+@all", "-function|list").Err(); err != nil {
		require.NoError(t, err, err)
	}
	seat := client(t, &redis.Options{Addr: admin.Options().Addr, Username: "seat", Password: "pw"})
	put(t, admin, "#!lua name=lib_old\n"+answering("fa", "old"))

	state, err := two().Check(ctx, seat)
	if state != Unknown || err == nil || !redis.HasErrorPrefix(err, "NOPERM") || isMismatch(err) {
		require.Failf(t, "", "Check = %v %v, want unknown and the store's NOPERM", state, err)
	}
	if line := err.Error(); !strings.HasPrefix(line, "redisfn: check lib_one: the store refused: NOPERM ") || !strings.HasSuffix(line, "; nothing was changed") {
		require.Failf(t, "", "the error reads %q", line)
	}
	if receipt, err := two().Ensure(ctx, seat); receipt.Outcome != Failed || !redis.HasErrorPrefix(err, "NOPERM") {
		require.Failf(t, "", "Ensure = %+v %v, want the store's NOPERM", receipt, err)
	}
	// A seat that may not list is not the deployer: LoadMissing loads
	// nothing and says why, with a nil error.
	if receipt, err := two().LoadMissing(ctx, seat); err != nil || receipt.Outcome != Skipped || !strings.HasPrefix(receipt.Why, "NOPERM ") {
		require.Failf(t, "", "LoadMissing = %+v %v, want skipped with the store's NOPERM", receipt, err)
	}

	if now := held(t, admin); now != "lib_old(fa)" {
		require.Equal(t, "lib_old(fa)", now, "the store holds %s", now)
	}

	// With nothing in its way and the read granted back the seat's load is taken.
	if err := admin.FunctionDelete(ctx, "lib_old").Err(); err != nil {
		require.NoError(t, err, err)
	}
	if err := admin.Do(ctx, "ACL", "SETUSER", "seat", "+function|list").Err(); err != nil {
		require.NoError(t, err, err)
	}
	if receipt, err := two().Ensure(ctx, seat); err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Loaded, Digest: twoDigest}) {
		require.Failf(t, "", "Ensure = %+v %v", receipt, err)
	}
}

// silent is a store that takes every connection and never answers.
func silent(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		require.NoError(t, err, err)
	}
	var mu sync.Mutex
	var taken []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			taken = append(taken, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range taken {
			_ = conn.Close()
		}
	})
	return listener.Addr().String()
}

// A store that cannot be reached: every call returns with an error. One that
// takes the connection and never answers would hold a client without
// timeouts for ever; the call returns when its bound passes.
func TestAStoreThatDoesNotAnswerIsLeftAtTheBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lib := two()
	lib.Bound = 50 * time.Millisecond

	never := client(t, &redis.Options{Addr: silent(t), ReadTimeout: -1, WriteTimeout: -1, MaxRetries: -1})
	receipt, err := lib.Ensure(ctx, never)
	if receipt != (Receipt{Library: "lib_one", Digest: twoDigest}) || !errors.Is(err, context.DeadlineExceeded) ||
		err.Error() != "redisfn: load lib_one: no answer from the store before the wait ended: context deadline exceeded; the store holds the whole library it held before or the whole of this one, and Check says which" {
		require.Failf(t, "", "Ensure = %+v %v", receipt, err)
	}
	state, err := lib.Check(ctx, never)
	if state != Unknown || !errors.Is(err, context.DeadlineExceeded) || isMismatch(err) ||
		err.Error() != "redisfn: check lib_one: no answer from the store before the wait ended: context deadline exceeded; nothing was changed" {
		require.Failf(t, "", "Check = %v %v", state, err)
	}
	for name, call := range map[string]func(Library, context.Context, redis.UniversalClient) (Receipt, error){"Ensure": Library.Ensure, "LoadMissing": Library.LoadMissing} {
		receipt, err := call(lib, ctx, never)
		if receipt != (Receipt{Library: "lib_one", Digest: twoDigest}) || !errors.Is(err, context.DeadlineExceeded) {
			require.Failf(t, "", "%s = %+v %v", name, receipt, err)
		}
	}

	// No store at the address at all: the error is the dial's, well inside
	// the bound. Port 0 is no port a server can listen on, so no other
	// test's server is ever there.
	lib.Bound = 0
	nobody := client(t, &redis.Options{Addr: "127.0.0.1:0", MaxRetries: -1, DialerRetries: 1})
	// No store at the address: the read of Ensure fails first, so its error
	// is the read's.
	if receipt, err := lib.Ensure(ctx, nobody); receipt.Outcome != Failed || err == nil || !strings.HasPrefix(err.Error(), "redisfn: check lib_one: the store did not answer: dial tcp ") {
		require.Failf(t, "", "Ensure with no store = %+v %v", receipt, err)
	}
	state, err = lib.Check(ctx, nobody)
	if state != Unknown || err == nil || isMismatch(err) || !strings.HasPrefix(err.Error(), "redisfn: check lib_one: the store did not answer: dial tcp ") {
		require.Failf(t, "", "Check with no store = %v %v", state, err)
	}
	if receipt, err := lib.LoadMissing(ctx, nobody); receipt.Outcome != Failed || err == nil {
		require.Failf(t, "", "LoadMissing with no store = %+v %v", receipt, err)
	}
}
