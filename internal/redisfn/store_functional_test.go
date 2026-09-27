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

	// until internal/testredis lands; then this import moves
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// What the package says of a store, held against a redis-server: every test
// starts a throwaway one of its own, so no library of one test is on the
// store of another.

// store is a client of a redis-server only this test uses.
func store(t *testing.T) *redis.Client {
	t.Helper()
	return client(t, &redis.Options{Addr: testutil.Start(t)})
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
		t.Fatalf("FCALL %s: %v", function, err)
	}
	return answer
}

// put loads code with no loader: what another writer does to the store.
func put(t *testing.T, c *redis.Client, code string) {
	t.Helper()
	if err := c.FunctionLoadReplace(context.Background(), code).Err(); err != nil {
		t.Fatalf("FUNCTION LOAD REPLACE: %v\n%s", err, code)
	}
}

// held is what the store holds: every library's name with its functions'
// names, sorted.
func held(t *testing.T, c *redis.Client) string {
	t.Helper()
	libs, err := c.FunctionList(context.Background(), redis.FunctionListQuery{}).Result()
	if err != nil {
		t.Fatal(err)
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

func TestLoadPutsTheSourceOnTheStoreAndItsFunctionsAnswer(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	digest, err := two().Load(ctx, c)
	if err != nil || digest != twoDigest {
		t.Fatalf("Load = %q %v, want %q", digest, err, twoDigest)
	}
	if fa, fb := call(t, c, "fa"), call(t, c, "fb"); fa != "fa" || fb != "fb" {
		t.Fatalf("fa answers %q and fb %q", fa, fb)
	}
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{WithCode: true}).Result()
	if err != nil || len(libs) != 1 || libs[0].Name != "lib_one" || libs[0].Code != twoSource {
		t.Fatalf("the store holds %+v (%v), want lib_one with the source byte for byte", libs, err)
	}
}

func TestLoadTwiceLeavesWhatLoadOnceLeft(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := two()
	first, err := lib.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	after := held(t, c)
	second, err := lib.Load(ctx, c)
	if err != nil || second != first {
		t.Fatalf("the second Load = %q %v, the first gave %q", second, err, first)
	}
	if now := held(t, c); now != after || now != "lib_one(fa,fb)" {
		t.Fatalf("after two loads the store holds %s, after one %s", now, after)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		t.Fatalf("Check = %v %v", state, err)
	}
	if fa := call(t, c, "fa"); fa != "fa" {
		t.Fatalf("fa answers %q", fa)
	}
}

func TestLoadOfChangedSourceChangesWhatTheStoreDoesAndTheDigest(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	one := Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": answering("fa", "one"), "b.lua": answering("fb", "b")}), Glob: "*.lua"}
	two := Library{Name: "lib_one", Files: tree(map[string]string{"a.lua": answering("fa", "two"), "c.lua": answering("fc", "c")}), Glob: "*.lua"}
	first, err := one.Load(ctx, c)
	if err != nil || call(t, c, "fa") != "one" {
		t.Fatalf("Load of the first = %q %v", first, err)
	}
	second, err := two.Load(ctx, c)
	if err != nil || second == first {
		t.Fatalf("Load of the second = %q %v, the first was %q", second, err, first)
	}
	if fa, fc := call(t, c, "fa"), call(t, c, "fc"); fa != "two" || fc != "c" {
		t.Fatalf("after the second, fa answers %q and fc %q", fa, fc)
	}
	// The whole library was replaced: what only the first registered is gone.
	if err := c.FCall(ctx, "fb", nil).Err(); err == nil || !strings.Contains(err.Error(), "Function not found") {
		t.Fatalf("fb, which the second does not register, answers: %v", err)
	}
	if now := held(t, c); now != "lib_one(fa,fc)" {
		t.Fatalf("the store holds %s", now)
	}
	state, err := one.Check(ctx, c)
	var mismatch *MismatchError
	if state != Different || !errors.As(err, &mismatch) || mismatch.Loaded != second || mismatch.Want != first {
		t.Fatalf("Check of the first = %v %v, want different with loaded=%s want=%s", state, err, second, first)
	}
	if state, err := two.Check(ctx, c); state != Same || err != nil {
		t.Fatalf("Check of the second = %v %v", state, err)
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
		t.Fatalf("on an empty store Check = %v %v", state, err)
	}
	if line := err.Error(); line != "redisfn: library lib_one is absent from the store: loaded=none want="+twoDigest+"; remedy: load this binary's library (Load)" {
		t.Fatalf("the error reads %q", line)
	}
	if now := held(t, c); now != "" {
		t.Fatalf("after Check the store holds %s", now)
	}

	if _, err := lib.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		t.Fatalf("after Load, Check = %v %v", state, err)
	}

	other := strings.Replace(twoSource, "local b = 1", "local b = 2", 1)
	put(t, c, other)
	state, err = lib.Check(ctx, c)
	if state != Different || !errors.As(err, &mismatch) || *mismatch != (MismatchError{Library: "lib_one", Want: twoDigest, Loaded: DigestOf(other), Remedy: mismatch.Remedy}) {
		t.Fatalf("over other code Check = %v %v", state, err)
	}
	if line := err.Error(); line != "redisfn: library lib_one on the store is not the one this binary was built with: loaded="+DigestOf(other)+" want="+twoDigest+
		"; remedy: load this binary's library over it (Load), or run the binary the store's library came from" {
		t.Fatalf("the error reads %q", line)
	}
	if libs, err := c.FunctionList(ctx, lib.Query()).Result(); err != nil || len(libs) != 1 || libs[0].Code != other {
		t.Fatalf("after Check the store holds %+v (%v), want the other code as it was", libs, err)
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
		t.Fatalf("FUNCTION LIST LIBRARYNAME lib_one returns %+v (%v), want LIB_ONE and Lib_One", libs, err)
	}
	if state, err := lib.Check(ctx, c); state != Absent || !isMismatch(err) {
		t.Fatalf("Check = %v %v, want absent: no library is named lib_one", state, err)
	}
	receipt, err := lib.LoadMissing(ctx, c)
	if err != nil || receipt.Outcome != Loaded || receipt.Was != "" {
		t.Fatalf("LoadMissing = %+v %v, want loaded", receipt, err)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		t.Fatalf("after the load Check = %v %v", state, err)
	}
	if now := held(t, c); now != "LIB_ONE(upper) Lib_One(mixed) lib_one(fa,fb)" {
		t.Fatalf("the store holds %s", now)
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
	if _, err := lib.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	if keys, err := c.Keys(ctx, "*").Result(); err != nil || len(keys) != 0 {
		t.Fatalf("after Load the store has the keys %q (%v), want none", keys, err)
	}
	for _, key := range []string{"lib_one", "lib_one:sha", "redisfn:lib_one", twoDigest} {
		if err := c.Set(ctx, key, "0000000000000000", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		t.Fatalf("with keys another writer set, Check = %v %v", state, err)
	}
	if err := c.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		t.Fatalf("after FLUSHALL, Check = %v %v", state, err)
	}
	if err := c.FunctionDelete(ctx, "lib_one").Err(); err != nil {
		t.Fatal(err)
	}
	if state, err := lib.Check(ctx, c); state != Absent || !isMismatch(err) {
		t.Fatalf("after FUNCTION DELETE, Check = %v %v", state, err)
	}
}

func TestLoadMissingLoadsWhenAbsentOrDifferentAndSaysWhich(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	lib := two()

	receipt, err := lib.LoadMissing(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Loaded, Digest: twoDigest}) {
		t.Fatalf("on an empty store LoadMissing = %+v %v", receipt, err)
	}
	if line := receipt.String(); line != "LOADED lib_one sha="+twoDigest {
		t.Fatalf("the receipt reads %q", line)
	}
	if fa := call(t, c, "fa"); fa != "fa" {
		t.Fatalf("fa answers %q", fa)
	}

	receipt, err = lib.LoadMissing(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Unchanged, Digest: twoDigest}) {
		t.Fatalf("over itself LoadMissing = %+v %v", receipt, err)
	}

	other := "#!lua name=lib_one\n" + answering("fa", "other")
	put(t, c, other)
	receipt, err = lib.LoadMissing(ctx, c)
	if err != nil || receipt != (Receipt{Library: "lib_one", Outcome: Replaced, Digest: twoDigest, Was: DigestOf(other)}) {
		t.Fatalf("over other code LoadMissing = %+v %v", receipt, err)
	}
	if line := receipt.String(); line != "REPLACED lib_one sha="+twoDigest+" was="+DigestOf(other) {
		t.Fatalf("the receipt reads %q", line)
	}
	if fa := call(t, c, "fa"); fa != "fa" {
		t.Fatalf("after the replace fa answers %q", fa)
	}
	if state, err := lib.Check(ctx, c); state != Same || err != nil {
		t.Fatalf("Check = %v %v", state, err)
	}
}

