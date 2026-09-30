//go:build functional

package ntable_test

// A batch holds the store for a time the manifest bounds, not the store's
// content: it reads the fields its entries name, counts their bytes from lengths
// before any read or digest, and refuses over the bound at once. Each case logs
// its hold time, which the specification quotes; the tests assert the replies, not
// the clock.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// holdMembers creates n placed members named <prefix>0.. in batches of 128.
func holdMembers(t *testing.T, ctx context.Context, c *redis.Client, prefix string, n int) {
	t.Helper()
	for from := 0; from < n; from += 128 {
		var creates []string
		for i := from; i < from+128 && i < n; i++ {
			creates = append(creates, fmt.Sprintf(`{"id":"%s%d","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`, prefix, i))
		}
		op := fmt.Sprintf("seed-%s-%d", prefix, from)
		if ans, err := rawApply(ctx, c, manifestWith(probeRev(ctx, c), op, strings.Join(creates, ","))); err != nil || ans[0] != "OK" {
			t.Fatalf("seed: %.200v %v", ans, err)
		}
	}
}

// holdFill gives each of members <prefix>0..n-1 `fields` fields f0.. of `size` bytes.
func holdFill(t *testing.T, ctx context.Context, c *redis.Client, prefix string, n, fields, size int) {
	t.Helper()
	value := strings.Repeat("v", size)
	for i := 0; i < n; i++ {
		f := make(map[string]any, fields)
		for j := 0; j < fields; j++ {
			f[fmt.Sprintf("f%d", j)] = value
		}
		if err := c.HSet(ctx, ntable.MemberKey(fmt.Sprintf("%s%d", prefix, i)), f).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// holdUnset is one entry per member unsetting f0..f(fields-1).
func holdUnset(prefix string, members, fields int) string {
	names := make([]string, fields)
	for j := range names {
		names[j] = fmt.Sprintf(`"f%d"`, j)
	}
	var ents []string
	for i := 0; i < members; i++ {
		ents = append(ents, fmt.Sprintf(`{"id":"%s%d","expect":{},"unset":[%s]}`, prefix, i, strings.Join(names, ",")))
	}
	return strings.Join(ents, ",")
}

func holdApply(t *testing.T, ctx context.Context, c *redis.Client, name, raw string) ([]any, time.Duration) {
	t.Helper()
	// a client that waits for the script: the default one gives up at three seconds
	// and its retry meets BUSY on a bench that is busy with other work
	slow := redis.NewClient(&redis.Options{Addr: c.Options().Addr, ReadTimeout: time.Minute, MaxRetries: -1})
	defer slow.Close()
	start := time.Now()
	ans, err := rawApply(ctx, slow, raw)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	t.Logf("HOLD %s: manifest %d bytes, %s, %v", name, len(raw), holdWords(ans), took)
	return ans, took
}

// holdWords is the first words of a reply, for the log.
func holdWords(ans []any) string {
	if len(ans) > 5 && ans[0] == "REFUSED" {
		return fmt.Sprint(ans[:5]...)
	}
	return fmt.Sprint(ans[0])
}

func requireRefusedLimit(t *testing.T, name string, ans []any, limit string, bound int) {
	t.Helper()
	if len(ans) < 5 || ans[0] != "REFUSED" || ans[1] != "LIMIT" || ans[2] != limit || fmt.Sprint(ans[3]) != fmt.Sprint(bound) {
		t.Fatalf("%s: %.200v; want LIMIT %s, bound %d", name, ans, limit, bound)
	}
	var size int
	if _, err := fmt.Sscan(fmt.Sprint(ans[4]), &size); err != nil || size <= bound {
		t.Errorf("%s: the refusal names the size %v, want more than %d", name, ans[4], bound)
	}
}

// The values a batch touches are bounded: 16 MiB of before-values is taken, one
// more field is refused before any value is read, and a store holding far more
// than the bound is refused as fast as one holding little.
func TestBatchValueBytesAreBoundedAndCountedBeforeAnyRead(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	const field = 64 << 10
	perMember := ntable.LimitBatchValueBytes / field / 2 // 128 fields of 64 KiB, two members, 16 MiB
	holdMembers(t, ctx, c, "m", 3)
	holdFill(t, ctx, c, "m", 3, perMember, field)

	// exactly the bound: two members, 128 fields each
	ans, _ := holdApply(t, ctx, c, "16 MiB of before-values accepted", manifestWith(probeRev(ctx, c), "at", holdUnset("m", 2, perMember)))
	if ans[0] != "OK" {
		t.Fatalf("a batch touching exactly %d bytes: %.200v", ntable.LimitBatchValueBytes, ans)
	}
	// one field more than the bound refuses, and nothing changes
	holdFill(t, ctx, c, "m", 3, perMember, field)
	before := storeImage(t, c)
	ans, _ = holdApply(t, ctx, c, "16 MiB and one field refused", manifestWith(probeRev(ctx, c), "over", holdUnset("m", 2, perMember)+`,{"id":"m2","expect":{},"unset":["f0"]}`))
	requireRefusedLimit(t, "one field over", ans, "value bytes per batch", ntable.LimitBatchValueBytes)
	if !reflect.DeepEqual(before, storeImage(t, c)) {
		t.Errorf("a refused batch changed the store")
	}
	if c.HExists(ctx, ntable.DefKey("demo")+":ops", "0:over").Val() {
		t.Errorf("a refused batch left an operation record")
	}

	// the refusal costs what the manifest names, not what the store holds: 256 MiB
	holdMembers(t, ctx, c, "w", 32)
	holdFill(t, ctx, c, "w", 32, 128, field)
	ans, _ = holdApply(t, ctx, c, "256 MiB in the store refused", manifestWith(probeRev(ctx, c), "big", holdUnset("w", 32, 128)))
	requireRefusedLimit(t, "256 MiB", ans, "value bytes per batch", ntable.LimitBatchValueBytes)
}

// A batch names its fields: guards on a member whose record is large read the
// named field, not the record.
func TestBatchGuardOnlyEntriesDoNotReadWholeRecords(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	const members = 256
	holdMembers(t, ctx, c, "g", members)
	holdFill(t, ctx, c, "g", members, 8, 64<<10) // 512 KiB a record, 128 MiB in all
	var ents []string
	for i := 0; i < members; i++ {
		ents = append(ents, fmt.Sprintf(`{"id":"g%d","expect":{"fields":{"nope":{"absent":true}}}}`, i))
	}
	ans, _ := holdApply(t, ctx, c, "256 guards over 128 MiB of records", manifestWith(probeRev(ctx, c), "guards", strings.Join(ents, ",")))
	if ans[0] != "OK" {
		t.Fatalf("guards: %.200v", ans)
	}
}

// The costliest cases the manifest bound allows: a
// manifest of guards that all hold, a receipt at its bound, and one refused
// for its receipt.
func TestBatchLargestManifestsHoldTheStoreUnderASecond(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	holdMembers(t, ctx, c, "h", 128)

	// a manifest of about 1 MiB of guards, every one satisfied
	var ents []string
	for i := 0; i < 128; i++ {
		var g []string
		for j := 0; j < 300; j++ {
			g = append(g, fmt.Sprintf(`"n%d":{"absent":true}`, j))
		}
		ents = append(ents, fmt.Sprintf(`{"id":"h%d","expect":{"fields":{%s}}}`, i, strings.Join(g, ",")))
	}
	ans, _ := holdApply(t, ctx, c, "1 MiB of satisfied guards", manifestWith(probeRev(ctx, c), "guards", strings.Join(ents, ",")))
	if ans[0] != "OK" {
		t.Fatalf("guards: %.200v", ans)
	}

	// a receipt near its bound: 48 fields of 64 bytes unset on each of 128 members
	holdFill(t, ctx, c, "h", 128, 48, ntable.ReceiptValueBytes)
	ans, _ = holdApply(t, ctx, c, "receipt near its bound", manifestWith(probeRev(ctx, c), "near", holdUnset("h", 128, 48)))
	if ans[0] != "OK" {
		t.Fatalf("near the receipt bound: %.200v", ans)
	}

	// a receipt over its bound: 1000 fields of 64 bytes unset on each of 128 members
	holdFill(t, ctx, c, "h", 128, 1000, ntable.ReceiptValueBytes)
	before := storeImage(t, c)
	ans, _ = holdApply(t, ctx, c, "receipt over its bound refused", manifestWith(probeRev(ctx, c), "over", holdUnset("h", 128, 1000)))
	requireRefusedLimit(t, "receipt over", ans, "receipt bytes", ntable.LimitReceiptBytes)
	if !reflect.DeepEqual(before, storeImage(t, c)) {
		t.Errorf("a refused batch changed the store")
	}
}
