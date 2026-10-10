package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// The tests here reach redis.go without a live store: pure helpers run their
// real code paths with no client at all, and the plain-command methods run
// against a fake of Redis.C (redis.UniversalClient). The fake answers every
// command in process, the way acl_steps_test.go's keyRecorder does, and never
// dials. The table layer's Lua-backed calls (Shapes, Apply and the row writes)
// have no function library on the fake; TestRedisCoverTableLayerNeedsLiveStore
// pins that refusal and the report names the live store they need.

// coverRedis is a Redis backend on an in-process fake of Redis.C. Close marks
// the fake closed so a later command is refused, as a client that has gone
// away is refused.
func coverRedis(t *testing.T, epoch uint64) (*Redis, context.Context) {
	t.Helper()
	db := &coverDB{keys: map[string]*coverVal{}}
	c := redis.NewClient(&redis.Options{Addr: "store.invalid:1"})
	c.AddHook(&coverHook{db: db})
	wrapped := &coverRedisClient{Client: c, db: db}
	t.Cleanup(func() { _ = wrapped.Close() })
	return &Redis{C: wrapped, Names: sprint.Names{Prefix: "t-"}, Pinned: epoch}, context.Background()
}

// coverRedisClient is the seam Redis.C: the client's commands, answered by
// coverHook, and a Close the tests can call to refuse what follows.
type coverRedisClient struct {
	*redis.Client
	db *coverDB
}

func (c *coverRedisClient) Close() error {
	c.db.closed.Store(true)
	return c.Client.Close()
}

// coverHook answers every command from the fake and never dials.
type coverHook struct{ db *coverDB }

func (coverHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("cover redis does not dial")
	}
}

func (h *coverHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error { return h.db.exec(cmd) }
}

func (h *coverHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		var first error
		for _, cmd := range cmds {
			if err := h.db.exec(cmd); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
}

type coverKind int

const (
	coverString coverKind = iota + 1
	coverHash
	coverList
	coverZSet
	coverStream
)

type coverVal struct {
	kind   coverKind
	s      string
	h      map[string]string
	list   []string
	z      []redis.Z
	stream []redis.XMessage
	seq    int64
}

type coverDB struct {
	mu     sync.Mutex
	keys   map[string]*coverVal
	closed atomic.Bool
}

var errCoverWrongType = coverRedisErr("WRONGTYPE Operation against a key holding the wrong kind of value")

func (db *coverDB) exec(cmd redis.Cmder) error {
	if db.closed.Load() {
		return coverFail(cmd, errors.New("redis: client is closed"))
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	err := db.apply(cmd)
	if err != nil {
		cmd.SetErr(err)
	}
	return err
}

func (db *coverDB) apply(cmd redis.Cmder) error {
	args := cmd.Args()
	if len(args) == 0 {
		return fmt.Errorf("cover redis: empty command")
	}
	name := strings.ToLower(fmt.Sprint(args[0]))
	switch name {
	case "watch", "unwatch", "multi":
		return coverOK(cmd)
	case "exec":
		return coverFill(cmd, []any(nil))
	case "get":
		return db.cmdGet(cmd, coverStr(args, 1))
	case "set":
		return db.cmdSet(cmd, coverStr(args, 1), coverStr(args, 2))
	case "getrange":
		return db.cmdGetRange(cmd, coverStr(args, 1), coverInt(args, 2), coverInt(args, 3))
	case "mget":
		return db.cmdMGet(cmd, args[1:])
	case "hget":
		return db.cmdHGet(cmd, coverStr(args, 1), coverStr(args, 2))
	case "hset":
		return db.cmdHSet(cmd, coverStr(args, 1), args[2:])
	case "hdel":
		return db.cmdHDel(cmd, coverStr(args, 1), args[2:])
	case "hexists":
		return db.cmdHExists(cmd, coverStr(args, 1), coverStr(args, 2))
	case "hmget":
		return db.cmdHMGet(cmd, coverStr(args, 1), args[2:])
	case "hgetall":
		return db.cmdHGetAll(cmd, coverStr(args, 1))
	case "del":
		return db.cmdDel(cmd, args[1:])
	case "exists":
		return db.cmdExists(cmd, args[1:])
	case "rpush":
		return db.cmdRPush(cmd, coverStr(args, 1), args[2:])
	case "lrange":
		return db.cmdLRange(cmd, coverStr(args, 1), coverInt(args, 2), coverInt(args, 3))
	case "llen":
		return db.cmdLLen(cmd, coverStr(args, 1))
	case "ltrim":
		return db.cmdLTrim(cmd, coverStr(args, 1), coverInt(args, 2), coverInt(args, 3))
	case "xadd":
		return db.cmdXAdd(cmd, coverStr(args, 1), args[2:])
	case "xrange":
		return db.cmdXRange(cmd, coverStr(args, 1), coverStr(args, 2), coverStr(args, 3), coverCount(args), false)
	case "xrevrange":
		return db.cmdXRange(cmd, coverStr(args, 1), coverStr(args, 3), coverStr(args, 2), coverCount(args), true)
	case "zadd":
		return db.cmdZAdd(cmd, coverStr(args, 1), args[2:])
	case "zcard":
		return db.cmdZCard(cmd, coverStr(args, 1))
	case "zrange":
		return db.cmdZRange(cmd, coverStr(args, 1), coverInt(args, 2), coverInt(args, 3))
	case "fcall", "fcall_ro":
		return coverRedisErr("ERR Function not found")
	default:
		return fmt.Errorf("cover redis: %s is not faked", name)
	}
}

func (db *coverDB) cmdGet(cmd redis.Cmder, key string) error {
	v, err := db.need(key, coverString)
	if err != nil {
		return err
	}
	return coverFill(cmd, v.s)
}

func (db *coverDB) cmdSet(cmd redis.Cmder, key, val string) error {
	db.keys[key] = &coverVal{kind: coverString, s: val}
	return coverOK(cmd)
}

func (db *coverDB) cmdGetRange(cmd redis.Cmder, key string, start, end int64) error {
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, "")
	}
	if v.kind != coverString {
		return errCoverWrongType
	}
	return coverFill(cmd, coverSubstr(v.s, start, end))
}

func (db *coverDB) cmdMGet(cmd redis.Cmder, keys []any) error {
	out := make([]any, len(keys))
	for i := range keys {
		v := db.keys[coverStr(keys, i)]
		if v == nil {
			continue
		}
		if v.kind != coverString {
			return errCoverWrongType
		}
		out[i] = v.s
	}
	return coverFill(cmd, out)
}

func (db *coverDB) cmdHGet(cmd redis.Cmder, key, field string) error {
	v, err := db.need(key, coverHash)
	if err != nil {
		return err
	}
	s, ok := v.h[field]
	if !ok {
		return redis.Nil
	}
	return coverFill(cmd, s)
}

func (db *coverDB) cmdHSet(cmd redis.Cmder, key string, pairs []any) error {
	if len(pairs)%2 != 0 {
		return fmt.Errorf("cover redis: hset wants field value pairs")
	}
	v, err := db.hash(key)
	if err != nil {
		return err
	}
	var added int64
	for i := 0; i < len(pairs); i += 2 {
		f, val := coverStr(pairs, i), coverStr(pairs, i+1)
		if _, ok := v.h[f]; !ok {
			added++
		}
		v.h[f] = val
	}
	return coverFill(cmd, added)
}

