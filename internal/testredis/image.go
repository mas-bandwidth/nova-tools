package testredis

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/redis/go-redis/v9"
)

// The image reads a store in bounded pieces: a cursor loop of SCAN calls, then
// the keys it named in batches of bounded size.
const (
	// imageScanCount is the COUNT of every SCAN: how much of the keyspace one
	// call looks through. It is a hint the server may exceed, and it is what
	// keeps a scan of a large store from being one long call.
	imageScanCount = 1000

	// imageBatchKeys is how many keys one batch reads. A batch is two pipelines,
	// TYPE and PEXPIRETIME of every key (two commands each) and then the content
	// of every key that is there (one command each), and a third, when the batch
	// holds a hash, with the HPEXPIRETIME of each hash (one command each, of all
	// the fields of the hash). It bounds the count of keys whose replies are held
	// at once and the time one round trip holds the server. It is a bound on keys
	// and not on bytes: 500 large values are held together and read under one
	// read deadline, and an image is for keys of the size a test writes.
	imageBatchKeys = 500
)

// The marks Diff puts before a key, and the types TYPE answers that the image
// reads by their own command: every other type, and the type "none" a key that
// does not exist has, is read as its DUMP or left out.
const (
	markAdded   = "+"
	markRemoved = "-"
	markChanged = "~"

	typeNone = "none"
	typeHash = "hash"
	typeSet  = "set"
	typeZSet = "zset"
)

// What PEXPIRETIME answers for a key that exists and has no expiry, and for a
// key that does not exist, and what HPEXPIRETIME answers for a field that has no
// expiry, and for a field that does not exist (or a key that does not). Every
// other answer is the expiry time.
const (
	expiryNone  = -1
	expiryNoKey = -2
)

// Entry is what an image holds of one key: the key's TYPE and a sum of its
// content and its expiry times. Two entries are equal when the key has the same
// type, the same content and the same expiry times: the key's, and for a hash
// each field's.
//
// THE SUM IS A CONTENT SUM, NOT THE SHA256 OF THE KEY'S DUMP ALONE. The
// specification of this helper said the SHA256 of DUMP; it is amended to this.
// A DUMP is the server's serialization of a value: it carries the encoding, and
// for a hash or a set in the hashtable encoding the order of the table.
// Reading a key was measured to change that order (HGET and SISMEMBER of every
// field and member of a large hash and set changed the DUMP of both), and a
// value written in another order, or one that grew and shrank back, has another
// DUMP with the same content. So the DUMP of a hash, a set or a sorted set is
// not a fact about what the key holds, and the sum is not of it.
//
// The sum is the SHA256 of these bytes, in this order. Every number is 8 bytes,
// big endian, and a length is the number of bytes of the string after it.
//
//   - The content, by the key's type. A string, a list and a stream, and any
//     type not named here, contribute their DUMP. A hash contributes the number
//     of its fields, then each field, sorted bytewise, as its length, the field,
//     the length of its value and the value. A set contributes the number of its
//     members, then each member, sorted bytewise, as its length and the member. A
//     sorted set contributes the number of its members, then each member, sorted
//     bytewise, as its length, the member and the IEEE 754 bits of its score.
//   - The expiry of each field of a hash, after the content of the hash and in
//     the same order as its fields: HPEXPIRETIME, the time in milliseconds since
//     the Unix epoch at which the field expires, as a signed number, and -1 for a
//     field with no expiry. The other types have no such part.
//   - The key's absolute expiry, in every key's sum: PEXPIRETIME, the time in
//     milliseconds since the Unix epoch at which the key expires, as a signed
//     number, and -1 for a key with no expiry.
//
// So the sum of a hash is the SHA256 of: its field count; each field and its
// value, in field order; the expiry of each field, in field order; and the key's
// expiry. A field's expiry is in neither HGETALL nor PEXPIRETIME, so it has its
// own part, and it is read with one HPEXPIRETIME for each hash.
//
// What follows. A read changes no Sum. A hash, set or sorted set rewritten in
// another order, or in another encoding, with the same content, has the same
// Sum. A write of the expiry alone (EXPIRE, PEXPIRE, PEXPIREAT to another time,
// PERSIST; and on a field of a hash HEXPIRE, HPEXPIRE, HPEXPIREAT, HPERSIST)
// changes the Sum, so a step that wrote nothing but a time to live is seen,
// whether it is the key's or a field's. The expiry is absolute, so a time to
// live that runs down by itself is not a change, and the same absolute time set
// again is not either.
//
// The sum of a string, a list and a stream stays the sum of their DUMP: a write
// that leaves one of them with the same content in another encoding is a change
// too, a difference a test that expects no write should hear about.
type Entry struct {
	Type string   // the key's TYPE: string, list, set, zset, hash or stream
	Sum  [32]byte // the content sum described above
}

