//go:build functional

package tset

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// This is deliberately a whole-fixture check, independent of Mem and of the
// writer's point-read plan. It is not a production scan or a performance test.
func independentStoreInvariant(c *redis.Client, space string) error {
	ctx := context.Background()
	keys, err := c.Keys(ctx, space+"*").Result()
	if err != nil {
		return err
	}
	sort.Strings(keys)
	kinds := make(map[string]string, len(keys))
	for _, key := range keys {
		kinds[key], err = c.Type(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("TYPE %s: %w", key, err)
		}
	}
	needType := func(key, want string) error {
		if kinds[key] != want {
			return fmt.Errorf("%s type %q, want %q", key, kinds[key], want)
		}
		return nil
	}
	activeKey := space + "sprint:epoch"
	if err := needType(activeKey, "hash"); err != nil {
		return err
	}
	active, err := c.HGetAll(ctx, activeKey).Result()
	if err != nil {
		return err
	}
	if err := independentMarker(active, "", true); err != nil {
		return fmt.Errorf("active marker: %w", err)
	}
	activeEpoch := active["n"]
	markers := make(map[string]bool)
	for _, key := range keys {
		if !strings.HasPrefix(key, space+"sprint:epoch@") {
			continue
		}
		epoch := strings.TrimPrefix(key, space+"sprint:epoch@")
		if err := needType(key, "hash"); err != nil {
			return err
		}
		fields, err := c.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		if err := independentMarker(fields, epoch, false); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		markers[epoch] = true
	}
	if !markers[activeEpoch] || !markers["0"] {
		return fmt.Errorf("active or initial epoch marker absent: active=%s markers=%v", activeEpoch, markers)
	}
	if mustInvariantUint(activeEpoch) > 1 { // This bounded fixture advances exactly once.
		return fmt.Errorf("unexpected active fixture epoch %s", activeEpoch)
	}
	for e := uint64(0); e <= mustInvariantUint(activeEpoch); e++ {
		if !markers[strconv.FormatUint(e, 10)] {
			return fmt.Errorf("missing retained epoch marker %d", e)
		}
	}

	// The fixture has one configured table. Definitions, rows and cells are
	// found from actual keys, including cells under rows absent from :rows.
	definition := space + "table:work"
	if err := needType(definition, "hash"); err != nil {
		return err
	}
	base, err := c.HGetAll(ctx, definition).Result()
	if err != nil {
		return err
	}
	if err := independentDefinition(base, space); err != nil {
		return fmt.Errorf("live definition: %w", err)
	}
	rows := make(map[string]map[string]bool)
	placements := make(map[string][]independentPlace)
	seen := make(map[string]bool)
	for epoch := range markers {
		prefix := definition
		if epoch != "0" {
			prefix += ":" + epoch
		}
		defKey := prefix + ":definition"
		if err := needType(defKey, "hash"); err != nil {
			return err
		}
		snapshot, err := c.HGetAll(ctx, defKey).Result()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(base, snapshot) {
			return fmt.Errorf("definition snapshot differs in epoch %s", epoch)
		}
		seen[defKey] = true
		rows[epoch] = make(map[string]bool)
		rowKey := prefix + ":rows"
		if kind := kinds[rowKey]; kind != "" {
			if err := needType(rowKey, "zset"); err != nil {
				return err
			}
			all, err := c.ZRangeWithScores(ctx, rowKey, 0, -1).Result()
			if err != nil {
				return err
			}
			for _, z := range all {
				row, ok := z.Member.(string)
				if !ok || row == "" || strings.Contains(row, ":") ||
					z.Score < 0 || z.Score > 9007199254740991 || math.Trunc(z.Score) != z.Score {
					return fmt.Errorf("invalid row/rank in %s: %+v", rowKey, z)
				}
				// Distinct rows may have tied ranks. Their names, rather than
				// their scores, establish row membership.
				rows[epoch][row] = true
			}
			seen[rowKey] = true
		}
		for _, key := range keys {
			if !strings.HasPrefix(key, prefix+":cell:") {
				continue
			}
			// Epoch zero's prefix also prefixes later epochs. A literal cell
			// segment distinguishes its own keys from later-epoch keys.
			cell := strings.TrimPrefix(key, prefix+":cell:")
			parts := strings.Split(cell, ":")
			if len(parts) != 2 || !rows[epoch][parts[0]] || (parts[1] != "c" && parts[1] != "d") {
				return fmt.Errorf("cell %s has no defined row/column", key)
			}
			if err := needType(key, "zset"); err != nil {
				return err
			}
			all, err := c.ZRangeWithScores(ctx, key, 0, -1).Result()
			if err != nil {
				return err
			}
			for _, z := range all {
				id, ok := z.Member.(string)
				if !ok || id == "" || math.IsNaN(z.Score) || math.IsInf(z.Score, 0) {
					return fmt.Errorf("invalid member/score in %s: %+v", key, z)
				}
				placements[id] = append(placements[id], independentPlace{epoch, cell, z.Score})
			}
			seen[key] = true
		}
	}
	seen[activeKey], seen[definition] = true, true
	for epoch := range markers {
		seen[space+"sprint:epoch@"+epoch] = true
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, space+"member:work:") {
			continue
		}
		if err := needType(key, "hash"); err != nil {
			return err
		}
		id := strings.TrimPrefix(key, space+"member:work:")
		if id == "" || strings.Contains(id, ":") {
			return fmt.Errorf("invalid record key %s", key)
		}
		fields, err := c.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		epoch, revision := fields["epoch"], fields["revision"]
		if !markers[epoch] || !independentUint(revision, true) {
			return fmt.Errorf("record %s has invalid epoch/revision %q/%q", key, epoch, revision)
		}
		for field := range fields {
			if strings.HasPrefix(field, "place:") && field != "place:work" {
				return fmt.Errorf("record %s has foreign placement field %s", key, field)
			}
		}
		if place, present := fields["place:work"]; present && place == "" {
			return fmt.Errorf("record %s has an empty placement field", key)
		}
		places := placements[id]
		if len(places) > 1 {
			return fmt.Errorf("record %s has %d cell placements", key, len(places))
		}
		place := fields["place:work"]
		if place == "" && len(places) != 0 {
			return fmt.Errorf("unplaced record %s occurs in cell", key)
		}
		if place != "" && (len(places) != 1 || places[0].epoch != epoch || places[0].cell != place) {
			return fmt.Errorf("record %s placement %s@%s disagrees with cells %v", key, place, epoch, places)
		}
		delete(placements, id)
		seen[key] = true
	}
	if len(placements) != 0 {
		return fmt.Errorf("cell members without records: %v", placements)
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, space+"sprint:done@") {
			continue
		}
		epoch := strings.TrimPrefix(key, space+"sprint:done@")
		if !markers[epoch] {
			return fmt.Errorf("receipt %s has no epoch marker", key)
		}
		if err := needType(key, "hash"); err != nil {
			return err
		}
		values, err := c.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		for op, value := range values {
			if op == "" || !independentReceipt(value, epoch, markers) {
				return fmt.Errorf("malformed receipt %s[%s]", key, op)
			}
		}
		seen[key] = true
	}
	for _, key := range keys {
		if !seen[key] {
			return fmt.Errorf("unclassified store key %s (%s)", key, kinds[key])
		}
	}
	return nil
}