func (db *coverDB) cmdHDel(cmd redis.Cmder, key string, fields []any) error {
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, int64(0))
	}
	if v.kind != coverHash {
		return errCoverWrongType
	}
	var n int64
	for _, f := range fields {
		if _, ok := v.h[coverStr([]any{f}, 0)]; ok {
			delete(v.h, coverStr([]any{f}, 0))
			n++
		}
	}
	return coverFill(cmd, n)
}

func (db *coverDB) cmdHExists(cmd redis.Cmder, key, field string) error {
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, false)
	}
	if v.kind != coverHash {
		return errCoverWrongType
	}
	_, ok := v.h[field]
	return coverFill(cmd, ok)
}

func (db *coverDB) cmdHMGet(cmd redis.Cmder, key string, fields []any) error {
	out := make([]any, len(fields))
	v := db.keys[key]
	if v != nil && v.kind != coverHash {
		return errCoverWrongType
	}
	for i := range fields {
		if v == nil {
			continue
		}
		if s, ok := v.h[coverStr(fields, i)]; ok {
			out[i] = s
		}
	}
	return coverFill(cmd, out)
}

func (db *coverDB) cmdHGetAll(cmd redis.Cmder, key string) error {
	out := map[string]string{}
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, out)
	}
	if v.kind != coverHash {
		return errCoverWrongType
	}
	for k, s := range v.h {
		out[k] = s
	}
	return coverFill(cmd, out)
}

func (db *coverDB) cmdDel(cmd redis.Cmder, keys []any) error {
	var n int64
	for i := range keys {
		k := coverStr(keys, i)
		if _, ok := db.keys[k]; ok {
			delete(db.keys, k)
			n++
		}
	}
	return coverFill(cmd, n)
}

func (db *coverDB) cmdExists(cmd redis.Cmder, keys []any) error {
	var n int64
	for i := range keys {
		if _, ok := db.keys[coverStr(keys, i)]; ok {
			n++
		}
	}
	return coverFill(cmd, n)
}

func (db *coverDB) cmdRPush(cmd redis.Cmder, key string, vals []any) error {
	v, err := db.list(key)
	if err != nil {
		return err
	}
	for i := range vals {
		v.list = append(v.list, coverStr(vals, i))
	}
	return coverFill(cmd, int64(len(v.list)))
}

func (db *coverDB) cmdLRange(cmd redis.Cmder, key string, start, stop int64) error {
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, []string{})
	}
	if v.kind != coverList {
		return errCoverWrongType
	}
	a, b, ok := coverSpan(start, stop, int64(len(v.list)))
	if !ok {
		return coverFill(cmd, []string{})
	}
	return coverFill(cmd, append([]string{}, v.list[a:b+1]...))
}

func (db *coverDB) cmdLLen(cmd redis.Cmder, key string) error {
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, int64(0))
	}
	if v.kind != coverList {
		return errCoverWrongType
	}
	return coverFill(cmd, int64(len(v.list)))
}

func (db *coverDB) cmdLTrim(cmd redis.Cmder, key string, start, stop int64) error {
	v := db.keys[key]
	if v == nil {
		return coverOK(cmd)
	}
	if v.kind != coverList {
		return errCoverWrongType
	}
	a, b, ok := coverSpan(start, stop, int64(len(v.list)))
	if !ok {
		delete(db.keys, key)
		return coverOK(cmd)
	}
	v.list = append([]string{}, v.list[a:b+1]...)
	return coverOK(cmd)
}

func (db *coverDB) cmdXAdd(cmd redis.Cmder, key string, rest []any) error {
	v, err := db.stream(key)
	if err != nil {
		return err
	}
	if len(rest) == 0 || (coverStr(rest, 0) != "*" && !strings.Contains(coverStr(rest, 0), "-")) {
		return fmt.Errorf("cover redis: xadd id is not faked: %v", rest)
	}
	id := coverStr(rest, 0)
	if id == "*" {
		v.seq++
		id = strconv.FormatInt(v.seq, 10) + "-0"
	}
	vals := map[string]any{}
	for i := 1; i+1 < len(rest); i += 2 {
		vals[coverStr(rest, i)] = coverStr(rest, i+1)
	}
	v.stream = append(v.stream, redis.XMessage{ID: id, Values: vals})
	return coverFill(cmd, id)
}

func (db *coverDB) cmdXRange(cmd redis.Cmder, key, min, max string, count int64, rev bool) error {
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, []redis.XMessage{})
	}
	if v.kind != coverStream {
		return errCoverWrongType
	}
	var got []redis.XMessage
	for _, m := range v.stream {
		if coverStreamIn(m.ID, min, max) {
			got = append(got, m)
		}
	}
	if rev {
		for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
			got[i], got[j] = got[j], got[i]
		}
	}
	if count >= 0 && int64(len(got)) > count {
		got = got[:count]
	}
	return coverFill(cmd, got)
}

func (db *coverDB) cmdZAdd(cmd redis.Cmder, key string, rest []any) error {
	if len(rest)%2 != 0 {
		return fmt.Errorf("cover redis: zadd wants score member pairs")
	}
	v, err := db.zset(key)
	if err != nil {
		return err
	}
	var added int64
	for i := 0; i < len(rest); i += 2 {
		member := coverStr(rest, i+1)
		score := coverFloat(rest[i])
		found := false
		for j := range v.z {
			if fmt.Sprint(v.z[j].Member) == member {
				v.z[j].Score = score
				found = true
				break
			}
		}
		if !found {
			v.z = append(v.z, redis.Z{Score: score, Member: member})
			added++
		}
	}
	sort.Slice(v.z, func(i, j int) bool {
		if v.z[i].Score != v.z[j].Score {
			return v.z[i].Score < v.z[j].Score
		}
		return fmt.Sprint(v.z[i].Member) < fmt.Sprint(v.z[j].Member)
	})
	return coverFill(cmd, added)
}

func (db *coverDB) cmdZCard(cmd redis.Cmder, key string) error {
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, int64(0))
	}
	if v.kind != coverZSet {
		return errCoverWrongType
	}
	return coverFill(cmd, int64(len(v.z)))
}

func (db *coverDB) cmdZRange(cmd redis.Cmder, key string, start, stop int64) error {
	v := db.keys[key]
	if v == nil {
		return coverFill(cmd, []redis.Z{})
	}
	if v.kind != coverZSet {
		return errCoverWrongType
	}
	a, b, ok := coverSpan(start, stop, int64(len(v.z)))
	if !ok {
		return coverFill(cmd, []redis.Z{})
	}
	return coverFill(cmd, append([]redis.Z{}, v.z[a:b+1]...))
}

func (db *coverDB) need(key string, kind coverKind) (*coverVal, error) {
	v := db.keys[key]
	if v == nil {
		return nil, redis.Nil
	}
	if v.kind != kind {
		return nil, errCoverWrongType
	}
	return v, nil
}

func (db *coverDB) hash(key string) (*coverVal, error) {
	v := db.keys[key]
	if v == nil {
		v = &coverVal{kind: coverHash, h: map[string]string{}}
		db.keys[key] = v
		return v, nil
	}
	if v.kind != coverHash {
		return nil, errCoverWrongType
	}
	return v, nil
}

