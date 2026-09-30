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
)

// TestFactF1NoRollback verifies that a function that errors keeps its earlier writes.
// Redis functions do not roll back; writes executed before an error persist.
func TestFactF1NoRollback(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

	code := `#!lua name=facts_test
redis.register_function('fct_f1_seterr', function(keys, args)
    redis.call('SET', keys[1], args[1])
    error('deliberate error after SET')
end)`
	if err := c.FunctionLoadReplace(ctx, code).Err(); err != nil {
		t.Fatalf("load: %v", err)
	}

	key := "dev-facts-f1"
	err := c.FCall(ctx, "fct_f1_seterr", []string{key}, "1").Err()
	if err == nil || !strings.Contains(err.Error(), "deliberate error after SET") {
		t.Fatalf("expected deliberate error from fct_f1_seterr, got: %v", err)
	}

	val, err := c.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	if val != "1" {
		t.Fatalf("expected write to persist with value '1', got %q", val)
	}
}

// TestFactF2KillOnlyBeforeWrite verifies that FUNCTION KILL stops only a function
// that has not executed write commands once busy-reply-threshold is exceeded.
func TestFactF2KillOnlyBeforeWrite(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

	// Verify default busy-reply-threshold is 5000 ms
	cfg, err := c.ConfigGet(ctx, "busy-reply-threshold").Result()
	if err != nil {
		t.Fatalf("config get busy-reply-threshold: %v", err)
	}
	if cfg["busy-reply-threshold"] != "5000" {
		t.Fatalf("expected default busy-reply-threshold 5000, got %s", cfg["busy-reply-threshold"])
	}

	// Lower threshold to 1000 ms for test execution
	if err := c.ConfigSet(ctx, "busy-reply-threshold", "1000").Err(); err != nil {
		t.Fatalf("config set busy-reply-threshold 1000: %v", err)
	}

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
	if err := c.FunctionLoadReplace(ctx, code).Err(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Case 1: Writing function -> FUNCTION KILL answers UNKILLABLE
	{
		cRunner := redis.NewClient(&redis.Options{Addr: addr})
		defer cRunner.Close()
		cKiller := redis.NewClient(&redis.Options{Addr: addr})
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

		if killErr == nil || !strings.Contains(killErr.Error(), "UNKILLABLE") {
			t.Fatalf("expected UNKILLABLE for writing function, got: %v", killErr)
		}

		wg.Wait()
		if runnerErr != nil {
			t.Fatalf("writing function should run to end, but failed: %v", runnerErr)
		}
		if runnerRes != "done" {
			t.Fatalf("expected 'done', got %q", runnerRes)
		}
	}

	// Case 2: No-writes function -> FUNCTION KILL answers OK
	{
		cRunner := redis.NewClient(&redis.Options{Addr: addr})
		defer cRunner.Close()
		cKiller := redis.NewClient(&redis.Options{Addr: addr})
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

		if killErr != nil {
			t.Fatalf("expected OK (nil error) for killing no-write function, got: %v", killErr)
		}

		wg.Wait()
		if runnerErr == nil || !strings.Contains(runnerErr.Error(), "Script killed by user") {
			t.Fatalf("expected 'Script killed by user' error, got res=%q err=%v", runnerRes, runnerErr)
		}
	}

	// Restore 5000 and assert it was restored
	if err := c.ConfigSet(ctx, "busy-reply-threshold", "5000").Err(); err != nil {
		t.Fatalf("config set busy-reply-threshold 5000: %v", err)
	}
	cfgRestored, err := c.ConfigGet(ctx, "busy-reply-threshold").Result()
	if err != nil {
		t.Fatalf("config get busy-reply-threshold: %v", err)
	}
	if cfgRestored["busy-reply-threshold"] != "5000" {
		t.Fatalf("expected restored busy-reply-threshold 5000, got %s", cfgRestored["busy-reply-threshold"])
	}
}

// TestFactF3UnpackLimit verifies the exact unpack bound: 7,999 values works and 8,000 fails.
func TestFactF3UnpackLimit(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

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
	if err := c.FunctionLoadReplace(ctx, code).Err(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// unpack of 7,999 values works
	res, err := c.FCall(ctx, "fct_f3_unpack", nil, 7999).Int()
	if err != nil {
		t.Fatalf("unpack of 7,999 values failed: %v", err)
	}
	if res != 7999 {
		t.Fatalf("expected 7999, got %d", res)
	}

	// unpack of 8,000 values fails
	err = c.FCall(ctx, "fct_f3_unpack", nil, 8000).Err()
	if err == nil {
		t.Fatalf("unpack of 8,000 values unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "too many results to unpack") {
		t.Fatalf("expected 'too many results to unpack', got: %v", err)
	}
}

// TestFactF4OOMInsideStartedFunction verifies that commands inside a started function
// do not fail on OOM, but the next write starting over maxmemory is refused.
func TestFactF4OOMInsideStartedFunction(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

	if err := c.ConfigSet(ctx, "maxmemory-policy", "noeviction").Err(); err != nil {
		t.Fatalf("config set maxmemory-policy noeviction: %v", err)
	}

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
	if err := c.FunctionLoadReplace(ctx, code).Err(); err != nil {
		t.Fatalf("load: %v", err)
	}

	info, err := c.Info(ctx, "memory").Result()
	if err != nil {
		t.Fatalf("info memory: %v", err)
	}
	used := parseUsedMemory(info)
	if used == 0 {
		t.Fatalf("could not parse used_memory from info:\n%s", info)
	}

	// Set maxmemory to used + 300 KiB.
	// The function will write 2,000 records of 1 KiB (~2 MB+ of data), crossing maxmemory.
	maxMem := used + 300*1024
	if err := c.ConfigSet(ctx, "maxmemory", strconv.FormatInt(maxMem, 10)).Err(); err != nil {
		t.Fatalf("config set maxmemory: %v", err)
	}

	// Started function runs to end and writes everything
	res, err := c.FCall(ctx, "fct_f4_write", []string{"dev-facts-f4"}, 2000).Int()
	if err != nil {
		t.Fatalf("fct_f4_write failed: %v", err)
	}
	if res != 2000 {
		t.Fatalf("expected 2000, got %d", res)
	}

	infoAfter, err := c.Info(ctx, "memory").Result()
	if err != nil {
		t.Fatalf("info memory after: %v", err)
	}
	usedAfter := parseUsedMemory(infoAfter)
	if usedAfter <= maxMem {
		t.Fatalf("expected used_memory (%d) > maxmemory (%d)", usedAfter, maxMem)
	}

	// Next write over maxmemory is refused at start with raw OOM error
	oomErr := c.FCall(ctx, "fct_f4_write", []string{"dev-facts-f4-next"}, 1).Err()
	if oomErr == nil || !strings.Contains(oomErr.Error(), "OOM command not allowed") {
		t.Fatalf("expected OOM error at start of next write, got: %v", oomErr)
	}

	// Read (FCALL_RO) still works while over maxmemory
	val, err := c.FCallRO(ctx, "fct_f4_read", []string{"dev-facts-f4:1"}).Text()
	if err != nil {
		t.Fatalf("fct_f4_read failed while over maxmemory: %v", err)
	}
	if len(val) != 1024 {
		t.Fatalf("expected 1024 bytes, got %d", len(val))
	}

	// Restore maxmemory to 0 and assert it was restored
	if err := c.ConfigSet(ctx, "maxmemory", "0").Err(); err != nil {
		t.Fatalf("config set maxmemory 0: %v", err)
	}
	cfg, err := c.ConfigGet(ctx, "maxmemory").Result()
	if err != nil {
		t.Fatalf("config get maxmemory: %v", err)
	}
	if cfg["maxmemory"] != "0" {
		t.Fatalf("expected restored maxmemory 0, got %s", cfg["maxmemory"])
	}
}

// TestFactF5GlobalNamesAndReplaceWhole verifies that function names are global across
// libraries and that a library is replaced whole on FUNCTION LOAD REPLACE.
func TestFactF5GlobalNamesAndReplaceWhole(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

	// 1. Function names are global across libraries
	code1 := `#!lua name=facts_test
redis.register_function('fct_f5_step', function() return 1 end)`
	if err := c.FunctionLoad(ctx, code1).Err(); err != nil {
		t.Fatalf("load facts_test: %v", err)
	}

	codeProbe := `#!lua name=facts_test_probe
redis.register_function('fct_f5_step', function() return 2 end)`
	err := c.FunctionLoad(ctx, codeProbe).Err()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected 'already exists' for duplicate function name across libraries, got: %v", err)
	}

	// 2. A library is replaced whole: functions from previous version not in new version disappear
	codeReplace1 := `#!lua name=facts_test_replace
redis.register_function('fct_f5_only', function() return 'first' end)`
	if err := c.FunctionLoad(ctx, codeReplace1).Err(); err != nil {
		t.Fatalf("load facts_test_replace: %v", err)
	}

	res1, err := c.FCall(ctx, "fct_f5_only", nil).Text()
	if err != nil || res1 != "first" {
		t.Fatalf("fct_f5_only: res=%s err=%v", res1, err)
	}

	codeReplace2 := `#!lua name=facts_test_replace
redis.register_function('fct_f5_other', function() return 'second' end)`
	if err := c.FunctionLoadReplace(ctx, codeReplace2).Err(); err != nil {
		t.Fatalf("load replace facts_test_replace: %v", err)
	}

	// fct_f5_only should no longer exist
	err = c.FCall(ctx, "fct_f5_only", nil).Err()
	if err == nil || !strings.Contains(err.Error(), "Function not found") {
		t.Fatalf("expected 'Function not found' for old function after library replacement, got: %v", err)
	}

	// fct_f5_other should exist
	res2, err := c.FCall(ctx, "fct_f5_other", nil).Text()
	if err != nil || res2 != "second" {
		t.Fatalf("fct_f5_other: res=%s err=%v", res2, err)
	}

	// Clean up replace library
	if err := c.FunctionDelete(ctx, "facts_test_replace").Err(); err != nil {
		t.Fatalf("function delete: %v", err)
	}
}

// TestFactF6UndeclaredKeys verifies that undeclared keys work on a standalone server.
func TestFactF6UndeclaredKeys(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

	// Verify standalone server (cluster_enabled:0)
	clusterInfo, err := c.Info(ctx, "cluster").Result()
	if err != nil {
		t.Fatalf("info cluster: %v", err)
	}
	if !strings.Contains(clusterInfo, "cluster_enabled:0") {
		t.Fatalf("expected cluster_enabled:0, got:\n%s", clusterInfo)
	}

	code := `#!lua name=facts_test
redis.register_function('fct_f6_undeclared', function(keys, args)
    redis.call('SET', 'dev-facts-f6', 'x')
    return redis.call('GET', 'dev-facts-f6')
end)`
	if err := c.FunctionLoadReplace(ctx, code).Err(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Call with 0 KEYS
	res, err := c.FCall(ctx, "fct_f6_undeclared", nil).Text()
	if err != nil {
		t.Fatalf("fct_f6_undeclared: %v", err)
	}
	if res != "x" {
		t.Fatalf("expected 'x', got %q", res)
	}

	val, err := c.Get(ctx, "dev-facts-f6").Result()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "x" {
		t.Fatalf("expected 'x', got %q", val)
	}
}

// TestFactF7StreamIDsAndReset verifies explicit stream id constraints, trimming,
// and that sequence resets only upon key deletion.
func TestFactF7StreamIDsAndReset(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()
	streamKey := "dev-facts-f7"

	// 1. XADD 5-0 works
	id, err := c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "5-0",
		Values: []string{"field", "val"},
	}).Result()
	if err != nil {
		t.Fatalf("xadd 5-0: %v", err)
	}
	if id != "5-0" {
		t.Fatalf("expected 5-0, got %s", id)
	}

	// 2. 5-0 again and 4-0 are refused "equal or smaller than the target stream top item"
	err = c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "5-0",
		Values: []string{"field", "val"},
	}).Err()
	if err == nil || !strings.Contains(err.Error(), "equal or smaller than the target stream top item") {
		t.Fatalf("expected 'equal or smaller than the target stream top item', got: %v", err)
	}

	err = c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "4-0",
		Values: []string{"field", "val"},
	}).Err()
	if err == nil || !strings.Contains(err.Error(), "equal or smaller than the target stream top item") {
		t.Fatalf("expected 'equal or smaller than the target stream top item', got: %v", err)
	}

	// 3. 0-0 is refused "must be greater than 0-0"
	err = c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "0-0",
		Values: []string{"field", "val"},
	}).Err()
	if err == nil || !strings.Contains(err.Error(), "must be greater than 0-0") {
		t.Fatalf("expected 'must be greater than 0-0', got: %v", err)
	}

	// 4. After XTRIM MAXLEN 0, XINFO shows last-generated-id 5-0 and entries-added 1, and XADD 5-0 is still refused
	if err := c.XTrimMaxLen(ctx, streamKey, 0).Err(); err != nil {
		t.Fatalf("xtrim: %v", err)
	}

	info, err := c.XInfoStream(ctx, streamKey).Result()
	if err != nil {
		t.Fatalf("xinfo: %v", err)
	}
	if info.LastGeneratedID != "5-0" {
		t.Fatalf("expected last-generated-id 5-0, got %s", info.LastGeneratedID)
	}
	if info.EntriesAdded != 1 {
		t.Fatalf("expected entries-added 1, got %d", info.EntriesAdded)
	}
	if info.Length != 0 {
		t.Fatalf("expected length 0, got %d", info.Length)
	}

	err = c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "5-0",
		Values: []string{"field", "val"},
	}).Err()
	if err == nil || !strings.Contains(err.Error(), "equal or smaller than the target stream top item") {
		t.Fatalf("expected XADD 5-0 refused after XTRIM, got: %v", err)
	}

	// 5. After XDEL 6-0, XADD 6-0 is refused
	id6, err := c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "6-0",
		Values: []string{"field", "val"},
	}).Result()
	if err != nil || id6 != "6-0" {
		t.Fatalf("xadd 6-0: id=%s err=%v", id6, err)
	}

	delCount, err := c.XDel(ctx, streamKey, "6-0").Result()
	if err != nil || delCount != 1 {
		t.Fatalf("xdel 6-0: count=%d err=%v", delCount, err)
	}

	err = c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "6-0",
		Values: []string{"field", "val"},
	}).Err()
	if err == nil || !strings.Contains(err.Error(), "equal or smaller than the target stream top item") {
		t.Fatalf("expected XADD 6-0 refused after XDEL, got: %v", err)
	}

	// 6. After DEL of the stream, XADD 1-0 works: deleting the key resets the seq
	if err := c.Del(ctx, streamKey).Err(); err != nil {
		t.Fatalf("del: %v", err)
	}

	id1, err := c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "1-0",
		Values: []string{"field", "val"},
	}).Result()
	if err != nil {
		t.Fatalf("xadd 1-0 after DEL failed: %v", err)
	}
	if id1 != "1-0" {
		t.Fatalf("expected 1-0, got %s", id1)
	}
}