type independentPlace struct {
	epoch string
	cell  string
	score float64
}

func independentUint(s string, positive bool) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') || (positive && s == "0") {
		return false
	}
	for _, b := range []byte(s) {
		if b < '0' || b > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

func mustInvariantUint(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64) // validated by independentMarker
	return n
}

func independentMarker(fields map[string]string, epoch string, active bool) error {
	if len(fields) != 3 || fields["engine"] != Version || !independentUint(fields["n"], false) ||
		(!active && fields["n"] != epoch) {
		return fmt.Errorf("invalid marker fields %v", fields)
	}
	var tables []string
	if err := json.Unmarshal([]byte(fields["tables"]), &tables); err != nil || !reflect.DeepEqual(tables, []string{"work"}) {
		return fmt.Errorf("invalid table catalog %q", fields["tables"])
	}
	return nil
}

func independentDefinition(fields map[string]string, space string) error {
	if len(fields) != 7 || fields["engine"] != Version || fields["order"] != "c,d" ||
		fields["member_prefix"] != space+"member:work:" || fields["epoch_key"] != space+"sprint:epoch" ||
		fields["epoch_field"] != "n" || fields["col:c"] != "set" || fields["col:d"] != "set" {
		return fmt.Errorf("invalid definition fields %v", fields)
	}
	return nil
}