func (db *coverDB) list(key string) (*coverVal, error) {
	v := db.keys[key]
	if v == nil {
		v = &coverVal{kind: coverList}
		db.keys[key] = v
		return v, nil
	}
	if v.kind != coverList {
		return nil, errCoverWrongType
	}
	return v, nil
}

func (db *coverDB) stream(key string) (*coverVal, error) {
	v := db.keys[key]
	if v == nil {
		v = &coverVal{kind: coverStream}
		db.keys[key] = v
		return v, nil
	}
	if v.kind != coverStream {
		return nil, errCoverWrongType
	}
	return v, nil
}

func (db *coverDB) zset(key string) (*coverVal, error) {
	v := db.keys[key]
	if v == nil {
		v = &coverVal{kind: coverZSet}
		db.keys[key] = v
		return v, nil
	}
	if v.kind != coverZSet {
		return nil, errCoverWrongType
	}
	return v, nil
}

func coverOK(cmd redis.Cmder) error { return coverFill(cmd, "OK") }

func coverFail(cmd redis.Cmder, err error) error {
	cmd.SetErr(err)
	return err
}

func coverFill(cmd redis.Cmder, val any) error {
	switch c := cmd.(type) {
	case *redis.StatusCmd:
		c.SetVal("OK")
	case *redis.StringCmd:
		c.SetVal(val.(string))
	case *redis.IntCmd:
		c.SetVal(val.(int64))
	case *redis.BoolCmd:
		c.SetVal(val.(bool))
	case *redis.SliceCmd:
		if val == nil {
			c.SetVal(nil)
			return nil
		}
		c.SetVal(val.([]any))
	case *redis.StringSliceCmd:
		c.SetVal(val.([]string))
	case *redis.MapStringStringCmd:
		c.SetVal(val.(map[string]string))
	case *redis.XMessageSliceCmd:
		c.SetVal(val.([]redis.XMessage))
	case *redis.ZSliceCmd:
		c.SetVal(val.([]redis.Z))
	default:
		return fmt.Errorf("cover redis: reply %T for %v", cmd, cmd.Args())
	}
	return nil
}

func coverStr(args []any, i int) string {
	if i >= len(args) || args[i] == nil {
		return ""
	}
	switch s := args[i].(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(s)
	}
}

func coverInt(args []any, i int) int64 {
	if i >= len(args) || args[i] == nil {
		return 0
	}
	switch n := args[i].(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	default:
		v, _ := strconv.ParseInt(fmt.Sprint(n), 10, 64)
		return v
	}
}

func coverFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f
	}
}

func coverCount(args []any) int64 {
	for i := 0; i+1 < len(args); i++ {
		if strings.EqualFold(fmt.Sprint(args[i]), "count") {
			return coverInt(args, i+1)
		}
	}
	return -1
}

func coverSpan(start, stop, n int64) (int64, int64, bool) {
	if n == 0 {
		return 0, 0, false
	}
	if start < 0 {
		start += n
	}
	if stop < 0 {
		stop += n
	}
	if start < 0 {
		start = 0
	}
	if stop >= n {
		stop = n - 1
	}
	if start > stop || start >= n {
		return 0, 0, false
	}
	return start, stop, true
}

func coverSubstr(s string, start, end int64) string {
	a, b, ok := coverSpan(start, end, int64(len(s)))
	if !ok {
		return ""
	}
	return s[a : b+1]
}

func coverStreamIn(id, min, max string) bool {
	if !coverStreamGE(id, min) {
		return false
	}
	return coverStreamLE(id, max)
}

func coverStreamGE(id, bound string) bool {
	if bound == "-" || bound == "" {
		return true
	}
	ex := strings.HasPrefix(bound, "(")
	if ex {
		bound = bound[1:]
	}
	cmp := coverStreamCmp(id, bound)
	return cmp > 0 || (cmp == 0 && !ex)
}

func coverStreamLE(id, bound string) bool {
	if bound == "+" || bound == "" {
		return true
	}
	ex := strings.HasPrefix(bound, "(")
	if ex {
		bound = bound[1:]
	}
	cmp := coverStreamCmp(id, bound)
	return cmp < 0 || (cmp == 0 && !ex)
}

func coverStreamCmp(a, b string) int {
	am, as := coverStreamParts(a)
	bm, bs := coverStreamParts(b)
	if am != bm {
		if am < bm {
			return -1
		}
		return 1
	}
	if as != bs {
		if as < bs {
			return -1
		}
		return 1
	}
	return 0
}

func coverStreamParts(id string) (int64, int64) {
	ms, seq, ok := strings.Cut(id, "-")
	if !ok {
		n, _ := strconv.ParseInt(id, 10, 64)
		return n, 0
	}
	m, _ := strconv.ParseInt(ms, 10, 64)
	s, _ := strconv.ParseInt(seq, 10, 64)
	return m, s
}

// coverRedisErr is a Redis error reply for isReply's table.
type coverRedisErr string

func (e coverRedisErr) Error() string { return string(e) }
func (e coverRedisErr) RedisError()   {}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestRedisCoverKey pins key: the sprint key at the pinned epoch.
func TestRedisCoverKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		pinned uint64
		want   string
	}{
		{name: "epoch zero is the bare key", pinned: 0, want: "t-sprint:fence"},
		{name: "a later epoch is suffixed", pinned: 3, want: "t-sprint:fence@3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &Redis{Names: sprint.Names{Prefix: "t-"}, Pinned: tc.pinned}
			assert.Equal(t, tc.want, r.key(keyFence))
		})
	}
}

// TestRedisCoverAtEpoch pins AtEpoch: the copy is pinned to the epoch with
// its old flag, and the receiver is left where it was.
func TestRedisCoverAtEpoch(t *testing.T) {
	t.Parallel()
	r := &Redis{Names: sprint.Names{Prefix: "t-"}, Pinned: 2}
	cases := []struct {
		name       string
		epoch      uint64
		old        bool
		wantPinned uint64
	}{
		{name: "a live pin", epoch: 4, old: false, wantPinned: 4},
		{name: "an old pin", epoch: 4, old: true, wantPinned: 4},
		{name: "epoch zero", epoch: 0, old: false, wantPinned: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := r.AtEpoch(tc.epoch, tc.old).(*Redis)
			require.True(t, ok, "AtEpoch answers a *Redis")
			assert.Equal(t, tc.wantPinned, got.Pinned)
			assert.Equal(t, tc.old, got.Old)
			assert.Equal(t, uint64(2), r.Pinned, "the receiver keeps its pin")
			assert.False(t, r.Old, "the receiver keeps its flag")
		})
	}
}

// TestRedisCoverWriteOpts pins writeOpts: the table layer's writes carry the
// pinned epoch.
func TestRedisCoverWriteOpts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		pinned uint64
	}{
		{name: "epoch zero", pinned: 0},
		{name: "a later epoch", pinned: 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &Redis{Pinned: tc.pinned}
			assert.Equal(t, ntable.WriteOptions{Epoch: tc.pinned}, r.writeOpts())
		})
	}
}