// TestFactF8TimeFrozenInFunction verifies that TIME is frozen for the duration
// of a function call, in both writing and no-writes functions.
func TestFactF8TimeFrozenInFunction(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

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
	if err := c.FunctionLoadReplace(ctx, code).Err(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// 1. Plain / writing function
	res, err := c.FCall(ctx, "fct_f8_time", nil).Slice()
	if err != nil {
		t.Fatalf("fct_f8_time: %v", err)
	}
	if len(res) != 4 {
		t.Fatalf("expected 4 elements, got %v", res)
	}
	sec1, usec1 := res[0], res[1]
	sec2, usec2 := res[2], res[3]
	if sec1 != sec2 || usec1 != usec2 {
		t.Fatalf("time was not frozen in fct_f8_time: start=%v.%v end=%v.%v", sec1, usec1, sec2, usec2)
	}

	// 2. Read-only / no-writes function
	resRo, err := c.FCallRO(ctx, "fct_f8_time_ro", nil).Slice()
	if err != nil {
		t.Fatalf("fct_f8_time_ro: %v", err)
	}
	if len(resRo) != 4 {
		t.Fatalf("expected 4 elements, got %v", resRo)
	}
	secRo1, usecRo1 := resRo[0], resRo[1]
	secRo2, usecRo2 := resRo[2], resRo[3]
	if secRo1 != secRo2 || usecRo1 != usecRo2 {
		t.Fatalf("time was not frozen in fct_f8_time_ro: start=%v.%v end=%v.%v", secRo1, usecRo1, secRo2, usecRo2)
	}
}

// TestFactF9ZMSCOREAndXINFO verifies ZMSCORE of 2,000 members in one command
// and that XINFO STREAM latency is O(1) in stream length.
func TestFactF9ZMSCOREAndXINFO(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

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
	if err := c.FunctionLoadReplace(ctx, code).Err(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Part 1: ZMSCORE of 2,000 members inside a function
	zsetKey := "dev-facts-f9-zset"
	const numMembers = 2000
	added, err := c.FCall(ctx, "fct_f9_zadd", []string{zsetKey}, numMembers).Int()
	if err != nil || added != numMembers {
		t.Fatalf("fct_f9_zadd: added=%d err=%v", added, err)
	}

	var memberArgs []any
	for i := 1; i <= numMembers; i++ {
		memberArgs = append(memberArgs, fmt.Sprintf("m:%d", i))
	}
	scores, err := c.FCall(ctx, "fct_f9_zmscore", []string{zsetKey}, memberArgs...).Slice()
	if err != nil {
		t.Fatalf("fct_f9_zmscore: %v", err)
	}
	if len(scores) != numMembers {
		t.Fatalf("expected %d scores, got %d", numMembers, len(scores))
	}
	for i := 0; i < numMembers; i++ {
		wantScore := fmt.Sprintf("%d", i+1)
		gotScore, ok := scores[i].(string)
		if !ok || gotScore != wantScore {
			t.Fatalf("score[%d]: got %v, want %s", i, scores[i], wantScore)
		}
	}

	// Part 2: XINFO STREAM O(1) time
	streamKey := "dev-facts-f9-stream"
	// 10 entries
	if _, err := c.FCall(ctx, "fct_f9_fill", []string{streamKey}, 10).Result(); err != nil {
		t.Fatalf("fill 10: %v", err)
	}

	sampleXInfo := func(samples int) time.Duration {
		var durations []time.Duration
		for i := 0; i < samples; i++ {
			t0 := time.Now()
			_, err := c.XInfoStream(ctx, streamKey).Result()
			if err != nil {
				t.Fatalf("xinfo: %v", err)
			}
			durations = append(durations, time.Now().Sub(t0))
		}
		slices.Sort(durations)
		return durations[len(durations)/2] // median
	}

	med10 := sampleXInfo(30)

	// Add 500,000 entries (runs in well under 20 seconds)
	const largeSize = 500000
	fillStart := time.Now()
	res, err := c.FCall(ctx, "fct_f9_fill", []string{streamKey}, largeSize).Int()
	elapsedSec := float64(time.Now().Sub(fillStart).Milliseconds()) / 1000.0
	if err != nil || res != largeSize {
		t.Fatalf("fill %d failed in %.3fs: res=%d err=%v", largeSize, elapsedSec, res, err)
	}
	if elapsedSec > 20.0 {
		t.Fatalf("fill %d took %.3fs (> 20s bound)", largeSize, elapsedSec)
	}

	medLarge := sampleXInfo(30)

	// Assert XINFO STREAM time does not grow by more than ten times
	base := med10
	if base < 100*time.Microsecond {
		base = 100 * time.Microsecond
	}
	limit := 10 * base
	if medLarge > limit {
		t.Fatalf("XINFO STREAM time grew by more than 10x: med10=%v medLarge=%v limit=%v", med10, medLarge, limit)
	}
}

// TestFactF10Commandstats verifies that commands issued inside functions are counted
// in INFO commandstats.
func TestFactF10Commandstats(t *testing.T) {
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()

	ctx := context.Background()

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
	if err := c.FunctionLoadReplace(ctx, code).Err(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if err := c.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatalf("config resetstat: %v", err)
	}

	res, err := c.FCall(ctx, "fct_f10_cmds", []string{"dev-facts-f10"}).Int()
	if err != nil {
		t.Fatalf("fcall: %v", err)
	}
	if res != 1 {
		t.Fatalf("expected 1, got %d", res)
	}

	info, err := c.Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatalf("info commandstats: %v", err)
	}
	stats := parseCommandStats(info)

	if stats["set"] != 100 {
		t.Fatalf("expected 100 SET calls, got %d (info: %s)", stats["set"], info)
	}
	if stats["get"] != 100 {
		t.Fatalf("expected 100 GET calls, got %d (info: %s)", stats["get"], info)
	}
	if stats["lpush"] != 1 {
		t.Fatalf("expected 1 LPUSH call, got %d", stats["lpush"])
	}
	if stats["rpop"] != 1 {
		t.Fatalf("expected 1 RPOP call, got %d", stats["rpop"])
	}
	if stats["fcall"] != 1 {
		t.Fatalf("expected 1 FCALL call, got %d", stats["fcall"])
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
