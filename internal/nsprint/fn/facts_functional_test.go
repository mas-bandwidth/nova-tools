//go:build functional

package fn

import (
	"bufio"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// startStore starts a throwaway store, opens one client with the read timeout
// the test needs and logs the server version; the client closes at test end.
func startStore(t *testing.T, readTimeout time.Duration) (addr string, c *redis.Client, ctx context.Context) {
	t.Helper()
	addr = testredis.Start(t)
	c = redis.NewClient(&redis.Options{Addr: addr, ReadTimeout: readTimeout})
	t.Cleanup(func() { _ = c.Close() })
	logRedisVersion(t, c)
	return addr, c, context.Background()
}

// loadReplace loads code with FUNCTION LOAD REPLACE and fails on a refusal.
func loadReplace(t *testing.T, ctx context.Context, c *redis.Client, code string) {
	t.Helper()
	require.NoError(t, c.FunctionLoadReplace(ctx, code).Err(), "load the library")
}

// TestFactF1NoRollback verifies that a function that errors keeps its earlier writes.
// Redis functions do not roll back; writes executed before an error persist.
func TestFactF1NoRollback(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	code := `#!lua name=facts_test
redis.register_function('fct_f1_seterr', function(keys, args)
    redis.call('SET', keys[1], args[1])
    error('deliberate error after SET')
end)`
	loadReplace(t, ctx, c, code)

	key := "dev-facts-f1"
	err := c.FCall(ctx, "fct_f1_seterr", []string{key}, "1").Err()
	require.Error(t, err)
	require.Contains(t, err.Error(), "deliberate error after SET")

	val, err := c.Get(ctx, key).Result()
	require.NoError(t, err, "get %s", key)
	require.Equal(t, "1", val, "the write before the error persists")
}

// TestFactF2KillOnlyBeforeWrite verifies that FUNCTION KILL stops only a function
// that has not executed write commands once busy-reply-threshold is exceeded.
func TestFactF2KillOnlyBeforeWrite(t *testing.T) {
	t.Parallel()
	addr, c, ctx := startStore(t, 30*time.Second)

	// Verify default busy-reply-threshold is 5000 ms
	cfg, err := c.ConfigGet(ctx, "busy-reply-threshold").Result()
	require.NoError(t, err, "config get busy-reply-threshold")
	require.Equal(t, "5000", cfg["busy-reply-threshold"], "default busy-reply-threshold")

	// Lower threshold to 1000 ms for test execution
	require.NoError(t, c.ConfigSet(ctx, "busy-reply-threshold", "1000").Err(), "config set busy-reply-threshold 1000")
	t.Cleanup(func() {
		require.NoError(t, c.ConfigSet(ctx, "busy-reply-threshold", "5000").Err(), "config set busy-reply-threshold 5000")
		cfgRestored, err := c.ConfigGet(ctx, "busy-reply-threshold").Result()
		require.NoError(t, err, "config get busy-reply-threshold")
		require.Equal(t, "5000", cfgRestored["busy-reply-threshold"], "restored busy-reply-threshold")
	})

	code := `#!lua name=facts_test
redis.register_function('fct_f2_writeloop', function(keys, args)
    redis.call('SET', keys[1], '1')
    local t0 = os.clock()
    while os.clock() - t0 < 3.0 do
    end
    return 'done'
end)
redis.register_function('fct_f2_readloop', function(keys, args)
    local t0 = os.clock()
    while os.clock() - t0 < 3.0 do
    end
    return 'done'
end)`
	loadReplace(t, ctx, c, code)

	// Case 1: Writing function -> FUNCTION KILL answers UNKILLABLE
	{
		cRunner := redis.NewClient(&redis.Options{Addr: addr, ReadTimeout: 30 * time.Second})
		defer cRunner.Close()
		cKiller := redis.NewClient(&redis.Options{Addr: addr, ReadTimeout: 30 * time.Second})
		defer cKiller.Close()

		var wg sync.WaitGroup
		var runnerErr error
		var runnerRes string
		wg.Add(1)
		go func() {
			defer wg.Done()
			runnerRes, runnerErr = cRunner.FCall(ctx, "fct_f2_writeloop", []string{"dev-facts-f2"}, "1").Text()
		}()

		start := time.Now()
		var killErr error
		for time.Now().Sub(start) < 4*time.Second {
			time.Sleep(50 * time.Millisecond)
			if time.Now().Sub(start) < 1200*time.Millisecond {
				continue
			}
			killErr = cKiller.FunctionKill(ctx).Err()
			if killErr != nil && strings.Contains(killErr.Error(), "UNKILLABLE") {
				break
			}
			if killErr != nil && strings.Contains(killErr.Error(), "NOTBUSY") {
				continue
			}
			break
		}

		require.Error(t, killErr)
		require.Contains(t, killErr.Error(), "UNKILLABLE", "a writing function is unkillable")

		wg.Wait()
		require.NoError(t, runnerErr, "writing function should run to end")
		require.Equal(t, "done", runnerRes)
	}

	// Case 2: No-writes function -> FUNCTION KILL answers OK
	{
		cRunner := redis.NewClient(&redis.Options{Addr: addr, ReadTimeout: 30 * time.Second})
		defer cRunner.Close()
		cKiller := redis.NewClient(&redis.Options{Addr: addr, ReadTimeout: 30 * time.Second})
		defer cKiller.Close()

		var wg sync.WaitGroup
		var runnerErr error
		var runnerRes string
		wg.Add(1)
		go func() {
			defer wg.Done()
			runnerRes, runnerErr = cRunner.FCall(ctx, "fct_f2_readloop", nil).Text()
		}()

		start := time.Now()
		var killErr error
		for time.Now().Sub(start) < 4*time.Second {
			time.Sleep(50 * time.Millisecond)
			if time.Now().Sub(start) < 1200*time.Millisecond {
				continue
			}
			killErr = cKiller.FunctionKill(ctx).Err()
			if killErr == nil {
				break
			}
			if strings.Contains(killErr.Error(), "NOTBUSY") {
				continue
			}
			break
		}

		require.NoError(t, killErr, "a no-writes function is killable")

		wg.Wait()
		require.Error(t, runnerErr)
		require.Contains(t, runnerErr.Error(), "Script killed by user", "res=%q", runnerRes)
	}
}

// TestFactF3UnpackLimit verifies the exact unpack bound: 7,999 values works and 8,000 fails.
func TestFactF3UnpackLimit(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	code := `#!lua name=facts_test
redis.register_function('fct_f3_unpack', function(keys, args)
    local n = tonumber(args[1])
    local t = {}
    for i = 1, n do
        t[i] = i
    end
    local function sink(...)
        return select('#', ...)
    end
    return sink(unpack(t))
end)`
	loadReplace(t, ctx, c, code)

	res, err := c.FCall(ctx, "fct_f3_unpack", nil, 7999).Int()
	require.NoError(t, err, "unpack of 7,999 values")
	require.Equal(t, 7999, res)

	err = c.FCall(ctx, "fct_f3_unpack", nil, 8000).Err()
	require.Error(t, err, "unpack of 8,000 values unexpectedly succeeded")
	require.Contains(t, err.Error(), "too many results to unpack")
}

// TestFactF4OOMInsideStartedFunction verifies that commands inside a started function
// do not fail on OOM, but the next write starting over maxmemory is refused.
func TestFactF4OOMInsideStartedFunction(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	require.NoError(t, c.ConfigSet(ctx, "maxmemory-policy", "noeviction").Err(), "config set maxmemory-policy noeviction")

	code := `#!lua name=facts_test
redis.register_function('fct_f4_write', function(keys, args)
    local prefix = keys[1]
    local n = tonumber(args[1])
    local val = string.rep('x', 1024)
    for i = 1, n do
        redis.call('SET', prefix .. ':' .. i, val)
    end
    return n
end)
redis.register_function{
    function_name = 'fct_f4_read',
    flags = {'no-writes'},
    callback = function(keys, args)
        return redis.call('GET', keys[1])
    end
}`
	loadReplace(t, ctx, c, code)

	info, err := c.Info(ctx, "memory").Result()
	require.NoError(t, err, "info memory")
	used := parseUsedMemory(info)
	require.NotZero(t, used, "could not parse used_memory from info:\n%s", info)

	// Set maxmemory to used + 300 KiB: the function writes 2,000 records of
	// 1 KiB (~2 MB+), crossing maxmemory.
	maxMem := used + 300*1024
	require.NoError(t, c.ConfigSet(ctx, "maxmemory", strconv.FormatInt(maxMem, 10)).Err(), "config set maxmemory")
	t.Cleanup(func() {
		require.NoError(t, c.ConfigSet(ctx, "maxmemory", "0").Err(), "config set maxmemory 0")
		cfg, err := c.ConfigGet(ctx, "maxmemory").Result()
		require.NoError(t, err, "config get maxmemory")
		require.Equal(t, "0", cfg["maxmemory"], "restored maxmemory")
	})

	// Started function runs to end and writes everything
	res, err := c.FCall(ctx, "fct_f4_write", []string{"dev-facts-f4"}, 2000).Int()
	require.NoError(t, err, "fct_f4_write")
	require.Equal(t, 2000, res)

	infoAfter, err := c.Info(ctx, "memory").Result()
	require.NoError(t, err, "info memory after")
	usedAfter := parseUsedMemory(infoAfter)
	require.Greater(t, usedAfter, maxMem, "used_memory (%d) over maxmemory (%d)", usedAfter, maxMem)

	// Next write over maxmemory is refused at start with raw OOM error
	oomErr := c.FCall(ctx, "fct_f4_write", []string{"dev-facts-f4-next"}, 1).Err()
	require.Error(t, oomErr)
	require.Contains(t, oomErr.Error(), "OOM command not allowed", "OOM at start of next write")

	// Read (FCALL_RO) still works while over maxmemory
	val, err := c.FCallRO(ctx, "fct_f4_read", []string{"dev-facts-f4:1"}).Text()
	require.NoError(t, err, "fct_f4_read over maxmemory")
	require.Len(t, val, 1024)
}

// TestFactF5GlobalNamesAndReplaceWhole verifies that function names are global across
// libraries and that a library is replaced whole on FUNCTION LOAD REPLACE.
func TestFactF5GlobalNamesAndReplaceWhole(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	// 1. Function names are global across libraries
	code1 := `#!lua name=facts_test
redis.register_function('fct_f5_step', function() return 1 end)`
	require.NoError(t, c.FunctionLoad(ctx, code1).Err(), "load facts_test")

	codeProbe := `#!lua name=facts_test_probe
redis.register_function('fct_f5_step', function() return 2 end)`
	err := c.FunctionLoad(ctx, codeProbe).Err()
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists", "a function name is global across libraries")

	// 2. A library is replaced whole: functions from the old version disappear
	codeReplace1 := `#!lua name=facts_test_replace
redis.register_function('fct_f5_only', function() return 'first' end)`
	require.NoError(t, c.FunctionLoad(ctx, codeReplace1).Err(), "load facts_test_replace")

	res1, err := c.FCall(ctx, "fct_f5_only", nil).Text()
	require.NoError(t, err)
	require.Equal(t, "first", res1)

	codeReplace2 := `#!lua name=facts_test_replace
redis.register_function('fct_f5_other', function() return 'second' end)`
	require.NoError(t, c.FunctionLoadReplace(ctx, codeReplace2).Err(), "replace facts_test_replace")

	err = c.FCall(ctx, "fct_f5_only", nil).Err()
	require.Error(t, err)
	require.Contains(t, err.Error(), "Function not found", "the old function is gone after the replace")

	res2, err := c.FCall(ctx, "fct_f5_other", nil).Text()
	require.NoError(t, err)
	require.Equal(t, "second", res2)

	require.NoError(t, c.FunctionDelete(ctx, "facts_test_replace").Err(), "function delete")
}

// TestFactF6UndeclaredKeys verifies that undeclared keys work on a standalone server.
func TestFactF6UndeclaredKeys(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	clusterInfo, err := c.Info(ctx, "cluster").Result()
	require.NoError(t, err, "info cluster")
	require.Contains(t, clusterInfo, "cluster_enabled:0", "a standalone server")

	code := `#!lua name=facts_test
redis.register_function('fct_f6_undeclared', function(keys, args)
    redis.call('SET', 'dev-facts-f6', 'x')
    return redis.call('GET', 'dev-facts-f6')
end)`
	loadReplace(t, ctx, c, code)

	res, err := c.FCall(ctx, "fct_f6_undeclared", nil).Text()
	require.NoError(t, err, "fct_f6_undeclared with 0 KEYS")
	require.Equal(t, "x", res)

	val, err := c.Get(ctx, "dev-facts-f6").Result()
	require.NoError(t, err, "get")
	require.Equal(t, "x", val)
}

// TestFactF7StreamIDsAndReset verifies explicit stream id constraints, trimming,
// and that sequence resets only upon key deletion.
func TestFactF7StreamIDsAndReset(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	streamKey := "dev-facts-f7"
	xadd := func(id string) error {
		return c.XAdd(ctx, &redis.XAddArgs{Stream: streamKey, ID: id, Values: []string{"field", "val"}}).Err()
	}
	topItem := "equal or smaller than the target stream top item"

	// 1. XADD 5-0 works
	id, err := c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "5-0",
		Values: []string{"field", "val"},
	}).Result()
	require.NoError(t, err, "xadd 5-0")
	require.Equal(t, "5-0", id)

	// 2. 5-0 again and 4-0 are refused
	for _, id := range []string{"5-0", "4-0"} {
		err = xadd(id)
		require.Error(t, err)
		require.Contains(t, err.Error(), topItem, "xadd %s", id)
	}

	// 3. 0-0 is refused "must be greater than 0-0"
	err = xadd("0-0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be greater than 0-0")

	// 4. After XTRIM MAXLEN 0, XINFO shows last-generated-id 5-0 and entries-added 1
	require.NoError(t, c.XTrimMaxLen(ctx, streamKey, 0).Err(), "xtrim")

	info, err := c.XInfoStream(ctx, streamKey).Result()
	require.NoError(t, err, "xinfo")
	require.Equal(t, "5-0", info.LastGeneratedID)
	require.Equal(t, int64(1), info.EntriesAdded)
	require.Equal(t, int64(0), info.Length)

	err = xadd("5-0")
	require.Error(t, err)
	require.Contains(t, err.Error(), topItem, "XADD 5-0 refused after XTRIM")

	// 5. After XDEL 6-0, XADD 6-0 is refused
	id6, err := c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "6-0",
		Values: []string{"field", "val"},
	}).Result()
	require.NoError(t, err)
	require.Equal(t, "6-0", id6)

	delCount, err := c.XDel(ctx, streamKey, "6-0").Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), delCount)

	err = xadd("6-0")
	require.Error(t, err)
	require.Contains(t, err.Error(), topItem, "XADD 6-0 refused after XDEL")

	// 6. After DEL of the stream, XADD 1-0 works: deleting the key resets the seq
	require.NoError(t, c.Del(ctx, streamKey).Err(), "del")

	id1, err := c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "1-0",
		Values: []string{"field", "val"},
	}).Result()
	require.NoError(t, err, "xadd 1-0 after DEL")
	require.Equal(t, "1-0", id1)
}