// TestRedisCoverIsReply pins isReply: a Redis Error is a reply, anything else
// is a transport failure.
func TestRedisCoverIsReply(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "no error is no reply", err: nil, want: false},
		{name: "a Redis error is a reply", err: coverRedisErr("NOSCRIPT nope"), want: true},
		{name: "a transport failure is not", err: errors.New("boom"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isReply(tc.err))
		})
	}
}

// TestRedisCoverFenceOf pins fenceOf: the fence a pipeline read, and the two
// ways the read is refused.
func TestRedisCoverFenceOf(t *testing.T) {
	t.Parallel()
	op := mustJSON(t, OpRecord{ID: "op1", Verb: "tick"})
	machine := mustJSON(t, Machine{State: Running})
	t.Run("the whole fence", func(t *testing.T) {
		t.Parallel()
		f, err := fenceOf(redis.NewSliceResult([]any{op, "3", machine, "stuck-why"}, nil), redis.NewIntResult(2, nil))
		require.NoError(t, err)
		require.NotNil(t, f.Pending)
		assert.Equal(t, "op1", f.Pending.ID)
		assert.Equal(t, uint64(3), f.Gen)
		assert.True(t, f.Running)
		assert.Equal(t, 2, f.Queued)
		assert.Equal(t, "stuck-why", f.Stuck)
	})
	t.Run("an empty fence", func(t *testing.T) {
		t.Parallel()
		f, err := fenceOf(redis.NewSliceResult([]any{nil, nil, nil, nil}, nil), redis.NewIntResult(0, nil))
		require.NoError(t, err)
		assert.Nil(t, f.Pending)
		assert.False(t, f.Running)
		assert.Empty(t, f.Stuck)
	})
	t.Run("an unreadable operation record is refused", func(t *testing.T) {
		t.Parallel()
		_, err := fenceOf(redis.NewSliceResult([]any{"{nope", nil, nil, nil}, nil), redis.NewIntResult(0, nil))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unreadable operation record")
	})
	t.Run("a failed read is refused", func(t *testing.T) {
		t.Parallel()
		_, err := fenceOf(redis.NewSliceResult(nil, errors.New("boom")), redis.NewIntResult(0, nil))
		require.Error(t, err)
	})
}

// TestRedisCoverReleased pins released: when a release whose transaction kept
// failing is done.
func TestRedisCoverReleased(t *testing.T) {
	t.Parallel()
	op := OpRecord{ID: "op1", CallerOp: "call1"}
	cases := []struct {
		name     string
		commit   bool
		held     string
		recorded bool
		want     bool
	}{
		{name: "the fence still holding it is not done", commit: true, held: "op1", recorded: true, want: false},
		{name: "an emptied fence with its result recorded is done", commit: true, held: "", recorded: true, want: true},
		{name: "another operation's fence with its result recorded is done", commit: true, held: "op2", recorded: true, want: true},
		{name: "an unrecorded caller result is not done", commit: true, held: "", recorded: false, want: false},
		{name: "a non-commit needs no result", commit: false, held: "", recorded: false, want: true},
		{name: "no caller op needs no result", commit: true, held: "op2", recorded: false, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := op
			if tc.name == "no caller op needs no result" {
				o.CallerOp = ""
			}
			assert.Equal(t, tc.want, released(o, tc.commit, tc.held, tc.recorded))
		})
	}
}

// TestRedisCoverReadChange pins readChange: the twin's fields of a change
// event, zeros when the event names none.
func TestRedisCoverReadChange(t *testing.T) {
	t.Parallel()
	t.Run("every field", func(t *testing.T) {
		t.Parallel()
		ev := readChange(map[string]any{"epoch": "4", "verb": "apply", "rev_before": "9", "rev_after": "10", "members": `[{"id":"a"}]`, "batch_delta": `{"members":[]}`})
		assert.Equal(t, "4", ev.epoch)
		assert.Equal(t, "apply", ev.verb)
		assert.Equal(t, uint64(9), ev.before)
		assert.Equal(t, uint64(10), ev.after)
		assert.NotEmpty(t, ev.members)
	})
	t.Run("an event naming nothing is zeros", func(t *testing.T) {
		t.Parallel()
		ev := readChange(map[string]any{})
		assert.Empty(t, ev.verb)
		assert.Zero(t, ev.before)
		assert.Zero(t, ev.after)
	})
	t.Run("a non-number revision is zero", func(t *testing.T) {
		t.Parallel()
		ev := readChange(map[string]any{"rev_before": "nope", "rev_after": "10"})
		assert.Zero(t, ev.before)
		assert.Equal(t, uint64(10), ev.after)
	})
}

// TestRedisCoverChangeIDs pins changeIDs: the records an event names, and the
// two ways the event is unreadable.
func TestRedisCoverChangeIDs(t *testing.T) {
	t.Parallel()
	t.Run("members and the batch account", func(t *testing.T) {
		t.Parallel()
		got, err := changeIDs(changeEvent{verb: "apply", members: `[{"id":"a"},{"id":"b"}]`, batchDelta: `{"members":[{"id":"c"}]}`})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"a", "b", "c"}, got)
	})
	t.Run("an empty event names none", func(t *testing.T) {
		t.Parallel()
		got, err := changeIDs(changeEvent{verb: "row_set"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("a batch that changed no record names none", func(t *testing.T) {
		t.Parallel()
		// the store's cjson encodes an empty members list as an object: a
		// properties-only apply (promoted) is no gap for the twin
		got, err := changeIDs(changeEvent{verb: "apply", members: "[]", batchDelta: `{"changed_count":0,"members":{},"props":{"promoted_sha":"0123abc"}}`})
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("a batch event without its account is refused", func(t *testing.T) {
		t.Parallel()
		_, err := changeIDs(changeEvent{verb: "apply"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "without its account")
	})
	t.Run("unreadable members are refused", func(t *testing.T) {
		t.Parallel()
		_, err := changeIDs(changeEvent{verb: "row_set", members: "{nope"})
		require.Error(t, err)
	})
	t.Run("an unreadable batch account is refused", func(t *testing.T) {
		t.Parallel()
		_, err := changeIDs(changeEvent{verb: "apply", batchDelta: "{nope"})
		require.Error(t, err)
	})
}

// TestRedisCoverEpoch pins Epoch: the sprint's number, its clearing time and
// whether its restore is owed, and the refusal of a non-number.
func TestRedisCoverEpoch(t *testing.T) {
	t.Parallel()
	t.Run("an empty hash is epoch zero", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.Epoch(ctx)
		require.NoError(t, err)
		assert.Zero(t, got.N)
		assert.False(t, got.Owed)
	})
	t.Run("number, clearing and owed", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		require.NoError(t, r.C.HSet(ctx, r.Names.EpochKey(), "n", "4", "cleared", at.Format(time.RFC3339Nano), "restore", "3").Err())
		got, err := r.Epoch(ctx)
		require.NoError(t, err)
		assert.Equal(t, uint64(4), got.N)
		assert.True(t, got.Cleared.Equal(at))
		assert.True(t, got.Owed)
	})
	t.Run("a non-number epoch is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.Names.EpochKey(), "n", "nope").Err())
		_, err := r.Epoch(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not a number")
	})
	t.Run("a store that does not answer errors", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.Close())
		_, err := r.Epoch(ctx)
		require.Error(t, err)
	})
}

// TestRedisCoverAdvanceEpoch pins AdvanceEpoch: the sprint moves from n to
// n+1 with its clearing time and what it owes, and stays where it is when it
// is no longer at from.
func TestRedisCoverAdvanceEpoch(t *testing.T) {
	t.Parallel()
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	t.Run("the sprint advances", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		ok, err := r.AdvanceEpoch(ctx, 0, at)
		require.NoError(t, err)
		assert.True(t, ok)
		got, err := r.Epoch(ctx)
		require.NoError(t, err)
		assert.Equal(t, uint64(1), got.N)
		assert.True(t, got.Cleared.Equal(at.UTC()))
		assert.True(t, got.Owed)
	})
	t.Run("a sprint past from stays", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.Names.EpochKey(), "n", "9").Err())
		ok, err := r.AdvanceEpoch(ctx, 0, at)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, "9", r.C.HGet(ctx, r.Names.EpochKey(), "n").Val())
	})
	t.Run("a store that does not answer errors", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.Close())
		_, err := r.AdvanceEpoch(ctx, 0, at)
		require.Error(t, err)
	})
}