// Image is every key of the store under prefix, each with its Entry: the whole
// of what a test needs to prove that a step wrote nothing (Diff of an image
// before it and one after it is empty) or wrote exactly what it should. The
// Entry of a key is its type and a content sum that folds in its expiry time, and
// for a hash each field's (see Entry): a write of a time to live alone is seen,
// and a read, or a rewrite of a hash, set or sorted set in another order, is not.
//
// THE KEYS ARE FOUND BY SCAN, never KEYS. SCAN MATCH prefix*, COUNT 1000,
// follows the cursor until the server says it is done, and every key is read
// once whatever number of times SCAN names it. The prefix is a literal: the
// glob characters in it (* ? [ ] and the backslash) are escaped, so a prefix
// such as "a[1]:" matches keys that begin with those five characters and no
// others. The empty prefix is every key of the database.
//
// THE KEYS ARE READ IN PIPELINES, IN BATCHES OF AT MOST 500. A batch is two
// round trips: TYPE and PEXPIRETIME of every key, then the content of the keys
// that are there, by type (HGETALL of a hash, SMEMBERS of a set, ZRANGE WITHSCORES
// of a sorted set, DUMP of every other type); and a third when the batch holds a
// hash, HPEXPIRETIME of all the fields of each hash. An image of ten thousand
// keys is at most sixty pipelines and the SCAN calls, not thirty thousand round
// trips. A batch is bounded by its count of keys and not by bytes: 500 large
// values are held together and read under one read deadline, so an image is for
// keys of the size a test writes. The server needs PEXPIRETIME (Redis 7.0 or
// later) and, for a hash, HPEXPIRETIME of fields (Redis 7.4 or later).
//
// IT IS AN IMAGE OF A STORE AT REST. The keys are read in several calls, so an
// image taken while another client writes is not a snapshot. A key that is gone
// when its turn to be read comes (deleted, or expired on its own) is not in the
// image. A key that was gone at TYPE and there at PEXPIRETIME, a hash, set or
// sorted set that is not of the type TYPE said when its content is read (the
// server answers WRONGTYPE), and a hash that is not of that type, or that has lost
// a field that HGETALL read, when its fields' expiries are read, were written
// while the image was taken: Image fails and names the key. A key read by DUMP (a
// string, list, stream, or another type) that is replaced by a key of another type
// between the round trips is not found out: its Entry has the type TYPE said and
// the sum of the DUMP that answered.
//
// The image is of the one database the client reads. Image refuses a cluster
// client and a ring, whose SCAN reads one node: an image of part of a store
// proves nothing about the rest. An error is never a partial image: the result
// is the whole map or the error.
func Image(ctx context.Context, c redis.UniversalClient, prefix string) (map[string]Entry, error) {
	return real.image(ctx, c, prefix)
}

func (l launch) image(ctx context.Context, c redis.UniversalClient, prefix string) (map[string]Entry, error) {
	l.refuse()
	if c == nil {
		return nil, errors.New("testredis: image of a nil client")
	}
	switch c.(type) {
	case *redis.ClusterClient, *redis.Ring:
		return nil, fmt.Errorf("testredis: image refuses a %T: SCAN reads one node, and an image of part of a store proves nothing about the rest", c)
	}
	keys, err := scanKeys(ctx, c, prefix)
	if err != nil {
		return nil, fmt.Errorf("testredis: image %q: %w", prefix, err)
	}
	image := make(map[string]Entry, len(keys))
	for len(keys) > 0 {
		n := min(len(keys), imageBatchKeys)
		if err := readEntries(ctx, c, keys[:n], image); err != nil {
			return nil, fmt.Errorf("testredis: image %q: %w", prefix, err)
		}
		keys = keys[n:]
	}
	return image, nil
}