func independentReceipt(value, epoch string, markers map[string]bool) bool {
	// This proves structural epoch linkage to retained markers. The receipt
	// body has no original request, so known operation transitions are checked
	// separately by the fixture below.
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &fields) != nil || len(fields) != 8 {
		return false
	}
	var r struct {
		Digest      string `json:"intent_digest"`
		Status      string `json:"status"`
		EpochBefore string `json:"epoch_before"`
		EpochAfter  string `json:"epoch_after"`
		FirstSeq    string `json:"first_seq"`
		LastSeq     string `json:"last_seq"`
		Changed     int    `json:"changed"`
		Result      string `json:"result"`
	}
	if json.Unmarshal([]byte(value), &r) != nil || r.EpochBefore != epoch || !markers[r.EpochAfter] ||
		(r.Status != "ok" && r.Status != "fenced") || r.Changed < 0 || r.Changed > MaxMemberCandidates+MaxPropEntries ||
		!independentUint(r.FirstSeq, false) || !independentUint(r.LastSeq, false) ||
		len(r.Result) > 4096 || len(r.Digest) != 40 {
		return false
	}
	if _, err := hex.DecodeString(r.Digest); err != nil {
		return false
	}
	for _, field := range []string{"intent_digest", "status", "epoch_before", "epoch_after", "first_seq", "last_seq", "changed", "result"} {
		if _, ok := fields[field]; !ok {
			return false
		}
	}
	// Unmarshalling JSON null into an int or string leaves Go's zero value
	// without an error. Verify the raw types before accepting the receipt.
	var changed, result any
	if json.Unmarshal(fields["changed"], &changed) != nil || json.Unmarshal(fields["result"], &result) != nil {
		return false
	}
	if _, ok := changed.(float64); !ok {
		return false
	}
	if _, ok := result.(string); !ok {
		return false
	}
	return r.Status != "fenced" || (r.EpochBefore == r.EpochAfter && r.FirstSeq == "0" && r.LastSeq == "0" && r.Changed == 0 && r.Result == "")
}

