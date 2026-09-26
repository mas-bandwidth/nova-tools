-- The ws index: the sprint's work-stream data structure (nova-tools #3662,
-- #3659, #3660; contract: rowan-new specs/ws-index.md). No shebang: loader.go
-- prepends the single library header.
--
-- Keys:
--   ws:names                 SET  of stream names (display strings, e.g. "swarm: cards")
--   ws:order                 ZSET stream -> rank (1 = top priority)
--   ws:<stream>:<state>      ZSET id -> the task's created_at ms, for every state
--                            (waiting, ready, working, merging, landed, parked)
--   task:<id>                HASH the task card (02_card_move.lua, NS.task): stream,
--                            where, created_at, parked_from, ...
--
-- Every set's score is the task's age (created_at in ms, Glenn 2026-09-25
-- 12:22 AM: "the sorted order should be in order of age of the card,
-- uniformly"): a ZRANGE reads oldest first in any set, and a move never
-- changes the score.
--   ws:log                   STREAM one entry per move: id stream from to by why at
--
-- Invariant: a task id is in exactly one ws:<stream>:<where> set, the one its
-- record's stream and where name, or in none when where is empty. This file
-- writes no task set and no task pointer itself: every move is NS.task.move
-- and migrate's placing NS.task.place (02_card_move.lua, nova-tools #3778).
-- Every function here is O(1) or O(k) in the ids (or one stream's members)
-- it is handed; the one scan is ns_ws_migrate, a SCAN page per call, run once.
--
-- Callers: every function is the coordinator seat's (ns-coordinator), FCALLed
-- by internal/nsprint/ws (ws.go); ns_ws_counts is no-writes (FCALL_RO), so the
-- read-only table seat may be granted it too.

local W = {
  STATES = { 'waiting', 'ready', 'working', 'merging', 'landed', 'parked' },
  -- every where a task's stream has a set for (NS.task's), done included
  WHERE = { 'waiting', 'ready', 'working', 'merging', 'landed', 'parked', 'done' },
}

-- W.created_ms reads a created_at field as epoch ms (NS.task.ms: a number,
-- or friend-queue's YYYY-MM-DDTHH:MM:SSZ); nil for anything else.
function W.created_ms(v)
  return NS.task.ms(v)
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
  redis.call('XADD', 'ws:log', 'MAXLEN', '~', '200000', '*', 'id', id, 'stream', stream,
    'from', from, 'to', to, 'by', by or '', 'why', why or '', 'at', tostring(at))
end

-- W.place registers a stream: ws:names, and a rank after the last in
-- ws:order when it has none (NS.card.register, the one registration).
function W.place(stream)
  NS.card.register(stream)
end

-- W.move_one is a thin call of the one task move (NS.task.move,
-- 02_card_move.lua, nova-tools #3778): closed is done/fail (a cancel; the
-- why defaults to closed), landed needs the merge sha. Returns
-- 'MOVED'|'SAME'|'REFUSED' and, for REFUSED, the reason; otherwise the
-- stream and the from where.
function W.move_one(id, to, by, why, now, sha)
  local o = { by = by, why = why, sha = sha }
  if to == 'closed' then
    to, o.ok = 'done', 'fail'
    if not why or why == '' then o.why = 'closed' end
  end
  local p = NS.task.read(id)
  if not p then
    return 'REFUSED', 'no task ' .. id
  end
  if p.stream == '' then
    return 'REFUSED', 'task ' .. id .. ' has no stream'
  end
  local err, info = NS.task.move(id, to, o)
  if err then
    return 'REFUSED', err
  end
  if info.same then
    return 'SAME', p.stream, info.to
  end
  return 'MOVED', p.stream, info.from
end

-- ns_ws_move(id, to, by, why[, sha]) -> MOVED stream from to | SAME stream
-- where | REFUSED why
local function ws_move(keys, args)
  local id, to, by, why = args[1], args[2], args[3], args[4]
  if not id or id == '' then
    return { 'REFUSED', 'no id' }
  end
  local status, a, b = W.move_one(id, to, by, why, W.now(), args[5])
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

-- W.park moves one stream's waiting and ready tasks to parked through the
-- one task move (the record keeps parked_from) and returns how many it
-- parked; a task the move refuses stays where it is.
function W.park(stream, by, why, now)
  local n = 0
  for _, from in ipairs({ 'waiting', 'ready' }) do
    for _, id in ipairs(redis.call('ZRANGE', W.key(stream, from), 0, -1)) do
      if not NS.task.move(id, 'parked', { by = by, why = why, fields = { 'parked_from', from } }) and
          not NS.task.is_sentinel(id) then
        n = n + 1 -- the stop parks with its stream, uncounted (#4318)
      end
    end
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
  local n = 0
  for _, id in ipairs(redis.call('ZRANGE', W.key(stream, 'parked'), 0, -1)) do
    local to = redis.call('HGET', 'task:' .. id, 'parked_from')
    if to ~= 'ready' then
      to = 'waiting'
    end
    if not NS.task.move(id, to, { by = by, why = why }) then
      redis.call('HDEL', 'task:' .. id, 'parked_from')
      if not NS.task.is_sentinel(id) then n = n + 1 end
    end
  end
  return { 'UNPARKED', n }
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
  -- The same slug (a display change) keeps the stream's stop; another slug
  -- ends the old stop and the new name gets its own (#4318).
  local same_slug = NS.task.slug(old) ~= '' and NS.task.slug(old) == NS.task.slug(new)
  if not same_slug then
    local serr = NS.task.slug_clash(new)
    if serr then
      return { 'REFUSED', serr }
    end
  end
  for _, state in ipairs(W.WHERE) do
    if redis.call('EXISTS', W.key(new, state)) == 1 then
      return { 'REFUSED', 'key ' .. W.key(new, state) .. ' exists' }
    end
  end
  -- A card (ns_card_move's; its record names the stream) is refused before
  -- any write; every task moves through the one task move, where kept,
  -- stream new, so the old sets empty themselves.
  for _, state in ipairs(W.WHERE) do
    for _, id in ipairs(redis.call('ZRANGE', W.key(old, state), 0, -1)) do
      if string.match(id, '^s:[-a-z0-9]+:card:') then
        return { 'REFUSED', 'stream holds card ' .. id .. '; cards keep their stream' }
      end
    end
  end
  -- The old name's sentinel (#4318, its id carries the old slug) ends under
  -- the new name: done/fail from waiting or parked, done/ok from landed (the
  -- new name's stop then lands at the same sha); the new name's sentinel is
  -- created as the first move registers the stream. With the same slug the
  -- one stop moves with its stream.
  if same_slug then
    redis.call('SET', 'ws:slug:' .. NS.task.slug(new), new)
  end
  local n, landed_sha = 0, nil
  local why_new = 'rename: stream ' .. old .. ' is ' .. new .. '; its sentinel is ' .. NS.task.sentinel_id(new)
  for _, state in ipairs(W.WHERE) do
    for _, id in ipairs(redis.call('ZRANGE', W.key(old, state), 0, -1)) do
      local err
      if NS.task.is_sentinel(id) and not same_slug and (state == 'waiting' or state == 'parked') then
        err = NS.task.move(id, 'done', { by = by, ok = 'fail', stream = new, rename = true, why = why_new })
      elseif NS.task.is_sentinel(id) and not same_slug and state == 'landed' then
        landed_sha = redis.call('HGET', 'task:' .. id, 'merge_sha')
        err = NS.task.move(id, 'done', { by = by, ok = 'ok', stream = new, rename = true, why = why_new })
      else
        err = NS.task.move(id, state, { by = by, why = 'rename', stream = new, rename = true })
      end
      if err then
        return { 'REFUSED', err }
      end
      if not NS.task.is_sentinel(id) then n = n + 1 end
    end
  end
  if landed_sha and landed_sha ~= '' then
    local nsid = NS.task.sentinel_id(new)
    if redis.call('EXISTS', 'task:' .. nsid) == 1 then
      local err = NS.task.move(nsid, 'landed', { by = by, sha = landed_sha, why = 'rename: landed as ' .. old })
      if err then
        return { 'REFUSED', err }
      end
    end
  end
  local rank = redis.call('ZSCORE', 'ws:order', old)
  if not same_slug and NS.task.slug(old) ~= '' and redis.call('GET', 'ws:slug:' .. NS.task.slug(old)) == old then
    redis.call('DEL', 'ws:slug:' .. NS.task.slug(old))
  end
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
  -- ordering registers each named stream (its sentinel exists, #4318)
  for _, s in ipairs(list) do
    W.place(s)
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
-- counts are the stream table's columns. The stream's sentinel (#4318) is
-- the stream's stop, not one of its cards: it is left out of every count
-- (no new column; ws show --order lists it).
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
    local sid = NS.task.sentinel_id(s)
    for _, state in ipairs(W.STATES) do
      local n = redis.call('ZCARD', W.key(s, state))
      if sid ~= '' and redis.call('ZSCORE', W.key(s, state), sid) then n = n - 1 end
      out[#out + 1] = n
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

-- W.migrate_one places one task hash through NS.task.place (the one-time
-- placer beside the one task move); returns 'placed', 'same', 'nostream'
-- or 'skipped'.
function W.migrate_one(id, sprint, by, now)
  local f = redis.call('HMGET', 'task:' .. id, 'stream', 'title', 'where')
  local stream = W.derive(f[1], f[2])
  if stream and not W.valid_name(stream) then
    return 'skipped'
  end
  local before = f[3]
  local w = NS.task.place(id, nil, nil, { by = by, sprint = sprint, stream = stream })
  if not w then
    return 'skipped'
  end
  if not stream then
    return 'nostream'
  end
  if before == w then
    return 'same'
  end
  return 'placed'
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