// TestRedisCoverSettleEpoch pins SettleEpoch: the owed restore goes once the
// sprint is still at n, and anything else is left alone.
func TestRedisCoverSettleEpoch(t *testing.T) {
	t.Parallel()
	t.Run("the owed restore is settled", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.Names.EpochKey(), "n", "2", "restore", "1").Err())
		require.NoError(t, r.SettleEpoch(ctx, 2))
		assert.False(t, r.C.HExists(ctx, r.Names.EpochKey(), "restore").Val())
		assert.Equal(t, "2", r.C.HGet(ctx, r.Names.EpochKey(), "n").Val())
	})
	t.Run("a sprint past n is left alone", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.Names.EpochKey(), "n", "3", "restore", "2").Err())
		require.NoError(t, r.SettleEpoch(ctx, 2))
		assert.True(t, r.C.HExists(ctx, r.Names.EpochKey(), "restore").Val())
	})
	t.Run("no epoch hash is nothing to settle", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.SettleEpoch(ctx, 0))
	})
}

// TestRedisCoverDelIfEmptyGuards pins delIf's empty guard list: no guards is
// no transaction, not even a client.
func TestRedisCoverDelIfEmptyGuards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		do   func(context.Context, *Redis) ([]string, error)
	}{
		{name: "RowsDelIf", do: func(ctx context.Context, r *Redis) ([]string, error) { return r.RowsDelIf(ctx, "work", nil) }},
		{name: "KeysDelIf", do: func(ctx context.Context, r *Redis) ([]string, error) { return r.KeysDelIf(ctx, "work", nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.do(context.Background(), &Redis{})
			require.NoError(t, err)
			assert.Nil(t, got)
		})
	}
}

// TestRedisCoverQueueRead pins QueueRead: the work table's queue oldest
// first, and the refusal of an unreadable entry.
func TestRedisCoverQueueRead(t *testing.T) {
	t.Parallel()
	t.Run("entries oldest first", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		first := sprint.QueuedChange{Verb: "op1"}
		second := sprint.QueuedChange{Verb: "op2"}
		require.NoError(t, r.C.RPush(ctx, r.key(keyQueue), mustJSON(t, first), mustJSON(t, second)).Err())
		got, err := r.QueueRead(ctx)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "op1", got[0].Verb)
		assert.Equal(t, "op2", got[1].Verb)
	})
	t.Run("an empty queue is empty", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.QueueRead(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("an unreadable entry is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.RPush(ctx, r.key(keyQueue), "{nope").Err())
		_, err := r.QueueRead(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unreadable entry")
	})
}

// TestRedisCoverDone pins Done: the recorded result, absent when none was
// recorded, and the refusal of a wrong-typed key.
func TestRedisCoverDone(t *testing.T) {
	t.Parallel()
	t.Run("a recorded result", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.key(keyDone), "call1", "won").Err())
		v, ok, err := r.Done(ctx, "call1")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "won", v)
	})
	t.Run("nothing recorded is absent", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		_, ok, err := r.Done(ctx, "call1")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("a wrong-typed key errors", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.Set(ctx, r.key(keyDone), "plain", 0).Err())
		_, _, err := r.Done(ctx, "call1")
		require.Error(t, err)
	})
}

