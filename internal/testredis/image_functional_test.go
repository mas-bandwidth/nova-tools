//go:build functional

package testredis

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The functional tier of the image: a real redis-server, and Image and Diff
// read against what the server holds. ONE SERVER SERVES THE TESTS OF THE IMAGE
// (docs/TESTING.md: one server per package, not one per test). The tests run
// side by side as subtests of TestImageOnARealStore, whose cleanup stops the
// server after the last of them has ended, and each works under a prefix of
// its own, so none sees another's keys in its image.

const (
	bigImageKeys = 10000 // the keys of the large image
	seedChunk    = 1000  // the keys one seeding pipeline writes
	hsetKeys     = 300   // the keys the HSET test starts from
	twiceKeys    = 600   // the keys the unchanged-store test starts from
	tableKeys    = 5000  // the members of a hash, set and sorted set in the hashtable and skiplist encodings
)

// keyKinds are the types a seeded key can have, in the order seedKeys writes
// them. Every value is small, so every key is in the compact encoding.
var keyKinds = []string{"string", "hash", "list", "set", "zset", "stream"}

// seeded is a key the seeding wrote: its type, and the time it expires in
// milliseconds since the epoch, 0 for a key with no expiry.
type seeded struct {
	kind   string
	expiry int64
}

// writeKey adds to pipe the commands that write a small key of the kind, whose
// content num names.
func writeKey(ctx context.Context, pipe redis.Pipeliner, kind, key, num string) {
	switch kind {
	case "string":
		pipe.Set(ctx, key, "value "+num, 0)
	case "hash":
		pipe.HSet(ctx, key, "one", "a "+num, "two", "b "+num)
	case "list":
		pipe.RPush(ctx, key, "x", "y", num)
	case "set":
		pipe.SAdd(ctx, key, "x", "y", num)
	case "zset":
		pipe.ZAdd(ctx, key, redis.Z{Score: 1, Member: "x"}, redis.Z{Score: 2, Member: num})
	case "stream":
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: key, ID: "1-1", Values: []string{"field", num}})
	}
}

// seedKeys writes n keys under prefix, cycling through keyKinds, and returns
// every key with its type and expiry. The key names its kind and its number,
// and every fourth key expires, at an absolute time an hour ahead that the test
// knows, so the expected sum does not have to ask the server for it.
func seedKeys(ctx context.Context, t *testing.T, c *redis.Client, prefix string, n int) map[string]seeded {
	t.Helper()
	expiryBase := time.Now().Add(time.Hour).UnixMilli()
	written := make(map[string]seeded, n)
	for start := 0; start < n; start += seedChunk {
		pipe := c.Pipeline()
		for i := start; i < min(start+seedChunk, n); i++ {
			kind := keyKinds[i%len(keyKinds)]
			key := prefix + kind + ":" + strconv.Itoa(i)
			writeKey(ctx, pipe, kind, key, strconv.Itoa(i))
			entry := seeded{kind: kind}
			if i%4 == 3 {
				entry.expiry = expiryBase + int64(i)
				pipe.PExpireAt(ctx, key, time.UnixMilli(entry.expiry))
			}
			written[key] = entry
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("seed %q from %d: %v", prefix, start, err)
		}
	}
	return written
}

