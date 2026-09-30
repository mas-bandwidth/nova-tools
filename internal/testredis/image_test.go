package testredis

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The unit tier of the image: Diff is a pure function, and Image runs against
// a fake store that sits behind the client's hooks, answers SCAN, TYPE and DUMP
// from a map and writes nothing to any socket. What the fake records is what
// Image sent.

// The size of the fake store and of one page of its SCAN, small enough that
// the cursor loop takes many turns and large enough that the keys need several
// pipelines.
const (
	fakeImageKeys = 1234 // the keys under the prefix
	fakeScanPage  = 7    // the keys one SCAN call looks through
	fakeStranger  = 300  // the keys before and the keys after, each, that are not under the prefix
)

// fakeKey is one key of the fake store.
type fakeKey struct {
	kind string
	dump string
}

// fakeStore answers a client's commands from a map, in the order SCAN walks
// it, and records them. Each field after page is a way for the store to
// misbehave that a real store does.
type fakeStore struct {
	mu   sync.Mutex
	keys map[string]fakeKey

	// What Image sent.
	scans     []fakeScan     // every SCAN
	pipelines [][]string     // the command names of every pipeline
	commands  map[string]int // every command by name, in lower case
	dumps     map[string]int // how many times each key was DUMPed

	// Behaviour.
	page     int              // keys per SCAN call
	echo     bool             // SCAN names the last key of the page before again
	scanErr  error            // SCAN fails with this
	typeErr  map[string]error // TYPE of the key fails with this
	dumpErr  map[string]error // DUMP of the key fails with this
	pipeErr  error            // every pipeline fails as a whole with this
	gone     map[string]bool  // the key is gone before it is read: TYPE says none, DUMP is nil
	lost     map[string]bool  // the key is gone after TYPE: TYPE says its type, DUMP is nil
	appeared map[string]bool  // the key is there after TYPE: TYPE says none, DUMP answers
	onScan   func(fakeScan)   // called with every SCAN, before it answers
}

// fakeScan is one SCAN as the client sent it.
type fakeScan struct {
	cursor uint64
	match  string
	count  int64
}

// newFakeStore is a store of the keys, walked page keys at a time.
func newFakeStore(keys map[string]fakeKey) *fakeStore {
	return &fakeStore{
		keys:     keys,
		commands: map[string]int{},
		dumps:    map[string]int{},
		page:     fakeScanPage,
		typeErr:  map[string]error{},
		dumpErr:  map[string]error{},
		gone:     map[string]bool{},
		lost:     map[string]bool{},
		appeared: map[string]bool{},
	}
}

// client is a client whose every command goes to the store. Nothing is dialed:
// the hooks answer, and a dial is a failure of the test.
func (s *fakeStore) client(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0", MaxRetries: -1})
	c.AddHook(fakeHook{s})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

type fakeHook struct{ s *fakeStore }

func (h fakeHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("the fake store is not dialed")
	}
}

func (h fakeHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error { return h.s.process(cmd) }
}

func (h fakeHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error { return h.s.pipeline(cmds) }
}

// process answers the one command that is not in a pipeline: SCAN.
func (s *fakeStore) process(cmd redis.Cmder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.ToLower(cmd.Name())
	s.commands[name]++
	scan, ok := cmd.(*redis.ScanCmd)
	if !ok || name != "scan" {
		err := fmt.Errorf("the fake store answers SCAN outside a pipeline and not %s", name)
		cmd.SetErr(err)
		return err
	}
	args := cmd.Args()
	cursor, _ := strconv.ParseUint(fmt.Sprint(args[1]), 10, 64)
	var call fakeScan
	call.cursor = cursor
	for i := 2; i+1 < len(args); i += 2 {
		switch strings.ToLower(fmt.Sprint(args[i])) {
		case "match":
			call.match = fmt.Sprint(args[i+1])
		case "count":
			call.count, _ = strconv.ParseInt(fmt.Sprint(args[i+1]), 10, 64)
		}
	}
	s.scans = append(s.scans, call)
	if s.onScan != nil {
		s.onScan(call)
	}
	if s.scanErr != nil {
		scan.SetErr(s.scanErr)
		return s.scanErr
	}
	prefix, err := literalPrefix(call.match)
	if err != nil {
		scan.SetErr(err)
		return err
	}
	order := make([]string, 0, len(s.keys))
	for key := range s.keys {
		order = append(order, key)
	}
	sort.Strings(order)
	end := min(int(cursor)+s.page, len(order))
	var found []string
	if s.echo && cursor > 0 && strings.HasPrefix(order[cursor-1], prefix) {
		found = append(found, order[cursor-1])
	}
	for _, key := range order[cursor:end] {
		if strings.HasPrefix(key, prefix) {
			found = append(found, key)
		}
	}
	next := uint64(end)
	if end == len(order) {
		next = 0
	}
	scan.SetVal(found, next)
	return nil
}

