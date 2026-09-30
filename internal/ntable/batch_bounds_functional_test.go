//go:build functional

package ntable_test

// One set of bounds: the server (table.lua), the Go validator and ApplyBatch
// agree on every number. At the bound a manifest is accepted; one over it is
// refused by name, with the bound and the count found, the store is untouched,
// and no part of the input is echoed.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func joinN(n int, f func(i int) string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = f(i)
	}
	return strings.Join(parts, ",")
}

type boundCase struct {
	name   string
	bound  int
	limit  string // the name of the limit as refused
	member string // the member named in the refusal, if any
	// build returns the members array body for n units of the bounded thing.
	build func(n int) string
	// actor pads the manifest (manifest bytes only).
	actorPad func(n int) string
}

const echoMarker = "zzq"

func boundCases() []boundCase {
	return []boundCase{
		{name: "entries with changes", bound: ntable.LimitChangedEntries, limit: "entries with changes",
			build: func(n int) string {
				return joinN(n, func(i int) string {
					return fmt.Sprintf(`{"id":"%sc%d","expect":{"absent":true},"create":{"row":"build","col":"ready","score":%d}}`, echoMarker, i, i)
				})
			}},
		{name: "guard-only entries", bound: ntable.LimitGuardEntries, limit: "guard-only entries",
			build: func(n int) string {
				return joinN(n, func(i int) string { return fmt.Sprintf(`{"id":"%sg%d","expect":{"absent":true}}`, echoMarker, i) })
			}},
		{name: "member id bytes", bound: ntable.LimitMemberIDBytes, limit: "member id bytes",
			build: func(n int) string {
				return `{"id":"` + strings.Repeat("i", n) + `","expect":{"absent":true}}`
			}},
		{name: "field value bytes", bound: ntable.LimitFieldValueBytes, limit: "field value bytes", member: "v",
			build: func(n int) string {
				return `{"id":"v","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1},"set":{"k":"` + strings.Repeat("v", n) + `"}}`
			}},
		{name: "set fields per member", bound: ntable.LimitSetFields, limit: "set fields per member", member: "b",
			build: func(n int) string {
				return `{"id":"b","expect":{},"set":{` + joinN(n, func(i int) string { return fmt.Sprintf(`"%ss%d":"v"`, echoMarker, i) }) + `}}`
			}},
		{name: "unset fields per member", bound: ntable.LimitUnsetFields, limit: "unset fields per member", member: "a",
			build: func(n int) string {
				return `{"id":"a","expect":{},"unset":[` + joinN(n, func(i int) string { return fmt.Sprintf(`"%su%d"`, echoMarker, i) }) + `]}`
			}},
		{name: "guards per member", bound: ntable.LimitFieldGuards, limit: "guards per member", member: "b",
			build: func(n int) string {
				return `{"id":"b","expect":{"fields":{` + joinN(n, func(i int) string { return fmt.Sprintf(`"%sg%d":{"absent":true}`, echoMarker, i) }) + `}}}`
			}},
		{name: "one_of options", bound: ntable.LimitOneOfOptions, limit: "one_of options", member: "a",
			build: func(n int) string {
				// the last option matches the field, so a manifest at the bound passes its guard
				return `{"id":"a","expect":{"fields":{"role":{"one_of":[` + joinN(n, func(i int) string {
					if i == n-1 {
						return `"x"`
					}
					return fmt.Sprintf(`"%so%d"`, echoMarker, i)
				}) + `]}}}}`
			}},
	}
}