// TestRedisCoverDoneBefore pins DoneBefore: the latest earlier epoch with a
// result, none when before is zero or nothing was recorded.
func TestRedisCoverDoneBefore(t *testing.T) {
	t.Parallel()
	t.Run("the latest earlier epoch wins", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 3)
		require.NoError(t, r.C.HSet(ctx, r.Names.KeyAt(keyDone, 0), "call1", "old").Err())
		require.NoError(t, r.C.HSet(ctx, r.Names.KeyAt(keyDone, 1), "call1", "new").Err())
		e, ok, err := r.DoneBefore(ctx, "call1", 2)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, uint64(1), e)
	})
	t.Run("epoch zero looks nowhere", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		_, ok, err := r.DoneBefore(ctx, "call1", 0)
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("nothing recorded is absent", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		_, ok, err := r.DoneBefore(ctx, "call1", 3)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

// TestRedisCoverCursorRoundTrip pins Cursor and SetCursor: the coordinator's
// last read stream id, empty when none was set.
func TestRedisCoverCursorRoundTrip(t *testing.T) {
	t.Parallel()
	t.Run("unset is empty", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.Cursor(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("a set cursor reads back", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.SetCursor(ctx, "12-0"))
		got, err := r.Cursor(ctx)
		require.NoError(t, err)
		assert.Equal(t, "12-0", got)
	})
}

// TestRedisCoverCoordinatorRoundTrip pins Coordinator and SetCoordinator: the
// sprint's coordinator, empty when none was set.
func TestRedisCoverCoordinatorRoundTrip(t *testing.T) {
	t.Parallel()
	t.Run("unset is empty", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.Coordinator(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("a set coordinator reads back", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.SetCoordinator(ctx, "m1"))
		got, err := r.Coordinator(ctx)
		require.NoError(t, err)
		assert.Equal(t, "m1", got)
	})
}

// TestRedisCoverProgress pins Progress: each stream's last progress, with a
// time a reader cannot parse reading as zero.
func TestRedisCoverProgress(t *testing.T) {
	t.Parallel()
	t.Run("every stream's time", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		require.NoError(t, r.C.HSet(ctx, r.key(keyProgress), "s1", at.Format(time.RFC3339)).Err())
		got, err := r.Progress(ctx)
		require.NoError(t, err)
		assert.True(t, got["s1"].Equal(at))
	})
	t.Run("an unparseable time is zero", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.key(keyProgress), "s1", "nope").Err())
		got, err := r.Progress(ctx)
		require.NoError(t, err)
		assert.True(t, got["s1"].IsZero())
	})
	t.Run("no progress is empty", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.Progress(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

// TestRedisCoverOpenOf pins openOf: the open judgments of an index read, and
// the refusal of an unreadable note.
func TestRedisCoverOpenOf(t *testing.T) {
	t.Parallel()
	note := func(id string) sprint.Note {
		return sprint.Note{ID: id, Kind: sprint.Judgment, Primaries: []string{"s1-1"}}
	}
	t.Run("no open index is none", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.openOf(ctx, map[string]string{})
		require.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("the index resolves to its notes", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.key(keyNotes), "n1", mustJSON(t, note("n1")), "n2", mustJSON(t, note("n2"))).Err())
		got, err := r.openOf(ctx, map[string]string{"b|subj": "n2", "a|subj": "n1"})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "a|subj", got[0].Key)
		assert.Equal(t, "n1", got[0].Note.ID)
	})
	t.Run("an unreadable note is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.key(keyNotes), "n1", "{nope").Err())
		_, err := r.openOf(ctx, map[string]string{"a|subj": "n1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "notification n1")
	})
	t.Run("a missing note reads as zero", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.openOf(ctx, map[string]string{"a|subj": "gone"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Empty(t, got[0].Note.ID)
	})
}

// TestRedisCoverOpenNotes pins OpenNotes: every open judgment through the
// index and its notes.
func TestRedisCoverOpenNotes(t *testing.T) {
	t.Parallel()
	t.Run("the index and its notes", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		n := sprint.Note{ID: "n1", Kind: sprint.Judgment, Primaries: []string{"s1-1"}}
		require.NoError(t, r.C.HSet(ctx, r.key(keyOpen), sprint.OpenKey("n1", "s1-1"), "n1").Err())
		require.NoError(t, r.C.HSet(ctx, r.key(keyNotes), "n1", mustJSON(t, n)).Err())
		got, err := r.OpenNotes(ctx)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "n1", got[0].Note.ID)
	})
	t.Run("no open judgment is none", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.OpenNotes(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

// TestRedisCoverNotesSince pins NotesSince: the notifications after an id,
// skipping entries that carry no note.
func TestRedisCoverNotesSince(t *testing.T) {
	t.Parallel()
	seed := func(t *testing.T, r *Redis, ctx context.Context) {
		t.Helper()
		n1 := mustJSON(t, sprint.Note{ID: "n1", Kind: "happened"})
		n2 := mustJSON(t, sprint.Note{ID: "n2", Kind: "happened"})
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyInbox), Values: []any{"note", n1}}).Err())
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyInbox), Values: []any{"other", "junk"}}).Err())
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyInbox), Values: []any{"note", n2}}).Err())
	}
	t.Run("every notification with its id", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		seed(t, r, ctx)
		notes, ids, err := r.NotesSince(ctx, "", 10)
		require.NoError(t, err)
		require.Len(t, notes, 2)
		assert.Equal(t, "n1", notes[0].ID)
		assert.Equal(t, "n2", notes[1].ID)
		require.Len(t, ids, 2)
	})
	t.Run("after an id reads what follows", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		seed(t, r, ctx)
		_, ids, err := r.NotesSince(ctx, "", 10)
		require.NoError(t, err)
		require.Len(t, ids, 2)
		notes, _, err := r.NotesSince(ctx, ids[0], 10)
		require.NoError(t, err)
		require.Len(t, notes, 1)
		assert.Equal(t, "n2", notes[0].ID)
	})
	t.Run("an empty inbox is empty", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		notes, ids, err := r.NotesSince(ctx, "", 10)
		require.NoError(t, err)
		assert.Empty(t, notes)
		assert.Empty(t, ids)
	})
}

// TestRedisCoverLogSince pins LogSince: the log's lines after an id,
// skipping entries that carry no line.
func TestRedisCoverLogSince(t *testing.T) {
	t.Parallel()
	t.Run("every line with its id", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		l1 := mustJSON(t, sprint.Line{Verb: "tick"})
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyLog), Values: []any{"line", l1}}).Err())
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyLog), Values: []any{"other", "junk"}}).Err())
		lines, ids, err := r.LogSince(ctx, "", 10)
		require.NoError(t, err)
		require.Len(t, lines, 1)
		assert.Equal(t, "tick", lines[0].Verb)
		require.Len(t, ids, 1)
		rest, _, err := r.LogSince(ctx, ids[0], 10)
		require.NoError(t, err)
		assert.Empty(t, rest)
	})
	t.Run("an empty log is empty", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		lines, ids, err := r.LogSince(ctx, "", 10)
		require.NoError(t, err)
		assert.Empty(t, lines)
		assert.Empty(t, ids)
	})
}

// TestRedisCoverTails pins Tails: the last id of the log and of the inbox,
// empty when each is.
func TestRedisCoverTails(t *testing.T) {
	t.Parallel()
	t.Run("empty streams have no tails", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		lg, in, err := r.Tails(ctx)
		require.NoError(t, err)
		assert.Empty(t, lg)
		assert.Empty(t, in)
	})
	t.Run("each tail is its stream's last id", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		l1 := mustJSON(t, sprint.Line{Verb: "tick"})
		n1 := mustJSON(t, sprint.Note{ID: "n1"})
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyLog), Values: []any{"line", l1}}).Err())
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyInbox), Values: []any{"note", n1}}).Err())
		lg, in, err := r.Tails(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, lg)
		assert.NotEmpty(t, in)
	})
}

// TestRedisCoverTableChanges pins TableChanges: the records a span of table
// writes named, and every gap that reads the table whole.
func TestRedisCoverTableChanges(t *testing.T) {
	t.Parallel()
	t.Run("no span names nothing and is whole", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		ids, ok, err := r.TableChanges(ctx, "work", 5, 5)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Empty(t, ids)
	})
	t.Run("the twin ahead of the table is a gap", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		_, ok, err := r.TableChanges(ctx, "work", 6, 5)
		require.Error(t, err)
		assert.False(t, ok)
		var gap *GapError
		require.ErrorAs(t, err, &gap)
	})
	t.Run("a stream ending early is a gap", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		_, ok, err := r.TableChanges(ctx, "work", 0, 3)
		require.Error(t, err)
		assert.False(t, ok)
	})
	t.Run("a chained span names its records", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		key := ntable.DefKey("work") + ":changes"
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: []any{
			"epoch", "0", "verb", "row_set", "rev_before", "0", "rev_after", "1", "members", `[{"id":"a"}]`,
		}}).Err())
		ids, ok, err := r.TableChanges(ctx, "work", 0, 1)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, []string{"a"}, ids)
	})
	t.Run("a write naming no records is a gap", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		key := ntable.DefKey("merge") + ":changes"
		require.NoError(t, r.C.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: []any{
			"epoch", "0", "verb", "drop", "rev_before", "0", "rev_after", "1",
		}}).Err())
		_, ok, err := r.TableChanges(ctx, "merge", 0, 1)
		require.Error(t, err)
		assert.False(t, ok)
	})
}