// Whatever the store refuses a library for, it holds afterwards the whole
// library it held before: the functions answer as they did and Check says so.
func TestALoadTheStoreRefusesLeavesTheLibraryItHeld(t *testing.T) {
	t.Parallel()
	c := store(t)
	ctx := context.Background()
	good := Library{Name: "lib_one", Files: tree(map[string]string{"lua/a.lua": answering("fa", "good"), "lua/b.lua": answering("fb", "good")}), Glob: "lua/*.lua"}
	if _, err := good.Load(ctx, c); err != nil {
		t.Fatal(err)
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
		digest, err := lib.Load(ctx, c)
		if err == nil || digest != "" {
			t.Fatalf("%s: Load = %q %v, want the store's refusal", bad.name, digest, err)
		}
		line := err.Error()
		if strings.ContainsAny(line, "\n\r") {
			t.Errorf("%s: the error is more than one line: %q", bad.name, line)
		}
		for _, want := range bad.want {
			if !strings.Contains(line, want) {
				t.Errorf("%s: the error does not say %q: %s", bad.name, want, line)
			}
		}
		var reply redis.Error
		if !errors.As(err, &reply) {
			t.Errorf("%s: the error does not wrap the store's reply: %v", bad.name, err)
		}
		if fa, fb := call(t, c, "fa"), call(t, c, "fb"); fa != "good" || fb != "good" {
			t.Errorf("%s: after the refusal fa answers %q and fb %q", bad.name, fa, fb)
		}
		if state, err := good.Check(ctx, c); state != Same || err != nil {
			t.Errorf("%s: after the refusal Check of the library held = %v %v", bad.name, state, err)
		}
		if receipt, err := lib.LoadMissing(ctx, c); err == nil || receipt.Outcome != Failed || receipt.Was == "" {
			t.Errorf("%s: LoadMissing = %+v %v, want the store's refusal", bad.name, receipt, err)
		}
		if now := held(t, c); now != "lib_one(fa,fb)" {
			t.Errorf("%s: after the refusal the store holds %s", bad.name, now)
		}
	}

	// A library that registers nothing is refused as well, and an empty store
	// stays empty.
	empty := store(t)
	none := Library{Name: "lib_none", Files: tree(map[string]string{"a.lua": "local x = 1\n"}), Glob: "*.lua"}
	if _, err := none.Load(ctx, empty); err == nil || !strings.Contains(err.Error(), "the store refused: ERR No functions registered") {
		t.Errorf("a library that registers nothing: %v", err)
	}
	if now := held(t, empty); now != "" {
		t.Errorf("after the refusal the empty store holds %s", now)
	}
}

