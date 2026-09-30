package testredis

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"math"
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
// a fake store that sits behind the client's hooks, answers SCAN, TYPE,
// PEXPIRETIME, DUMP, HGETALL, SMEMBERS and ZRANGE from a map the way a server
// does, and writes nothing to any socket. What the fake records is what Image
// sent.

// The size of the fake store and of one page of its SCAN, small enough that
// the cursor loop takes many turns and large enough that the keys need several
// batches.
const (
	fakeImageKeys = 1234 // the keys under the prefix
	fakeScanPage  = 7    // the keys one SCAN call looks through
	fakeStranger  = 300  // the keys before and the keys after, each, that are not under the prefix
)

// fakeKey is one key of the fake store. Its kind says which of the fields holds
// its content, and it is read by the command of that kind.
type fakeKey struct {
	kind    string
	dump    string            // string, list, stream and any other type: what DUMP answers
	fields  map[string]string // hash: what HGETALL answers
	members []string          // set: what SMEMBERS answers, in the order given
	scored  []redis.Z         // sorted set: what ZRANGE WITHSCORES answers, in the order given
	expires int64             // the key's expiry in milliseconds since the epoch; 0 is no expiry
}

// The keys of each kind.
func fakeString(dump string) fakeKey { return fakeKey{kind: "string", dump: dump} }
func fakeList(dump string) fakeKey   { return fakeKey{kind: "list", dump: dump} }
func fakeStream(dump string) fakeKey { return fakeKey{kind: "stream", dump: dump} }
func fakeSet(members ...string) fakeKey {
	return fakeKey{kind: "set", members: members}
}
func fakeZSet(scored ...redis.Z) fakeKey {
	return fakeKey{kind: "zset", scored: scored}
}

// fakeHash is a hash of the field and value pairs.
func fakeHash(pairs ...string) fakeKey {
	fields := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		fields[pairs[i]] = pairs[i+1]
	}
	return fakeKey{kind: "hash", fields: fields}
}

// expiring is the key with an expiry.
func (k fakeKey) expiring(ms int64) fakeKey {
	k.expires = ms
	return k
}

// serverError is a server's answer to a command that failed: a redis.Error, as
// a real server's is, and not a failure to reach it.
type serverError string

func (e serverError) Error() string { return string(e) }
func (serverError) RedisError()     {}

// wrongType is what a server answers to a command of another type's.
const wrongType = serverError("WRONGTYPE Operation against a key holding the wrong kind of value")

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
	sent      map[string]int // how many times each command was sent for each key, as "name key"

	// Behaviour.
	page         int              // keys per SCAN call
	echo         bool             // SCAN names the last key of the page before again
	scanErr      error            // SCAN fails with this
	fail         map[string]error // the command "name key" fails with this
	pipeErr      error            // every pipeline fails as a whole with this
	gone         map[string]bool  // the key is gone before it is read: TYPE says none, PEXPIRETIME says -2, the content is nothing
	lost         map[string]bool  // the key is gone after TYPE and PEXPIRETIME: its content is nothing
	lostAtExpiry map[string]bool  // the key is gone after TYPE: PEXPIRETIME says -2 and the content is nothing
	appeared     map[string]bool  // the key is there after TYPE: TYPE says none, PEXPIRETIME answers
	retyped      map[string]bool  // the key is another type by the time its content is read: HGETALL, SMEMBERS and ZRANGE are refused
	onScan       func(fakeScan)   // called with every SCAN, before it answers
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
		keys:         keys,
		commands:     map[string]int{},
		sent:         map[string]int{},
		page:         fakeScanPage,
		fail:         map[string]error{},
		gone:         map[string]bool{},
		lost:         map[string]bool{},
		lostAtExpiry: map[string]bool{},
		appeared:     map[string]bool{},
		retyped:      map[string]bool{},
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