// pipeline answers a pipeline of TYPE and DUMP as the client does: each command
// has its own answer, and the pipeline's is the first command's error, so a
// DUMP that found nothing (redis.Nil) hides the errors behind it.
func (s *fakeStore) pipeline(cmds []redis.Cmder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, len(cmds))
	var first error
	for i, cmd := range cmds {
		names[i] = strings.ToLower(cmd.Name())
		s.commands[names[i]]++
		key := fmt.Sprint(cmd.Args()[1])
		var err error
		switch c := cmd.(type) {
		case *redis.StatusCmd:
			err = s.answerType(c, key)
		case *redis.StringCmd:
			err = s.answerDump(c, key)
		default:
			err = fmt.Errorf("the fake store answers TYPE and DUMP in a pipeline and not %s", names[i])
			cmd.SetErr(err)
		}
		if first == nil {
			first = err
		}
	}
	s.pipelines = append(s.pipelines, names)
	if s.pipeErr != nil {
		return s.pipeErr
	}
	return first
}

func (s *fakeStore) answerType(cmd *redis.StatusCmd, key string) error {
	if err := s.typeErr[key]; err != nil {
		cmd.SetErr(err)
		return err
	}
	k, there := s.keys[key]
	switch {
	case s.gone[key] || s.appeared[key] || !there:
		cmd.SetVal(typeNone)
	default:
		cmd.SetVal(k.kind)
	}
	return nil
}

func (s *fakeStore) answerDump(cmd *redis.StringCmd, key string) error {
	s.dumps[key]++
	if err := s.dumpErr[key]; err != nil {
		cmd.SetErr(err)
		return err
	}
	k, there := s.keys[key]
	if s.gone[key] || s.lost[key] || !there {
		cmd.SetErr(redis.Nil)
		return redis.Nil
	}
	cmd.SetVal(k.dump)
	return nil
}

// literalPrefix reads a MATCH pattern as a prefix that is a literal, the one
// shape Image sends: characters are themselves, a backslash makes the next one
// itself, and an unescaped * that ends the pattern is the end. Any other glob
// character is not literal, and a store asked to match one as a glob would
// match keys the prefix does not name.
func literalPrefix(pattern string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '\\':
			i++
			if i == len(pattern) {
				return "", fmt.Errorf("pattern %q ends in a lone backslash", pattern)
			}
			b.WriteByte(pattern[i])
		case '*':
			if i != len(pattern)-1 {
				return "", fmt.Errorf("pattern %q has an unescaped * before its end", pattern)
			}
			return b.String(), nil
		case '?', '[', ']':
			return "", fmt.Errorf("pattern %q has an unescaped %q: the prefix is not taken literally", pattern, c)
		default:
			b.WriteByte(c)
		}
	}
	return "", fmt.Errorf("pattern %q does not end in *", pattern)
}

// fakeKeysUnder are n keys under prefix, each of one of three types, with a
// dump that names the key so no two are alike.
func fakeKeysUnder(prefix string, n int) map[string]fakeKey {
	kinds := []string{"string", "hash", "list"}
	keys := make(map[string]fakeKey, n)
	for i := 0; i < n; i++ {
		key := prefix + strconv.Itoa(i)
		keys[key] = fakeKey{kind: kinds[i%len(kinds)], dump: "dump of " + key}
	}
	return keys
}

// merge is the union of the maps.
func merge(parts ...map[string]fakeKey) map[string]fakeKey {
	all := map[string]fakeKey{}
	for _, part := range parts {
		for k, v := range part {
			all[k] = v
		}
	}
	return all
}

// expected is what an image of keys is.
func expected(keys map[string]fakeKey) map[string]Entry {
	want := make(map[string]Entry, len(keys))
	for key, k := range keys {
		want[key] = Entry{Type: k.kind, Sum: sha256.Sum256([]byte(k.dump))}
	}
	return want
}

