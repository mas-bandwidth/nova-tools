package redisfn

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// fake is a store that answers what a test tells it to and keeps what it was
// sent. The functional tests hold the same calls against a real redis-server;
// the replies and the error texts here are that server's (Redis 8.10.2).
type fake struct {
	redis.UniversalClient // nil: a call the package is not meant to make panics

	load func(ctx context.Context, code string) (string, error)
	list func(ctx context.Context, q redis.FunctionListQuery) ([]redis.Library, error)

	mu   sync.Mutex
	sent []string
}

func (f *fake) note(command string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, command)
}

func (f *fake) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

func (f *fake) FunctionLoadReplace(ctx context.Context, code string) *redis.StringCmd {
	f.note("FUNCTION LOAD REPLACE " + code)
	cmd := redis.NewStringCmd(ctx, "function", "load", "replace", code)
	name, err := f.load(ctx, code)
	if err != nil {
		cmd.SetErr(err)
		return cmd
	}
	cmd.SetVal(name)
	return cmd
}

func (f *fake) FunctionList(ctx context.Context, q redis.FunctionListQuery) *redis.FunctionListCmd {
	command := "FUNCTION LIST"
	if q.LibraryNamePattern != "" {
		command += " LIBRARYNAME " + q.LibraryNamePattern
	}
	if q.WithCode {
		command += " WITHCODE"
	}
	f.note(command)
	cmd := redis.NewFunctionListCmd(ctx, "function", "list")
	libs, err := f.list(ctx, q)
	if err != nil {
		cmd.SetErr(err)
		return cmd
	}
	cmd.SetVal(libs)
	return cmd
}

// refusal is an error the store itself replied with.
type refusal string

func (e refusal) Error() string { return string(e) }

func (refusal) RedisError() {}

// holding is a fake whose FUNCTION LIST answers these libraries, and which
// takes any load.
func holding(libs ...redis.Library) *fake {
	return &fake{
		load: func(_ context.Context, code string) (string, error) {
			name, _, _ := strings.Cut(strings.TrimPrefix(code, "#!lua name="), "\n")
			return name, nil
		},
		list: func(context.Context, redis.FunctionListQuery) ([]redis.Library, error) { return libs, nil },
	}
}

func oneLine(t *testing.T, what string, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error", what)
	}
	line := err.Error()
	if strings.ContainsAny(line, "\n\r") {
		t.Errorf("%s: the error is more than one line: %q", what, line)
	}
	return line
}

func TestQueryAsksForTheLibrarysCodeByItsName(t *testing.T) {
	t.Parallel()
	if got, want := two().Query(), (redis.FunctionListQuery{LibraryNamePattern: "lib_one", WithCode: true}); got != want {
		t.Fatalf("Query = %+v, want %+v", got, want)
	}
}