// TestRedisCoverReadFence pins ReadFence: the fence, its generation, the
// machine's state and the queue's length in one exchange.
func TestRedisCoverReadFence(t *testing.T) {
	t.Parallel()
	t.Run("an empty fence", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		f, err := r.ReadFence(ctx)
		require.NoError(t, err)
		assert.Nil(t, f.Pending)
		assert.Zero(t, f.Gen)
		assert.False(t, f.Running)
		assert.Zero(t, f.Queued)
	})
	t.Run("the whole fence", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		op := OpRecord{ID: "op1", Verb: "tick"}
		require.NoError(t, r.C.Set(ctx, r.key(keyFence), mustJSON(t, op), 0).Err())
		require.NoError(t, r.C.Set(ctx, r.key(keyGen), "6", 0).Err())
		require.NoError(t, r.C.Set(ctx, r.Names.Key(keyMachine), mustJSON(t, Machine{State: Running}), 0).Err())
		require.NoError(t, r.C.Set(ctx, r.Names.Key(keyStuck), "why", 0).Err())
		require.NoError(t, r.C.RPush(ctx, r.key(keyQueue), mustJSON(t, sprint.QueuedChange{Verb: "q"})).Err())
		f, err := r.ReadFence(ctx)
		require.NoError(t, err)
		require.NotNil(t, f.Pending)
		assert.Equal(t, "op1", f.Pending.ID)
		assert.Equal(t, uint64(6), f.Gen)
		assert.True(t, f.Running)
		assert.Equal(t, "why", f.Stuck)
		assert.Equal(t, 1, f.Queued)
	})
	t.Run("an unreadable fence is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.Set(ctx, r.key(keyFence), "{nope", 0).Err())
		_, err := r.ReadFence(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unreadable operation record")
	})
}

// TestRedisCoverAcquire pins Acquire: the fence is taken once at the read
// generation and epoch, and refused after it moves.
func TestRedisCoverAcquire(t *testing.T) {
	t.Parallel()
	t.Run("an empty fence at its generation is taken", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		ok, err := r.Acquire(ctx, 0, OpRecord{ID: "op1", Verb: "tick"})
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "1", r.C.Get(ctx, r.key(keyGen)).Val())
	})
	t.Run("a taken fence is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		ok, err := r.Acquire(ctx, 0, OpRecord{ID: "op1", Verb: "tick"})
		require.NoError(t, err)
		require.True(t, ok)
		ok, err = r.Acquire(ctx, 1, OpRecord{ID: "op2", Verb: "tick"})
		require.NoError(t, err)
		assert.False(t, ok, "the fence moved")
	})
	t.Run("a stale generation is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		ok, err := r.Acquire(ctx, 9, OpRecord{ID: "op1", Verb: "tick"})
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("a cleared sprint is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.Names.EpochKey(), "n", "1").Err())
		ok, err := r.Acquire(ctx, 0, OpRecord{ID: "op1", Verb: "tick"})
		require.NoError(t, err)
		assert.False(t, ok, "the sprint was cleared since the step read it")
	})
}

// TestRedisCoverRelease pins Release: the fence is emptied of the operation
// it holds, and a fence holding anything else is left alone.
func TestRedisCoverRelease(t *testing.T) {
	t.Parallel()
	t.Run("the held operation is released", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		op := OpRecord{ID: "op1", Verb: "tick"}
		ok, err := r.Acquire(ctx, 0, op)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, r.Release(ctx, op, false))
		assert.False(t, r.C.Exists(ctx, r.key(keyFence)).Val() == 1)
	})
	t.Run("a commit records the caller's result", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		op := OpRecord{ID: "op1", Verb: "tick", CallerOp: "call1", Result: "won"}
		ok, err := r.Acquire(ctx, 0, op)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, r.Release(ctx, op, true))
		v, found, err := r.Done(ctx, "call1")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "won", v)
	})
	t.Run("an empty fence is nothing to release", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.Release(ctx, OpRecord{ID: "op1", Verb: "tick"}, false))
	})
	t.Run("another operation's fence is left alone", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		other := OpRecord{ID: "op9", Verb: "tick"}
		ok, err := r.Acquire(ctx, 0, other)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, r.Release(ctx, OpRecord{ID: "op1", Verb: "tick"}, false))
		held, err := r.heldOp(ctx)
		require.NoError(t, err)
		assert.Equal(t, "op9", held)
	})
}

// TestRedisCoverHeldOp pins heldOp: the id of the operation the fence holds,
// empty when it holds none, and the refusal of an unreadable record.
func TestRedisCoverHeldOp(t *testing.T) {
	t.Parallel()
	t.Run("an empty fence holds nothing", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.heldOp(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("the held operation's id", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.Set(ctx, r.key(keyFence), mustJSON(t, OpRecord{ID: "op1"}), 0).Err())
		got, err := r.heldOp(ctx)
		require.NoError(t, err)
		assert.Equal(t, "op1", got)
	})
	t.Run("an unreadable fence is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.Set(ctx, r.key(keyFence), "{nope", 0).Err())
		_, err := r.heldOp(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unreadable operation record")
	})
}

// TestRedisCoverCommit pins commit: the release's writes land on the queued
// pipeline in one exchange.
func TestRedisCoverCommit(t *testing.T) {
	t.Parallel()
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	t.Run("drain, queue, log, progress and the caller's result", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.RPush(ctx, r.key(keyQueue), mustJSON(t, sprint.QueuedChange{Verb: "stale"}), mustJSON(t, sprint.QueuedChange{Verb: "stale2"})).Err())
		op := OpRecord{
			ID: "op1", Verb: "tick", At: at, Drain: 1,
			Queue:    []sprint.QueuedChange{{Verb: "fresh"}},
			Log:      []sprint.Line{{Verb: "tick"}},
			Streams:  []string{"s1"},
			CallerOp: "call1", Result: "won",
		}
		p := r.C.Pipeline()
		require.NoError(t, r.commit(ctx, p, op))
		_, err := p.Exec(ctx)
		require.NoError(t, err)
		q, err := r.QueueRead(ctx)
		require.NoError(t, err)
		require.Len(t, q, 2)
		assert.Equal(t, "stale2", q[0].Verb)
		assert.Equal(t, "fresh", q[1].Verb)
		v, ok, err := r.Done(ctx, "call1")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "won", v)
		prog, err := r.Progress(ctx)
		require.NoError(t, err)
		assert.True(t, prog["s1"].Equal(at.UTC()))
		lines, _, err := r.LogSince(ctx, "", 10)
		require.NoError(t, err)
		assert.NotEmpty(t, lines)
	})
	t.Run("judgments open and notes land on the inbox", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		n := sprint.Note{ID: "n1", Kind: sprint.Judgment, Primaries: []string{"s1-1"}}
		op := OpRecord{ID: "op1", Verb: "tick", At: at, Notes: []sprint.Note{n}}
		p := r.C.Pipeline()
		require.NoError(t, r.commit(ctx, p, op))
		_, err := p.Exec(ctx)
		require.NoError(t, err)
		open, err := r.OpenNotes(ctx)
		require.NoError(t, err)
		require.Len(t, open, 1)
		assert.Equal(t, "n1", open[0].Note.ID)
		notes, _, err := r.NotesSince(ctx, "", 10)
		require.NoError(t, err)
		require.Len(t, notes, 1)
		assert.Equal(t, "n1", notes[0].ID)
	})
	t.Run("closes and the stuck record go", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.key(keyOpen), "n1|s1-1", "n1").Err())
		require.NoError(t, r.C.Set(ctx, r.Names.Key(keyStuck), "stuck", 0).Err())
		op := OpRecord{ID: "op1", Verb: "tick", At: at, Closes: []string{"n1|s1-1"}, Stuck: "why"}
		p := r.C.Pipeline()
		require.NoError(t, r.commit(ctx, p, op))
		_, err := p.Exec(ctx)
		require.NoError(t, err)
		assert.False(t, r.C.HExists(ctx, r.key(keyOpen), "n1|s1-1").Val())
		assert.Equal(t, int64(0), r.C.Exists(ctx, r.Names.Key(keyStuck)).Val())
	})
}