func TestImageIsEveryKeyUnderThePrefixReadBySCANInPipelines(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	mine := fakeKeysUnder(prefix, fakeImageKeys)
	// Strangers on both sides of the prefix in the order SCAN walks, so there
	// are pages that name nothing and a cursor that is not zero.
	before := fakeKeysUnder("a-before:", fakeStranger)
	after := fakeKeysUnder("z-after:", fakeStranger)
	near := map[string]fakeKey{"img": {kind: "string", dump: "no colon"}, "imgs:1": {kind: "string", dump: "another prefix"}}
	s := newFakeStore(merge(mine, before, after, near))

	got, err := Image(context.Background(), s.client(t), prefix)
	if err != nil {
		t.Fatal(err)
	}
	want := expected(mine)
	if d := Diff(want, got); len(d) != 0 {
		t.Fatalf("the image differs from the %d keys under %q in %d places, the first: %v", fakeImageKeys, prefix, len(d), d[:min(len(d), 5)])
	}

	// Only SCAN, TYPE and DUMP were sent: never KEYS.
	for name := range s.commands {
		if name != "scan" && name != "type" && name != "dump" {
			t.Errorf("Image sent %s; it sends SCAN, TYPE and DUMP", name)
		}
	}
	if n := s.commands["keys"]; n != 0 {
		t.Errorf("Image sent KEYS %d times", n)
	}

	// The cursor loop: one call per page, from the start to the end of the
	// store, every call with the escaped prefix and COUNT.
	stored := len(s.keys)
	wantScans := (stored + fakeScanPage - 1) / fakeScanPage
	if len(s.scans) != wantScans {
		t.Fatalf("Image sent %d SCAN calls for %d keys in pages of %d; want %d", len(s.scans), stored, fakeScanPage, wantScans)
	}
	for i, call := range s.scans {
		if call.cursor != uint64(i*fakeScanPage) || call.match != prefix+"*" || call.count != imageScanCount {
			t.Fatalf("SCAN %d was cursor %d MATCH %q COUNT %d; want cursor %d MATCH %q COUNT %d",
				i, call.cursor, call.match, call.count, i*fakeScanPage, prefix+"*", imageScanCount)
		}
	}

	// The reads: TYPE and DUMP of each key once, in pipelines of at most
	// imageBatchKeys keys, and the round trips are the pipelines and the scans.
	wantPipelines := (fakeImageKeys + imageBatchKeys - 1) / imageBatchKeys
	if len(s.pipelines) != wantPipelines {
		t.Fatalf("Image sent %d pipelines for %d keys; want %d", len(s.pipelines), fakeImageKeys, wantPipelines)
	}
	commands := 0
	for i, names := range s.pipelines {
		if len(names) > 2*imageBatchKeys {
			t.Errorf("pipeline %d holds %d commands; want at most %d", i, len(names), 2*imageBatchKeys)
		}
		commands += len(names)
	}
	if commands != 2*fakeImageKeys || s.commands["type"] != fakeImageKeys || s.commands["dump"] != fakeImageKeys {
		t.Errorf("Image sent %d pipelined commands, %d TYPE and %d DUMP for %d keys; want %d, %d and %d",
			commands, s.commands["type"], s.commands["dump"], fakeImageKeys, 2*fakeImageKeys, fakeImageKeys, fakeImageKeys)
	}
}

func TestImagePatternTakesThePrefixLiterally(t *testing.T) {
	t.Parallel()

	for prefix, want := range map[string]string{
		"":               "*",
		"plain:":         "plain:*",
		"a*b":            `a\*b*`,
		"a?b":            `a\?b*`,
		"a[1]:":          `a\[1\]:*`,
		`a\b`:            `a\\b*`,
		`\*?[]`:          `\\\*\?\[\]*`,
		"naïve:":         "naïve:*",
		"\xff:\xfe":      "\xff:\xfe*",
		"dash-and^caret": "dash-and^caret*",
	} {
		if got := imagePattern(prefix); got != want {
			t.Errorf("imagePattern(%q) = %q; want %q", prefix, got, want)
		}
		// The fake reads the pattern as a store does, and gives the prefix back.
		if back, err := literalPrefix(imagePattern(prefix)); err != nil || back != prefix {
			t.Errorf("the pattern of %q reads back as %q, %v", prefix, back, err)
		}
	}
}