func TestIndependentStoredInvariantsAcrossPublicSteps(t *testing.T) {
	t.Parallel()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c", "d")
	fx.AddRow(t, "work", "r0", 0)
	fx.Activate(t)
	ctx := context.Background()
	store := newFixtureRedis(t, fx.Client)
	check := func(label string) {
		t.Helper()
		if err := independentStoreInvariant(fx.Client, fx.Space); err != nil {
			t.Fatalf("%s: independent invariant: %v", label, err)
		}
	}
	apply := func(label string, step Step) Reply {
		t.Helper()
		r, err := store.Step(ctx, step)
		if err != nil || r.Status != "ok" || r.Replay {
			t.Fatalf("%s: public step reply=%+v err=%v", label, r, err)
		}
		check(label)
		return r
	}
	refuse := func(label string, step Step, code string) {
		t.Helper()
		before := commitProbeImage(t, fx.Client)
		_, err := store.Step(ctx, step)
		var r *Refusal
		if !errors.As(err, &r) || r.Code != code {
			t.Fatalf("%s: refusal=%v, want %s", label, err, code)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: refusal changed raw store", label)
		}
		check(label)
	}
	step := func(epoch Decimal, entries ...Entry) Step {
		return Step{Epoch: epoch, Space: fx.Space, Entries: entries}
	}
	assertRecord := func(id, epoch, revision, place string) {
		t.Helper()
		fields, err := fx.Client.HGetAll(ctx, fixtureRecordKey(fx.Space, "work", id)).Result()
		if err != nil || fields["epoch"] != epoch || fields["revision"] != revision || fields["place:work"] != place {
			t.Fatalf("record %s after public step: %v, err=%v; want epoch/revision/place=%s/%s/%s", id, fields, err, epoch, revision, place)
		}
	}
	check("seed")
	apply("add r1", step("0", Entry{Kind: "rows", Table: "work", Add: []string{"r1"}}))
	// The public writer assigns unique ranks, but the stored row contract
	// permits ties. Exercise that valid state so the checker cannot silently
	// strengthen the invariant to uniqueness.
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "0"), redis.Z{Score: 0, Member: "r1"}).Err(); err != nil {
		t.Fatal(err)
	}
	check("legal tied row ranks")
	if err := fx.Client.ZAdd(ctx, fixtureRowsKey(fx.Space, "work", "0"), redis.Z{Score: 1, Member: "r1"}).Err(); err != nil {
		t.Fatal(err)
	}
	check("restored public row rank")
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("old-%02d", i)
		create := step("0", Entry{Kind: "create", Table: "work", To: "r0:c", IDs: []string{id}, Scores: []string{"1"}, Set: map[string]string{"phase": "new"}})
		if i == 0 {
			op, intent := "old-anchor", "stable old placement"
			create.Op, create.Intent, create.Result = &op, &intent, "created"
		}
		apply("create "+id, create)
		assertRecord(id, "0", "1", "r0:c")
		if i == 0 { // Leave a real old-epoch cell after the advance.
			refuse("occupied old row", step("0", Entry{Kind: "rows", Table: "work", Del: []string{"r0"}}), "OCCUPIED")
			continue
		}
		apply("move "+id, step("0", Entry{Kind: "move", Table: "work", From: "r0:c", To: "r1:d", IDs: []string{id}, Scores: []string{"2"}, Set: map[string]string{"phase": "moved"}}))
		assertRecord(id, "0", "2", "r1:d")
		if i%7 == 0 {
			refuse("wrong source "+id, step("0", Entry{Kind: "move", Table: "work", From: "r0:c", To: "r1:d", IDs: []string{id}}), "PLACE")
		}
		apply("remove "+id, step("0", Entry{Kind: "remove", Table: "work", From: "r1:d", IDs: []string{id}}))
		assertRecord(id, "0", "3", "")
	}
	apply("delete empty r1", step("0", Entry{Kind: "rows", Table: "work", Del: []string{"r1"}}))
	op, intent := "clear-0", "advance while retaining historical records"
	advance := step("0", Entry{Kind: "advance", AdvanceFrom: "0"}, Entry{Kind: "rows", Table: "work", Add: []string{"r0"}})
	advance.Op, advance.Intent, advance.Result = &op, &intent, "advanced"
	apply("advance to epoch 1", advance)
	assertRecord("old-00", "0", "1", "r0:c")
	assertRecord("old-01", "0", "3", "")
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("new-%02d", i)
		apply("create "+id, step("1", Entry{Kind: "create", Table: "work", To: "r0:c", IDs: []string{id}, Scores: []string{"3"}}))
		assertRecord(id, "1", "1", "r0:c")
		apply("move "+id, step("1", Entry{Kind: "move", Table: "work", From: "r0:c", To: "r0:d", IDs: []string{id}, Scores: []string{"4"}}))
		assertRecord(id, "1", "2", "r0:d")
		apply("remove "+id, step("1", Entry{Kind: "remove", Table: "work", From: "r0:d", IDs: []string{id}}))
		assertRecord(id, "1", "3", "")
	}
	refuse("old epoch write", step("0", Entry{Kind: "remove", Table: "work", From: "r0:c", IDs: []string{"old-00"}}), "STALE")
	// A historical receipt must remain in its original epoch after advance.
	value, err := fx.Client.HGet(ctx, fixtureDoneKey(fx.Space, "0"), "old-anchor").Result()
	if err != nil || !independentReceipt(value, "0", map[string]bool{"0": true, "1": true}) {
		t.Fatalf("historical receipt missing/malformed: %v", err)
	}
	wantDigest := sha1.Sum([]byte("stable old placement"))
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &receipt); err != nil ||
		string(receipt["intent_digest"]) != strconv.Quote(hex.EncodeToString(wantDigest[:])) ||
		string(receipt["result"]) != strconv.Quote("created") ||
		string(receipt["epoch_before"]) != strconv.Quote("0") ||
		string(receipt["epoch_after"]) != strconv.Quote("0") {
		t.Fatalf("historical receipt lost original intent/result: %q (%v)", value, err)
	}
	advancedValue, err := fx.Client.HGet(ctx, fixtureDoneKey(fx.Space, "0"), "clear-0").Result()
	if err != nil || !independentReceipt(advancedValue, "0", map[string]bool{"0": true, "1": true}) {
		t.Fatalf("advance receipt missing/malformed: %q (%v)", advancedValue, err)
	}
	var advancedReceipt map[string]json.RawMessage
	if err := json.Unmarshal([]byte(advancedValue), &advancedReceipt); err != nil ||
		string(advancedReceipt["epoch_before"]) != strconv.Quote("0") ||
		string(advancedReceipt["epoch_after"]) != strconv.Quote("1") {
		t.Fatalf("advance receipt lost original epoch transition: %q (%v)", advancedValue, err)
	}

	// Negative controls mutate only this throwaway Redis server and restore each
	// key. The checker must reject corruption independent of the public writer.
	corrupt := func(label string, damage, restore func() error) {
		t.Helper()
		if err := damage(); err != nil {
			t.Fatalf("%s damage: %v", label, err)
		}
		if err := independentStoreInvariant(fx.Client, fx.Space); err == nil {
			t.Fatalf("%s corruption escaped independent checker", label)
		}
		if err := restore(); err != nil {
			t.Fatalf("%s restore: %v", label, err)
		}
		check("restore " + label)
	}
	foreign := fixtureCellKey(fx.Space, "work", "1", "r0", "d")
	corrupt("duplicate cross-epoch cell", func() error {
		return fx.Client.ZAdd(ctx, foreign, redis.Z{Score: 5, Member: "old-00"}).Err()
	}, func() error { return fx.Client.ZRem(ctx, foreign, "old-00").Err() })
	oldRecord := fixtureRecordKey(fx.Space, "work", "old-00")
	corrupt("zero revision", func() error { return fx.Client.HSet(ctx, oldRecord, "revision", "0").Err() },
		func() error { return fx.Client.HSet(ctx, oldRecord, "revision", "1").Err() })
	rowKey := fixtureRowsKey(fx.Space, "work", "0")
	corrupt("orphaned occupied row", func() error { return fx.Client.ZRem(ctx, rowKey, "r0").Err() },
		func() error { return fx.Client.ZAdd(ctx, rowKey, redis.Z{Score: 0, Member: "r0"}).Err() })
	marker := fx.Space + "sprint:epoch@1"
	corrupt("wrong epoch marker", func() error { return fx.Client.HSet(ctx, marker, "n", "2").Err() },
		func() error { return fx.Client.HSet(ctx, marker, "n", "1").Err() })
	corrupt("malformed compact receipt", func() error {
		return fx.Client.HSet(ctx, fixtureDoneKey(fx.Space, "0"), "old-anchor", "not-json").Err()
	},
		func() error { return fx.Client.HSet(ctx, fixtureDoneKey(fx.Space, "0"), "old-anchor", value).Err() })
	for _, field := range []string{"changed", "result"} {
		var receiptFields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(value), &receiptFields); err != nil {
			t.Fatal(err)
		}
		receiptFields[field] = json.RawMessage("null")
		encoded, err := json.Marshal(receiptFields)
		if err != nil {
			t.Fatal(err)
		}
		corrupt("null receipt "+field, func() error {
			return fx.Client.HSet(ctx, fixtureDoneKey(fx.Space, "0"), "old-anchor", string(encoded)).Err()
		}, func() error {
			return fx.Client.HSet(ctx, fixtureDoneKey(fx.Space, "0"), "old-anchor", value).Err()
		})
	}
}