// TestFactF8TimeFrozenInFunction verifies that TIME is frozen for the duration
// of a function call, in both writing and no-writes functions.
func TestFactF8TimeFrozenInFunction(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	code := `#!lua name=facts_test
redis.register_function('fct_f8_time', function(keys, args)
    local t1 = redis.call('TIME')
    local c0 = os.clock()
    while os.clock() - c0 < 0.05 do
    end
    local t2 = redis.call('TIME')
    return {t1[1], t1[2], t2[1], t2[2]}
end)
redis.register_function{
    function_name = 'fct_f8_time_ro',
    flags = {'no-writes'},
    callback = function(keys, args)
        local t1 = redis.call('TIME')
        local c0 = os.clock()
        while os.clock() - c0 < 0.05 do
        end
        local t2 = redis.call('TIME')
        return {t1[1], t1[2], t2[1], t2[2]}
    end
}`
	loadReplace(t, ctx, c, code)

	for _, tt := range []struct {
		name string
		call func() *redis.Cmd
	}{
		{"a writing function", func() *redis.Cmd { return c.FCall(ctx, "fct_f8_time", nil) }},
		{"a no-writes function", func() *redis.Cmd { return c.FCallRO(ctx, "fct_f8_time_ro", nil) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.call().Slice()
			require.NoError(t, err)
			require.Len(t, res, 4)
			require.Equal(t, res[0], res[2], "TIME seconds frozen in %s", tt.name)
			require.Equal(t, res[1], res[3], "TIME microseconds frozen in %s", tt.name)
		})
	}
}