// TestRedisCoverSetReview pins SetReview: an open judgment's next review
// time, and the refusal when the judgment is missing or unreadable.
func TestRedisCoverSetReview(t *testing.T) {
	t.Parallel()
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	set := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	t.Run("the review time is set", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		n := sprint.Note{ID: "n1", Kind: sprint.Judgment}
		require.NoError(t, r.C.HSet(ctx, r.key(keyNotes), "n1", mustJSON(t, n)).Err())
		require.NoError(t, r.SetReview(ctx, "n1", at, set))
		raw := r.C.HGet(ctx, r.key(keyNotes), "n1").Val()
		var got sprint.Note
		require.NoError(t, json.Unmarshal([]byte(raw), &got))
		assert.True(t, got.Review.Equal(at))
		assert.True(t, got.ReviewSet.Equal(set))
	})
	t.Run("a missing judgment is refused with its remedy", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		err := r.SetReview(ctx, "gone", at, set)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no judgment gone")
		assert.Contains(t, err.Error(), "run: nova-sprint inbox")
	})
	t.Run("an unreadable judgment errors", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.HSet(ctx, r.key(keyNotes), "n1", "{nope").Err())
		err := r.SetReview(ctx, "n1", at, set)
		require.Error(t, err)
	})
}

// TestRedisCoverReadViewOld pins ReadView's old-epoch refusal: a read of an
// earlier epoch reads no view, without touching the store.
func TestRedisCoverReadViewOld(t *testing.T) {
	t.Parallel()
	r := &Redis{Old: true}
	_, err := r.ReadView(context.Background(), []string{"t-work"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an earlier epoch reads no view")
}

// TestRedisCoverReadViewRefusal pins ReadView's shape refusal on a store
// without the table layer's functions.
func TestRedisCoverReadViewRefusal(t *testing.T) {
	t.Parallel()
	r, ctx := coverRedis(t, 0)
	_, err := r.ReadView(ctx, []string{"t-work"})
	require.Error(t, err, "the fake holds no table shapes: the twin's read is refused without a live store")
}

// TestRedisCoverCellIDs pins CellIDs: a shape with no set cells reads no
// ids, a members cell reads its members' ids, and a cell the store answers
// with the wrong type is refused.
func TestRedisCoverCellIDs(t *testing.T) {
	t.Parallel()
	shape := func() []ntable.Table {
		return []ntable.Table{{
			Name:    "t-work",
			Columns: []ntable.Column{{Name: "c", Projection: ntable.Members}},
			Rows:    []ntable.Row{{Key: "a", Cells: []ntable.Cell{{Key: "t-work:c:a"}}}},
		}}
	}
	t.Run("no set cells is no ids", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		got, err := r.CellIDs(ctx, []ntable.Table{{Name: "t-work"}})
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("a members cell reads its ids", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.ZAdd(ctx, "t-work:c:a", redis.Z{Score: 1, Member: "m1"}, redis.Z{Score: 2, Member: "m2"}).Err())
		got, err := r.CellIDs(ctx, shape())
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"m1", "m2"}, got["t-work"])
	})
	t.Run("a cell of the wrong type is refused", func(t *testing.T) {
		t.Parallel()
		r, ctx := coverRedis(t, 0)
		require.NoError(t, r.C.Set(ctx, "t-work:c:a", "plain", 0).Err())
		_, err := r.CellIDs(ctx, shape())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "did not come back")
	})
}

// TestRedisCoverTableLayerNeedsLiveStore pins the table layer's refusals on a
// store without its function library: every Lua-backed call errors there, so
// its main path needs a live store (the report names it).
func TestRedisCoverTableLayerNeedsLiveStore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		do   func(context.Context, *Redis) error
	}{
		{name: "Shapes", do: func(ctx context.Context, r *Redis) error { _, err := r.Shapes(ctx, []string{"t-work"}); return err }},
		{name: "ReadSet", do: func(ctx context.Context, r *Redis) error {
			_, err := r.ReadSet(ctx, "t-work", []string{"a"})
			return err
		}},
		{name: "Apply", do: func(ctx context.Context, r *Redis) error {
			_, err := r.Apply(ctx, ntable.BatchManifest{Table: "t-work"})
			return err
		}},
		{name: "ApplyAll", do: func(ctx context.Context, r *Redis) error {
			_, errs := r.ApplyAll(ctx, []ntable.BatchManifest{{Table: "t-work"}})
			return errs[0]
		}},
		{name: "Create", do: func(ctx context.Context, r *Redis) error { return r.Create(ctx, ntable.Table{Name: "t-work"}) }},
		{name: "RowsAdd", do: func(ctx context.Context, r *Redis) error { return r.RowsAdd(ctx, "t-work", []string{"a"}) }},
		{name: "RowsHide", do: func(ctx context.Context, r *Redis) error { return r.RowsHide(ctx, "t-work", []string{"a"}) }},
		{name: "RowsShow", do: func(ctx context.Context, r *Redis) error { return r.RowsShow(ctx, "t-work", []string{"a"}) }},
		{name: "RowsDel", do: func(ctx context.Context, r *Redis) error { return r.RowsDel(ctx, "t-work", []string{"a"}) }},
		{name: "RowsDelIf", do: func(ctx context.Context, r *Redis) error {
			_, err := r.RowsDelIf(ctx, "t-work", []RowGuard{{Row: "a", ID: "a", Key: "k", Rev: 1}})
			return err
		}},
		{name: "KeysDelIf", do: func(ctx context.Context, r *Redis) error {
			_, err := r.KeysDelIf(ctx, "t-work", []RowGuard{{Row: "a", ID: "a", Key: "k", Rev: 1, Keys: []string{"k"}}})
			return err
		}},
		{name: "Place", do: func(ctx context.Context, r *Redis) error { return r.Place(ctx, "t-work", "a", "c", "id", 1) }},
		{name: "RowSet", do: func(ctx context.Context, r *Redis) error {
			return r.RowSet(ctx, "t-work", "a", map[string]string{"c": "v"})
		}},
		{name: "ViewSet", do: func(ctx context.Context, r *Redis) error { return r.ViewSet(ctx, ntable.View{Name: "v"}) }},
		{name: "ViewDelete", do: func(ctx context.Context, r *Redis) error { return r.ViewDelete(ctx, "v") }},
		{name: "DropTable", do: func(ctx context.Context, r *Redis) error { return r.DropTable(ctx, "t-work") }},
		{name: "CheckTable", do: func(ctx context.Context, r *Redis) error { return r.CheckTable(ctx, "t-work") }},
		{name: "RowsSet", do: func(ctx context.Context, r *Redis) error {
			return r.RowsSet(ctx, "t-work", map[string]map[string]string{"a": {"c": "v"}})
		}},
		{name: "RowsOrder", do: func(ctx context.Context, r *Redis) error { return r.RowsOrder(ctx, "t-work", []string{"a"}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, ctx := coverRedis(t, 0)
			err := tc.do(ctx, r)
			require.Error(t, err, "%s needs the table layer's function library: a live store", tc.name)
		})
	}
}