// pipeline answers a pipeline as the client does: each command has its own
// answer, and the pipeline's is the first command's error, so a DUMP that
// found nothing (redis.Nil) hides the errors behind it.
func (s *fakeStore) pipeline(cmds []redis.Cmder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, len(cmds))
	var first error
	for i, cmd := range cmds {
		names[i] = strings.ToLower(cmd.Name())
		s.commands[names[i]]++
		key := fmt.Sprint(cmd.Args()[1])
		s.sent[names[i]+" "+key]++
		err := s.answer(cmd, names[i], key)
		if err != nil {
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

// answer is the store's answer to one pipelined command on a key, or its error.
func (s *fakeStore) answer(cmd redis.Cmder, name, key string) error {
	if err := s.fail[name+" "+key]; err != nil {
		return err
	}
	k, there := s.keys[key]
	exists := there && !s.gone[key]
	inContent := exists && !s.lostAtExpiry[key] && !s.lost[key]
	switch c := cmd.(type) {
	case *redis.StatusCmd: // TYPE
		if name != "type" || !exists || s.appeared[key] {
			c.SetVal(typeNone)
			break
		}
		c.SetVal(k.kind)
	case *redis.Cmd: // PEXPIRETIME
		switch {
		case name != "pexpiretime":
			return fmt.Errorf("the fake store does not answer %s as a Cmd", name)
		case !exists || s.lostAtExpiry[key]:
			c.SetVal(int64(expiryNoKey))
		case k.expires == 0:
			c.SetVal(int64(expiryNone))
		default:
			c.SetVal(k.expires)
		}
	case *redis.StringCmd: // DUMP
		if name != "dump" {
			return fmt.Errorf("the fake store does not answer %s as a StringCmd", name)
		}
		if !inContent {
			return redis.Nil
		}
		c.SetVal(k.dump)
	case *redis.MapStringStringCmd: // HGETALL
		switch {
		case name != "hgetall":
			return fmt.Errorf("the fake store does not answer %s as a MapStringStringCmd", name)
		case !inContent:
			c.SetVal(map[string]string{})
		case k.kind != "hash" || s.retyped[key]:
			return wrongType
		default:
			c.SetVal(maps.Clone(k.fields))
		}
	case *redis.StringSliceCmd: // SMEMBERS
		switch {
		case name != "smembers":
			return fmt.Errorf("the fake store does not answer %s as a StringSliceCmd", name)
		case !inContent:
			c.SetVal([]string{})
		case k.kind != "set" || s.retyped[key]:
			return wrongType
		default:
			c.SetVal(slices.Clone(k.members))
		}
	case *redis.ZSliceCmd: // ZRANGE key 0 -1 WITHSCORES
		args := cmd.Args()
		if name != "zrange" || len(args) != 5 || fmt.Sprintf("%v %v %v", args[2], args[3], args[4]) != "0 -1 withscores" {
			return fmt.Errorf("the fake store answers ZRANGE key 0 -1 withscores and not %v", args)
		}
		switch {
		case !inContent:
			c.SetVal([]redis.Z{})
		case k.kind != "zset" || s.retyped[key]:
			return wrongType
		default:
			c.SetVal(slices.Clone(k.scored))
		}
	default:
		return fmt.Errorf("the fake store does not answer %s", name)
	}
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

// fakeKinds are the kinds fakeKeysUnder cycles through.
var fakeKinds = []string{"string", "hash", "list", "set", "zset", "stream"}

// fakeKeysUnder are n keys under prefix, cycling through the six kinds, each
// with a content that names the key so no two are alike, and every fourth with
// an expiry.
func fakeKeysUnder(prefix string, n int) map[string]fakeKey {
	keys := make(map[string]fakeKey, n)
	for i := 0; i < n; i++ {
		key := prefix + strconv.Itoa(i)
		var k fakeKey
		switch fakeKinds[i%len(fakeKinds)] {
		case "string":
			k = fakeString("dump of " + key)
		case "hash":
			k = fakeHash("one", "a "+key, "two", "b "+key)
		case "list":
			k = fakeList("dump of " + key)
		case "set":
			k = fakeSet("x", "y", key)
		case "zset":
			k = fakeZSet(redis.Z{Score: 1, Member: "x"}, redis.Z{Score: 2, Member: key})
		case "stream":
			k = fakeStream("dump of " + key)
		}
		if i%4 == 3 {
			k = k.expiring(1_900_000_000_000 + int64(i))
		}
		keys[key] = k
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

// oracleSum is the sum of Entry's documentation, written out byte by byte from
// the documented layout and sharing no code with Image: a string, a list, a
// stream and any other type give their dump, a hash its fields, a set its
// members and a sorted set its members and scores, each sorted bytewise; and
// every sum ends in the key's expiry, -1 for none. Every number is 8 bytes, big
// endian, and every string follows its length.
func oracleSum(kind, dump string, fields map[string]string, members []string, scores map[string]float64, expiry int64) [32]byte {
	var b []byte
	num := func(n uint64) { b = binary.BigEndian.AppendUint64(b, n) }
	text := func(s string) {
		num(uint64(len(s)))
		b = append(b, s...)
	}
	switch kind {
	case "hash":
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		sort.Strings(names)
		num(uint64(len(names)))
		for _, name := range names {
			text(name)
			text(fields[name])
		}
	case "set":
		sorted := append([]string(nil), members...)
		sort.Strings(sorted)
		num(uint64(len(sorted)))
		for _, member := range sorted {
			text(member)
		}
	case "zset":
		names := make([]string, 0, len(scores))
		for name := range scores {
			names = append(names, name)
		}
		sort.Strings(names)
		num(uint64(len(names)))
		for _, name := range names {
			text(name)
			num(math.Float64bits(scores[name]))
		}
	default:
		b = append(b, dump...)
	}
	num(uint64(expiry))
	return sha256.Sum256(b)
}

// expectedEntry is what an image holds of a key.
func expectedEntry(k fakeKey) Entry {
	scores := map[string]float64{}
	for _, z := range k.scored {
		scores[fmt.Sprint(z.Member)] = z.Score
	}
	expiry := int64(-1)
	if k.expires != 0 {
		expiry = k.expires
	}
	return Entry{Type: k.kind, Sum: oracleSum(k.kind, k.dump, k.fields, k.members, scores, expiry)}
}

// expected is what an image of keys is.
func expected(keys map[string]fakeKey) map[string]Entry {
	want := make(map[string]Entry, len(keys))
	for key, k := range keys {
		want[key] = expectedEntry(k)
	}
	return want
}

// contentCommands are the commands that read the content of a key, by kind.
var contentCommands = map[string]string{
	"string": "dump", "list": "dump", "stream": "dump",
	"hash": "hgetall", "set": "smembers", "zset": "zrange",
}

func TestImageIsEveryKeyUnderThePrefixReadBySCANInPipelines(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	mine := fakeKeysUnder(prefix, fakeImageKeys)
	// Strangers on both sides of the prefix in the order SCAN walks, so there
	// are pages that name nothing and a cursor that is not zero.
	before := fakeKeysUnder("a-before:", fakeStranger)
	after := fakeKeysUnder("z-after:", fakeStranger)
	near := map[string]fakeKey{"img": fakeString("no colon"), "imgs:1": fakeString("another prefix")}
	s := newFakeStore(merge(mine, before, after, near))

	got, err := Image(context.Background(), s.client(t), prefix)
	if err != nil {
		t.Fatal(err)
	}
	want := expected(mine)
	if d := Diff(want, got); len(d) != 0 {
		t.Fatalf("the image differs from the %d keys under %q in %d places, the first: %v", fakeImageKeys, prefix, len(d), d[:min(len(d), 5)])
	}

	// Only SCAN and the commands that read a key were sent: never KEYS.
	for name := range s.commands {
		if !slices.Contains([]string{"scan", "type", "pexpiretime", "dump", "hgetall", "smembers", "zrange"}, name) {
			t.Errorf("Image sent %s; it sends SCAN, TYPE, PEXPIRETIME and the command that reads the type", name)
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

	// The reads: a batch of at most imageBatchKeys keys is two pipelines, TYPE
	// and PEXPIRETIME of each key and then one content command of each, so the
	// round trips are the pipelines and the scans.
	batches := (fakeImageKeys + imageBatchKeys - 1) / imageBatchKeys
	if len(s.pipelines) != 2*batches {
		t.Fatalf("Image sent %d pipelines for %d keys; want %d, two for each of %d batches", len(s.pipelines), fakeImageKeys, 2*batches, batches)
	}
	for b := 0; b < batches; b++ {
		keys := min(imageBatchKeys, fakeImageKeys-b*imageBatchKeys)
		kinds, contents := s.pipelines[2*b], s.pipelines[2*b+1]
		if len(kinds) != 2*keys || len(contents) != keys {
			t.Errorf("batch %d of %d keys was pipelines of %d and %d commands; want %d and %d", b, keys, len(kinds), len(contents), 2*keys, keys)
		}
		for i, name := range kinds {
			if want := []string{"type", "pexpiretime"}[i%2]; name != want {
				t.Fatalf("batch %d: command %d of the first pipeline is %s; want %s", b, i, name, want)
			}
		}
		for i, name := range contents {
			if !slices.Contains([]string{"dump", "hgetall", "smembers", "zrange"}, name) {
				t.Fatalf("batch %d: command %d of the second pipeline is %s; want a content read", b, i, name)
			}
		}
	}
	// Each key is read by the command of its type, once.
	wantCommands := map[string]int{"type": fakeImageKeys, "pexpiretime": fakeImageKeys}
	for _, k := range mine {
		wantCommands[contentCommands[k.kind]]++
	}
	for name, n := range wantCommands {
		if s.commands[name] != n {
			t.Errorf("Image sent %d %s for %d keys; want %d", s.commands[name], name, fakeImageKeys, n)
		}
	}
}

func TestImageReadsAnyOtherTypeByItsDump(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	module := fakeKey{kind: "ReJSON-RL", dump: "a document"}
	s := newFakeStore(map[string]fakeKey{prefix + "doc": module})
	got, err := Image(context.Background(), s.client(t), prefix)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(expected(map[string]fakeKey{prefix + "doc": module}), got); len(d) != 0 || got[prefix+"doc"].Type != "ReJSON-RL" {
		t.Fatalf("the image of a key of another type is %v, diff %q; want its type and the sum of its dump", got, d)
	}
	if s.commands["dump"] != 1 {
		t.Fatalf("Image sent %d DUMP for the key of another type; want 1", s.commands["dump"])
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
		prefix + "a": fakeString("mine a"),
		prefix + "b": fakeHash("field", "mine b"),
	}
	// What the prefix would match if it were a glob: [1] is a 1, * is anything,
	// ? is one character, \d is a d.
	glob := map[string]fakeKey{
		"we1xyd:a": fakeString("a glob would take this"),
		"we1d:a":   fakeString("and this"),
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
	// Three commands for each key, each once.
	if len(s.sent) != 3*len(mine) {
		t.Fatalf("Image sent %d distinct commands for %d keys that SCAN named more than once; want %d", len(s.sent), len(mine), 3*len(mine))
	}
	for command, n := range s.sent {
		if n != 1 {
			t.Errorf("%s was sent %d times", command, n)
		}
	}
}

func TestImageLeavesOutAKeyThatIsGoneWhenItsTurnComes(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	mine := map[string]fakeKey{
		prefix + "stays":          fakeString("stays"),
		prefix + "gone":           fakeString("gone"),
		prefix + "lost-at-expiry": fakeString("lost at expiry"),
		prefix + "lost-string":    fakeString("lost"),
		prefix + "lost-hash":      fakeHash("field", "value"),
		prefix + "lost-set":       fakeSet("member"),
		prefix + "lost-zset":      fakeZSet(redis.Z{Score: 1, Member: "member"}),
	}
	s := newFakeStore(mine)
	s.gone[prefix+"gone"] = true                   // gone before TYPE
	s.lostAtExpiry[prefix+"lost-at-expiry"] = true // gone between TYPE and PEXPIRETIME
	for _, kind := range []string{"string", "hash", "set", "zset"} {
		s.lost[prefix+"lost-"+kind] = true // gone between the first round trip and the second
	}

	got, err := Image(context.Background(), s.client(t), prefix)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]fakeKey{prefix + "stays": mine[prefix+"stays"]}
	if d := Diff(expected(want), got); len(d) != 0 {
		t.Fatalf("the image differs from the keys that were there: %v", d)
	}
}

func TestImageRefusesAKeyThatWasWrittenWhileItWasRead(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	for name, tc := range map[string]struct {
		spoil func(s *fakeStore, key string)
		want  string
	}{
		"not there for TYPE and there for PEXPIRETIME": {func(s *fakeStore, key string) { s.appeared[key] = true }, "written while"},
		"another type when its content is read":        {func(s *fakeStore, key string) { s.retyped[key] = true }, "written while"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mine := fakeKeysUnder(prefix, 4) // key 3 is a set
			s := newFakeStore(mine)
			tc.spoil(s, prefix+"3")

			got, err := Image(context.Background(), s.client(t), prefix)
			if err == nil || !strings.Contains(err.Error(), prefix+"3") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Image = %v, %v; want the error that names %s and says it was written while the image was taken", got, err, prefix+"3")
			}
			if got != nil {
				t.Fatalf("Image answered %d entries with its error; want none, an error is never a partial image", len(got))
			}
		})
	}
}

func TestImageReturnsTheErrorOfAFailedCommandAndNoImage(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	boom := errors.New("boom")                       // a failure to reach the server
	refused := serverError("ERR the server says no") // the server's answer to one command
	// Keys 0 to 5 are a string, a hash, a list, a set, a sorted set and a stream.
	for name, tc := range map[string]struct {
		spoil func(s *fakeStore)
		err   error
		want  string
	}{
		"a SCAN":                    {func(s *fakeStore) { s.scanErr = boom }, boom, "scan"},
		"a pipeline":                {func(s *fakeStore) { s.pipeErr = boom }, boom, "read"},
		"a TYPE":                    {func(s *fakeStore) { s.fail["type "+prefix+"1"] = refused }, refused, `type of "img:1"`},
		"a PEXPIRETIME":             {func(s *fakeStore) { s.fail["pexpiretime "+prefix+"1"] = refused }, refused, `expiry of "img:1"`},
		"a DUMP behind a redis.Nil": {func(s *fakeStore) { s.lost[prefix+"0"] = true; s.fail["dump "+prefix+"2"] = refused }, refused, `dump of "img:2"`},
		"an HGETALL behind a redis.Nil": {func(s *fakeStore) {
			s.lost[prefix+"0"] = true
			s.fail["hgetall "+prefix+"1"] = refused
		}, refused, `hgetall of "img:1"`},
		"a SMEMBERS behind a redis.Nil": {func(s *fakeStore) {
			s.lost[prefix+"0"] = true
			s.fail["smembers "+prefix+"3"] = refused
		}, refused, `smembers of "img:3"`},
		"a ZRANGE behind a redis.Nil": {func(s *fakeStore) {
			s.lost[prefix+"0"] = true
			s.fail["zrange "+prefix+"4"] = refused
		}, refused, `zrange of "img:4"`},
		"a sorted set member that is not a string": {func(s *fakeStore) {
			s.keys[prefix+"4"] = fakeZSet(redis.Z{Score: 1, Member: 42})
		}, nil, `zrange of "img:4"`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newFakeStore(fakeKeysUnder(prefix, 6))
			tc.spoil(s)
			got, err := Image(context.Background(), s.client(t), prefix)
			if err == nil || (tc.err != nil && !errors.Is(err, tc.err)) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Image = %v; want an error that is %v and says %q", err, tc.err, tc.want)
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

// imageOf is the image of the store's keys under prefix.
func imageOf(t *testing.T, s *fakeStore, prefix string) map[string]Entry {
	t.Helper()
	got, err := Image(context.Background(), s.client(t), prefix)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestImageSumOfAHashSetOrSortedSetDoesNotDependOnItsOrder(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	// Thirty fields, so that the order of a Go map, which the fake's HGETALL
	// answers in, shows in a sum that does not sort.
	var pairs []string
	for i := 0; i < 30; i++ {
		pairs = append(pairs, fmt.Sprintf("field-%02d", i), fmt.Sprintf("value-%02d", i))
	}
	members := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	reversed := slices.Clone(members)
	slices.Reverse(reversed)
	scored := []redis.Z{{Score: 1, Member: "a"}, {Score: 2, Member: "b"}, {Score: 3, Member: "c"}, {Score: 4, Member: "d"}}
	shuffled := []redis.Z{scored[2], scored[0], scored[3], scored[1]}

	s := newFakeStore(map[string]fakeKey{
		prefix + "hash": fakeHash(pairs...),
		prefix + "set":  fakeSet(members...),
		prefix + "zset": fakeZSet(scored...),
	})
	first := imageOf(t, s, prefix)
	for i := 0; i < 100; i++ {
		if d := Diff(first, imageOf(t, s, prefix)); len(d) != 0 {
			t.Fatalf("the image of the same store differs from one call to the next: %q", d)
		}
	}
	// The same members, and the same fields and scores, in another order.
	s.keys[prefix+"set"] = fakeSet(reversed...)
	s.keys[prefix+"zset"] = fakeZSet(shuffled...)
	if d := Diff(first, imageOf(t, s, prefix)); len(d) != 0 {
		t.Fatalf("a set and a sorted set in another order are a change: %q", d)
	}
	if d := Diff(expected(s.keys), first); len(d) != 0 {
		t.Fatalf("the image is not what the documented sum is: %q", d)
	}
	// And other content is another sum: a field, a member, a score.
	for name, changed := range map[string]fakeKey{
		"hash": fakeHash(append(slices.Clone(pairs[:len(pairs)-1]), "another value")...),
		"set":  fakeSet(append(slices.Clone(members[:4]), "omega")...),
		"zset": fakeZSet(redis.Z{Score: 1, Member: "a"}, redis.Z{Score: 2, Member: "b"}, redis.Z{Score: 3, Member: "c"}, redis.Z{Score: 4.5, Member: "d"}),
	} {
		other := newFakeStore(maps.Clone(s.keys))
		other.keys[prefix+name] = changed
		if d := Diff(first, imageOf(t, other, prefix)); !slices.Equal(d, []string{"~" + prefix + name}) {
			t.Errorf("a changed %s gave the diff %q; want %q", name, d, []string{"~" + prefix + name})
		}
	}
}

func TestImageSumFoldsInTheExpiry(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	const at = 1_900_000_000_000
	bases := map[string]fakeKey{
		"string": fakeString("s"),
		"hash":   fakeHash("f", "v"),
		"list":   fakeList("l"),
		"set":    fakeSet("m"),
		"zset":   fakeZSet(redis.Z{Score: 1, Member: "m"}),
		"stream": fakeStream("x"),
	}
	for kind, base := range bases {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			key := prefix + kind
			s := newFakeStore(map[string]fakeKey{key: base})
			none := imageOf(t, s, prefix)
			for _, step := range []struct {
				name string
				ms   int64
				want []string
			}{
				{"an expiry set", at, []string{"~" + key}},
				{"the same expiry again", at, []string{}},
				{"another expiry", at + 1, []string{"~" + key}},
				{"the expiry removed", 0, []string{"~" + key}},
				{"no expiry again", 0, []string{}},
			} {
				s.keys[key] = base.expiring(step.ms)
				next := imageOf(t, s, prefix)
				if d := Diff(none, next); !slices.Equal(d, step.want) {
					t.Fatalf("after %s the diff is %q; want %q", step.name, d, step.want)
				}
				none = next
			}
		})
	}
}

func TestImageSumIsTheLayoutEntryDocuments(t *testing.T) {
	t.Parallel()

	const prefix = "img:"
	const none = "\xff\xff\xff\xff\xff\xff\xff\xff" // an expiry of -1
	const at = "\x00\x00\x01\xd1\xa9\x4a\x20\x00"   // an expiry of 2,000,000,000,000 ms
	num := func(n byte) string { return "\x00\x00\x00\x00\x00\x00\x00" + string([]byte{n}) }
	for name, tc := range map[string]struct {
		key   fakeKey
		bytes string
	}{
		"a string is its dump and then its expiry":   {fakeString("abc"), "abc" + none},
		"a list is its dump too":                     {fakeList("xyz"), "xyz" + none},
		"a hash is its fields in order":              {fakeHash("b", "22", "a", "1"), num(2) + num(1) + "a" + num(1) + "1" + num(1) + "b" + num(2) + "22" + none},
		"a set is its members in order":              {fakeSet("b", "a"), num(2) + num(1) + "a" + num(1) + "b" + none},
		"a sorted set is its members and their bits": {fakeZSet(redis.Z{Score: 1, Member: "m"}), num(1) + num(1) + "m" + "\x3f\xf0\x00\x00\x00\x00\x00\x00" + none},
		"an expiry is the last of it":                {fakeString("abc").expiring(2_000_000_000_000), "abc" + at},
		"an empty string dump is the expiry alone":   {fakeString(""), none},
	} {
		s := newFakeStore(map[string]fakeKey{prefix + "k": tc.key})
		got := imageOf(t, s, prefix)[prefix+"k"]
		if want := sha256.Sum256([]byte(tc.bytes)); got.Sum != want {
			t.Errorf("%s: the sum is %x; want the SHA256 of %q, %x", name, got.Sum[:6], tc.bytes, want[:6])
		}
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

func TestDiffIsInKeyOrderWithTheMarksInterleavedAndTheSameEveryTime(t *testing.T) {
	t.Parallel()

	const keys = 200 // enough that map order shows if the lines were not sorted
	entry := func(n int) Entry { return Entry{Type: "string", Sum: sha256.Sum256([]byte(strconv.Itoa(n)))} }
	before, after := map[string]Entry{}, map[string]Entry{}
	var want []string // in key order, because the keys are made in it
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
	// The marks do not sort in this order, so the test tells a sort of the
	// lines, which groups by mark, from a sort of the keys.
	if sort.StringsAreSorted(want) {
		t.Fatalf("the test's own order is the order of the lines as strings, so it would not tell the two sorts apart: %q", want[:6])
	}
	first := Diff(before, after)
	if !slices.Equal(first, want) {
		at := 0
		for at < min(len(first), len(want)) && first[at] == want[at] {
			at++
		}
		got, wantAt := "(none)", "(none)"
		if at < len(first) {
			got = first[at]
		}
		if at < len(want) {
			wantAt = want[at]
		}
		t.Fatalf("Diff is not the lines of the %d keys that differ in key order: %d lines, want %d, and they first differ at line %d: got %s, want %s", len(want), len(first), len(want), at, got, wantAt)
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
