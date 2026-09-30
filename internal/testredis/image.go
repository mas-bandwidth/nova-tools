package testredis

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

// The image reads a store in bounded pieces: a cursor loop of SCAN calls, then
// the keys it named in pipelines of bounded size.
const (
	// imageScanCount is the COUNT of every SCAN: how much of the keyspace one
	// call looks through. It is a hint the server may exceed, and it is what
	// keeps a scan of a large store from being one long call.
	imageScanCount = 1000

	// imageBatchKeys is how many keys one pipeline reads, two commands each
	// (TYPE and DUMP). It bounds the memory of the replies held at once and the
	// time one round trip holds the server.
	imageBatchKeys = 500
)

// The marks Diff puts before a key, and the type TYPE answers for a key that
// does not exist.
const (
	markAdded   = "+"
	markRemoved = "-"
	markChanged = "~"

	typeNone = "none"
)

// Entry is what an image holds of one key: the key's TYPE and the SHA256 of its
// DUMP. Two entries are equal when the key has the same type and the same
// serialized value. The DUMP carries the value and its encoding; it does not
// carry the key's time to live, so a change of expiry alone is not a change of
// the entry.
type Entry struct {
	Type string   // the key's TYPE: string, list, set, zset, hash or stream
	Sum  [32]byte // the SHA256 of the key's DUMP
}

// Image is every key of the store under prefix, each with its Entry: the whole
// of what a test needs to prove that a step wrote nothing (Diff of an image
// before it and one after it is empty) or wrote exactly what it should.
//
// THE KEYS ARE FOUND BY SCAN, never KEYS. SCAN MATCH prefix*, COUNT 1000,
// follows the cursor until the server says it is done, and every key is read
// once whatever number of times SCAN names it. The prefix is a literal: the
// glob characters in it (* ? [ ] and the backslash) are escaped, so a prefix
// such as "a[1]:" matches keys that begin with those five characters and no
// others. The empty prefix is every key of the database.
//
// THE KEYS ARE READ IN PIPELINES. TYPE and DUMP of up to 500 keys go in one
// round trip, so an image of ten thousand keys is twenty pipelines and the
// SCAN calls, not twenty thousand round trips.
//
// IT IS AN IMAGE OF A STORE AT REST. The keys are read in several calls, so an
// image taken while another client writes is not a snapshot. A key that is gone
// when its turn to be read comes (deleted, or expired on its own) is not in the
// image. A key that was gone at TYPE and there at DUMP was written while the
// image was taken, and Image fails and names it.
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

// readEntries reads TYPE and DUMP of every key in one pipeline and adds the
// entries of the keys that are there to image.
func readEntries(ctx context.Context, c redis.UniversalClient, keys []string, image map[string]Entry) error {
	pipe := c.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	dumps := make([]*redis.StringCmd, len(keys))
	for i, key := range keys {
		types[i] = pipe.Type(ctx, key)
		dumps[i] = pipe.Dump(ctx, key)
	}
	// Exec answers with the first command's error, and redis.Nil (a DUMP of a
	// key that is gone) is a normal answer here. The answer of every command is
	// read below, so an error behind a Nil is not lost; this one is for a
	// pipeline that failed as a whole.
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("read %d keys: %w", len(keys), err)
	}
	for i, key := range keys {
		kind, err := types[i].Result()
		if err != nil {
			return fmt.Errorf("type of %q: %w", key, err)
		}
		dump, err := dumps[i].Bytes()
		switch {
		case errors.Is(err, redis.Nil):
			continue // the key is gone: it expired or was deleted after SCAN named it
		case err != nil:
			return fmt.Errorf("dump of %q: %w", key, err)
		case kind == typeNone:
			return fmt.Errorf("%q was not there for TYPE and was there for DUMP: it was written while the image was taken", key)
		}
		image[key] = Entry{Type: kind, Sum: sha256.Sum256(dump)}
	}
	return nil
}

// Diff is what changed between two images, as one line per key: "+key" for a
// key the second image has and the first does not, "-key" for a key the first
// has and the second does not, "~key" for a key both have whose Entry is not
// the same. The lines are sorted as strings, so the added keys come first, then
// the removed, then the changed, each group in key order. It is empty, and not
// nil, when the images are equal, and a nil image is an empty one.
//
// Diff compares serialized values, so a write that leaves a key with the same
// content and a different encoding is a change too: it is a difference a test
// that expects no write should hear about.
func Diff(before, after map[string]Entry) []string {
	lines := []string{}
	for key, was := range before {
		now, there := after[key]
		switch {
		case !there:
			lines = append(lines, markRemoved+key)
		case now != was:
			lines = append(lines, markChanged+key)
		}
	}
	for key := range after {
		if _, there := before[key]; !there {
			lines = append(lines, markAdded+key)
		}
	}
	sort.Strings(lines)
	return lines
}
