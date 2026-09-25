-- The ws index: the sprint's work-stream data structure (nova-tools #3662,
-- #3659, #3660; contract: rowan-new specs/ws-index.md). No shebang: loader.go
-- prepends the single library header.
--
-- Keys:
--   ws:names                 SET  of stream names (display strings, e.g. "swarm: cards")
--   ws:order                 ZSET stream -> rank (1 = top priority)
--   ws:<stream>:<state>      ZSET id -> the task's created_at ms, for every state
--                            (waiting, ready, working, merging, landed, parked)
--   task:<id>                HASH stream, state, created_at, parked_from, state_at, ...
--
-- Every set's score is the task's age (created_at in ms, Glenn 2026-09-25
-- 12:22 AM: "the sorted order should be in order of age of the card,
-- uniformly"): a ZRANGE reads oldest first in any set, and a move never
-- changes the score.
--   ws:log                   STREAM one entry per move: id stream from to by why at
--
-- Invariant: a task id is in exactly one ws:<stream>:<state> set, the one its
-- hash's stream and state name; closed is in none. Every function here is O(1)
-- or O(k) in the ids (or one stream's members) it is handed; the one scan is
-- ns_ws_migrate, a SCAN page per call, run once.
--
-- Callers: every function is the coordinator seat's (ns-coordinator), FCALLed
-- by internal/nsprint/ws (ws.go); ns_ws_counts is no-writes (FCALL_RO), so the
-- read-only table seat may be granted it too.

local W = {
  STATES = { 'waiting', 'ready', 'working', 'merging', 'landed', 'parked' },
  NEXT = { waiting = 'ready', ready = 'working', working = 'merging', merging = 'landed' },
  KNOWN = { waiting = true, ready = true, working = true, merging = true, landed = true,
    parked = true, closed = true },
  -- States only ws writes (friend-queue never did): a hash in one is ws's.
  WS_ONLY = { ready = true, merging = true, landed = true, parked = true },
  LOG_MAX = '200000',
}

-- W.days: days from 1970-01-01 to y-m-d (the proleptic Gregorian calendar).
function W.days(y, m, d)
  if m <= 2 then
    y = y - 1
  end
  local era = math.floor(y / 400)
  local yoe = y - era * 400
  local mp = (m + 9) % 12
  local doy = math.floor((153 * mp + 2) / 5) + d - 1
  local doe = yoe * 365 + math.floor(yoe / 4) - math.floor(yoe / 100) + doy
  return era * 146097 + doe - 719468
end

-- W.created_ms reads a created_at field as epoch ms: a number is ms already;
-- friend-queue wrote "YYYY-MM-DDTHH:MM:SSZ" (UTC). nil for anything else.
function W.created_ms(v)
  if not v or v == '' then
    return nil
  end
  local n = tonumber(v)
  if n then
    return n
  end
  local y, mo, d, h, mi, se = string.match(v, '^(%d%d%d%d)-(%d%d)-(%d%d)T(%d%d):(%d%d):(%d%d)')
  if not y then
    return nil
  end
  return ((W.days(tonumber(y), tonumber(mo), tonumber(d)) * 24 + tonumber(h)) * 60 + tonumber(mi)) * 60000 +
    tonumber(se) * 1000
end

function W.key(stream, state)
  return 'ws:' .. stream .. ':' .. state
end

function W.now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- A stream name is a display string: 1..64 bytes, no control character and
-- no '|', which separates names on `scope keep --streams`.
function W.valid_name(s)
  return type(s) == 'string' and #s >= 1 and #s <= 64 and not string.find(s, '[%c|]')
end

function W.log(id, stream, from, to, by, why, at)
  redis.call('XADD', 'ws:log', 'MAXLEN', '~', W.LOG_MAX, '*', 'id', id, 'stream', stream,
    'from', from, 'to', to, 'by', by or '', 'why', why or '', 'at', tostring(at))
end

function W.allowed(from, to)
  if to == 'closed' then
    return true
  end
  if to == 'parked' then
    return from ~= 'closed'
  end
  if from == 'parked' then
    return to == 'waiting'
  end
  return W.NEXT[from] == to
end

-- W.place registers a stream: ws:names, and a rank after the last in
-- ws:order when it has none.
function W.place(stream)
  redis.call('SADD', 'ws:names', stream)
  if not redis.call('ZSCORE', 'ws:order', stream) then
    redis.call('ZADD', 'ws:order', redis.call('ZCARD', 'ws:order') + 1, stream)
  end
end

-- W.move_one: returns 'MOVED'|'SAME'|'REFUSED' and, for REFUSED, the reason;
-- otherwise the stream and the from state.
function W.move_one(id, to, by, why, now)
  if not W.KNOWN[to] then
    return 'REFUSED', 'unknown state ' .. tostring(to)
  end
  local f = redis.call('HMGET', 'task:' .. id, 'stream', 'state', 'created_at')
  local stream, from = f[1], f[2]
  if not stream and not from then
    return 'REFUSED', 'no task ' .. id
  end
  if not stream or stream == '' then
    return 'REFUSED', 'task ' .. id .. ' has no stream'
  end
  if not W.KNOWN[from or ''] then
    return 'REFUSED', 'task ' .. id .. ' state ' .. tostring(from) .. ' is not a ws state; run ws migrate'
  end
  if from == to then
    return 'SAME', stream, from
  end
  if not W.allowed(from, to) then
    return 'REFUSED', 'task ' .. id .. ' ' .. from .. '->' .. to .. ' is not an allowed move'
  end
  -- ONE PLACE (the links are valid both ways) before any write: the id must
  -- be in the set its record names, and not already in the target set;
  -- otherwise the mismatch is refused by name and nothing is written.
  local cur
  if from ~= 'closed' then
    cur = redis.call('ZSCORE', W.key(stream, from), id)
    if not cur then
      return 'REFUSED', 'task ' .. id .. ' says ' .. from .. ' but is not in ' .. W.key(stream, from)
    end
  end
  if to ~= 'closed' and redis.call('ZSCORE', W.key(stream, to), id) then
    return 'REFUSED', 'task ' .. id .. ' says ' .. from .. ' but is already in ' .. W.key(stream, to)
  end
  local fields = { 'state', to, 'state_at', tostring(now) }
  -- The score is the task's age: created_at, else the score it already has,
  -- else now (written back as created_at so every later move keeps it).
  local age = W.created_ms(f[3])
  if not age then
    age = tonumber(cur)
  end
  if not age then
    age = now
  end
  if not f[3] or f[3] == '' then
    fields[#fields + 1] = 'created_at'
    fields[#fields + 1] = tostring(age)
  end
  if from ~= 'closed' then
    redis.call('ZREM', W.key(stream, from), id)
  end
  if to ~= 'closed' then
    redis.call('ZADD', W.key(stream, to), age, id)
  end
  if to == 'parked' then
    fields[#fields + 1] = 'parked_from'
    fields[#fields + 1] = from
  end
  redis.call('HSET', 'task:' .. id, unpack(fields))
  W.log(id, stream, from, to, by, why, now)
  return 'MOVED', stream, from
end

-- ns_ws_move(id, to, by, why) -> MOVED stream from to | SAME stream state | REFUSED why
local function ws_move(keys, args)
  local id, to, by, why = args[1], args[2], args[3], args[4]
  if not id or id == '' then
    return { 'REFUSED', 'no id' }
  end
  local status, a, b = W.move_one(id, to, by, why, W.now())
  if status == 'REFUSED' then
    return { 'REFUSED', a }
  end
  if status == 'SAME' then
    return { 'SAME', a, b }
  end
  return { 'MOVED', a, b, to }
end

-- ns_ws_move_many(to, by, why, id...) -> MOVED moved same refused, then
-- (id, why) per refused id. A refused id is left where it is; the rest move.
local function ws_move_many(keys, args)
  local to, by, why = args[1], args[2], args[3]
  local now = W.now()
  local moved, same, refused = 0, 0, {}
  for i = 4, #args do
    local status, a = W.move_one(args[i], to, by, why, now)
    if status == 'MOVED' then
      moved = moved + 1
    elseif status == 'SAME' then
      same = same + 1
    else
      refused[#refused + 1] = args[i]
      refused[#refused + 1] = a
    end
  end
  local out = { 'MOVED', moved, same, #refused / 2 }
  for _, v in ipairs(refused) do
    out[#out + 1] = v
  end
  return out
end

-- W.park moves one stream's waiting and ready sets into parked (scores, the
-- tasks' created_at, kept) and returns how many it parked.
function W.park(stream, by, why, now)
  local wk, rk, pk = W.key(stream, 'waiting'), W.key(stream, 'ready'), W.key(stream, 'parked')
  local n = 0
  for _, from in ipairs({ 'waiting', 'ready' }) do
    for _, id in ipairs(redis.call('ZRANGE', W.key(stream, from), 0, -1)) do
      redis.call('HSET', 'task:' .. id, 'state', 'parked', 'state_at', tostring(now), 'parked_from', from)
      W.log(id, stream, from, 'parked', by, why, now)
      n = n + 1
    end
  end
  if n > 0 then
    redis.call('ZUNIONSTORE', pk, 3, pk, wk, rk, 'AGGREGATE', 'MIN')
    redis.call('DEL', wk, rk)
  end
  return n
end

function W.known(stream)
  return W.valid_name(stream) and redis.call('SISMEMBER', 'ws:names', stream) == 1
end

-- ns_ws_park_stream(stream, by, why) -> PARKED n | REFUSED why
local function ws_park_stream(keys, args)
  local stream, by, why = args[1], args[2], args[3]
  if not W.known(stream) then
    return { 'REFUSED', 'unknown stream ' .. tostring(stream) }
  end
  return { 'PARKED', W.park(stream, by, why, W.now()) }
end

-- ns_ws_unpark_stream(stream, by, why) -> UNPARKED n | REFUSED why. The
-- inverse of park: each task returns to the set it was parked from (its
-- parked_from field; waiting when absent) with its score, its created_at.
local function ws_unpark_stream(keys, args)
  local stream, by, why = args[1], args[2], args[3]
  if not W.known(stream) then
    return { 'REFUSED', 'unknown stream ' .. tostring(stream) }
  end
  local now = W.now()
  local pk = W.key(stream, 'parked')
  local rows = redis.call('ZRANGE', pk, 0, -1, 'WITHSCORES')
  for i = 1, #rows, 2 do
    local id, score = rows[i], rows[i + 1]
    local to = redis.call('HGET', 'task:' .. id, 'parked_from')
    if to ~= 'ready' then
      to = 'waiting'
    end
    redis.call('ZADD', W.key(stream, to), score, id)
    redis.call('HSET', 'task:' .. id, 'state', to, 'state_at', tostring(now))
    redis.call('HDEL', 'task:' .. id, 'parked_from')
    W.log(id, stream, 'parked', to, by, why, now)
  end
  redis.call('DEL', pk)
  return { 'UNPARKED', #rows / 2 }
end

-- ns_ws_keep(by, why, stream...) -> KEPT kept parked_streams parked_tasks |
-- REFUSED why. Parks every stream in ws:names that is not named; a named
-- stream that is not in ws:names refuses the whole call before any write.
local function ws_keep(keys, args)
  local by, why = args[1], args[2]
  if #args < 3 then
    return { 'REFUSED', 'name at least one stream to keep' }
  end
  local keep = {}
  for i = 3, #args do
    if not W.known(args[i]) then
      return { 'REFUSED', 'unknown stream ' .. tostring(args[i]) }
    end
    keep[args[i]] = true
  end
  local now = W.now()
  local kept, streams, tasks = 0, 0, 0
  for _, s in ipairs(redis.call('SMEMBERS', 'ws:names')) do
    if keep[s] then
      kept = kept + 1
    else
      local n = W.park(s, by, why, now)
      streams = streams + 1
      tasks = tasks + n
    end
  end
  return { 'KEPT', kept, streams, tasks }
end

-- ns_ws_rename(old, new[, by]) -> RENAMED members | REFUSED why
local function ws_rename(keys, args)
  local old, new, by = args[1], args[2], args[3]
  if not W.known(old) then
    return { 'REFUSED', 'unknown stream ' .. tostring(old) }
  end
  if not W.valid_name(new) then
    return { 'REFUSED', 'bad stream name ' .. tostring(new) }
  end
  if redis.call('SISMEMBER', 'ws:names', new) == 1 then
    return { 'REFUSED', 'stream ' .. new .. ' exists' }
  end
  for _, state in ipairs(W.STATES) do
    if redis.call('EXISTS', W.key(new, state)) == 1 then
      return { 'REFUSED', 'key ' .. W.key(new, state) .. ' exists' }
    end
  end
  local n = 0
  for _, state in ipairs(W.STATES) do
    local k = W.key(old, state)
    for _, id in ipairs(redis.call('ZRANGE', k, 0, -1)) do
      redis.call('HSET', 'task:' .. id, 'stream', new)
      n = n + 1
    end
    if redis.call('EXISTS', k) == 1 then
      redis.call('RENAME', k, W.key(new, state))
    end
  end
  local rank = redis.call('ZSCORE', 'ws:order', old)
  redis.call('SREM', 'ws:names', old)
  redis.call('SADD', 'ws:names', new)
  redis.call('ZREM', 'ws:order', old)
  redis.call('ZADD', 'ws:order', rank or (redis.call('ZCARD', 'ws:order') + 1), new)
  W.log('', new, old, new, by, 'rename', W.now())
  return { 'RENAMED', n }
end

-- ns_ws_order(stream...) -> ORDERED n | REFUSED why. The named streams take
-- ranks 1..k in the order given; every other stream keeps its relative order
-- after them (streams in ws:names with no rank last, by name).
local function ws_order(keys, args)
  local seen, list = {}, {}
  for i = 1, #args do
    if not W.known(args[i]) then
      return { 'REFUSED', 'unknown stream ' .. tostring(args[i]) }
    end
    if seen[args[i]] then
      return { 'REFUSED', 'stream ' .. args[i] .. ' named twice' }
    end
    seen[args[i]] = true
    list[#list + 1] = args[i]
  end
  if #list == 0 then
    return { 'REFUSED', 'name at least one stream' }
  end
  for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
    if not seen[s] then
      seen[s] = true
      list[#list + 1] = s
    end
  end
  local rest = {}
  for _, s in ipairs(redis.call('SMEMBERS', 'ws:names')) do
    if not seen[s] then
      rest[#rest + 1] = s
    end
  end
  table.sort(rest)
  for _, s in ipairs(rest) do
    list[#list + 1] = s
  end
  local zadd = {}
  for i, s in ipairs(list) do
    zadd[#zadd + 1] = i
    zadd[#zadd + 1] = s
  end
  redis.call('DEL', 'ws:order')
  redis.call('ZADD', 'ws:order', unpack(zadd))
  return { 'ORDERED', #list }
end

-- ns_ws_counts() -> per stream in ws:order (then unranked names, by name):
-- name, waiting, ready, working, merging, landed, parked. The first five
-- counts are the stream table's columns.
local function ws_counts(keys, args)
  local out, seen, list = {}, {}, {}
  for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
    seen[s] = true
    list[#list + 1] = s
  end
  local rest = {}
  for _, s in ipairs(redis.call('SMEMBERS', 'ws:names')) do
    if not seen[s] then
      rest[#rest + 1] = s
    end
  end
  table.sort(rest)
  for _, s in ipairs(rest) do
    list[#list + 1] = s
  end
  for _, s in ipairs(list) do
    out[#out + 1] = s
    for _, state in ipairs(W.STATES) do
      out[#out + 1] = redis.call('ZCARD', W.key(s, state))
    end
  end
  return out
end

-- W.derive: the stream of a task hash, from its stream field or its title's
-- "STREAM: <s> |" prefix; nil when it names none.
function W.derive(stream, title)
  if stream and stream ~= '' then
    return stream
  end
  if title then
    local s = string.match(title, '^%s*STREAM:%s*(.-)%s*|')
    if s and s ~= '' then
      return s
    end
  end
  return nil
end

-- W.legacy_state maps a friend-queue task to a ws state: q:waiting or
-- q:blocked -> waiting; closed/cancelled -> closed; the owner's idx working
-- or open set (sprint:<S>:idx:<owner>:<state>) -> working or ready; else the
-- hash's own state. nil when none of these says anything.
function W.legacy_state(id, state, owner, cancelled, sprint)
  if redis.call('ZSCORE', 'q:waiting', id) or redis.call('ZSCORE', 'q:blocked', id) then
    return 'waiting'
  end
  if state == 'closed' or state == 'cancelled' or cancelled == '1' then
    return 'closed'
  end
  if sprint ~= '' and owner and owner ~= '' then
    local ix = 'sprint:' .. sprint .. ':idx:' .. owner .. ':'
    if redis.call('SISMEMBER', ix .. 'working', id) == 1 then
      return 'working'
    end
    if redis.call('SISMEMBER', ix .. 'open', id) == 1 then
      return 'ready'
    end
    if redis.call('SISMEMBER', ix .. 'closed', id) == 1 then
      return 'closed'
    end
  end
  if state == 'working' then
    return 'working'
  end
  if state == 'open' then
    return 'ready'
  end
  if state == 'waiting' or state == 'blocked' then
    return 'waiting'
  end
  return nil
end

-- W.migrate_one places one task hash; returns 'placed', 'same', 'nostream'
-- or 'skipped'.
function W.migrate_one(id, sprint, by, now)
  local f = redis.call('HMGET', 'task:' .. id, 'stream', 'title', 'state', 'owner', 'created_at',
    'cancelled', 'ws_migrated_at')
  local stream = W.derive(f[1], f[2])
  if not stream then
    return 'nostream'
  end
  if not W.valid_name(stream) then
    return 'skipped'
  end
  local state = f[3]
  -- Migrated once, or in a state only ws writes: the hash is the truth.
  if not ((f[7] and W.KNOWN[state or '']) or W.WS_ONLY[state or '']) then
    state = W.legacy_state(id, f[3], f[4], f[6], sprint)
  end
  if not state then
    return 'skipped'
  end
  W.place(stream)
  local target = W.key(stream, state)
  local changed = false
  for _, s in ipairs(W.STATES) do
    if s ~= state and redis.call('ZREM', W.key(stream, s), id) == 1 then
      changed = true
    end
  end
  local fields = {}
  if state ~= 'closed' and not redis.call('ZSCORE', target, id) then
    local score = W.created_ms(f[5]) or tonumber(redis.call('ZSCORE', 'q:waiting', id)) or now
    redis.call('ZADD', target, score, id)
    if not f[5] or f[5] == '' then
      fields[#fields + 1] = 'created_at'
      fields[#fields + 1] = tostring(score)
    end
    changed = true
  end
  if f[1] ~= stream or f[3] ~= state then
    fields[#fields + 1] = 'stream'
    fields[#fields + 1] = stream
    fields[#fields + 1] = 'state'
    fields[#fields + 1] = state
    fields[#fields + 1] = 'state_at'
    fields[#fields + 1] = tostring(now)
    if f[3] and f[3] ~= state then
      fields[#fields + 1] = 'fq_state'
      fields[#fields + 1] = f[3]
    end
    changed = true
  end
  if not f[7] then
    changed = true -- the first placement of a task is a move from friend-queue
    fields[#fields + 1] = 'ws_migrated_at'
    fields[#fields + 1] = tostring(now)
  end
  if #fields > 0 then
    redis.call('HSET', 'task:' .. id, unpack(fields))
  end
  if changed then
    W.log(id, stream, f[3] or '', state, by, 'migrate', now)
    return 'placed'
  end
  return 'same'
end

-- ns_ws_migrate(cursor[, sprint[, count[, by]]]) -> next cursor, scanned,
-- placed, same, nostream, skipped. One SCAN page of task:* per call (the only
-- scan, run once); sprint names the friend-queue idx sets to read. Idempotent:
-- a second pass over the same keys places nothing.
local function ws_migrate(keys, args)
  local cursor, sprint, count, by = args[1] or '0', args[2] or '', tonumber(args[3]) or 1000, args[4] or ''
  local now = W.now()
  local page = redis.call('SCAN', cursor, 'MATCH', 'task:*', 'COUNT', count)
  local tally = { placed = 0, same = 0, nostream = 0, skipped = 0 }
  local scanned = 0
  for _, key in ipairs(page[2]) do
    local id = string.sub(key, 6)
    local kind = redis.call('TYPE', key)
    if type(kind) == 'table' then
      kind = kind['ok']
    end
    scanned = scanned + 1
    if kind == 'hash' and id ~= '' then
      local r = W.migrate_one(id, sprint, by, now)
      tally[r] = tally[r] + 1
    else
      tally.skipped = tally.skipped + 1
    end
  end
  return { page[1], scanned, tally.placed, tally.same, tally.nostream, tally.skipped }
end

redis.register_function('ns_ws_move', ws_move)
redis.register_function('ns_ws_move_many', ws_move_many)
redis.register_function('ns_ws_park_stream', ws_park_stream)
redis.register_function('ns_ws_unpark_stream', ws_unpark_stream)
redis.register_function('ns_ws_keep', ws_keep)
redis.register_function('ns_ws_rename', ws_rename)
redis.register_function('ns_ws_order', ws_order)
redis.register_function{ function_name = 'ns_ws_counts', flags = { 'no-writes' },
  callback = ws_counts }
redis.register_function('ns_ws_migrate', ws_migrate)