func TestImageOfAPrefixWithGlobCharactersReadsOnlyItsOwnKeys(t *testing.T) {
	t.Parallel()

	const prefix = `we[1]*?\d:`
	mine := map[string]fakeKey{
		prefix + "a": {kind: "string", dump: "mine a"},
		prefix + "b": {kind: "hash", dump: "mine b"},
	}
	// What the prefix would match if it were a glob: [1] is a 1, * is anything,
	// ? is one character, \d is a d.
	glob := map[string]fakeKey{
		"we1xyd:a": {kind: "string", dump: "a glob would take this"},
		"we1d:a":   {kind: "string", dump: "and this"},
	}
	s := newFakeStore(merge(mine, glob))

	got, err := Image(context.Background(), s.client(t), prefix)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(expected(mine), got); len(d) != 0 {
		t.Fatalf("the image of %q differs from its own keys: %v", prefix, d)
	}
}

func TestImageOfTheEmptyPrefixIsEveryKey(t *testing.T) {
	t.Parallel()

	keys := merge(fakeKeysUnder("one:", fakeScanPage), fakeKeysUnder("two:", fakeScanPage))
	s := newFakeStore(keys)

	got, err := Image(context.Background(), s.client(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(expected(keys), got); len(d) != 0 {
		t.Fatalf("the image of the empty prefix differs from the whole store: %v", d)
	}
	if s.scans[0].match != "*" {
		t.Fatalf("the empty prefix was sent as MATCH %q; want *", s.scans[0].match)
	}
}

func TestImageOfAnEmptyStoreIsAnEmptyImage(t *testing.T) {
	t.Parallel()

	s := newFakeStore(map[string]fakeKey{})
	got, err := Image(context.Background(), s.client(t), "img:")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("the image of an empty store is %v; want an empty map that is not nil", got)
	}
	if len(s.pipelines) != 0 {
		t.Fatalf("Image sent %d pipelines to a store that named no key", len(s.pipelines))
	}
}

func TestImageReadsAKeyOnceWhateverNumberOfTimesSCANNamesIt(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	mine := fakeKeysUnder(prefix, fakeScanPage*3)
	s := newFakeStore(mine)
	s.echo = true

	got, err := Image(context.Background(), s.client(t), prefix)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(expected(mine), got); len(d) != 0 {
		t.Fatalf("the image differs from the store: %v", d)
	}
	if n := s.commands["dump"]; n != len(mine) {
		t.Fatalf("Image sent %d DUMPs for %d keys that SCAN named more than once; want one each", n, len(mine))
	}
	for key, n := range s.dumps {
		if n != 1 {
			t.Errorf("%s was DUMPed %d times", key, n)
		}
	}
}

func TestImageLeavesOutAKeyThatIsGoneWhenItsTurnComes(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	mine := fakeKeysUnder(prefix, 6)
	s := newFakeStore(mine)
	s.gone[prefix+"1"] = true // gone before TYPE
	s.lost[prefix+"2"] = true // gone between TYPE and DUMP

	got, err := Image(context.Background(), s.client(t), prefix)
	if err != nil {
		t.Fatal(err)
	}
	want := expected(mine)
	delete(want, prefix+"1")
	delete(want, prefix+"2")
	if d := Diff(want, got); len(d) != 0 {
		t.Fatalf("the image differs from the keys that were there: %v", d)
	}
}

func TestImageRefusesAKeyThatWasWrittenWhileItWasRead(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	mine := fakeKeysUnder(prefix, 4)
	s := newFakeStore(mine)
	s.appeared[prefix+"3"] = true // not there for TYPE, there for DUMP

	got, err := Image(context.Background(), s.client(t), prefix)
	if err == nil || !strings.Contains(err.Error(), prefix+"3") || !strings.Contains(err.Error(), "written while") {
		t.Fatalf("Image = %v, %v; want the error that names %s and says it was written while the image was taken", got, err, prefix+"3")
	}
	if got != nil {
		t.Fatalf("Image answered %d entries with its error; want none, an error is never a partial image", len(got))
	}
}

func TestImageReturnsTheErrorOfAFailedCommandAndNoImage(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		spoil func(s *fakeStore)
		want  string
	}{
		"a SCAN": {func(s *fakeStore) { s.scanErr = boom }, "scan"},
		// The DUMP of the first key finds nothing, which the pipeline reports
		// as redis.Nil, and the TYPE of the second key fails: Image reads the
		// answer of every command and not only the pipeline's.
		"a TYPE behind a redis.Nil": {func(s *fakeStore) {
			s.gone[prefix+"0"] = true
			s.typeErr[prefix+"1"] = boom
		}, `type of "img:1"`},
		"a DUMP behind a redis.Nil": {func(s *fakeStore) {
			s.gone[prefix+"0"] = true
			s.dumpErr[prefix+"1"] = boom
		}, `dump of "img:1"`},
		"a pipeline": {func(s *fakeStore) { s.pipeErr = boom }, "read"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newFakeStore(fakeKeysUnder(prefix, 3))
			tc.spoil(s)
			got, err := Image(context.Background(), s.client(t), prefix)
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Image = %v; want an error that is boom and says %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), prefix) {
				t.Errorf("the error %q does not name the prefix %q", err, prefix)
			}
			if got != nil {
				t.Fatalf("Image answered %d entries with its error; want none", len(got))
			}
		})
	}
}

func TestImageStopsWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	t.Run("before the first call", func(t *testing.T) {
		t.Parallel()

		s := newFakeStore(fakeKeysUnder(prefix, fakeScanPage))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := Image(ctx, s.client(t), prefix)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("Image = %v, %v; want no image and context.Canceled", got, err)
		}
		if len(s.scans) != 0 {
			t.Fatalf("Image sent %d SCAN calls on a context that had ended", len(s.scans))
		}
	})
	t.Run("between two SCAN calls", func(t *testing.T) {
		t.Parallel()

		const stopAfter = 2 // the SCAN call that ends the context
		s := newFakeStore(fakeKeysUnder(prefix, fakeScanPage*5))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s.onScan = func(fakeScan) {
			if len(s.scans) == stopAfter {
				cancel()
			}
		}
		got, err := Image(ctx, s.client(t), prefix)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("Image = %v, %v; want no image and context.Canceled", got, err)
		}
		if len(s.scans) != stopAfter || len(s.pipelines) != 0 {
			t.Fatalf("Image sent %d SCAN calls and %d pipelines; want %d and none", len(s.scans), len(s.pipelines), stopAfter)
		}
	})
}

func TestImageRefusesAClientThatReadsOneNode(t *testing.T) {
	t.Parallel()

	cluster := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{"127.0.0.1:0"}})
	t.Cleanup(func() { _ = cluster.Close() })
	ring := redis.NewRing(&redis.RingOptions{}) // no shard, so nothing to dial
	t.Cleanup(func() { _ = ring.Close() })
	for name, c := range map[string]redis.UniversalClient{"a cluster client": cluster, "a ring": ring, "no client": nil} {
		got, err := Image(context.Background(), c, "img:")
		if err == nil || got != nil {
			t.Errorf("Image of %s = %v, %v; want an error and no image", name, got, err)
		}
	}
	if _, err := Image(context.Background(), cluster, "img:"); err == nil || !strings.Contains(err.Error(), "one node") {
		t.Errorf("Image of a cluster client said %v; want the reason, that SCAN reads one node", err)
	}
}

func TestImageIsForTestsAndNothingElse(t *testing.T) {
	t.Parallel()

	s := newFakeStore(fakeKeysUnder("img:", 1))
	l := real
	l.inTest = func() bool { return false }
	said := panics(func() { _, _ = l.image(context.Background(), s.client(t), "img:") })
	if msg, _ := said.(string); !strings.Contains(msg, "outside a test binary") {
		t.Fatalf("image outside a test binary panicked with %v; want the package's refusal", said)
	}
	if len(s.scans) != 0 {
		t.Fatalf("a refused image sent %d SCAN calls", len(s.scans))
	}
}