// oracleImage is what an image of the seeded keys is, worked out without Image
// and without SCAN, TYPE, PEXPIRETIME, HGETALL, SMEMBERS or ZRANGE: the DUMP of
// a string, list or stream, the HSCAN, SSCAN or ZSCAN of a hash, set or sorted
// set, the expiry the seeding set, and the sum written out byte by byte by
// oracleSum from the layout Entry documents. The keys are small, so one page of
// a scan is the whole key.
func oracleImage(ctx context.Context, t *testing.T, c *redis.Client, written map[string]seeded) map[string]Entry {
	t.Helper()
	keys := slices.Sorted(maps.Keys(written))
	image := make(map[string]Entry, len(keys))
	for start := 0; start < len(keys); start += seedChunk {
		chunk := keys[start:min(start+seedChunk, len(keys))]
		pipe := c.Pipeline()
		cmds := make([]*redis.StringCmd, len(chunk))
		scans := make([]*redis.ScanCmd, len(chunk))
		for i, key := range chunk {
			switch written[key].kind {
			case "hash":
				scans[i] = pipe.HScan(ctx, key, 0, "*", seedChunk)
			case "set":
				scans[i] = pipe.SScan(ctx, key, 0, "*", seedChunk)
			case "zset":
				scans[i] = pipe.ZScan(ctx, key, 0, "*", seedChunk)
			default:
				cmds[i] = pipe.Dump(ctx, key)
			}
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("read %d keys from %q for the oracle: %v", len(chunk), chunk[0], err)
		}
		for i, key := range chunk {
			kind, expiry := written[key].kind, int64(-1)
			if written[key].expiry != 0 {
				expiry = written[key].expiry
			}
			var dump string
			var fields map[string]string
			var members []string
			var scores map[string]float64
			if cmds[i] != nil {
				bytes, err := cmds[i].Bytes()
				if err != nil {
					t.Fatalf("dump of %q: %v", key, err)
				}
				dump = string(bytes)
			} else {
				page, cursor, err := scans[i].Result()
				if err != nil || cursor != 0 {
					t.Fatalf("scan of %q: cursor %d, %v; want the whole key in one page", key, cursor, err)
				}
				switch kind {
				case "hash":
					fields = map[string]string{}
					for j := 0; j+1 < len(page); j += 2 {
						fields[page[j]] = page[j+1]
					}
				case "set":
					members = page
				case "zset":
					scores = map[string]float64{}
					for j := 0; j+1 < len(page); j += 2 {
						score, err := strconv.ParseFloat(page[j+1], 64)
						if err != nil {
							t.Fatalf("score %q of %q: %v", page[j+1], key, err)
						}
						scores[page[j]] = score
					}
				}
			}
			image[key] = Entry{Type: kind, Sum: oracleSum(kind, dump, fields, members, scores, expiry)}
		}
	}
	return image
}