func boundsManifest(rev, op, members, actor string) string {
	return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"` + op + `","actor":"` + actor + `","members":[` + members + `]}`
}

func replyNumber(v any) string { return fmt.Sprint(v) }

func TestBatchBoundsAreOneSet(t *testing.T) {
	t.Parallel()
	for _, bc := range boundCases() {
		bc := bc
		t.Run(bc.name, func(t *testing.T) {
			t.Parallel()
			c, ctx := probeTable(t)
			seedTwo(t, ctx, c)

			// At the bound: the server accepts, the validator accepts.
			raw := boundsManifest(probeRev(ctx, c), "at", bc.build(bc.bound), "p")
			if _, err := ntable.ValidateBatchManifestRaw([]byte(raw)); err != nil {
				t.Fatalf("at the bound %d the Go validator refuses: %v", bc.bound, err)
			}
			ans, err := rawApply(ctx, c, raw)
			if err != nil || len(ans) == 0 || ans[0] != "OK" {
				t.Fatalf("at the bound %d the server refuses: %v %v", bc.bound, trunc(ans), err)
			}

			// One over: refused on every path with the same name, bound and count.
			raw = boundsManifest(probeRev(ctx, c), "over", bc.build(bc.bound+1), "p")
			before := storeImage(t, c)

			ans, err = rawApply(ctx, c, raw)
			require.NoError(t, err, "raw FCALL")
			if len(ans) < 5 || ans[0] != "REFUSED" || ans[1] != "LIMIT" {
				t.Fatalf("raw FCALL one over the bound: %v", trunc(ans))
			}
			if ans[2] != bc.limit || replyNumber(ans[3]) != fmt.Sprint(bc.bound) || replyNumber(ans[4]) != fmt.Sprint(bc.bound+1) {
				t.Errorf("raw FCALL refusal %v; want %s, bound %d, observed %d", trunc(ans), bc.limit, bc.bound, bc.bound+1)
			}
			if bc.member != "" && (len(ans) < 6 || ans[5] != bc.member) {
				t.Errorf("raw FCALL refusal does not name member %q: %v", bc.member, trunc(ans))
			}
			if bc.member == "" && len(ans) > 5 {
				t.Errorf("raw FCALL refusal names a member for a whole-manifest bound: %v", trunc(ans))
			}

			_, verr := ntable.ValidateBatchManifestRaw([]byte(raw))
			var le *ntable.LimitError
			require.ErrorAs(t, verr, &le, "Go validator one over the bound: %v", verr)
			require.ErrorIs(t, verr, ntable.ErrLimit, "Go validator one over the bound: %v", verr)
			if le.Name != bc.limit || le.Bound != bc.bound || le.Observed != bc.bound+1 || le.Member != bc.member {
				t.Errorf("Go validator refusal %+v; want %s, bound %d, observed %d, member %q", le, bc.limit, bc.bound, bc.bound+1, bc.member)
			}

			var m ntable.BatchManifest
			require.NoError(t, json.Unmarshal([]byte(raw), &m))
			_, aerr := ntable.ApplyBatch(ctx, c, m)
			require.ErrorIs(t, aerr, ntable.ErrLimit, "ApplyBatch one over the bound")
			for _, w := range []string{bc.limit, fmt.Sprintf("bound %d, observed %d", bc.bound, bc.bound+1), "changed=no", "; run: nova-table"} {
				assert.ErrorContains(t, aerr, w, "ApplyBatch refusal lacks %q: %s", w, aerr)
			}
			for _, e := range []error{verr, aerr} {
				if strings.Contains(e.Error(), echoMarker) || (bc.name == "member id bytes" && strings.Contains(e.Error(), "iiii")) || strings.Contains(e.Error(), "vvvv") {
					t.Errorf("a refusal echoes the input: %.200s", e)
				}
			}
			assert.NotContains(t, fmt.Sprint(ans), echoMarker, "the server refusal echoes the input: %.200s", fmt.Sprint(ans))
			assert.Equal(t, before, storeImage(t, c), "a refused over-bound manifest changed the store")
		})
	}
}

// The manifest is bounded by its bytes: a 1 MiB request passes, one byte more
// is refused on every path.
func TestBatchManifestBytesBound(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	// The padding is in a guard's options, which no receipt records: the actor is
	// echoed in the receipt, so a manifest padded with it can exceed the receipt bound.
	build := func(rev, op string, size int) string {
		entry := func(pad string) string {
			return `{"id":"a","expect":{"fields":{"role":{"one_of":["x"` + pad + `]}}}}`
		}
		need := size - len(boundsManifest(rev, op, entry(""), ""))
		// k distinct options, sharing what is left of the size between them
		k := (need + 59999) / 60000
		total := need - 3*k // each option is , " n bytes "
		pad := ""
		for i := 0; i < k; i++ {
			n := total / k
			if i < total%k {
				n++
			}
			pad += `,"` + fmt.Sprintf("%04d", i) + strings.Repeat("p", n-4) + `"`
		}
		return boundsManifest(rev, op, entry(pad), "")
	}
	rev := probeRev(ctx, c)
	at := build(rev, "at", ntable.LimitManifestBytes)
	require.Len(t, at, ntable.LimitManifestBytes, "built %d bytes", len(at))
	if _, err := ntable.ValidateBatchManifestRaw([]byte(at)); err != nil {
		t.Fatalf("Go validator at the bound: %v", err)
	}
	if ans, err := rawApply(ctx, c, at); err != nil || ans[0] != "OK" {
		t.Fatalf("server at the bound: %v %v", trunc(ans), err)
	}

	over := build(probeRev(ctx, c), "over", ntable.LimitManifestBytes+1)
	before := storeImage(t, c)
	ans, err := rawApply(ctx, c, over)
	if err != nil || len(ans) < 5 || ans[0] != "REFUSED" || ans[1] != "LIMIT" || ans[2] != "manifest bytes" ||
		replyNumber(ans[3]) != fmt.Sprint(ntable.LimitManifestBytes) || replyNumber(ans[4]) != fmt.Sprint(ntable.LimitManifestBytes+1) {
		t.Fatalf("server one over the bound: %v %v", trunc(ans), err)
	}
	_, verr := ntable.ValidateBatchManifestRaw([]byte(over))
	var le *ntable.LimitError
	if !errors.As(verr, &le) || le.Name != "manifest bytes" || le.Bound != ntable.LimitManifestBytes || le.Observed != ntable.LimitManifestBytes+1 {
		t.Fatalf("Go validator one over the bound: %v", verr)
	}
	var m ntable.BatchManifest
	require.NoError(t, json.Unmarshal([]byte(over), &m))
	// The encoded request ApplyBatch sends is the canonical one; pad past the bound.
	m.Actor += strings.Repeat("p", 2*1024)
	_, aerr := ntable.ApplyBatch(ctx, c, m)
	if !errors.Is(aerr, ntable.ErrLimit) || !strings.Contains(aerr.Error(), "manifest bytes") || !strings.Contains(aerr.Error(), "changed=no") {
		t.Errorf("ApplyBatch one over the bound: %v", aerr)
	}
	if strings.Contains(fmt.Sprint(ans), "pppp") || strings.Contains(verr.Error(), "pppp") || strings.Contains(aerr.Error(), "pppp") {
		t.Errorf("a refusal echoes the input")
	}
	assert.Equal(t, before, storeImage(t, c), "a refused over-bound manifest changed the store")
}

// A read set is bounded by its members: 1,024 are read, 1,025 refuse.
func TestReadSetMembersBound(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	ids := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%sm%d", echoMarker, i)
		}
		return out
	}
	if _, err := ntable.ReadSetMembers(ctx, c, "demo", ids(ntable.LimitReadSetMembers)); err != nil {
		t.Fatalf("at the bound: %v", err)
	}
	_, err := ntable.ReadSetMembers(ctx, c, "demo", ids(ntable.LimitReadSetMembers+1))
	var le *ntable.LimitError
	if !errors.As(err, &le) || le.Name != "read set members" || le.Bound != ntable.LimitReadSetMembers || le.Observed != ntable.LimitReadSetMembers+1 {
		t.Fatalf("one over the bound: %v", err)
	}
	for _, w := range []string{`table "demo" read set`, "changed=no", "; run: nova-table show 'demo'"} {
		assert.ErrorContains(t, err, w, "refusal lacks %q: %s", w, err)
	}
	assert.NotContains(t, err.Error(), echoMarker, "refusal echoes the input: %.200s", err)
}
