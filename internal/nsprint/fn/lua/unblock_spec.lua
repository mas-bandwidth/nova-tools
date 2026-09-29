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
-- Done: at least quorum distinct who= with score >= pass at the
-- latest rev; default pass=9, quorum=2 (cfg:spec pass, quorum).
-- Quorum falls to 1 when readers-up count (friends with friend:<f>:beat at
-- timestamp within 60s, excluding spec owner) is <= 1.
-- A newer rev resets the count (the old rev's scores stay on the record
-- and do not count); a line at an older rev is refused STALE_REV with nothing written.

local function us_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- us_qualifying counts distinct readers with score >= pass at rev on the record.
local function us_qualifying(rec, rev, pass)
  local flat = redis.call('HGETALL', rec)
  local srev = tostring(rev)
  local n = 0
  for i = 1, #flat, 2 do
    if string.sub(flat[i], 1, 11) == 'spec_score:' then
      local val = flat[i + 1]
      local sp = string.find(val, ' ')
      if sp then
        local r = string.sub(val, 1, sp - 1)
        local sc = tonumber(string.sub(val, sp + 1))
        if r == srev and sc and sc >= pass then
          n = n + 1
        end
      end
    end
  end
  return n
end

-- us_spec_gate reads cfg:spec pass and quorum (defaults 9 and 2), and falls
-- quorum to 1 when readers-up (friends with beat <= 60s, excluding spec owner) <= 1.
local function us_spec_gate(rec, at)
  local pass = tonumber(redis.call('HGET', 'cfg:spec', 'pass') or '') or 9
  local quorum = tonumber(redis.call('HGET', 'cfg:spec', 'quorum') or '') or 2
  local owner = redis.call('HGET', rec, 'owner') or ''
  if owner == '' then owner = redis.call('HGET', rec, 'author') or '' end
  if owner == '' then owner = redis.call('HGET', rec, 'who') or '' end
  if owner == '' then owner = redis.call('HGET', rec, 'spec_owner') or '' end

  local readers_up = 0
  local friends = redis.call('SMEMBERS', 'friends')
  for _, f in ipairs(friends) do
    if f ~= owner then
      local beat = tonumber(redis.call('HGET', 'friend:' .. f .. ':beat', 'at') or '')
      if beat and at - beat <= 60000 and at >= beat - 5000 then
        readers_up = readers_up + 1
      end
    end
  end
  if readers_up <= 1 then
    quorum = 1
  end
  return pass, quorum
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
    local pass, _ = us_spec_gate(rec, us_now_ms())
    return { 'SAME', old_state, tostring(us_qualifying(rec, rev, pass)), '0', stream, tostring(redis.call('LLEN', rec .. ':lines')) }
  end
  local at = us_now_ms()
  local created = tonumber(cur[4] or '') or at
  redis.call('HSET', rec, 'spec_score:' .. who, fact, 'spec_rev', tostring(rev),
    'spec_stream', stream, 'spec_created_at', tostring(created), 'spec_at', tostring(at))
  local pass, quorum = us_spec_gate(rec, at)
  local qualifying = us_qualifying(rec, rev, pass)
  local state = 'working'
  if qualifying >= quorum then
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
  return { answer, state, tostring(qualifying), tostring(released), stream, tostring(redis.call('LLEN', rec .. ':lines')) }
end

-- ns_spec_revise(name, n, from_pr, note, line)
-- Bumps rev, resets state to working, moves set from done to working,
-- appends line to lines. Replies { 'REVISED', state, tostring(new_rev), stream }
local function spec_revise(keys, args)
  local name, n = args[1] or '', args[2] or ''
  local from_pr = args[3] or ''
  local note = args[4] or ''
  local line = args[5] or ''
  if name == '' or string.find(name, '[/#:%s]') then
    return { 'INVALID', 'want a bare repo name' }
  end
  if not string.match(n, '^%d+$') then
    return { 'INVALID', 'want a positive issue number' }
  end
  local rec = 'pr:' .. name .. ':' .. n
  local id = name .. '#' .. n
  local cur = redis.call('HMGET', rec, 'spec_rev', 'spec_state', 'spec_stream', 'spec_created_at', 'stream')
  local cur_rev = tonumber(cur[1] or '') or 0
  local old_state = cur[2] or ''
  local old_stream = cur[3] or ''
  local stream = old_stream
  if stream == '' then stream = cur[5] or '' end
  if stream == '' then stream = '-' end

  local new_rev = cur_rev + 1
  local at = us_now_ms()
  local created = tonumber(cur[4] or '') or at
  local new_state = 'working'

  if old_state ~= '' and old_stream ~= '' then
    redis.call('ZREM', 'specs:' .. old_stream .. ':' .. old_state, id)
  end
  redis.call('ZADD', 'specs:' .. stream .. ':' .. new_state, created, id)
  redis.call('SADD', 'specs:streams', stream)
  redis.call('HSET', rec, 'spec_rev', tostring(new_rev), 'spec_state', new_state,
    'spec_stream', stream, 'spec_created_at', tostring(created), 'spec_at', tostring(at))
  if line ~= '' then
    redis.call('RPUSH', rec .. ':lines', line)
    local first = string.match(line, '^[^\r\n]*')
    redis.call('HSET', rec, 'last_line', first, 'last_line_at', tostring(math.floor(at / 1000)))
  end
  return { 'REVISED', new_state, tostring(new_rev), stream }
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
redis.register_function('ns_spec_revise', spec_revise)
redis.register_function{ function_name = 'ns_spec_list', callback = spec_list, flags = { 'no-writes' } }