// liveImage is the image of the prefix, or the end of the test.
func liveImage(ctx context.Context, t *testing.T, c redis.UniversalClient, prefix string) map[string]Entry {
	t.Helper()
	image, err := Image(ctx, c, prefix)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

// mustDo is a command that has to be answered without an error.
func mustDo(ctx context.Context, t *testing.T, c *redis.Client, args ...any) {
	t.Helper()
	if err := c.Do(ctx, args...).Err(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
}

// encodingOf is the encoding the server holds a key in.
func encodingOf(ctx context.Context, t *testing.T, c *redis.Client, key string) string {
	t.Helper()
	encoding, err := c.ObjectEncoding(ctx, key).Result()
	if err != nil {
		t.Fatalf("object encoding of %q: %v", key, err)
	}
	return encoding
}

// dumpOf is the DUMP of a key, which a test reads to show that two keys that are
// the same content are not the same DUMP.
func dumpOf(ctx context.Context, t *testing.T, c *redis.Client, key string) string {
	t.Helper()
	dump, err := c.Dump(ctx, key).Result()
	if err != nil {
		t.Fatalf("dump of %q: %v", key, err)
	}
	return dump
}

// fillTable writes n members to a hash, set or sorted set one command at a
// time, so the table grows as it does when a program adds to it, in ascending
// order or in descending order.
func fillTable(ctx context.Context, t *testing.T, c *redis.Client, kind, key string, n int, descending bool) {
	t.Helper()
	for start := 0; start < n; start += seedChunk {
		pipe := c.Pipeline()
		for j := start; j < min(start+seedChunk, n); j++ {
			i := j
			if descending {
				i = n - 1 - j
			}
			num := strconv.Itoa(i)
			switch kind {
			case "hash":
				pipe.HSet(ctx, key, "field "+num, "value "+num)
			case "set":
				pipe.SAdd(ctx, key, "member "+num)
			case "zset":
				pipe.ZAdd(ctx, key, redis.Z{Score: float64(i), Member: "member " + num})
			}
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("fill %s %q from %d: %v", kind, key, start, err)
		}
	}
}

// commandLog is a client hook that writes down every command the client sends
// and every pipeline, so a test reads what Image asked the server.
type commandLog struct {
	mu        sync.Mutex
	alone     []string    // the name of each command sent outside a pipeline
	scans     [][2]string // the MATCH and COUNT of each SCAN
	pipelines [][]string  // the names of the commands of each pipeline
}

func (l *commandLog) DialHook(next redis.DialHook) redis.DialHook { return next }

func (l *commandLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		l.mu.Lock()
		name := strings.ToLower(cmd.Name())
		l.alone = append(l.alone, name)
		if name == "scan" {
			var match, count string
			args := cmd.Args()
			for i := 2; i+1 < len(args); i += 2 {
				switch strings.ToLower(fmt.Sprint(args[i])) {
				case "match":
					match = fmt.Sprint(args[i+1])
				case "count":
					count = fmt.Sprint(args[i+1])
				}
			}
			l.scans = append(l.scans, [2]string{match, count})
		}
		l.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (l *commandLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		names := make([]string, len(cmds))
		for i, cmd := range cmds {
			names[i] = strings.ToLower(cmd.Name())
		}
		l.mu.Lock()
		l.pipelines = append(l.pipelines, names)
		l.mu.Unlock()
		return next(ctx, cmds)
	}
}

// sent is how many commands named name the client sent, alone or in a pipeline.
func (l *commandLog) sent(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, got := range l.alone {
		if got == name {
			n++
		}
	}
	for _, names := range l.pipelines {
		for _, got := range names {
			if got == name {
				n++
			}
		}
	}
	return n
}

// pipelinesWith is how many pipelines held a command named name.
func (l *commandLog) pipelinesWith(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, names := range l.pipelines {
		if slices.Contains(names, name) {
			n++
		}
	}
	return n
}

// readPipelines is how many pipelines held a command that reads a key. The
// client's own pipeline on its first connection (CLIENT SETINFO) is not one.
func (l *commandLog) readPipelines() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, names := range l.pipelines {
		if slices.ContainsFunc(names, func(name string) bool { return name == "type" || name == "pexpiretime" || contentNames[name] }) {
			n++
		}
	}
	return n
}

// contentNames are the commands that read the content of a key.
var contentNames = map[string]bool{"dump": true, "hgetall": true, "smembers": true, "zrange": true}

// serverRefused is the error of a write the server refused, which is an answer of
// the server and not a failure to reach it.
func serverRefused(err error) bool {
	var answer redis.Error
	return errors.As(err, &answer)
}

// TestImageOnARealStore holds the image to a real server, in nine tests that
// run side by side against the one server:
//
//	an image of ten thousand keys          every key, its type and its content sum, and no other
//	the diff of one HSET                   names exactly the key the HSET wrote
//	the diff of refused writes             is empty
//	an image of an unchanged store         taken twice, and after reads, is the same image
//	a prefix of glob characters            is a literal
//	a write of a time to live alone        is a change, and a time to live that runs down is not
//	a rewrite in another order or encoding is the same content and the same sum
//	reads of large tables                  change no sum
//	scores                                 are compared to the last bit
func TestImageOnARealStore(t *testing.T) {
	t.Parallel()

	addr := Start(t)

	t.Run("an image of ten thousand keys", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		writer, reader := dial(t, addr), dial(t, addr)
		log := &commandLog{}
		reader.AddHook(log)
		const prefix = "image:ten-thousand:"
		written := seedKeys(ctx, t, writer, prefix, bigImageKeys)
		// Keys that are near the prefix and are not under it.
		strangers := []string{
			"image:ten-thousand",       // the prefix without its colon
			"image:ten-thousands:1",    // a longer word
			"image:ten-thousand-x:1",   // another separator
			"Image:ten-thousand:1",     // another case
			"image:other:1",            // another prefix altogether
			"xx" + prefix + "string:1", // the prefix inside a key
		}
		for _, key := range strangers {
			if err := writer.Set(ctx, key, "a stranger", 0).Err(); err != nil {
				t.Fatal(err)
			}
		}

		got, err := Image(ctx, reader, prefix)
		if err != nil {
			t.Fatal(err)
		}

		// Every key written is in the image with its type and the content sum
		// the oracle worked out another way, expiry included; nothing else is.
		if len(got) != bigImageKeys {
			t.Fatalf("the image holds %d keys; want the %d that were written", len(got), bigImageKeys)
		}
		want := oracleImage(ctx, t, writer, written)
		wrong := 0
		for key, entry := range written {
			have, there := got[key]
			expect := want[key]
			if !there || have != expect {
				if wrong++; wrong <= 5 {
					t.Errorf("%s: the image has type %q and sum %x (there: %v); want type %q and sum %x", key, have.Type, have.Sum[:4], there, entry.kind, expect.Sum[:4])
				}
			}
		}
		if wrong > 0 {
			t.Fatalf("%d of %d entries are wrong", wrong, bigImageKeys)
		}
		for _, key := range strangers {
			if _, there := got[key]; there {
				t.Errorf("the image holds %q, which is not under %q", key, prefix)
			}
		}
		// What the image asked: SCAN and never KEYS, in more than one call, each
		// with the prefix and COUNT; TYPE and PEXPIRETIME of each key, and the
		// content of each by its type, in pipelines of at most imageBatchKeys keys.
		if n := log.sent("keys"); n != 0 {
			t.Errorf("Image sent KEYS %d times", n)
		}
		if n := len(log.scans); n < 2 {
			t.Errorf("Image sent %d SCAN calls for %d keys; want the cursor loop, more than one", n, bigImageKeys)
		}
		for _, scan := range log.scans {
			if scan != [2]string{prefix + "*", strconv.Itoa(imageScanCount)} {
				t.Errorf("a SCAN was MATCH %q COUNT %q; want MATCH %q COUNT %d", scan[0], scan[1], prefix+"*", imageScanCount)
				break
			}
		}
		wantSent := map[string]int{"type": bigImageKeys, "pexpiretime": bigImageKeys}
		for _, entry := range written {
			wantSent[contentCommands[entry.kind]]++
		}
		for name, n := range wantSent {
			if got := log.sent(name); got != n {
				t.Errorf("Image sent %d %s for %d keys; want %d", got, name, bigImageKeys, n)
			}
		}
		batches := (bigImageKeys + imageBatchKeys - 1) / imageBatchKeys
		if got := log.pipelinesWith("type"); got != batches {
			t.Errorf("Image read the kinds of %d keys in %d pipelines; want %d of at most %d keys", bigImageKeys, got, batches, imageBatchKeys)
		}
		if got := log.readPipelines(); got != 2*batches {
			t.Errorf("Image read %d keys in %d pipelines; want %d, two for each of %d batches", bigImageKeys, got, 2*batches, batches)
		}
		for _, names := range log.pipelines {
			if len(names) > 2*imageBatchKeys {
				t.Errorf("a pipeline of Image holds %d commands; want at most %d, for %d keys", len(names), 2*imageBatchKeys, imageBatchKeys)
			}
		}
	})

	t.Run("after one HSET the diff names exactly that key", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c := dial(t, addr)
		const prefix = "image:hset:"
		written := seedKeys(ctx, t, c, prefix, hsetKeys)
		hash := prefix + "hash:1"
		if written[hash].kind != "hash" {
			t.Fatalf("%s is a %q; the test expects a hash", hash, written[hash].kind)
		}
		hset := func(key string) {
			t.Helper()
			if err := c.HSet(ctx, key, "three", "c").Err(); err != nil {
				t.Fatal(err)
			}
		}
		type step struct {
			name  string
			write func()
			want  []string
		}
		before := liveImage(ctx, t, c, prefix)
		if len(before) != hsetKeys {
			t.Fatalf("the image before holds %d keys; want %d", len(before), hsetKeys)
		}
		for _, s := range []step{
			{"a field added to a hash that was there", func() { hset(hash) }, []string{"~" + hash}},
			{"a hash that was not there", func() { hset(prefix + "new") }, []string{"+" + prefix + "new"}},
			{"the same field set to the same value", func() { hset(prefix + "new") }, []string{}},
			{"a field set to another value", func() {
				if err := c.HSet(ctx, prefix+"new", "three", "d").Err(); err != nil {
					t.Fatal(err)
				}
			}, []string{"~" + prefix + "new"}},
			{"a write outside the prefix", func() { hset("image:hset-outside:key") }, []string{}},
			{"a hash deleted", func() {
				if err := c.Del(ctx, hash).Err(); err != nil {
					t.Fatal(err)
				}
			}, []string{"-" + hash}},
		} {
			s.write()
			after := liveImage(ctx, t, c, prefix)
			if got := Diff(before, after); !slices.Equal(got, s.want) {
				t.Fatalf("after %s the diff is %q; want %q", s.name, got, s.want)
			}
			before = after
		}
	})

	t.Run("after a refused write the diff is empty", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c := dial(t, addr)
		const prefix = "image:refused:"
		key := func(kind string) string { return prefix + kind }
		for _, seed := range []error{
			c.Set(ctx, key("string"), "not a number", 0).Err(),
			c.HSet(ctx, key("hash"), "field", "value").Err(),
			c.RPush(ctx, key("list"), "x", "y").Err(),
			c.SAdd(ctx, key("set"), "x", "y").Err(),
			c.ZAdd(ctx, key("zset"), redis.Z{Score: 1, Member: "x"}).Err(),
			c.XAdd(ctx, &redis.XAddArgs{Stream: key("stream"), ID: "5-1", Values: []string{"field", "value"}}).Err(),
		} {
			if seed != nil {
				t.Fatal(seed)
			}
		}
		before := liveImage(ctx, t, c, prefix)
		if len(before) != len(keyKinds) {
			t.Fatalf("the image before holds %d keys; want the %d seeded", len(before), len(keyKinds))
		}

		// Ten writes the server refuses, on every type, each by its reason.
		for _, w := range []struct {
			args []any
			why  string
		}{
			{[]any{"HSET", key("string"), "field", "value"}, "WRONGTYPE"},
			{[]any{"INCR", key("string")}, "not an integer"},
			{[]any{"LPUSH", key("hash"), "x"}, "WRONGTYPE"},
			{[]any{"HINCRBY", key("hash"), "field", "x"}, "not an integer"},
			{[]any{"SADD", key("list"), "x"}, "WRONGTYPE"},
			{[]any{"LSET", key("list"), 99, "x"}, "index out of range"},
			{[]any{"ZADD", key("zset"), "nan", "m"}, "not a valid float"},
			{[]any{"XADD", key("stream"), "5-1", "field", "value"}, "equal or smaller"},
			{[]any{"RENAME", prefix + "missing", prefix + "anywhere"}, "no such key"},
			{[]any{"EXPIRE", key("set"), "soon"}, "not an integer"},
		} {
			err := c.Do(ctx, w.args...).Err()
			if err == nil || !serverRefused(err) || !strings.Contains(err.Error(), w.why) {
				t.Fatalf("%v was answered %v; want the server's refusal, %q", w.args, err, w.why)
			}
		}

		after := liveImage(ctx, t, c, prefix)
		if d := Diff(before, after); len(d) != 0 {
			t.Fatalf("ten refused writes changed the image: %q", d)
		}
		if !maps.Equal(before, after) {
			t.Fatalf("the diff is empty and the images are not equal")
		}

		// The same image hears a write that is not refused: the empty diff
		// above is not an image that cannot see.
		if err := c.HSet(ctx, key("hash"), "field", "another value").Err(); err != nil {
			t.Fatal(err)
		}
		if d := Diff(after, liveImage(ctx, t, c, prefix)); !slices.Equal(d, []string{"~" + key("hash")}) {
			t.Fatalf("after a write that was not refused the diff is %q; want %q", d, []string{"~" + key("hash")})
		}
	})

	t.Run("an image of an unchanged store is the same image", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c, other := dial(t, addr), dial(t, addr)
		const prefix = "image:twice:"
		written := seedKeys(ctx, t, c, prefix, twiceKeys)
		first := liveImage(ctx, t, c, prefix)
		if len(first) != twiceKeys {
			t.Fatalf("the image holds %d keys; want %d", len(first), twiceKeys)
		}
		same := func(what string, image map[string]Entry) {
			t.Helper()
			if d := Diff(first, image); len(d) != 0 {
				t.Fatalf("%s: the diff from the first image is %q; want none", what, d[:min(len(d), 5)])
			}
			if !maps.Equal(first, image) {
				t.Fatalf("%s: the diff is empty and the images are not equal", what)
			}
		}
		same("taken again", liveImage(ctx, t, c, prefix))
		same("taken by another client", liveImage(ctx, t, other, prefix))

		// Reading every key, in every way a test reads one, changes nothing.
		pipe := c.Pipeline()
		for key, entry := range written {
			pipe.Type(ctx, key)
			pipe.Exists(ctx, key)
			pipe.TTL(ctx, key)
			pipe.PTTL(ctx, key)
			pipe.ObjectEncoding(ctx, key)
			pipe.Dump(ctx, key)
			switch entry.kind {
			case "string":
				pipe.Get(ctx, key)
			case "hash":
				pipe.HGetAll(ctx, key)
			case "list":
				pipe.LRange(ctx, key, 0, -1)
			case "set":
				pipe.SMembers(ctx, key)
			case "zset":
				pipe.ZRangeWithScores(ctx, key, 0, -1)
			case "stream":
				pipe.XRange(ctx, key, "-", "+")
			}
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("read every key: %v", err)
		}
		same("after every key was read", liveImage(ctx, t, c, prefix))

		// A write outside the prefix is not in the image.
		if err := c.HSet(ctx, "image:twice-outside:key", "field", "value").Err(); err != nil {
			t.Fatal(err)
		}
		same("after a write outside the prefix", liveImage(ctx, t, c, prefix))
	})

	t.Run("a prefix of glob characters is a literal", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c := dial(t, addr)
		const prefix = `image:glob[1]*?\:`
		mine := []string{prefix + "a", prefix + "b", prefix + "[1]*"}
		// What the prefix would match as a glob: [1] is a 1, * is anything, ?
		// is one character, and \: is a colon.
		globbed := []string{"image:glob1x:a", "image:glob1xyz:b", "image:glob1:c"}
		for _, key := range append(slices.Clone(mine), globbed...) {
			if err := c.Set(ctx, key, "value of "+key, 0).Err(); err != nil {
				t.Fatal(err)
			}
		}
		got := liveImage(ctx, t, c, prefix)
		keys := slices.Sorted(maps.Keys(got))
		if want := slices.Sorted(slices.Values(mine)); !slices.Equal(keys, want) {
			t.Fatalf("the image of %q holds %q; want its own keys, %q", prefix, keys, want)
		}
	})

	t.Run("a write of a time to live alone is a change", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c := dial(t, addr)
		const prefix = "image:ttl:"
		for _, kind := range keyKinds {
			pipe := c.Pipeline()
			writeKey(ctx, pipe, kind, prefix+kind, "1")
			if _, err := pipe.Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		before := liveImage(ctx, t, c, prefix)
		if len(before) != len(keyKinds) {
			t.Fatalf("the image before holds %d keys; want the %d written", len(before), len(keyKinds))
		}
		for _, kind := range keyKinds {
			key := prefix + kind
			expiryOf := func() int64 {
				t.Helper()
				at, err := c.Do(ctx, "pexpiretime", key).Int64()
				if err != nil {
					t.Fatal(err)
				}
				return at
			}
			for _, step := range []struct {
				name  string
				write func()
				want  []string
			}{
				{"an EXPIRE", func() { mustDo(ctx, t, c, "expire", key, 3600) }, []string{"~" + key}},
				{"the expiry it already has, set again", func() { mustDo(ctx, t, c, "pexpireat", key, expiryOf()) }, []string{}},
				{"a PEXPIRE to another time", func() { mustDo(ctx, t, c, "pexpire", key, 7200000) }, []string{"~" + key}},
				{"a time to live that ran down by itself", func() {
					// No write: the clock moves until the server's time to live is less.
					left := c.PTTL(ctx, key).Val()
					for c.PTTL(ctx, key).Val() >= left {
						if err := ctx.Err(); err != nil {
							t.Fatalf("the time to live of %s did not run down: %v", key, err)
						}
					}
				}, []string{}},
				{"a PERSIST", func() { mustDo(ctx, t, c, "persist", key) }, []string{"~" + key}},
				{"a PERSIST of a key with no expiry", func() { mustDo(ctx, t, c, "persist", key) }, []string{}},
				{"an expiry in the past, which deletes it", func() { mustDo(ctx, t, c, "pexpireat", key, 1) }, []string{"-" + key}},
			} {
				step.write()
				after := liveImage(ctx, t, c, prefix)
				if got := Diff(before, after); !slices.Equal(got, step.want) {
					t.Fatalf("%s: after %s the diff is %q; want %q", kind, step.name, got, step.want)
				}
				before = after
			}
		}
	})

	t.Run("a rewrite in another order or encoding is the same content and the same sum", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c := dial(t, addr)
		const prefix = "image:rewrite:"
		hash, set, zset := prefix+"hash", prefix+"set", prefix+"zset"
		write := func(order []string) {
			t.Helper()
			pipe := c.Pipeline()
			for _, m := range order {
				pipe.HSet(ctx, hash, "field "+m, "value "+m)
				pipe.SAdd(ctx, set, "member "+m)
				pipe.ZAdd(ctx, zset, redis.Z{Score: float64(m[0]), Member: "member " + m})
			}
			if _, err := pipe.Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		write([]string{"c", "b", "a"})
		first := liveImage(ctx, t, c, prefix)
		dumps := map[string]string{hash: dumpOf(ctx, t, c, hash), set: dumpOf(ctx, t, c, set), zset: dumpOf(ctx, t, c, zset)}
		for key, want := range map[string]string{hash: "listpack", set: "listpack", zset: "listpack"} {
			if got := encodingOf(ctx, t, c, key); got != want {
				t.Fatalf("%s starts as %s; the test expects %s", key, got, want)
			}
		}

		// The same content, written in the other order.
		mustDo(ctx, t, c, "del", hash, set, zset)
		write([]string{"a", "b", "c"})
		if d := Diff(first, liveImage(ctx, t, c, prefix)); len(d) != 0 {
			t.Fatalf("the same content in another order gave the diff %q; want none", d)
		}
		// A listpack keeps the order it is written in, so the DUMP of a hash and
		// of a set is not the same: a sum of the DUMP would have seen a change.
		for _, key := range []string{hash, set} {
			if dumpOf(ctx, t, c, key) == dumps[key] {
				t.Fatalf("the DUMP of %s is the same in both orders; the test does not tell a sum of the content from a sum of the DUMP", key)
			}
		}

		// The same content again, in another encoding: a member longer than a
		// listpack keeps grows the key into a table, and the key stays one after
		// the member is removed.
		long := strings.Repeat("v", 100)
		for _, step := range [][]any{
			{"hset", hash, "long", long}, {"hdel", hash, "long"},
			{"sadd", set, long}, {"srem", set, long},
			{"zadd", zset, 99, long}, {"zrem", zset, long},
		} {
			mustDo(ctx, t, c, step...)
		}
		for key, want := range map[string]string{hash: "hashtable", set: "hashtable", zset: "skiplist"} {
			if got := encodingOf(ctx, t, c, key); got != want {
				t.Fatalf("%s is %s after it grew and shrank; the test expects %s", key, got, want)
			}
			if dumpOf(ctx, t, c, key) == dumps[key] {
				t.Fatalf("the DUMP of %s is the same in both encodings; the test does not tell a sum of the content from a sum of the DUMP", key)
			}
		}
		if d := Diff(first, liveImage(ctx, t, c, prefix)); len(d) != 0 {
			t.Fatalf("the same content in another encoding gave the diff %q; want none", d)
		}

		// Two tables of the same content, one grown in ascending order and one in
		// descending order, have the same sum: a sum does not name its key.
		for _, kind := range []string{"hash", "set"} {
			up, down := prefix+kind+"-up", prefix+kind+"-down"
			fillTable(ctx, t, c, kind, up, tableKeys/5, false)
			fillTable(ctx, t, c, kind, down, tableKeys/5, true)
			if a, b := encodingOf(ctx, t, c, up), encodingOf(ctx, t, c, down); a != "hashtable" || b != "hashtable" {
				t.Fatalf("the %s tables are %s and %s; the test expects hashtable", kind, a, b)
			}
		}
		image := liveImage(ctx, t, c, prefix)
		for _, kind := range []string{"hash", "set"} {
			up, down := image[prefix+kind+"-up"], image[prefix+kind+"-down"]
			if up.Type != kind || up != down {
				t.Fatalf("the %s grown in ascending order is %v and in descending order %v; want the same entry", kind, up, down)
			}
		}
	})

	t.Run("reads of large tables change no sum", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c := dial(t, addr)
		const prefix = "image:tables:"
		tables := map[string]string{"hash": "hashtable", "set": "hashtable", "zset": "skiplist"}
		for kind, want := range tables {
			key := prefix + kind
			fillTable(ctx, t, c, kind, key, tableKeys, false)
			if got := encodingOf(ctx, t, c, key); got != want {
				t.Fatalf("a %s of %d members is %s; the test expects %s", kind, tableKeys, got, want)
			}
		}
		first := liveImage(ctx, t, c, prefix)
		if len(first) != len(tables) {
			t.Fatalf("the image holds %d keys; want %d", len(first), len(tables))
		}
		if d := Diff(first, liveImage(ctx, t, c, prefix)); len(d) != 0 {
			t.Fatalf("an image taken again gave the diff %q; want none", d)
		}

		// Every member read, one command each, and every table read whole.
		pipe := c.Pipeline()
		for i := 0; i < tableKeys; i++ {
			num := strconv.Itoa(i)
			pipe.HGet(ctx, prefix+"hash", "field "+num)
			pipe.SIsMember(ctx, prefix+"set", "member "+num)
			pipe.ZScore(ctx, prefix+"zset", "member "+num)
		}
		pipe.HGetAll(ctx, prefix+"hash")
		pipe.SMembers(ctx, prefix+"set")
		pipe.ZRangeWithScores(ctx, prefix+"zset", 0, -1)
		pipe.HLen(ctx, prefix+"hash")
		pipe.SCard(ctx, prefix+"set")
		pipe.ZCard(ctx, prefix+"zset")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("read every member: %v", err)
		}
		if d := Diff(first, liveImage(ctx, t, c, prefix)); len(d) != 0 {
			t.Fatalf("after every member of three large tables was read the diff is %q; want none", d)
		}
	})

	t.Run("scores are compared to the last bit", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c := dial(t, addr)
		const prefix = "image:scores:"
		key := prefix + "zset"
		mustDo(ctx, t, c, "zadd", key, "0.1", "a", "inf", "b", "-inf", "c", "1", "d")
		first := liveImage(ctx, t, c, prefix)
		if d := Diff(first, liveImage(ctx, t, c, prefix)); len(d) != 0 {
			t.Fatalf("an image taken again gave the diff %q; want none", d)
		}
		next := strconv.FormatFloat(math.Nextafter(1, 2), 'g', -1, 64) // the score after 1
		for _, step := range []struct {
			name string
			args []any
			want []string
		}{
			{"the same scores set again", []any{"zadd", key, "0.1", "a", "inf", "b", "-inf", "c", "1", "d"}, []string{}},
			{"a score that is the next one after it", []any{"zadd", key, next, "d"}, []string{"~" + key}},
			{"infinity made the largest finite score", []any{"zadd", key, strconv.FormatFloat(math.MaxFloat64, 'g', -1, 64), "b"}, []string{"~" + key}},
		} {
			before := liveImage(ctx, t, c, prefix)
			mustDo(ctx, t, c, step.args...)
			if got := Diff(before, liveImage(ctx, t, c, prefix)); !slices.Equal(got, step.want) {
				t.Fatalf("after %s the diff is %q; want %q", step.name, got, step.want)
			}
		}
	})
}