func TestJudgeSaysSameOnlyForThisLibrarysNameAndCode(t *testing.T) {
	t.Parallel()
	lib := two()
	other := strings.Replace(twoSource, "local b = 1", "local b = 2", 1)
	for _, c := range []struct {
		name   string
		reply  []redis.Library
		want   State
		loaded string
	}{
		{"nothing on the store", nil, Absent, ""},
		{"this library", []redis.Library{{Name: "lib_one", Code: twoSource}}, Same, ""},
		{"this library among others", []redis.Library{{Name: "lib_on", Code: other}, {Name: "lib_one", Code: twoSource}, {Name: "lib_one_more", Code: other}}, Same, ""},
		{"other code under the name", []redis.Library{{Name: "lib_one", Code: other}}, Different, DigestOf(other)},
		{"the code with one byte more", []redis.Library{{Name: "lib_one", Code: twoSource + "\n"}}, Different, DigestOf(twoSource + "\n")},
		{"no code under the name", []redis.Library{{Name: "lib_one"}}, Different, DigestOf("")},
		// The store matches the name as a pattern and without case: what it
		// returns for lib_one may be another library.
		{"this code under the name in upper case", []redis.Library{{Name: "LIB_ONE", Code: twoSource}}, Absent, ""},
		{"this code under a longer name", []redis.Library{{Name: "lib_one_more", Code: twoSource}}, Absent, ""},
		{"another library first, in another case", []redis.Library{{Name: "Lib_One", Code: other}, {Name: "lib_one", Code: twoSource}}, Same, ""},
	} {
		state, err := lib.Judge(c.reply)
		if state != c.want {
			t.Errorf("%s: %v, want %v", c.name, state, c.want)
		}
		if c.want == Same {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		line := oneLine(t, c.name, err)
		var mismatch *MismatchError
		if !errors.As(err, &mismatch) {
			t.Errorf("%s: the error is no *MismatchError: %v", c.name, err)
			continue
		}
		if *mismatch != (MismatchError{Library: "lib_one", Want: twoDigest, Loaded: c.loaded, Remedy: mismatch.Remedy}) {
			t.Errorf("%s: %+v, want lib_one, want=%s loaded=%q", c.name, *mismatch, twoDigest, c.loaded)
		}
		loaded := c.loaded
		if c.want == Absent {
			loaded = "none"
		}
		for _, want := range []string{"library lib_one ", "loaded=" + loaded + " ", "want=" + twoDigest + ";", "remedy: load this binary's library"} {
			if !strings.Contains(line, want) {
				t.Errorf("%s: the error does not say %q: %s", c.name, want, line)
			}
		}
	}
}

func TestTheMismatchNamesTheRemedyTheLibraryCarries(t *testing.T) {
	t.Parallel()
	lib := two()
	lib.Remedy = "mytool fn load\n--redis <addr>"
	for _, reply := range [][]redis.Library{nil, {{Name: "lib_one", Code: "other"}}} {
		_, err := lib.Judge(reply)
		if line := oneLine(t, "Judge", err); !strings.HasSuffix(line, `; remedy: mytool fn load\x0a--redis <addr>`) {
			t.Errorf("the error does not end with the library's remedy on its one line: %s", line)
		}
	}
}

// The errors are one line whatever their fields hold: a caller may make one.
func TestTheErrorsAreOneLineWhateverTheyHold(t *testing.T) {
	t.Parallel()
	for want, err := range map[string]error{
		`redisfn: library a\x0ab is absent from the store: loaded=none want=c\x20d; remedy: e\x0af`:                                 &MismatchError{Library: "a\nb", Want: "c d", Remedy: "e\nf"},
		`redisfn: library a\x0ab on the store is not the one this binary was built with: loaded=g\x3dh want=c\x20d; remedy: e\x0af`: &MismatchError{Library: "a\nb", Want: "c d", Loaded: "g=h", Remedy: "e\nf"},
		`redisfn: load a\x0ab: the store refused it and holds what it held before: function c\x20d is registered by library e\x0af; ` +
			`remedy: a function name belongs to one library: load the version of the other library that no longer registers it, then load a\x0ab again`: &CollisionError{Library: "a\nb", Held: []Held{{"c d", "e\nf"}}},
	} {
		if got := oneLine(t, want, err); got != want {
			t.Errorf("the error reads\n%s\nwant\n%s", got, want)
		}
	}
}

func TestStatesAndOutcomesHaveTheirWords(t *testing.T) {
	t.Parallel()
	for state, want := range map[State]string{Unknown: "unknown", Same: "same", Different: "different", Absent: "absent", State(9): "unknown"} {
		if got := state.String(); got != want {
			t.Errorf("State %d reads %q, want %q", state, got, want)
		}
	}
	for outcome, want := range map[Outcome]string{Failed: "FAILED", Unchanged: "UNCHANGED", Loaded: "LOADED", Replaced: "REPLACED", Outcome(9): "FAILED"} {
		if got := outcome.String(); got != want {
			t.Errorf("Outcome %d reads %q, want %q", outcome, got, want)
		}
	}
	var state State
	var outcome Outcome
	if state != Unknown || outcome != Failed {
		t.Errorf("the zero State is %v and the zero Outcome %v: a value nobody set must claim nothing", state, outcome)
	}
}

func TestAReceiptIsOneLineInTheLoadersShape(t *testing.T) {
	t.Parallel()
	for want, receipt := range map[string]Receipt{
		"LOADED my_library sha=0123456789abcdef":                        {Library: "my_library", Outcome: Loaded, Digest: "0123456789abcdef"},
		"UNCHANGED my_library sha=0123456789abcdef":                     {Library: "my_library", Outcome: Unchanged, Digest: "0123456789abcdef"},
		"REPLACED my_library sha=0123456789abcdef was=fedcba9876543210": {Library: "my_library", Outcome: Replaced, Digest: "0123456789abcdef", Was: "fedcba9876543210"},
		"FAILED my_library sha=0123456789abcdef":                        {Library: "my_library", Digest: "0123456789abcdef", Was: "fedcba9876543210"},
		`FAILED not\x20a\x0aname sha=`:                                  {Library: "not a\nname"},
	} {
		if got := receipt.String(); got != want {
			t.Errorf("%+v reads %q, want %q", receipt, got, want)
		}
	}
}

func TestCheckAsksOnceAndChangesNothing(t *testing.T) {
	t.Parallel()
	lib := two()
	for _, c := range []struct {
		name  string
		reply []redis.Library
		want  State
	}{
		{"absent", nil, Absent},
		{"same", []redis.Library{{Name: "lib_one", Code: twoSource}}, Same},
		{"different", []redis.Library{{Name: "lib_one", Code: "other"}}, Different},
	} {
		store := holding(c.reply...)
		state, err := lib.Check(context.Background(), store)
		if state != c.want || (err == nil) != (c.want == Same) {
			t.Errorf("%s: Check = %v %v", c.name, state, err)
		}
		judged, jerr := lib.Judge(c.reply)
		if judged != state || (err != nil && err.Error() != jerr.Error()) {
			t.Errorf("%s: Check says %v %v and Judge %v %v", c.name, state, err, judged, jerr)
		}
		if sent := store.commands(); !slices.Equal(sent, []string{"FUNCTION LIST LIBRARYNAME lib_one WITHCODE"}) {
			t.Errorf("%s: Check sent %q, want one FUNCTION LIST", c.name, sent)
		}
	}
}

func TestCheckSaysUnknownWhenTheStoreCannotBeRead(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		cause error
		want  []string
	}{
		{"the store refuses", refusal("NOPERM User seat has no permissions to run the 'function|list' command"),
			[]string{"redisfn: check lib_one: the store refused: NOPERM User seat", "; nothing was changed"}},
		{"the store is not there", errors.New("dial tcp 127.0.0.1:1: connect: connection refused"),
			[]string{"redisfn: check lib_one: the store did not answer: dial tcp", "; nothing was changed"}},
		{"an error of two lines", errors.New("first\nsecond"),
			[]string{`the store did not answer: first\x0asecond;`}},
	} {
		store := holding()
		store.list = func(context.Context, redis.FunctionListQuery) ([]redis.Library, error) { return nil, c.cause }
		state, err := two().Check(context.Background(), store)
		if state != Unknown {
			t.Errorf("%s: Check = %v, want unknown", c.name, state)
		}
		line := oneLine(t, c.name, err)
		for _, want := range c.want {
			if !strings.Contains(line, want) {
				t.Errorf("%s: the error does not say %q: %s", c.name, want, line)
			}
		}
		if !errors.Is(err, c.cause) {
			t.Errorf("%s: the error does not wrap its cause", c.name)
		}
		var mismatch *MismatchError
		if errors.As(err, &mismatch) {
			t.Errorf("%s: a store that was not read is said to hold another library: %v", c.name, err)
		}
	}
}

