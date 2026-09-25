-- Specs in Redis (nova-tools#3370, part of #3364): a typed SPEC line's facts
-- (who, rev, score, stream) go onto the record pr:<name>:<n> in the same call
-- that stores the line, and the second distinct 10 at the current rev moves
-- the spec working -> done and releases every task waiting on
-- spec:<name>#<n> in that call. The file sorts after task_claim.lua so it may
-- read NS.DEP (the DEPENDS-ON release, task_queue.lua).
--
-- Keys (one writer: this file):
--   pr:<name>:<n>             spec_rev, spec_state (working|done),
--                             spec_stream, spec_created_at, spec_at,
--                             spec_done_at, spec_score:<who> = "<rev> <score>",
--                             last_line, last_line_at (as read post writes)
--   pr:<name>:<n>:lines       the typed line (RPUSH), as read post writes
--   specs:<stream>:working    ZSET of <name>#<n>, score spec_created_at (age)
--   specs:<stream>:done       ZSET, the same score; a move never changes it
--   specs:streams             SET of stream names the list reads
-- Invariant: a spec is in exactly the one set its spec_stream and spec_state
-- name; the pointer and the membership change in the same call.
--
-- Done (Glenn 2026-09-24 11:30 AM ET): two distinct who= with score 10 at the
-- latest rev; any typed 10 counts. A newer rev resets the count (the old
-- rev's 10s stay on the record and do not count); a line at an older rev is
-- refused STALE_REV with nothing written.

local US_TENS = 2

local function us_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- us_tens counts the distinct who with a 10 at rev on the record.
local function us_tens(rec, rev)
  local flat = redis.call('HGETALL', rec)
  local want = tostring(rev) .. ' 10'
  local n = 0
  for i = 1, #flat, 2 do
    if string.sub(flat[i], 1, 11) == 'spec_score:' and flat[i + 1] == want then
      n = n + 1
    end
  end
  return n
end

-- ns_spec_mark(name, n, who, rev, score, stream, line, sprint, actor)
-- Replies {answer, state, tens, released, stream, lines}; answer is RECORDED,
-- NEW_REV, DONE or SAME (written or already so), STALE_REV <rev> or
-- INVALID <why> (nothing written). stream '' keeps the spec's stream, else
-- the record's stream field, else '-'. sprint '' is the first of
-- sprint:order; with no sprint the release has no tasks to walk.
local function spec_mark(keys, args)
  local name, n, who = args[1] or '', args[2] or '', args[3] or ''
  local rev, score = tonumber(args[4] or ''), tonumber(args[5] or '')
  local stream, line, S, actor = args[6] or '', args[7] or '', args[8] or '', args[9] or ''
  if name == '' or string.find(name, '[/#:%s]') then
    return { 'INVALID', 'want a bare repo name' }
  end
  if not string.match(n, '^%d+$') then
    return { 'INVALID', 'want a positive issue number' }
  end
  if not string.match(who, '^[%w_.-]+$') then
    return { 'INVALID', 'want who=<friend>' }
  end
  if not rev or rev < 1 or rev ~= math.floor(rev) then
    return { 'INVALID', 'want rev=<k>, a positive integer' }
  end
  if not score or score < 0 or score > 10 or score ~= math.floor(score) then
    return { 'INVALID', 'want score=<0..10>' }
  end
  if string.find(stream, '[%s:]') then
    return { 'INVALID', 'want a stream name without spaces or colons' }
  end
  local rec = 'pr:' .. name .. ':' .. n
  local id = name .. '#' .. n
  local cur = redis.call('HMGET', rec, 'spec_rev', 'spec_state', 'spec_stream', 'spec_created_at', 'spec_score:' .. who, 'stream')
  local cur_rev = tonumber(cur[1] or '') or 0
  local old_state, old_stream = cur[2] or '', cur[3] or ''
  if cur_rev > rev then
    return { 'STALE_REV', tostring(cur_rev) }
  end
  if stream == '' then
    stream = old_stream
    if stream == '' then
      stream = cur[6] or ''
    end
    if stream == '' then
      stream = '-'
    end
  end
  local fact = tostring(rev) .. ' ' .. tostring(score)
  if cur[5] == fact and cur_rev == rev and old_stream == stream then
    return { 'SAME', old_state, tostring(us_tens(rec, rev)), '0', stream, tostring(redis.call('LLEN', rec .. ':lines')) }
  end
  local at = us_now_ms()
  local created = tonumber(cur[4] or '') or at
  redis.call('HSET', rec, 'spec_score:' .. who, fact, 'spec_rev', tostring(rev),
    'spec_stream', stream, 'spec_created_at', tostring(created), 'spec_at', tostring(at))
  local tens = us_tens(rec, rev)
  local state = 'working'
  if tens >= US_TENS then
    state = 'done'
  end
  if old_state ~= '' and old_stream ~= '' then
    redis.call('ZREM', 'specs:' .. old_stream .. ':' .. old_state, id)
  end
  redis.call('ZADD', 'specs:' .. stream .. ':' .. state, created, id)
  redis.call('SADD', 'specs:streams', stream)
  redis.call('HSET', rec, 'spec_state', state)
  if line ~= '' then
    redis.call('RPUSH', rec .. ':lines', line)
    local first = string.match(line, '^[^\r\n]*')
    redis.call('HSET', rec, 'last_line', first, 'last_line_at', tostring(math.floor(at / 1000)))
  end
  local answer = 'RECORDED'
  if cur_rev > 0 and rev > cur_rev then
    answer = 'NEW_REV'
  end
  local released = 0
  if state == 'done' and old_state ~= 'done' then
    answer = 'DONE'
    redis.call('HSET', rec, 'spec_done_at', tostring(at))
    if S == '' then
      S = redis.call('ZRANGE', 'sprint:order', 0, 0)[1] or ''
    end
    if S ~= '' then
      released = NS.DEP.resolve(S, 'spec:' .. id, true, actor, 'spec:' .. id .. '@r' .. tostring(rev), at)
    end
  end
  return { answer, state, tostring(tens), tostring(released), stream, tostring(redis.call('LLEN', rec .. ':lines')) }
end

-- ns_spec_list(stream): read-only. With stream '' one row {stream, working,
-- done} per stream in specs:streams, sorted; with a stream, that row, then
-- its working ids and its done ids, each oldest first.
local function spec_list(keys, args)
  local want = args[1] or ''
  local names = { want }
  if want == '' then
    names = redis.call('SMEMBERS', 'specs:streams')
    table.sort(names)
  end
  local rows = {}
  for _, s in ipairs(names) do
    rows[#rows + 1] = { s, redis.call('ZCARD', 'specs:' .. s .. ':working'), redis.call('ZCARD', 'specs:' .. s .. ':done') }
  end
  if want == '' then
    return rows
  end
  return { rows, redis.call('ZRANGE', 'specs:' .. want .. ':working', 0, -1), redis.call('ZRANGE', 'specs:' .. want .. ':done', 0, -1) }
end

redis.register_function('ns_spec_mark', spec_mark)
redis.register_function{ function_name = 'ns_spec_list', callback = spec_list, flags = { 'no-writes' } }