// The line the store names in an error of a running function is a line of
// the source; Explain and Locate say which file's.
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
	if _, err := lib.Load(ctx, c); err != nil {
		t.Fatal(err)
	}

	err := c.FCall(ctx, "fb", nil).Err()
	if err == nil || !strings.Contains(err.Error(), "attempt to index local 't'") {
		t.Fatalf("fb: %v", err)
	}
	explained := lib.Explain(err)
	if !strings.HasSuffix(explained.Error(), " = lua/b.lua:5]") || !errors.Is(explained, err) {
		t.Fatalf("Explain: %v", explained)
	}
	var n int
	if _, serr := fmt.Sscanf(err.Error()[strings.Index(err.Error(), "user_function:"):], "user_function:%d", &n); serr != nil {
		t.Fatal(serr)
	}
	if origin, lerr := lib.Locate(n); lerr != nil || origin != (Origin{File: "lua/b.lua", Line: 5}) {
		t.Fatalf("Locate(%d) = %+v %v, want line 5 of lua/b.lua", n, origin, lerr)
	}
	// The line is the source's own: the text there is the file's line.
	source, _ := lib.Source()
	if line := strings.Split(source, "\n")[n-1]; line != "  return t.field" {
		t.Fatalf("line %d of the source is %q", n, line)
	}

	// An error raised in the prelude, by a function of c.
	err = c.FCall(ctx, "fc", nil).Err()
	if err == nil || !strings.Contains(err.Error(), "failed from c") {
		t.Fatalf("fc: %v", err)
	}
	if explained := lib.Explain(err).Error(); !strings.Contains(explained, " = prelude:3") {
		t.Fatalf("Explain: %s", explained)
	}

	// An error of a command the function ran names the line of the call.
	if err := c.Set(ctx, "k", "not a number", 0).Err(); err != nil {
		t.Fatal(err)
	}
	err = c.FCall(ctx, "fd", []string{"k"}).Err()
	if err == nil || !strings.Contains(err.Error(), "not an integer") {
		t.Fatalf("fd: %v", err)
	}
	if explained := lib.Explain(err).Error(); !strings.HasSuffix(explained, " = lua/c.lua:5]") {
		t.Fatalf("Explain: %s", explained)
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
		t.Fatalf("the files with no blocks: %v, want Lua's refusal of over 200 locals", err)
	}

	if _, err := lib.Load(ctx, c); err != nil {
		t.Fatalf("the files each in its block: %v", err)
	}
	// The prelude's local is in scope in every file.
	if a, b, cc := call(t, c, "a"), call(t, c, "b"), call(t, c, "c"); a != "a90!" || b != "b90!" || cc != "c90!" {
		t.Fatalf("a, b and c answer %q %q %q", a, b, cc)
	}
	// A file's local is not in scope in a later file.
	if err := c.FCall(ctx, "reach", nil).Err(); err == nil || !strings.Contains(err.Error(), "nonexistent global variable 'a1'") {
		t.Fatalf("a function of c that reads a local of a: %v", err)
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
	if _, err := lib.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	read, err := lib.Functions()
	if err != nil {
		t.Fatal(err)
	}
	// computed is the one name the loader cannot read; the store has it.
	if got, want := strings.Join(read, ","), "double,escaped_1,long,plain,table_form,table_long"; got != want {
		t.Fatalf("the loader reads %s, want %s", got, want)
	}
	if now := held(t, c); now != "lib_one(computed,double,escaped_1,long,plain,table_form,table_long)" {
		t.Fatalf("the store holds %s", now)
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
	if _, err := old.Load(ctx, c); err != nil {
		t.Fatal(err)
	}

	for _, load := range []func() error{
		func() error { _, err := fresh.Load(ctx, c); return err },
		func() error { _, err := fresh.LoadMissing(ctx, c); return err },
	} {
		err := load()
		var collision *CollisionError
		if !errors.As(err, &collision) {
			t.Fatalf("the load of lib_new = %v, want a *CollisionError", err)
		}
		if want := []Held{{"moved_one", "lib_old"}, {"moved_two", "lib_old"}}; collision.Library != "lib_new" || !slices.Equal(collision.Held, want) || collision.Unread != nil {
			t.Fatalf("the collision is %+v, want lib_new and %+v", *collision, want)
		}
		if line := err.Error(); line != "redisfn: load lib_new: the store refused it and holds what it held before: "+
			"function moved_one is registered by library lib_old, function moved_two is registered by library lib_old; "+
			"remedy: a function name belongs to one library: load the version of the other library that no longer registers it, then load lib_new again" {
			t.Fatalf("the error reads %q", line)
		}
		if now := held(t, c); now != "lib_old(MOVED_TWO,kept,moved_one)" {
			t.Fatalf("after the refusal the store holds %s", now)
		}
		if one, two := call(t, c, "moved_one"), call(t, c, "moved_two"); one != "old" || two != "old" {
			t.Fatalf("after the refusal moved_one answers %q and moved_two %q", one, two)
		}
		if state, err := old.Check(ctx, c); state != Same || err != nil {
			t.Fatalf("after the refusal Check of lib_old = %v %v", state, err)
		}
		if state, err := fresh.Check(ctx, c); state != Absent || !isMismatch(err) {
			t.Fatalf("after the refusal Check of lib_new = %v %v", state, err)
		}
	}

	// The migration: the old library lets the functions go, the new one takes them.
	if _, err := less.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	if receipt, err := fresh.LoadMissing(ctx, c); err != nil || receipt.Outcome != Loaded {
		t.Fatalf("after lib_old let go, LoadMissing of lib_new = %+v %v", receipt, err)
	}
	if now := held(t, c); now != "lib_new(fresh,moved_one,moved_two) lib_old(kept)" {
		t.Fatalf("the store holds %s", now)
	}
	if one, two := call(t, c, "moved_one"), call(t, c, "MOVED_TWO"); one != "new" || two != "new" {
		t.Fatalf("moved_one answers %q and MOVED_TWO %q", one, two)
	}
	// And the other way round now: the old library, as it was, is the one refused.
	_, err := old.Load(ctx, c)
	var collision *CollisionError
	if !errors.As(err, &collision) || !slices.Equal(collision.Held, []Held{{"MOVED_TWO", "lib_new"}, {"moved_one", "lib_new"}}) {
		t.Fatalf("the load of lib_old as it was = %v, want a collision with lib_new", err)
	}
	if state, err := less.Check(ctx, c); state != Same || err != nil {
		t.Fatalf("Check of lib_old = %v %v", state, err)
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
	_, err := lib.Load(ctx, c)
	var collision *CollisionError
	if !errors.As(err, &collision) || !slices.Equal(collision.Held, []Held{{"moved", "lib_old"}}) {
		t.Fatalf("Load = %v, want a collision on moved with lib_old", err)
	}
}

// A seat that may load and may not list: Check says why it cannot answer,
// and a collision names the function and says why it names no holder.
func TestASeatThatMayNotListIsToldSo(t *testing.T) {
	t.Parallel()
	admin := store(t)
	ctx := context.Background()
	if err := admin.Do(ctx, "ACL", "SETUSER", "seat", "reset", "on", ">pw", "~*", "+@all", "-function|list").Err(); err != nil {
		t.Fatal(err)
	}
	seat := client(t, &redis.Options{Addr: admin.Options().Addr, Username: "seat", Password: "pw"})
	put(t, admin, "#!lua name=lib_old\n"+answering("fa", "old"))

	state, err := two().Check(ctx, seat)
	if state != Unknown || err == nil || !redis.HasErrorPrefix(err, "NOPERM") || isMismatch(err) {
		t.Fatalf("Check = %v %v, want unknown and the store's NOPERM", state, err)
	}
	if line := err.Error(); !strings.HasPrefix(line, "redisfn: check lib_one: the store refused: NOPERM ") || !strings.HasSuffix(line, "; nothing was changed") {
		t.Fatalf("the error reads %q", line)
	}
	if receipt, err := two().LoadMissing(ctx, seat); receipt.Outcome != Failed || !redis.HasErrorPrefix(err, "NOPERM") {
		t.Fatalf("LoadMissing = %+v %v, want the store's NOPERM", receipt, err)
	}

	_, err = two().Load(ctx, seat)
	var collision *CollisionError
	if !errors.As(err, &collision) || !slices.Equal(collision.Held, []Held{{Function: "fa"}}) || !redis.HasErrorPrefix(collision.Unread, "NOPERM") {
		t.Fatalf("Load = %v, want a collision on fa whose holder could not be read", err)
	}
	if now := held(t, admin); now != "lib_old(fa)" {
		t.Fatalf("the store holds %s", now)
	}

	// With nothing in its way the seat's load is taken.
	if err := admin.FunctionDelete(ctx, "lib_old").Err(); err != nil {
		t.Fatal(err)
	}
	if digest, err := two().Load(ctx, seat); err != nil || digest != twoDigest {
		t.Fatalf("Load = %q %v", digest, err)
	}
}

// silent is a store that takes every connection and never answers.
func silent(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
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
	digest, err := lib.Load(ctx, never)
	if digest != "" || !errors.Is(err, context.DeadlineExceeded) ||
		err.Error() != "redisfn: load lib_one: no answer from the store before the wait ended: context deadline exceeded; the store holds the whole library it held before or the whole of this one, and Check says which" {
		t.Fatalf("Load = %q %v", digest, err)
	}
	state, err := lib.Check(ctx, never)
	if state != Unknown || !errors.Is(err, context.DeadlineExceeded) || isMismatch(err) ||
		err.Error() != "redisfn: check lib_one: no answer from the store before the wait ended: context deadline exceeded; nothing was changed" {
		t.Fatalf("Check = %v %v", state, err)
	}
	receipt, err := lib.LoadMissing(ctx, never)
	if receipt != (Receipt{Library: "lib_one", Digest: twoDigest}) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("LoadMissing = %+v %v", receipt, err)
	}

	// No store at the address at all: the error is the dial's, well inside
	// the bound. Port 0 is no port a server can listen on, so no other
	// test's server is ever there.
	lib.Bound = 0
	nobody := client(t, &redis.Options{Addr: "127.0.0.1:0", MaxRetries: -1, DialerRetries: 1})
	digest, err = lib.Load(ctx, nobody)
	if digest != "" || err == nil || !strings.HasPrefix(err.Error(), "redisfn: load lib_one: the store did not answer: dial tcp ") {
		t.Fatalf("Load with no store = %q %v", digest, err)
	}
	state, err = lib.Check(ctx, nobody)
	if state != Unknown || err == nil || isMismatch(err) || !strings.HasPrefix(err.Error(), "redisfn: check lib_one: the store did not answer: dial tcp ") {
		t.Fatalf("Check with no store = %v %v", state, err)
	}
	if receipt, err := lib.LoadMissing(ctx, nobody); receipt.Outcome != Failed || err == nil {
		t.Fatalf("LoadMissing with no store = %+v %v", receipt, err)
	}
}