func TestDiffIsEmptyWhenTheImagesAreEqual(t *testing.T) {
	t.Parallel()

	a := Entry{Type: "hash", Sum: sha256.Sum256([]byte("a"))}
	b := Entry{Type: "string", Sum: sha256.Sum256([]byte("b"))}
	for name, tc := range map[string]struct{ before, after map[string]Entry }{
		"two nil images":        {nil, nil},
		"nil and empty":         {nil, map[string]Entry{}},
		"empty and nil":         {map[string]Entry{}, nil},
		"the same image":        {map[string]Entry{"a": a, "b": b}, map[string]Entry{"a": a, "b": b}},
		"a copy of an image":    {map[string]Entry{"a": a}, map[string]Entry{"a": {Type: a.Type, Sum: a.Sum}}},
		"one key in both again": {map[string]Entry{"only": b}, map[string]Entry{"only": b}},
	} {
		got := Diff(tc.before, tc.after)
		if got == nil || len(got) != 0 {
			t.Errorf("Diff of %s = %#v; want an empty slice that is not nil", name, got)
		}
	}
}

func TestDiffNamesTheKeysAddedRemovedAndChanged(t *testing.T) {
	t.Parallel()

	one := Entry{Type: "string", Sum: sha256.Sum256([]byte("one"))}
	two := Entry{Type: "string", Sum: sha256.Sum256([]byte("two"))}
	other := Entry{Type: "hash", Sum: one.Sum} // the same bytes under another type
	for name, tc := range map[string]struct {
		before, after map[string]Entry
		want          []string
	}{
		"a key added":                 {nil, map[string]Entry{"k": one}, []string{"+k"}},
		"a key removed":               {map[string]Entry{"k": one}, nil, []string{"-k"}},
		"a key whose value changed":   {map[string]Entry{"k": one}, map[string]Entry{"k": two}, []string{"~k"}},
		"a key whose type changed":    {map[string]Entry{"k": one}, map[string]Entry{"k": other}, []string{"~k"}},
		"a key that is not touched":   {map[string]Entry{"k": one, "j": one}, map[string]Entry{"k": one, "j": two}, []string{"~j"}},
		"a key with a colon and mark": {map[string]Entry{}, map[string]Entry{"+a:~b": one}, []string{"++a:~b"}},
	} {
		if got := Diff(tc.before, tc.after); !slices.Equal(got, tc.want) {
			t.Errorf("Diff of %s = %q; want %q", name, got, tc.want)
		}
	}
}

func TestDiffIsSortedAsStringsAndIsTheSameEveryTime(t *testing.T) {
	t.Parallel()

	const keys = 200 // enough that map order shows if the lines were not sorted
	entry := func(n int) Entry { return Entry{Type: "string", Sum: sha256.Sum256([]byte(strconv.Itoa(n)))} }
	before, after := map[string]Entry{}, map[string]Entry{}
	var want []string
	for i := 0; i < keys; i++ {
		key := fmt.Sprintf("k:%03d", i)
		switch i % 4 {
		case 0: // added
			after[key] = entry(i)
			want = append(want, "+"+key)
		case 1: // removed
			before[key] = entry(i)
			want = append(want, "-"+key)
		case 2: // changed
			before[key], after[key] = entry(i), entry(i+keys)
			want = append(want, "~"+key)
		default: // untouched
			before[key], after[key] = entry(i), entry(i)
		}
	}
	sort.Strings(want)
	// The marks sort + before - before ~, so the groups come in that order.
	if !strings.HasPrefix(want[0], "+") || !strings.HasPrefix(want[len(want)-1], "~") {
		t.Fatalf("the test's own order is not added, removed, changed: %q ... %q", want[0], want[len(want)-1])
	}
	first := Diff(before, after)
	if !slices.Equal(first, want) || !sort.StringsAreSorted(first) {
		t.Fatalf("Diff is not the sorted lines of the %d keys that differ: first lines %q, want %q", len(want), first[:3], want[:3])
	}
	for i := 0; i < keys; i++ {
		if again := Diff(before, after); !slices.Equal(again, first) {
			t.Fatalf("Diff of the same two images differs from one call to the next")
		}
	}
}

func TestDiffChangesNeitherImage(t *testing.T) {
	t.Parallel()

	one := Entry{Type: "string", Sum: sha256.Sum256([]byte("one"))}
	two := Entry{Type: "string", Sum: sha256.Sum256([]byte("two"))}
	before := map[string]Entry{"a": one, "b": one}
	after := map[string]Entry{"b": two, "c": one}
	_ = Diff(before, after)
	if len(before) != 2 || before["a"] != one || before["b"] != one || len(after) != 2 || after["b"] != two || after["c"] != one {
		t.Fatalf("Diff changed an image: before %v, after %v", before, after)
	}
}