// scanKeys is every key under prefix, each once, in the order SCAN found it.
// A page may be empty and the cursor not zero: the cursor ends the loop, never
// an empty page.
func scanKeys(ctx context.Context, c redis.UniversalClient, prefix string) ([]string, error) {
	pattern := imagePattern(prefix)
	seen := map[string]struct{}{}
	var keys []string
	var cursor uint64
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		page, next, err := c.Scan(ctx, cursor, pattern, imageScanCount).Result()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		for _, key := range page {
			if _, again := seen[key]; !again {
				seen[key] = struct{}{}
				keys = append(keys, key)
			}
		}
		if next == 0 {
			return keys, nil
		}
		cursor = next
	}
}

// imagePattern is the MATCH pattern of the keys that begin with prefix, the
// prefix taken literally.
func imagePattern(prefix string) string {
	var b strings.Builder
	b.Grow(len(prefix) + 1)
	// By byte: every glob character is ASCII, and a key is bytes, so a prefix
	// that is not valid UTF-8 reaches the server as it is written.
	for i := 0; i < len(prefix); i++ {
		if strings.IndexByte(`\*?[]`, prefix[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(prefix[i])
	}
	b.WriteByte('*')
	return b.String()
}

// readEntries reads a batch of keys in two round trips and adds the entries of
// the keys that are there to image.
func readEntries(ctx context.Context, c redis.UniversalClient, keys []string, image map[string]Entry) error {
	there, err := readKinds(ctx, c, keys)
	if err != nil {
		return err
	}
	if len(there) == 0 {
		return nil
	}
	return readContents(ctx, c, there, image)
}

// keyState is what the first round trip learns of a key that is there.
type keyState struct {
	key    string
	kind   string // the key's TYPE
	expiry int64  // the key's PEXPIRETIME: expiryNone, or milliseconds since the epoch
}

// readKinds is the first round trip: TYPE and PEXPIRETIME of every key. It
// answers the keys that are there, in order.
func readKinds(ctx context.Context, c redis.UniversalClient, keys []string) ([]keyState, error) {
	pipe := c.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	expiries := make([]*redis.Cmd, len(keys))
	for i, key := range keys {
		types[i] = pipe.Type(ctx, key)
		expiries[i] = pipe.Do(ctx, "pexpiretime", key)
	}
	if err := execPipeline(ctx, pipe, len(keys)); err != nil {
		return nil, err
	}
	there := make([]keyState, 0, len(keys))
	for i, key := range keys {
		kind, err := types[i].Result()
		if err != nil {
			return nil, fmt.Errorf("type of %q: %w", key, err)
		}
		expiry, err := expiries[i].Int64()
		if err != nil {
			return nil, fmt.Errorf("expiry of %q: %w", key, err)
		}
		switch {
		case expiry == expiryNoKey:
			continue // the key is gone: it expired or was deleted after SCAN named it
		case kind == typeNone:
			return nil, fmt.Errorf("%q was not there for TYPE and was there for PEXPIRETIME: it was written while the image was taken", key)
		}
		there = append(there, keyState{key: key, kind: kind, expiry: expiry})
	}
	return there, nil
}

// contentRead is the command that reads one key's content: the one that is not
// nil, by the key's type.
type contentRead struct {
	dump *redis.StringCmd
	hash *redis.MapStringStringCmd
	set  *redis.StringSliceCmd
	zset *redis.ZSliceCmd
}

// hashRead is a hash the second round trip read and whose fields' expiries the
// third reads: its state, its fields and their values, and the fields' names
// sorted bytewise, the order the third round trip asks for them and the sum
// takes them in.
type hashRead struct {
	key    keyState
	fields map[string]string
	names  []string
}

// readContents is the second round trip: the content of every key the first
// found, each by its type, and from it the entry; and for the hashes among them
// the third. A key whose content is gone is left out.
func readContents(ctx context.Context, c redis.UniversalClient, there []keyState, image map[string]Entry) error {
	pipe := c.Pipeline()
	reads := make([]contentRead, len(there))
	for i, k := range there {
		switch k.kind {
		case typeHash:
			reads[i].hash = pipe.HGetAll(ctx, k.key)
		case typeSet:
			reads[i].set = pipe.SMembers(ctx, k.key)
		case typeZSet:
			reads[i].zset = pipe.ZRangeWithScores(ctx, k.key, 0, -1)
		default:
			reads[i].dump = pipe.Dump(ctx, k.key)
		}
	}
	if err := execPipeline(ctx, pipe, len(there)); err != nil {
		return err
	}
	var hashes []hashRead
	for i, k := range there {
		if reads[i].hash != nil {
			fields, err := reads[i].hash.Result()
			if err != nil {
				return contentError(k, "hgetall", err)
			}
			if len(fields) > 0 { // a hash with no field is not a key of the store
				hashes = append(hashes, hashRead{key: k, fields: fields, names: slices.Sorted(maps.Keys(fields))})
			}
			continue
		}
		sum, present, err := reads[i].sum(k)
		if err != nil {
			return err
		}
		if present {
			image[k.key] = Entry{Type: k.kind, Sum: sum}
		}
	}
	return readFieldExpiries(ctx, c, hashes, image)
}

// readFieldExpiries is the third round trip, for the hashes of the batch: the
// HPEXPIRETIME of every field of each, in the order of the sorted names, and
// from it the entry of the hash. A field's expiry is in neither HGETALL nor the
// key's PEXPIRETIME. A hash that is gone (every field answers that it is not
// there: the key was deleted, or its last fields expired) is left out, as a key
// that is gone is; a hash that has lost some of the fields HGETALL read was
// written while the image was taken.
func readFieldExpiries(ctx context.Context, c redis.UniversalClient, hashes []hashRead, image map[string]Entry) error {
	if len(hashes) == 0 {
		return nil
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.IntSliceCmd, len(hashes))
	for i, h := range hashes {
		cmds[i] = pipe.HPExpireTime(ctx, h.key.key, h.names...)
	}
	if err := execPipeline(ctx, pipe, len(hashes)); err != nil {
		return err
	}
	for i, h := range hashes {
		expiries, err := cmds[i].Result()
		if err != nil {
			return contentError(h.key, "hpexpiretime", err)
		}
		if len(expiries) != len(h.names) {
			return fmt.Errorf("hpexpiretime of %q answered %d expiries for its %d fields", h.key.key, len(expiries), len(h.names))
		}
		missing := -1 // the first field the server says is not there
		gone := 0
		for j, at := range expiries {
			if at == expiryNoKey {
				gone++
				if missing < 0 {
					missing = j
				}
			}
		}
		switch {
		case gone == len(expiries):
			continue // the hash is gone
		case gone > 0:
			return fmt.Errorf("field %q of %q was there for HGETALL and is not there for HPEXPIRETIME: it was written while the image was taken", h.names[missing], h.key.key)
		}
		image[h.key.key] = Entry{Type: typeHash, Sum: sumOfHash(h.names, h.fields, expiries, h.key.expiry)}
	}
	return nil
}

// execPipeline sends the pipeline and answers with its failure as a whole, a
// failure to reach the server. Exec answers with the first command's error, and
// a server's answer to a command (a redis.Error, which redis.Nil is one of: the
// DUMP of a key that is gone) belongs to that command's key, which the caller
// reads from the command. The answer of every command is read there, so an error
// behind a Nil is not lost.
func execPipeline(ctx context.Context, pipe redis.Pipeliner, keys int) error {
	var answer redis.Error
	if _, err := pipe.Exec(ctx); err != nil && !errors.As(err, &answer) {
		return fmt.Errorf("read %d keys: %w", keys, err)
	}
	return nil
}

// sum is the content sum of a key that is not a hash, and whether the key is
// there. A key is not there when its content read found nothing: a DUMP that
// answered Nil, or a set or sorted set with no member, which no key of those types
// is. A hash is summed by readFieldExpiries, which has its fields' expiries.
func (r contentRead) sum(k keyState) ([32]byte, bool, error) {
	var none [32]byte
	switch {
	case r.dump != nil:
		dump, err := r.dump.Bytes()
		switch {
		case errors.Is(err, redis.Nil):
			return none, false, nil
		case err != nil:
			return none, false, contentError(k, "dump", err)
		}
		return sumOfDump(dump, k.expiry), true, nil
	case r.set != nil:
		members, err := r.set.Result()
		if err != nil {
			return none, false, contentError(k, "smembers", err)
		}
		return sumOfSet(members, k.expiry), len(members) > 0, nil
	default:
		scored, err := r.zset.Result()
		if err != nil {
			return none, false, contentError(k, "zrange", err)
		}
		sum, err := sumOfZSet(scored, k.expiry)
		if err != nil {
			return none, false, contentError(k, "zrange", err)
		}
		return sum, len(scored) > 0, nil
	}
}

// contentError is the error of a content read, of the fields' expiries of a hash
// too. A server that refuses to read a key as the type TYPE said it was says the
// key was written in between.
func contentError(k keyState, command string, err error) error {
	var answer redis.Error
	if errors.As(err, &answer) && strings.HasPrefix(answer.Error(), "WRONGTYPE") {
		return fmt.Errorf("%q was a %s for TYPE and the server refuses to read it as one (%w): it was written while the image was taken", k.key, k.kind, err)
	}
	return fmt.Errorf("%s of %q: %w", command, k.key, err)
}

// sumBuilder feeds the pieces of a sum to SHA256 in the layout Entry documents.
type sumBuilder struct{ h hash.Hash }

func newSumBuilder() sumBuilder { return sumBuilder{sha256.New()} }

// number adds n as 8 bytes, big endian.
func (b sumBuilder) number(n uint64) {
	b.h.Write(binary.BigEndian.AppendUint64(nil, n))
}

// text adds a string as its length and then its bytes.
func (b sumBuilder) text(s string) {
	b.number(uint64(len(s)))
	_, _ = io.WriteString(b.h, s)
}

// done adds the key's expiry, the last of every sum, and answers the sum.
func (b sumBuilder) done(expiry int64) (sum [32]byte) {
	b.number(uint64(expiry))
	b.h.Sum(sum[:0])
	return sum
}

func sumOfDump(dump []byte, expiry int64) [32]byte {
	b := newSumBuilder()
	b.h.Write(dump)
	return b.done(expiry)
}

// sumOfHash is the sum of a hash: the fields in the order of names, sorted
// bytewise, each with its value, then the expiry of each field in the same order.
func sumOfHash(names []string, fields map[string]string, fieldExpiries []int64, expiry int64) [32]byte {
	b := newSumBuilder()
	b.number(uint64(len(names)))
	for _, field := range names {
		b.text(field)
		b.text(fields[field])
	}
	for _, at := range fieldExpiries {
		b.number(uint64(at))
	}
	return b.done(expiry)
}

func sumOfSet(members []string, expiry int64) [32]byte {
	b := newSumBuilder()
	b.number(uint64(len(members)))
	for _, member := range slices.Sorted(slices.Values(members)) {
		b.text(member)
	}
	return b.done(expiry)
}

func sumOfZSet(scored []redis.Z, expiry int64) ([32]byte, error) {
	scores := make(map[string]float64, len(scored))
	for _, z := range scored {
		member, ok := z.Member.(string)
		if !ok {
			return [32]byte{}, fmt.Errorf("a member that is a %T and not a string", z.Member)
		}
		scores[member] = z.Score
	}
	b := newSumBuilder()
	b.number(uint64(len(scores)))
	for _, member := range slices.Sorted(maps.Keys(scores)) {
		b.text(member)
		b.number(math.Float64bits(scores[member]))
	}
	return b.done(expiry), nil
}

// Diff is what changed between two images, as one line per key: "+key" for a
// key the second image has and the first does not, "-key" for a key the first
// has and the second does not, "~key" for a key both have whose Entry is not
// the same, that is whose type, content, expiry time or (for a hash) fields'
// expiry times are not. The lines are in key order, sorted bytewise by the key,
// each with its mark in front, so the lines of added, removed and changed keys
// are interleaved as their keys are and a test reads them by key. It is empty,
// and not nil, when the images are equal, and a nil image is an empty one.
//
// A read and a rewrite of a hash, set or sorted set with the same content are
// not changes (see Entry). The sum of a string, a list and a stream is of their
// DUMP, so a write that leaves one of those with the same content in another
// encoding is a change: a difference a test that expects no write should hear
// about.
func Diff(before, after map[string]Entry) []string {
	marks := map[string]string{}
	for key, was := range before {
		now, there := after[key]
		switch {
		case !there:
			marks[key] = markRemoved
		case now != was:
			marks[key] = markChanged
		}
	}
	for key := range after {
		if _, there := before[key]; !there {
			marks[key] = markAdded
		}
	}
	lines := []string{}
	for _, key := range slices.Sorted(maps.Keys(marks)) {
		lines = append(lines, marks[key]+key)
	}
	return lines
}