func TestLoadSendsTheSourceOnceAndReturnsItsDigest(t *testing.T) {
	t.Parallel()
	store := holding()
	digest, err := two().Load(context.Background(), store)
	if err != nil || digest != twoDigest {
		t.Fatalf("Load = %q %v, want %q", digest, err, twoDigest)
	}
	if sent := store.commands(); !slices.Equal(sent, []string{"FUNCTION LOAD REPLACE " + twoSource}) {
		t.Fatalf("Load sent %q, want one FUNCTION LOAD REPLACE of the source", sent)
	}
}

func TestLoadSaysWhatTheStoreHoldsAfterAnError(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		cause error
		want  []string
	}{
		{"a syntax error", refusal("ERR Error compiling function: user_function:9: unexpected symbol near '='"),
			[]string{"redisfn: load lib_one: the store refused: ERR Error compiling function: user_function:9: unexpected symbol near '=' [user_function:9 = lua/b.lua:1]; the store holds what it held before"}},
		{"a library that registers nothing", refusal("ERR No functions registered"),
			[]string{"redisfn: load lib_one: the store refused: ERR No functions registered; the store holds what it held before"}},
		{"a name twice in the library", refusal("ERR Error registering functions: ERR Function already exists in the library"),
			[]string{"the store refused: ERR Error registering functions: ERR Function already exists in the library; the store holds what it held before"}},
		{"a connection that broke", errors.New("read tcp 127.0.0.1:6379: i/o timeout"),
			[]string{"redisfn: load lib_one: the store did not answer: read tcp 127.0.0.1:6379: i/o timeout; the store holds the whole library it held before or the whole of this one, and Check says which"}},
		{"a text that is not the store's but reads like its refusal", errors.New("ERR Function fa already exists"),
			[]string{"the store did not answer: ERR Function fa already exists; the store holds the whole library"}},
	} {
		store := holding()
		store.load = func(context.Context, string) (string, error) { return "", c.cause }
		digest, err := two().Load(context.Background(), store)
		if digest != "" {
			t.Errorf("%s: Load returned the digest %q beside its error", c.name, digest)
		}
		line := oneLine(t, c.name, err)
		for _, want := range c.want {
			if !strings.Contains(line, want) {
				t.Errorf("%s: the error does not say %q: %s", c.name, want, line)
			}
		}
		if !errors.Is(err, c.cause) {
			t.Errorf("%s: the error does not wrap its cause", c.name)
		}
		if sent := store.commands(); len(sent) != 1 {
			t.Errorf("%s: Load sent %d commands, want the load alone", c.name, len(sent))
		}
	}
}

