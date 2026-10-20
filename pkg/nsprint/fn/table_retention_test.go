package fn

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// The bounds are the values pkg/ntable/limits.go names (LimitOpsRecords and
// LimitChangeEvents). They are repeated here so this test stays in package fn;
// pkg/ntable/limits_test.go holds them to table.lua and the specification.
const (
	retentionOpsBound    = 200000
	retentionStreamBound = 1000000
)

// installBoundedCommands teaches the package's fake store the forms table.lua
// calls that it does not model: HSCAN's one-table reply {cursor, {field,
// value, ...}}, as the store's own T.scan reads SCAN's batch[1]/batch[2], and
// XADD's MAXLEN [~|=] threshold. The fake's XADD keeps every entry, as the
// store did before the bound, so the wrapper drops the oldest entries exactly
// as an untrimmed-by-approximation MAXLEN does. No server, socket or clock is
// used.
func installBoundedCommands(t *testing.T, s *fakeStore) {
	t.Helper()
	redis := s.L.GetGlobal("redis").(*lua.LTable)
	scan := func(L *lua.LState) int {
		key := L.CheckString(1)
		cursor, err := strconv.Atoi(L.CheckString(2))
		require.NoError(t, err)
		count := 10
		for i := 3; i < L.GetTop(); i++ {
			if strings.EqualFold(L.Get(i).String(), "COUNT") {
				count, err = strconv.Atoi(L.Get(i + 1).String())
				require.NoError(t, err)
			}
		}
		fields := make([]string, 0, len(s.hashes[key]))
		for f := range s.hashes[key] {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		if cursor > len(fields) {
			cursor = len(fields)
		}
		end := min(cursor+count, len(fields))
		next := end
		if end >= len(fields) {
			next = 0
		}
		flat := L.NewTable()
		for _, f := range fields[cursor:end] {
			flat.Append(lua.LString(f))
			flat.Append(lua.LString(s.hashes[key][f]))
		}
		reply := L.NewTable()
		reply.Append(lua.LString(strconv.Itoa(next)))
		reply.Append(flat)
		L.Push(reply)
		return 1
	}
	xadd := func(L *lua.LState) int {
		maxAt, fieldAt := 4, 6
		if sym := L.Get(4).String(); sym == "~" || sym == "=" {
			require.GreaterOrEqual(t, L.GetTop(), 7, "XADD MAXLEN %s needs a threshold", sym)
			maxAt, fieldAt = 5, 7
		}
		maxlen, err := strconv.Atoi(L.Get(maxAt).String())
		require.NoError(t, err)
		key := L.CheckString(2)
		s.lastID++
		entry := []string{fmt.Sprintf("%d-0", s.lastID)}
		for i := fieldAt; i <= L.GetTop(); i++ {
			entry = append(entry, L.Get(i).String())
		}
		s.streams[key] = append(s.streams[key], entry)
		if maxlen >= 0 && len(s.streams[key]) > maxlen {
			s.streams[key] = s.streams[key][len(s.streams[key])-maxlen:]
		}
		L.Push(lua.LString(entry[0]))
		return 1
	}
	wrap := func(protected bool) *lua.LFunction {
		return s.L.NewFunction(func(L *lua.LState) int {
			cmd := strings.ToUpper(L.CheckString(1))
			if cmd == "HSCAN" {
				L.Remove(1)
				return scan(L)
			}
			if cmd == "XADD" && L.GetTop() >= 6 && strings.EqualFold(L.Get(3).String(), "MAXLEN") {
				return xadd(L)
			}
			if protected {
				return s.pcall(L)
			}
			return s.call(L)
		})
	}
	redis.RawSetString("call", wrap(false))
	redis.RawSetString("pcall", wrap(true))
}

// newRetentionStore loads table.lua on the package's fake store, with HSCAN
// and XADD MAXLEN modelled.
func newRetentionStore(t *testing.T) *fakeStore {
	t.Helper()
	s := newFakeStoreOf(t, "lua/table.lua")
	installBoundedCommands(t, s)
	return s
}

func retentionCreate(t *testing.T, s *fakeStore, name string) {
	t.Helper()
	fields := map[string]string{"order": "todo,done", "col:todo": "members:none:20:", "col:done": "members:none:20:",
		"epoch_key": "demo:epoch", "epoch_field": "n"}
	require.Equal(t, "OK", s.fcallReply(t, "ns_table_create", name, jsonText(t, fields), `{"epoch":"0"}`)[0], "create %s", name)
	require.Equal(t, "ROW", s.fcallReply(t, "ns_table_row_add", name, "todo", `{"label":"T"}`, `{"epoch":"0"}`)[0], "row %s", name)
}

func retentionManifest(t *testing.T, s *fakeStore, name, rev, op string, member string) string {
	t.Helper()
	return jsonText(t, map[string]any{
		"schema": 1, "table": name, "epoch": "0", "expected_table_revision": rev, "operation_id": op, "actor": "t",
		"members": []map[string]any{{
			"id": member, "expect": map[string]any{"absent": true},
			"create": map[string]any{"row": "todo", "col": "todo", "score": 1},
		}},
	})
}

func retentionGuard(t *testing.T, s *fakeStore, name, epoch, rev, op string) string {
	t.Helper()
	return jsonText(t, map[string]any{
		"schema": 1, "table": name, "epoch": epoch, "expected_table_revision": rev, "operation_id": op, "actor": "t",
		"members": []map[string]any{{"id": "g-" + op, "expect": map[string]any{"absent": true}}},
	})
}

func retentionRevision(s *fakeStore) string { return s.hashes["table:demo:revision"]["n"] }

// TestTableOpsAndChangesAreBounded: days of beats leave an operation hash and
// a change stream at their bounds, not past them. A table at the record bound
// takes one more record and drops its oldest; two epoch advances drop the
// epochs older than the previous one. The stream keeps its MAXLEN every time.
// At BASE neither the record hash nor the stream is trimmed.
func TestTableOpsAndChangesAreBounded(t *testing.T) {
	t.Parallel()

	s := newRetentionStore(t)
	const name = "demo"
	retentionCreate(t, s, name)
	opsKey := "table:" + name + ":ops"

	// Days of beats: the hash and its oldest-first index stand at the bound.
	for i := 0; i < retentionOpsBound+1; i++ {
		id := fmt.Sprintf("old%d", i)
		s.hash(opsKey)["0:"+id] = `{"operation_id":"` + id + `","digest":"x","result":"[]"}`
		s.zset(opsKey + ":idx:0")[id] = float64(i + 1)
	}
	// The change stream stands one past its bound.
	line := []string{"1-0", "verb", "apply"}
	for i := 0; i < retentionStreamBound+1; i++ {
		s.streams["table:"+name+":changes"] = append(s.streams["table:"+name+":changes"], line)
	}

	raw := retentionManifest(t, s, name, retentionRevision(s), "over", "m")
	overReply := s.fcallReply(t, "ns_table_apply", name, raw)
	require.Equal(t, "OK", overReply[0], "the record over the bound: %v", overReply)
	require.LessOrEqual(t, len(s.hashes[opsKey]), retentionOpsBound, "the operation hash is at its bound")
	require.Len(t, s.streams["table:"+name+":changes"], retentionStreamBound, "the change stream is at its bound")
	require.NotContains(t, s.hashes[opsKey], "0:old0", "the oldest record is the one dropped")

	// Rollout: records already in the current epoch's hash but not in its
	// index are indexed by the bounded HSCAN sweep and then counted by the
	// bound. The apply above scanned the first batch; take the next batch out
	// of the index and let the next apply find it.
	scan, err := strconv.Atoi(s.hashes[opsKey+":meta"]["scan"])
	require.NoError(t, err)
	present := make([]string, 0, len(s.hashes[opsKey]))
	for f := range s.hashes[opsKey] {
		present = append(present, f)
	}
	sort.Strings(present)
	require.Greater(t, len(present), scan+512)
	unindexed := present[scan : scan+512]
	for _, f := range unindexed {
		delete(s.zsets[opsKey+":idx:0"], strings.TrimPrefix(f, "0:"))
	}
	mig := retentionManifest(t, s, name, retentionRevision(s), "mig", "mig")
	migReply := s.fcallReply(t, "ns_table_apply", name, mig)
	require.Equal(t, "OK", migReply[0], "the migrating record: %v", migReply)
	require.LessOrEqual(t, len(s.hashes[opsKey]), retentionOpsBound, "the existing ledger is brought to its bound")
	for _, f := range unindexed {
		if _, held := s.hashes[opsKey][f]; held {
			require.Contains(t, s.zsets[opsKey+":idx:0"], strings.TrimPrefix(f, "0:"), "the sweep indexes an existing record")
		}
	}

	// The bound is measured; the epoch sweep is exercised on a small epoch 0
	// so the test does not spend minutes deleting a stand-in for days of beats.
	s.hashes[opsKey] = map[string]string{"0:old0": `{"operation_id":"old0","digest":"x","result":"[]"}`}
	s.zsets[opsKey+":idx:0"] = map[string]float64{"old0": 1}

	// Two epoch advances. Epoch 0 is older than the previous epoch of epoch 2,
	// so its records and index go; epoch 1 stays.
	s.hash("demo:epoch")["n"] = "1"
	raw1 := retentionGuard(t, s, name, "1", retentionRevision(s), "e1")
	r1 := s.fcallReply(t, "ns_table_apply", name, raw1)
	require.Equal(t, "OK", r1[0], "epoch 1: %v", r1)
	s.hash("demo:epoch")["n"] = "2"
	raw2 := retentionGuard(t, s, name, "2", retentionRevision(s), "e2")
	r2 := s.fcallReply(t, "ns_table_apply", name, raw2)
	require.Equal(t, "OK", r2[0], "epoch 2: %v", r2)

	for f := range s.hashes[opsKey] {
		require.False(t, strings.HasPrefix(f, "0:"), "epoch 0's record %s survives an epoch advance", f)
	}
	require.NotContains(t, s.zsets, opsKey+":idx:0", "epoch 0's index survives an epoch advance")
	require.LessOrEqual(t, len(s.hashes[opsKey]), retentionOpsBound, "the operation hash stays at its bound")
	require.Len(t, s.streams["table:"+name+":changes"], retentionStreamBound, "the change stream stays at its bound")
}

// TestTableReplayKeepsItsContract: a held operation replays its recorded
// result, a different request under its id is OPCONFLICT, and an operation
// whose record is past the bound is OPEXPIRED by name with nothing applied.
// At BASE the expired id is applied again.
func TestTableReplayKeepsItsContract(t *testing.T) {
	t.Parallel()

	s := newRetentionStore(t)
	const name = "demo"
	retentionCreate(t, s, name)
	first := retentionManifest(t, s, name, retentionRevision(s), "held", "m1")
	reply := s.fcallReply(t, "ns_table_apply", name, first)
	require.Equal(t, "OK", reply[0], "the held operation")
	revision := retentionRevision(s)

	again := s.fcallReply(t, "ns_table_apply", name, first)
	require.Equal(t, "REPLAY", again[2], "a held operation replays its result")
	require.Equal(t, reply[1], again[1], "the replayed receipt is the recorded one")
	require.Equal(t, revision, retentionRevision(s), "a replay writes nothing")

	different := retentionManifest(t, s, name, revision, "held", "m2")
	conflict := s.fcallReply(t, "ns_table_apply", name, different)
	require.Equal(t, []any{"REFUSED", "OPCONFLICT", "held"}, conflict, "a different request under a held id")

	// The id was recorded once and its record was trimmed (its index entry is
	// what is left), as the oldest record past the bound is.
	opsKey := "table:" + name + ":ops"
	s.zset(opsKey + ":idx:0")["gone"] = 1
	expired := retentionManifest(t, s, name, revision, "gone", "m3")
	got := s.fcallReply(t, "ns_table_apply", name, expired)
	require.Equal(t, []any{"REFUSED", "OPEXPIRED", "gone"}, got, "an operation past the bound")
	require.Equal(t, revision, retentionRevision(s), "the expired operation is not applied")
	require.NotContains(t, s.hashes[opsKey], "0:gone", "the expired operation left no record")
	_, row := s.hashes["table:demo:row:todo"]["todo:todo"]
	require.False(t, row, "the expired operation left no row")
}

// TestTableOpRecordHoldsNoRequestBytes: a 4 KiB request leaves a record under
// 512 bytes, holding its digest rather than its bytes. At BASE the record is
// the request itself and is over 4 KiB.
func TestTableOpRecordHoldsNoRequestBytes(t *testing.T) {
	t.Parallel()

	s := newRetentionStore(t)
	const name = "demo"
	retentionCreate(t, s, name)
	value := strings.Repeat(" ", 4096)
	raw := jsonText(t, map[string]any{
		"schema": 1, "table": name, "epoch": "0", "expected_table_revision": retentionRevision(s),
		"operation_id": "big", "actor": "t", "members": []any{},
		"props": map[string]string{"note": "x"},
	}) + value
	require.Greater(t, len(raw), 4096, "the request is over 4 KiB")
	bigReply := s.fcallReply(t, "ns_table_apply", name, raw)
	require.Equal(t, "OK", bigReply[0], "the large operation: %v", bigReply)

	record := s.hashes["table:"+name+":ops"]["0:big"]
	require.NotEmpty(t, record, "the operation record")
	require.Less(t, len(record), 512, "the operation record stays under 512 bytes")
	require.NotContains(t, record, "request", "the record holds no request field")
	require.NotContains(t, record, value, "the record holds no request bytes")
	var held struct {
		Digest string `json:"digest"`
	}
	require.NoError(t, json.Unmarshal([]byte(record), &held))
	sum := sha1.Sum([]byte(raw))
	require.Equal(t, hex.EncodeToString(sum[:]), held.Digest, "the record holds the request's digest")
}