// TestFactF9ZMSCOREAndXINFO verifies ZMSCORE of 2,000 members in one command
// and that XINFO STREAM latency is O(1) in stream length.
func TestFactF9ZMSCOREAndXINFO(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 60*time.Second)

	require.NoError(t, c.ConfigSet(ctx, "slowlog-log-slower-than", "0").Err(), "config set slowlog-log-slower-than 0")

	code := `#!lua name=facts_test
redis.register_function('fct_f9_zadd', function(keys, args)
    local k = keys[1]
    local n = tonumber(args[1])
    for i = 1, n do
        redis.call('ZADD', k, i, 'm:' .. i)
    end
    return n
end)
redis.register_function('fct_f9_zmscore', function(keys, args)
    local k = keys[1]
    return redis.call('ZMSCORE', k, unpack(args))
end)
redis.register_function('fct_f9_fill', function(keys, args)
    local k = keys[1]
    local n = tonumber(args[1])
    for i = 1, n do
        redis.call('XADD', k, '*', 'f', 'v')
    end
    return n
end)`
	loadReplace(t, ctx, c, code)

	// Part 1: ZMSCORE of 2,000 members inside a function
	zsetKey := "dev-facts-f9-zset"
	const numMembers = 2000
	added, err := c.FCall(ctx, "fct_f9_zadd", []string{zsetKey}, numMembers).Int()
	require.NoError(t, err, "fct_f9_zadd")
	require.Equal(t, numMembers, added)

	memberArgs := make([]any, numMembers)
	for i := range memberArgs {
		memberArgs[i] = fmt.Sprintf("m:%d", i+1)
	}
	scores, err := c.FCall(ctx, "fct_f9_zmscore", []string{zsetKey}, memberArgs...).Slice()
	require.NoError(t, err, "fct_f9_zmscore")
	require.Len(t, scores, numMembers)
	for i, got := range scores {
		require.Equal(t, fmt.Sprintf("%d", i+1), got, "score[%d]", i)
	}

	// Part 2: XINFO STREAM O(1) time
	streamKey := "dev-facts-f9-stream"
	require.NoError(t, c.FCall(ctx, "fct_f9_fill", []string{streamKey}, 10).Err(), "fill 10")

	sampleXInfo := func(samples int) time.Duration {
		var durations []time.Duration
		for i := 0; i < samples; i++ {
			require.NoError(t, c.SlowLogReset(ctx).Err(), "slowlog reset")
			_, err := c.XInfoStream(ctx, streamKey).Result()
			require.NoError(t, err, "xinfo")
			entries, err := c.SlowLogGet(ctx, 1).Result()
			require.NoError(t, err, "slowlog get")
			require.NotEmpty(t, entries, "slowlog empty")
			durations = append(durations, entries[0].Duration)
		}
		slices.Sort(durations)
		return durations[len(durations)/2] // median
	}

	med10 := sampleXInfo(20)

	// Add 5,000,000 entries
	const largeSize = 5000000
	fillStart := time.Now()
	res, err := c.FCall(ctx, "fct_f9_fill", []string{streamKey}, largeSize).Int()
	elapsedSec := float64(time.Now().Sub(fillStart).Milliseconds()) / 1000.0
	require.NoError(t, err, "fill %d failed in %.3fs", largeSize, elapsedSec)
	require.Equal(t, largeSize, res)
	require.LessOrEqual(t, elapsedSec, 20.0, "fill %d took %.3fs (> 20s bound)", largeSize, elapsedSec)

	medLarge := sampleXInfo(20)

	require.LessOrEqual(t, medLarge, 10*med10, "XINFO STREAM time scaled with stream size: 10 entries=%v, 5000000 entries=%v (> 10x)", med10, medLarge)
	t.Logf("XINFO STREAM SLOWLOG: 10 entries=%v, 5000000 entries=%v", med10, medLarge)
}