// A store that answers a load with anything but the library's name has not
// said that it loaded it.
func TestLoadTakesOnlyTheLibrarysNameForAnAnswer(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{"", "OK", "lib_one\n", "LIB_ONE"} {
		store := holding()
		store.load = func(context.Context, string) (string, error) { return answer, nil }
		digest, err := two().Load(context.Background(), store)
		line := oneLine(t, "Load", err)
		if digest != "" || !strings.Contains(line, "redisfn: load lib_one: the store answered ") || !strings.Contains(line, "and not the library's name") {
			t.Errorf("answer %q: Load = %q %s", answer, digest, line)
		}
	}
}

// The store's refusal names one function. The error names it with its
// holder, and with it every other name of the library that another library
// holds; names are compared without case, as the store compares them.
func TestLoadNamesTheFunctionAndTheLibraryThatHoldsIt(t *testing.T) {
	t.Parallel()
	lib := Library{Name: "lib_new", Files: tree(map[string]string{
		"a.lua": fn("moved_one") + fn("Moved_Two") + fn("fresh"),
		"b.lua": "redis.register_function('computed' .. '', function() end)\n",
	}), Glob: "*.lua"}
	store := holding(
		redis.Library{Name: "lib_new", Functions: []redis.Function{{Name: "fresh"}, {Name: "moved_one"}}},
		redis.Library{Name: "lib_old", Functions: []redis.Function{{Name: "kept"}, {Name: "MOVED_ONE"}, {Name: "moved_two"}}},
		redis.Library{Name: "lib_other", Functions: []redis.Function{{Name: "computed"}}},
	)
	store.load = func(context.Context, string) (string, error) {
		return "", refusal("ERR Function computed already exists")
	}
	digest, err := lib.Load(context.Background(), store)
	line := oneLine(t, "Load", err)
	var collision *CollisionError
	if digest != "" || !errors.As(err, &collision) {
		t.Fatalf("Load = %q %v, want a *CollisionError", digest, err)
	}
	want := []Held{{"Moved_Two", "lib_old"}, {"computed", "lib_other"}, {"moved_one", "lib_old"}}
	if collision.Library != "lib_new" || !slices.Equal(collision.Held, want) || collision.Unread != nil {
		t.Errorf("the collision is %+v, want lib_new and %+v", *collision, want)
	}
	if want := "redisfn: load lib_new: the store refused it and holds what it held before: " +
		"function Moved_Two is registered by library lib_old, function computed is registered by library lib_other, function moved_one is registered by library lib_old; " +
		"remedy: a function name belongs to one library: load the version of the other library that no longer registers it, then load lib_new again"; line != want {
		t.Errorf("the error reads\n%s\nwant\n%s", line, want)
	}
	if sent := store.commands(); len(sent) != 2 || sent[1] != "FUNCTION LIST" {
		t.Errorf("Load sent %d commands ending in %q, want the load and one FUNCTION LIST of every library", len(sent), sent[len(sent)-1])
	}
}

