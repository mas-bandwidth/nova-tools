//go:build functional

package testredis

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

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
)

// keyKinds are the types a seeded key can have, in the order seedKeys writes
// them. Every value is small, so every key is in the compact encoding, which
// serializes the same way every time it is read.
var keyKinds = []string{"string", "hash", "list", "set", "zset", "stream"}

// seedKeys writes n keys under prefix, cycling through keyKinds, and returns
// every key with its type. The key names its kind and its number.
func seedKeys(ctx context.Context, t *testing.T, c *redis.Client, prefix string, n int) map[string]string {
	t.Helper()
	written := make(map[string]string, n)
	for start := 0; start < n; start += seedChunk {
		pipe := c.Pipeline()
		for i := start; i < min(start+seedChunk, n); i++ {
			kind := keyKinds[i%len(keyKinds)]
			key := prefix + kind + ":" + strconv.Itoa(i)
			num := strconv.Itoa(i)
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
			written[key] = kind
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("seed %q from %d: %v", prefix, start, err)
		}
	}
	return written
}

// dumpSums are the SHA256 of the DUMP of each key, read without SCAN and
// without TYPE: the oracle Image's sums are held to.
func dumpSums(ctx context.Context, t *testing.T, c *redis.Client, keys []string) map[string][32]byte {
	t.Helper()
	sums := make(map[string][32]byte, len(keys))
	for start := 0; start < len(keys); start += seedChunk {
		chunk := keys[start:min(start+seedChunk, len(keys))]
		pipe := c.Pipeline()
		cmds := make([]*redis.StringCmd, len(chunk))
		for i, key := range chunk {
			cmds[i] = pipe.Dump(ctx, key)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatalf("dump %d keys from %q: %v", len(chunk), chunk[0], err)
		}
		for i, key := range chunk {
			dump, err := cmds[i].Bytes()
			if err != nil {
				t.Fatalf("dump of %q: %v", key, err)
			}
			sums[key] = sha256.Sum256(dump)
		}
	}
	return sums
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

// refusal is the error of a write the server refused, which is an answer of
// the server and not a failure to reach it.
func refusal(err error) bool {
	var answer redis.Error
	return errors.As(err, &answer)
}

// TestImageOnARealStore holds the image to a real server, in five tests that
// run side by side against the one server:
//
//	an image of ten thousand keys          every key, its type and the SHA256 of its DUMP, and no other
//	the diff of one HSET                   names exactly the key the HSET wrote
//	the diff of refused writes             is empty
//	an image of an unchanged store         taken twice, and after reads, is the same image
//	a prefix of glob characters            is a literal
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

		// Every key written is in the image with its type and the SHA256 of its
		// DUMP, read another way; nothing else is.
		if len(got) != bigImageKeys {
			t.Fatalf("the image holds %d keys; want the %d that were written", len(got), bigImageKeys)
		}
		sums := dumpSums(ctx, t, writer, slices.Collect(maps.Keys(written)))
		wrong := 0
		for key, kind := range written {
			entry, there := got[key]
			want := sums[key]
			if !there || entry.Type != kind || entry.Sum != want {
				if wrong++; wrong <= 5 {
					t.Errorf("%s: the image has type %q and sum %x (there: %v); want type %q and sum %x", key, entry.Type, entry.Sum[:4], there, kind, want[:4])
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
		// with the prefix and COUNT; and TYPE and DUMP of each key, in pipelines.
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
		if n := log.sent("type"); n != bigImageKeys {
			t.Errorf("Image sent %d TYPE for %d keys", n, bigImageKeys)
		}
		if n := log.sent("dump"); n != bigImageKeys {
			t.Errorf("Image sent %d DUMP for %d keys", n, bigImageKeys)
		}
		if want, got := (bigImageKeys+imageBatchKeys-1)/imageBatchKeys, log.pipelinesWith("dump"); got != want {
			t.Errorf("Image read %d keys in %d pipelines; want %d of at most %d keys", bigImageKeys, got, want, imageBatchKeys)
		}
		for _, names := range log.pipelines {
			if slices.Contains(names, "dump") && len(names) != 2*imageBatchKeys {
				t.Errorf("a pipeline of Image holds %d commands; want %d, TYPE and DUMP of %d keys", len(names), 2*imageBatchKeys, imageBatchKeys)
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
		if written[hash] != "hash" {
			t.Fatalf("%s is a %q; the test expects a hash", hash, written[hash])
		}
		imaged := func() map[string]Entry {
			t.Helper()
			image, err := Image(ctx, c, prefix)
			if err != nil {
				t.Fatal(err)
			}
			return image
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
		before := imaged()
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
			after := imaged()
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
		imaged := func() map[string]Entry {
			t.Helper()
			image, err := Image(ctx, c, prefix)
			if err != nil {
				t.Fatal(err)
			}
			return image
		}
		before := imaged()
		if len(before) != len(keyKinds) {
			t.Fatalf("the image before holds %d keys; want the %d seeded", len(before), len(keyKinds))
		}

		// Nine writes the server refuses, on every type, each by its reason.
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
		} {
			err := c.Do(ctx, w.args...).Err()
			if err == nil || !refusal(err) || !strings.Contains(err.Error(), w.why) {
				t.Fatalf("%v was answered %v; want the server's refusal, %q", w.args, err, w.why)
			}
		}

		after := imaged()
		if d := Diff(before, after); len(d) != 0 {
			t.Fatalf("nine refused writes changed the image: %q", d)
		}
		if !maps.Equal(before, after) {
			t.Fatalf("the diff is empty and the images are not equal")
		}

		// The same image hears a write that is not refused: the empty diff
		// above is not an image that cannot see.
		if err := c.HSet(ctx, key("hash"), "field", "another value").Err(); err != nil {
			t.Fatal(err)
		}
		if d := Diff(after, imaged()); !slices.Equal(d, []string{"~" + key("hash")}) {
			t.Fatalf("after a write that was not refused the diff is %q; want %q", d, []string{"~" + key("hash")})
		}
	})

	t.Run("an image of an unchanged store is the same image", func(t *testing.T) {
		t.Parallel()

		ctx := bounded(t)
		c, other := dial(t, addr), dial(t, addr)
		const prefix = "image:twice:"
		written := seedKeys(ctx, t, c, prefix, twiceKeys)
		imaged := func(c *redis.Client) map[string]Entry {
			t.Helper()
			image, err := Image(ctx, c, prefix)
			if err != nil {
				t.Fatal(err)
			}
			return image
		}
		first := imaged(c)
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
		same("taken again", imaged(c))
		same("taken by another client", imaged(other))

		// Reading every key, in every way a test reads one, changes nothing.
		pipe := c.Pipeline()
		for key, kind := range written {
			pipe.Type(ctx, key)
			pipe.Exists(ctx, key)
			pipe.TTL(ctx, key)
			pipe.ObjectEncoding(ctx, key)
			pipe.Dump(ctx, key)
			switch kind {
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
		same("after every key was read", imaged(c))

		// A write outside the prefix is not in the image.
		if err := c.HSet(ctx, "image:twice-outside:key", "field", "value").Err(); err != nil {
			t.Fatal(err)
		}
		same("after a write outside the prefix", imaged(c))
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
		got, err := Image(ctx, c, prefix)
		if err != nil {
			t.Fatal(err)
		}
		keys := slices.Sorted(maps.Keys(got))
		if want := slices.Sorted(slices.Values(mine)); !slices.Equal(keys, want) {
			t.Fatalf("the image of %q holds %q; want its own keys, %q", prefix, keys, want)
		}
	})
}