// TestFactF10Commandstats verifies that commands issued inside functions are counted
// in INFO commandstats.
func TestFactF10Commandstats(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	code := `#!lua name=facts_test
redis.register_function('fct_f10_cmds', function(keys, args)
    local k = keys[1]
    for i = 1, 100 do
        redis.call('SET', k .. ':' .. i, i)
        redis.call('GET', k .. ':' .. i)
    end
    redis.call('LPUSH', k .. ':list', 'item')
    redis.call('RPOP', k .. ':list')
    return 1
end)`
	loadReplace(t, ctx, c, code)

	require.NoError(t, c.ConfigResetStat(ctx).Err(), "config resetstat")

	res, err := c.FCall(ctx, "fct_f10_cmds", []string{"dev-facts-f10"}).Int()
	require.NoError(t, err, "fcall")
	require.Equal(t, 1, res)

	info, err := c.Info(ctx, "commandstats").Result()
	require.NoError(t, err, "info commandstats")
	stats := parseCommandStats(info)

	for _, tt := range []struct {
		cmd  string
		want int64
	}{
		{"set", 100}, {"get", 100}, {"lpush", 1}, {"rpop", 1}, {"fcall", 1},
	} {
		require.Equal(t, tt.want, stats[tt.cmd], "%s calls (info: %s)", tt.cmd, info)
	}
}