// Between the store's refusal and the look for the holder another loader may
// have taken the function out of the other library (tla/RedisFn.tla,
// MCRedisFnHolderGone): the error says so and names no holder.
func TestLoadNamesTheFunctionWhenItsHolderIsGone(t *testing.T) {
	t.Parallel()
	store := holding(redis.Library{Name: "lib_old", Functions: []redis.Function{{Name: "kept"}}})
	store.load = func(context.Context, string) (string, error) { return "", refusal("ERR Function fa already exists") }
	_, err := two().Load(context.Background(), store)
	line := oneLine(t, "Load", err)
	var collision *CollisionError
	if !errors.As(err, &collision) || !slices.Equal(collision.Held, []Held{{Function: "fa"}}) || collision.Unread != nil {
		t.Fatalf("Load = %v, want a collision on fa with no holder", err)
	}
	if !strings.Contains(line, "function fa was registered by another library when the store refused, and by none a moment later;") {
		t.Errorf("the error does not say the holder is gone: %s", line)
	}
}

func TestLoadNamesTheFunctionWhenTheHoldersCannotBeRead(t *testing.T) {
	t.Parallel()
	cause := refusal("NOPERM User seat has no permissions to run the 'function|list' command")
	store := holding()
	store.load = func(context.Context, string) (string, error) { return "", refusal("ERR Function fa already exists") }
	store.list = func(context.Context, redis.FunctionListQuery) ([]redis.Library, error) { return nil, cause }
	_, err := two().Load(context.Background(), store)
	line := oneLine(t, "Load", err)
	var collision *CollisionError
	if !errors.As(err, &collision) || !slices.Equal(collision.Held, []Held{{Function: "fa"}}) || collision.Unread != error(cause) {
		t.Fatalf("Load = %v, want a collision on fa whose holders were not read", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("the error does not wrap the reason the holders were not read")
	}
	if !strings.Contains(line, "function fa is registered by another library (which one could not be read: NOPERM User seat has no permissions") {
		t.Errorf("the error does not say why the holder is not named: %s", line)
	}
}

func TestLoadMissingLoadsOnlyWhenTheStoreDoesNotHoldTheLibrary(t *testing.T) {
	t.Parallel()
	lib := two()
	for _, c := range []struct {
		name  string
		reply []redis.Library
		want  Receipt
		sent  []string
	}{
		{"absent", nil,
			Receipt{Library: "lib_one", Outcome: Loaded, Digest: twoDigest},
			[]string{"FUNCTION LIST LIBRARYNAME lib_one WITHCODE", "FUNCTION LOAD REPLACE " + twoSource}},
		{"absent, with its code under another name", []redis.Library{{Name: "LIB_ONE", Code: twoSource}},
			Receipt{Library: "lib_one", Outcome: Loaded, Digest: twoDigest},
			[]string{"FUNCTION LIST LIBRARYNAME lib_one WITHCODE", "FUNCTION LOAD REPLACE " + twoSource}},
		{"different", []redis.Library{{Name: "lib_one", Code: "other"}},
			Receipt{Library: "lib_one", Outcome: Replaced, Digest: twoDigest, Was: DigestOf("other")},
			[]string{"FUNCTION LIST LIBRARYNAME lib_one WITHCODE", "FUNCTION LOAD REPLACE " + twoSource}},
		{"same", []redis.Library{{Name: "lib_one", Code: twoSource}},
			Receipt{Library: "lib_one", Outcome: Unchanged, Digest: twoDigest},
			[]string{"FUNCTION LIST LIBRARYNAME lib_one WITHCODE"}},
	} {
		store := holding(c.reply...)
		receipt, err := lib.LoadMissing(context.Background(), store)
		if err != nil || receipt != c.want {
			t.Errorf("%s: LoadMissing = %+v %v, want %+v", c.name, receipt, err, c.want)
		}
		if sent := store.commands(); !slices.Equal(sent, c.sent) {
			t.Errorf("%s: LoadMissing sent %q, want %q", c.name, sent, c.sent)
		}
	}
}

func TestLoadMissingFailsWithTheErrorOfTheCommandThatFailed(t *testing.T) {
	t.Parallel()
	lib := two()

	unread := holding()
	unread.list = func(context.Context, redis.FunctionListQuery) ([]redis.Library, error) {
		return nil, refusal("NOPERM no permissions")
	}
	receipt, err := lib.LoadMissing(context.Background(), unread)
	if line := oneLine(t, "a store that cannot be read", err); receipt != (Receipt{Library: "lib_one", Digest: twoDigest}) ||
		!strings.Contains(line, "redisfn: check lib_one: the store refused: NOPERM no permissions; nothing was changed") {
		t.Errorf("a store that cannot be read: LoadMissing = %+v %s", receipt, line)
	}
	if sent := unread.commands(); len(sent) != 1 {
		t.Errorf("a store that cannot be read was sent %d commands, want the read alone", len(sent))
	}

	refused := holding(redis.Library{Name: "lib_one", Code: "other"})
	refused.load = func(context.Context, string) (string, error) { return "", refusal("ERR No functions registered") }
	receipt, err = lib.LoadMissing(context.Background(), refused)
	if line := oneLine(t, "a load the store refuses", err); receipt != (Receipt{Library: "lib_one", Digest: twoDigest, Was: DigestOf("other")}) ||
		!strings.Contains(line, "redisfn: load lib_one: the store refused: ERR No functions registered; the store holds what it held before") {
		t.Errorf("a load the store refuses: LoadMissing = %+v %s", receipt, line)
	}

	taken := holding(redis.Library{Name: "lib_old", Functions: []redis.Function{{Name: "FB"}}})
	taken.load = func(context.Context, string) (string, error) { return "", refusal("ERR Function fb already exists") }
	receipt, err = lib.LoadMissing(context.Background(), taken)
	var collision *CollisionError
	if !errors.As(err, &collision) || !slices.Equal(collision.Held, []Held{{"fb", "lib_old"}}) || receipt.Outcome != Failed {
		t.Errorf("a function another library holds: LoadMissing = %+v %v", receipt, err)
	}
}

// Nothing of a refused library reaches a store: not a read, not a load.
func TestARefusedLibraryNeverReachesTheStore(t *testing.T) {
	t.Parallel()
	for _, c := range refusals {
		store := holding()
		_, refused := c.lib.Source()
		digest, err := c.lib.Load(context.Background(), store)
		if digest != "" || !errors.Is(err, ErrRefused) || err.Error() != refused.Error() {
			t.Errorf("%s: Load = %q %v, want the refusal", c.name, digest, err)
		}
		state, err := c.lib.Check(context.Background(), store)
		if state != Unknown || !errors.Is(err, ErrRefused) || err.Error() != refused.Error() {
			t.Errorf("%s: Check = %v %v, want unknown and the refusal", c.name, state, err)
		}
		receipt, err := c.lib.LoadMissing(context.Background(), store)
		if receipt != (Receipt{Library: c.lib.Name}) || !errors.Is(err, ErrRefused) || err.Error() != refused.Error() {
			t.Errorf("%s: LoadMissing = %+v %v, want the refusal", c.name, receipt, err)
		}
		state, err = c.lib.Judge([]redis.Library{{Name: c.lib.Name, Code: "any"}})
		if state != Unknown || !errors.Is(err, ErrRefused) {
			t.Errorf("%s: Judge = %v %v, want unknown and the refusal", c.name, state, err)
		}
		if sent := store.commands(); len(sent) != 0 {
			t.Errorf("%s: the store was sent %q", c.name, sent)
		}
	}
}

func TestWithoutAClientNothingIsSent(t *testing.T) {
	t.Parallel()
	lib := two()
	const want = "redisfn: library lib_one: no client to reach a store with"
	if digest, err := lib.Load(context.Background(), nil); digest != "" || err == nil || err.Error() != want {
		t.Errorf("Load = %q %v", digest, err)
	}
	if state, err := lib.Check(context.Background(), nil); state != Unknown || err == nil || err.Error() != want {
		t.Errorf("Check = %v %v", state, err)
	}
	if receipt, err := lib.LoadMissing(context.Background(), nil); receipt != (Receipt{Library: "lib_one"}) || err == nil || err.Error() != want {
		t.Errorf("LoadMissing = %+v %v", receipt, err)
	}
}

// stalled is a store that never answers: each call ends the caller's wait
// (as the bound passing does) and then waits for the test's end. A call that
// returns has returned although its store did not.
func stalled(t *testing.T) (*fake, context.Context) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return &fake{
		load: func(context.Context, string) (string, error) {
			stop()
			<-release
			return "", errors.New("released")
		},
		list: func(context.Context, redis.FunctionListQuery) ([]redis.Library, error) {
			stop()
			<-release
			return nil, errors.New("released")
		},
	}, ctx
}

