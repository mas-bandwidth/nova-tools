-- The batch task verbs on the ws index (nova-tools #3661, sweep #3647; part
-- of #3662). The keys and invariants are rowan-new specs/ws-index.md:
--   task:<id>              HASH  stream, state, order, owner, front, ...
--   ws:<stream>:<state>    ZSET  one per state (waiting ready working merging
--                                landed parked); a task id is in exactly the
--                                one its task:<id> stream/state fields name,
--                                and a closed task is in none
--   ws:names               SET   the stream names
--   ws:log                 STREAM one receipt per moved row
-- This file is self-contained: it reads no NS field and no other file's
-- local. Its move primitive (TB.apply) repeats the key writes of #3662's
-- ns_ws_move on purpose until a follow-up folds the two.
--
-- Friend-queue compatibility, until bash friend-queue and the current table
-- are retired: every move also keeps the legacy per-owner index sets
-- sprint:<S>:idx:<owner>:open|working|closed|leased, the one global q:blocked
-- ZSET (waiting and parked are "blocked" there), and the owner's ready
-- streams q:<owner> and q:<owner>:front (a row leaving ready loses its entry;
-- a row entering ready gets one, on :front when front=1).
--
-- ns_task_batch(verb, S, by, why, param, source, source_value, id...)
--   verb   cancel | block | unblock | front | move-stream | move-state |
--          move-friend
--   param  block: the DEPENDS-ON conditions ('' with a reason in why);
--          move-stream: the stream; move-state: the state; move-friend: the
--          friend; otherwise ''
--   source ids (the ids follow), stream (source_value is a stream name: the
--          verb's default states of that stream, in state then ZSET order) or
--          set (source_value is a ZSET or SET key: its members)
-- Every row is checked before any is written, so a refusal changes nothing:
--   {'REFUSED', <first bad id or key>, <why>}
--   {'OK', <n moved>, <stream or *>}
--
-- ns_task_sweep(S, by, friend): ready must be READY (#3647). For each id in
-- the friend's ready index (sprint:<S>:idx:<friend>:open) whose ws state is
-- ready: a read whose PR head moved since push (task:<id> head differs from
-- the head field of the pr:<repo>:<n> hash, when Redis holds one) is
-- cancelled with the two heads as evidence; a task whose sprint (its sprint
-- field, else S) has s:<sprint>:pitstop (or the table's
-- sprint:<sprint>:pitstop) set moves to waiting.
--   {'OK', <n moved>, <stream or *>, <cancelled>, <waiting>, <skipped>}

local TB = {}

TB.STATES = { 'waiting', 'ready', 'working', 'merging', 'landed', 'parked' }
TB.ORDERED = { waiting = true, ready = true, parked = true }
TB.KNOWN = { waiting = true, ready = true, working = true, merging = true, landed = true, parked = true, closed = true }
-- The spec's graph: waiting->ready->working->merging->landed, parked->waiting;
-- any->parked and any->closed are in TB.allowed.
TB.NEXT = { waiting = 'ready', ready = 'working', working = 'merging', merging = 'landed', parked = 'waiting' }
-- The legacy friend-queue index set of each ws state; waiting and parked are
-- in q:blocked instead.
TB.LEGACY = { ready = 'open', working = 'working', merging = 'closed', landed = 'closed', closed = 'closed' }
-- The states a --stream source selects, per verb.
TB.SOURCES = {
  cancel = { 'waiting', 'ready', 'parked' },
  block = { 'ready', 'waiting' },
  unblock = { 'waiting' },
  front = { 'waiting', 'ready' },
  ['move-stream'] = { 'waiting', 'ready', 'parked' },
  ['move-state'] = { 'waiting', 'ready', 'parked' },
  ['move-friend'] = { 'waiting', 'ready', 'parked' },
}
TB.FIELDS = { 'stream', 'state', 'order', 'owner', 'front', 'queue', 'xid', 'kind', 'head', 'ref', 'pr', 'repo', 'sprint' }

function TB.now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

function TB.type(key)
  local kind = redis.call('TYPE', key)
  if type(kind) == 'table' then
    kind = kind['ok']
  end
  return kind
end

function TB.str(v)
  if v == nil or v == false then
    return ''
  end
  return tostring(v)
end

function TB.allowed(from, to)
  if from == 'closed' or from == to then
    return false
  end
  if to == 'closed' or to == 'parked' then
    return true
  end
  return TB.NEXT[from] == to
end

-- TB.read returns the row's fields and its score in the ZSET its fields name,
-- or nil and why the row cannot move (no hash or stream, an unknown state, or
-- not in the ZSET its fields name: the invariant is already broken).
function TB.read(id)
  local v = redis.call('HMGET', 'task:' .. id, unpack(TB.FIELDS))
  local t = { id = id }
  for i, f in ipairs(TB.FIELDS) do
    t[f] = TB.str(v[i])
  end
  if t.stream == '' then
    return nil, 'no-stream'
  end
  if not TB.KNOWN[t.state] then
    return nil, 'state-' .. (t.state == '' and 'none' or t.state)
  end
  if t.state ~= 'closed' then
    local s = redis.call('ZSCORE', 'ws:' .. t.stream .. ':' .. t.state, id)
    if not s then
      return nil, 'not-in-ws-' .. t.state
    end
    t.score = tonumber(s)
  end
  return t
end

-- TB.target returns the state a verb moves the row to, or nil and why not.
function TB.target(verb, param, t)
  local st = t.state
  if st == 'closed' then
    return nil, 'closed'
  end
  if verb == 'cancel' then
    return 'closed'
  elseif verb == 'block' then
    if st == 'ready' or st == 'waiting' then
      return 'waiting'
    end
  elseif verb == 'unblock' then
    if st == 'waiting' then
      return 'ready'
    end
  elseif verb == 'move-state' then
    if TB.allowed(st, param) then
      return param
    end
    return nil, st .. '-to-' .. param
  elseif verb == 'move-stream' then
    if t.stream == param then
      return nil, 'same-stream'
    end
    return st
  elseif verb == 'move-friend' then
    if t.owner == param then
      return nil, 'same-friend'
    end
    if TB.ORDERED[st] then
      return st
    end
  elseif verb == 'front' then
    if TB.ORDERED[st] then
      return st
    end
  end
  return nil, verb .. '-from-' .. st
end

-- TB.unqueue deletes the id's entries from the owner's two ready streams. The
-- streams carry no id index, so each owner's streams are read once per call.
function TB.unqueue(ctx, owner, id)
  local cache = ctx.queued[owner]
  if not cache then
    cache = {}
    for _, key in ipairs({ 'q:' .. owner .. ':front', 'q:' .. owner }) do
      for _, e in ipairs(redis.call('XRANGE', key, '-', '+')) do
        local kv = e[2]
        for i = 1, #kv, 2 do
          if kv[i] == 'id' then
            local list = cache[kv[i + 1]] or {}
            list[#list + 1] = { key, e[1] }
            cache[kv[i + 1]] = list
          end
        end
      end
    end
    ctx.queued[owner] = cache
  end
  for _, e in ipairs(cache[id] or {}) do
    redis.call('XDEL', e[1], e[2])
  end
  cache[id] = nil
end

-- TB.legacy keeps the friend-queue shapes consistent with one move.
function TB.legacy(ctx, t, to, owner, front)
  local id, old = t.id, t.owner
  local ix = 'sprint:' .. ctx.S .. ':idx:'
  local requeue = to == 'ready' and (t.state ~= 'ready' or owner ~= old or ctx.verb == 'front')
  if old ~= '' then
    redis.call('SREM', ix .. old .. ':open', id)
    redis.call('SREM', ix .. old .. ':working', id)
    if t.state == 'working' and t.xid ~= '' then
      if t.queue ~= '' then
        redis.pcall('XACK', t.queue, old, t.xid)
      end
      redis.call('SREM', ix .. old .. ':leased', t.xid)
    end
    if t.state == 'ready' and (to ~= 'ready' or requeue) then
      TB.unqueue(ctx, old, id)
    end
  end
  if owner ~= '' then
    local set = TB.LEGACY[to]
    if set then
      redis.call('SADD', ix .. owner .. ':' .. set, id)
    end
    if set ~= 'closed' then
      redis.call('SREM', ix .. owner .. ':closed', id)
    end
    if requeue then
      local q = 'q:' .. owner
      if front == '1' then
        q = q .. ':front'
      end
      redis.call('XADD', q, '*', 'id', id)
    end
  end
  if to == 'waiting' or to == 'parked' then
    redis.call('ZADD', 'q:blocked', 'NX', ctx.now, id)
  else
    redis.call('ZREM', 'q:blocked', id)
  end
end

-- TB.apply moves one checked row to state `to`, keeping every invariant.
-- score is the ordered-state score the caller chose (nil keeps the row's
-- order); why is this row's receipt reason.
function TB.apply(ctx, t, to, score, why)
  local id = t.id
  local stream, owner, front = t.stream, t.owner, t.front
  if ctx.verb == 'move-stream' then
    stream = ctx.param
  elseif ctx.verb == 'move-friend' then
    owner = ctx.param
  end
  local now = tostring(ctx.now)
  local hset = { 'state', to, 'stream', stream, 'state_at', now }
  if t.state ~= 'closed' then
    redis.call('ZREM', 'ws:' .. t.stream .. ':' .. t.state, id)
  end
  if to ~= 'closed' then
    if ctx.verb == 'front' then
      score, front = 0, '1'
      hset[#hset + 1] = 'front'
      hset[#hset + 1] = '1'
    end
    if TB.ORDERED[to] then
      if score == nil then
        score = tonumber(t.order) or (TB.ORDERED[t.state] and t.score) or 0
      end
      hset[#hset + 1] = 'order'
      hset[#hset + 1] = tostring(score)
    elseif to ~= t.state then
      score = ctx.now
    else
      score = t.score
    end
    redis.call('ZADD', 'ws:' .. stream .. ':' .. to, score, id)
  end
  if owner ~= t.owner then
    hset[#hset + 1] = 'owner'
    hset[#hset + 1] = owner
  end
  if to == 'closed' then
    for _, kv in ipairs({ { 'cancelled', '1' }, { 'done_at', now }, { 'evidence', why } }) do
      hset[#hset + 1] = kv[1]
      hset[#hset + 1] = kv[2]
    end
  elseif to == 'waiting' and t.state ~= 'parked' and (ctx.verb == 'block' or ctx.verb == 'sweep') then
    for _, kv in ipairs({ { 'blocked_on', ctx.on or '' }, { 'blocked_reason', why }, { 'blocked_at', now } }) do
      hset[#hset + 1] = kv[1]
      hset[#hset + 1] = kv[2]
    end
  end
  redis.call('HSET', 'task:' .. id, unpack(hset))
  if to == 'ready' and ctx.verb == 'unblock' then
    redis.call('HDEL', 'task:' .. id, 'blocked_on', 'blocked_reason', 'blocked_at')
  end
  if t.state == 'working' and to ~= 'working' then
    redis.call('HDEL', 'task:' .. id, 'queue', 'xid', 'leased_at')
  end
  TB.legacy(ctx, t, to, owner, front)
  local log = { 'id', id, 'stream', stream, 'from', t.state, 'to', to, 'by', ctx.by, 'why', why, 'at', now, 'verb', ctx.verb }
  if stream ~= t.stream then
    log[#log + 1] = 'from_stream'
    log[#log + 1] = t.stream
  end
  if owner ~= t.owner then
    log[#log + 1] = 'owner'
    log[#log + 1] = owner
  end
  redis.call('XADD', 'ws:log', 'MAXLEN', '~', '100000', '*', unpack(log))
  ctx.streams[stream] = true
end

-- TB.stream_of names the one stream every moved row is in, or '*'.
function TB.stream_of(ctx, fallback)
  local one, n = fallback, 0
  for s in pairs(ctx.streams) do
    one, n = s, n + 1
  end
  if n > 1 then
    return '*'
  end
  return one
end

-- TB.members lists the source ids, or nil and the refusal.
function TB.members(verb, source, value, args)
  local ids = {}
  if source == 'ids' then
    for i = 8, #args do
      ids[#ids + 1] = args[i]
    end
  elseif source == 'stream' then
    if redis.call('SISMEMBER', 'ws:names', value) == 0 then
      return nil, { 'REFUSED', value, 'unknown-stream' }
    end
    for _, st in ipairs(TB.SOURCES[verb]) do
      for _, id in ipairs(redis.call('ZRANGE', 'ws:' .. value .. ':' .. st, 0, -1)) do
        ids[#ids + 1] = id
      end
    end
  elseif source == 'set' then
    local kind = TB.type(value)
    if kind == 'zset' then
      ids = redis.call('ZRANGE', value, 0, -1)
    elseif kind == 'set' then
      ids = redis.call('SMEMBERS', value)
    else
      return nil, { 'REFUSED', value, 'not-a-set' }
    end
  else
    return nil, { 'REFUSED', source, 'bad-source' }
  end
  return ids
end

-- TB.tail is the highest order score in a stream's ordered sets, so rows moved
-- into it queue after its own work, never ahead of it.
function TB.tail(stream)
  local top = 0
  for st in pairs(TB.ORDERED) do
    local r = redis.call('ZRANGE', 'ws:' .. stream .. ':' .. st, -1, -1, 'WITHSCORES')
    if r[2] and tonumber(r[2]) > top then
      top = tonumber(r[2])
    end
  end
  return top
end

function TB.batch(keys, args)
  local verb, S, by, why, param = args[1], args[2], args[3], args[4], args[5]
  if not TB.SOURCES[verb] then
    return { 'REFUSED', TB.str(verb), 'bad-verb' }
  end
  if TB.str(S) == '' or TB.str(by) == '' then
    return { 'REFUSED', verb, 'want-sprint-and-by' }
  end
  if verb == 'move-state' and (not TB.KNOWN[param] or param == '') then
    return { 'REFUSED', TB.str(param), 'bad-state' }
  end
  if verb == 'move-stream' and redis.call('SISMEMBER', 'ws:names', param) == 0 then
    return { 'REFUSED', TB.str(param), 'unknown-stream' }
  end
  if verb == 'move-friend' and TB.str(param) == '' then
    return { 'REFUSED', verb, 'want-friend' }
  end
  local ids, refusal = TB.members(verb, args[6], args[7], args)
  if not ids then
    return refusal
  end
  local rows, seen = {}, {}
  for _, id in ipairs(ids) do
    if not seen[id] then
      seen[id] = true
      local t, bad = TB.read(id)
      if not t then
        return { 'REFUSED', id, bad }
      end
      local to
      to, bad = TB.target(verb, param, t)
      if not to then
        return { 'REFUSED', id, bad }
      end
      t.to = to
      t.rank = #rows + 1
      rows[#rows + 1] = t
    end
  end
  local ctx = { verb = verb, S = S, by = by, param = param, now = TB.now(), queued = {}, streams = {} }
  if verb == 'block' then
    ctx.on = param
  end
  if why == nil or why == '' then
    why = verb
  end
  if verb == 'move-stream' then
    -- Keep the rows' relative order: sort by their order, then queue them
    -- after the destination's tail.
    table.sort(rows, function(a, b)
      local x = tonumber(a.order) or a.score or 0
      local y = tonumber(b.order) or b.score or 0
      if x ~= y then
        return x < y
      end
      return a.rank < b.rank
    end)
    local base = TB.tail(param)
    for i, t in ipairs(rows) do
      local score
      if TB.ORDERED[t.to] then
        score = base + i
      end
      TB.apply(ctx, t, t.to, score, why)
    end
    return { 'OK', #rows, param }
  end
  for _, t in ipairs(rows) do
    TB.apply(ctx, t, t.to, nil, why)
  end
  local fallback = '-'
  if args[6] == 'stream' then
    fallback = args[7]
  end
  return { 'OK', #rows, TB.stream_of(ctx, fallback) }
end

-- TB.pr_head is the head the pr:<repo>:<n> hash records for the row's PR, or
-- '' when the row names no PR or Redis holds no such hash.
function TB.pr_head(t)
  local repo, n = t.repo, string.match(t.pr, '(%d+)$')
  if repo == '' or not n then
    repo, n = string.match(t.pr .. ' ' .. t.ref, '([%w%._%-/]+)#(%d+)')
    if not repo then
      repo, n = string.match(t.ref, '([%w%._%-]+/[%w%._%-]+)/pull/(%d+)')
    end
  end
  if not repo or not n then
    return ''
  end
  local names = { repo }
  local short = string.match(repo, '/([^/]+)$')
  if short then
    names[2] = short
  end
  for _, r in ipairs(names) do
    local key = 'pr:' .. r .. ':' .. n
    if TB.type(key) == 'hash' then
      return TB.str(redis.call('HGET', key, 'head'))
    end
  end
  return ''
end

function TB.moved(a, b)
  if a == '' or b == '' then
    return false
  end
  local n = math.min(#a, #b)
  return string.sub(a, 1, n) ~= string.sub(b, 1, n)
end

function TB.sweep(keys, args)
  local S, by, friend = args[1], args[2], args[3]
  if TB.str(S) == '' or TB.str(by) == '' or TB.str(friend) == '' then
    return { 'REFUSED', 'sweep', 'want-sprint-by-and-friend' }
  end
  local ctx = { verb = 'sweep', S = S, by = by, param = '', now = TB.now(), queued = {}, streams = {} }
  local cancelled, waiting, skipped = 0, 0, 0
  local pitstop = {}
  local ids = redis.call('SMEMBERS', 'sprint:' .. S .. ':idx:' .. friend .. ':open')
  table.sort(ids)
  for _, id in ipairs(ids) do
    local t = TB.read(id)
    if not t or t.state ~= 'ready' then
      skipped = skipped + 1
    else
      local sprint = t.sprint ~= '' and t.sprint or S
      if pitstop[sprint] == nil then
        pitstop[sprint] = redis.call('EXISTS', 's:' .. sprint .. ':pitstop', 'sprint:' .. sprint .. ':pitstop') > 0
      end
      local head = ''
      if t.kind == 'read' then
        head = TB.pr_head(t)
      end
      if TB.moved(t.head, head) then
        TB.apply(ctx, t, 'closed', nil, 'sweep: head moved ' .. t.head .. ' -> ' .. head)
        cancelled = cancelled + 1
      elseif pitstop[sprint] then
        TB.apply(ctx, t, 'waiting', nil, 'sweep: pitstop ' .. sprint)
        waiting = waiting + 1
      end
    end
  end
  return { 'OK', cancelled + waiting, TB.stream_of(ctx, '-'), cancelled, waiting, skipped }
end

redis.register_function('ns_task_batch', TB.batch)
redis.register_function('ns_task_sweep', TB.sweep)