// TestFactF12LoadTimeSandboxHasNoSetmetatable verifies that the environment a
// library's body runs in at FUNCTION LOAD time has no setmetatable (nor type,
// math, error, assert or pcall): a library that calls one at load is refused,
// naming the missing global, and is not left in FUNCTION LIST.
func TestFactF12LoadTimeSandboxHasNoSetmetatable(t *testing.T) {
	t.Parallel()
	_, c, ctx := startStore(t, 0)

	for _, tc := range []struct{ global, call string }{
		{"setmetatable", "setmetatable({}, {})"},
		{"type", "type(1)"},
		{"math", "math.floor(1)"},
		{"error", "error('x')"},
		{"assert", "assert(true)"},
		{"pcall", "pcall(function() end)"},
	} {
		code := "#!lua name=f12probe\n" + tc.call
		err := c.FunctionLoadReplace(ctx, code).Err()
		require.Error(t, err)
		require.Contains(t, err.Error(), tc.global, "FUNCTION LOAD calling %s at load", tc.call)
		t.Logf("%s at load: %v", tc.global, err)

		libs, err := c.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: "f12probe"}).Result()
		require.NoError(t, err, "function list")
		require.Empty(t, libs, "no f12probe library after a refused load of %s", tc.call)
	}
}

func parseUsedMemory(info string) int64 {
	scanner := bufio.NewScanner(strings.NewReader(info))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "used_memory:") {
			val := strings.TrimPrefix(line, "used_memory:")
			n, _ := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
			return n
		}
	}
	return 0
}

func parseCommandStats(info string) map[string]int64 {
	out := make(map[string]int64)
	scanner := bufio.NewScanner(strings.NewReader(info))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "cmdstat_") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				cmd := strings.TrimPrefix(parts[0], "cmdstat_")
				fields := strings.Split(parts[1], ",")
				for _, f := range fields {
					kv := strings.SplitN(f, "=", 2)
					if len(kv) == 2 && kv[0] == "calls" {
						calls, _ := strconv.ParseInt(kv[1], 10, 64)
						out[cmd] = calls
					}
				}
			}
		}
	}
	return out
}

func logRedisVersion(t *testing.T, c *redis.Client) {
	t.Helper()
	info, err := c.Info(context.Background(), "server").Result()
	require.NoError(t, err, "info server")
	scanner := bufio.NewScanner(strings.NewReader(info))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "redis_version:") {
			ver := strings.TrimPrefix(line, "redis_version:")
			t.Logf("redis_version: %s", strings.TrimSpace(ver))
			return
		}
	}
	require.Failf(t, "assertion failed", "redis_version not found in INFO server:\n%s", info)
}