// A caller whose context has ended already has nothing sent for it.
func TestACallWhoseContextHasEndedSendsNothing(t *testing.T) {
	t.Parallel()
	lib := two()
	ended, stop := context.WithCancel(context.Background())
	stop()
	store := holding()
	if digest, err := lib.Load(ended, store); digest != "" || !errors.Is(err, context.Canceled) {
		t.Errorf("Load = %q %v", digest, err)
	}
	if state, err := lib.Check(ended, store); state != Unknown || !errors.Is(err, context.Canceled) {
		t.Errorf("Check = %v %v", state, err)
	}
	if receipt, err := lib.LoadMissing(ended, store); receipt.Outcome != Failed || !errors.Is(err, context.Canceled) {
		t.Errorf("LoadMissing = %+v %v", receipt, err)
	}
	if sent := store.commands(); len(sent) != 0 {
		t.Errorf("the store was sent %q", sent)
	}
}

func TestEveryCallReturnsWhenItsWaitEndsThoughTheStoreNeverAnswers(t *testing.T) {
	t.Parallel()
	lib := two()

	store, ctx := stalled(t)
	digest, err := lib.Load(ctx, store)
	if line := oneLine(t, "Load", err); digest != "" || !errors.Is(err, context.Canceled) ||
		line != "redisfn: load lib_one: no answer from the store before the wait ended: context canceled; the store holds the whole library it held before or the whole of this one, and Check says which" {
		t.Errorf("Load = %q %s", digest, line)
	}

	store, ctx = stalled(t)
	state, err := lib.Check(ctx, store)
	if line := oneLine(t, "Check", err); state != Unknown || !errors.Is(err, context.Canceled) ||
		line != "redisfn: check lib_one: no answer from the store before the wait ended: context canceled; nothing was changed" {
		t.Errorf("Check = %v %s", state, line)
	}

	store, ctx = stalled(t)
	receipt, err := lib.LoadMissing(ctx, store)
	if line := oneLine(t, "LoadMissing", err); receipt != (Receipt{Library: "lib_one", Digest: twoDigest}) || !errors.Is(err, context.Canceled) ||
		!strings.Contains(line, "no answer from the store before the wait ended: context canceled") {
		t.Errorf("LoadMissing = %+v %s", receipt, line)
	}
}

// The bound is the library's, and DefaultBound when it names none: the
// context a call hands the client ends at the bound, or at the end of the
// caller's own context when that comes first.
func TestTheContextOfEveryCallEndsAtTheBound(t *testing.T) {
	t.Parallel()
	sooner, stop := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer stop()
	for _, c := range []struct {
		name  string
		ctx   context.Context
		bound time.Duration // the library's
		want  time.Duration // how long the client's context may last
	}{
		{"the library's bound", context.Background(), 5 * DefaultBound, 5 * DefaultBound},
		{"no bound", context.Background(), 0, DefaultBound},
		{"a bound below nothing", context.Background(), -1, DefaultBound},
		{"a caller whose own context ends sooner", sooner, 2 * time.Hour, time.Hour},
		{"a caller whose own context ends later", sooner, time.Minute, time.Minute},
	} {
		lib := two()
		lib.Bound = c.bound
		store := holding()
		reached := 0
		store.list = func(ctx context.Context, _ redis.FunctionListQuery) ([]redis.Library, error) {
			reached++
			return nil, endsWithin(ctx, c.want)
		}
		store.load = func(ctx context.Context, _ string) (string, error) {
			reached++
			return "lib_one", endsWithin(ctx, c.want)
		}
		if _, err := lib.Check(c.ctx, store); !isMismatch(err) {
			t.Errorf("%s: Check: %v", c.name, err)
		}
		if _, err := lib.Load(c.ctx, store); err != nil {
			t.Errorf("%s: Load: %v", c.name, err)
		}
		if _, err := lib.LoadMissing(c.ctx, store); err != nil {
			t.Errorf("%s: LoadMissing: %v", c.name, err)
		}
		if reached != 4 {
			t.Errorf("%s: %d calls reached the store, want 4", c.name, reached)
		}
	}
}

// endsWithin is nil when ctx ends no later than the bound from now, and no
// sooner than half of it: the bound was put on it a moment ago.
func endsWithin(ctx context.Context, bound time.Duration) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("the context of the call never ends")
	}
	if left := time.Until(deadline); left > bound || left < bound/2 {
		return fmt.Errorf("the context of the call ends in %v, want the bound of %v", left, bound)
	}
	return nil
}

func isMismatch(err error) bool {
	var mismatch *MismatchError
	return errors.As(err, &mismatch)
}
