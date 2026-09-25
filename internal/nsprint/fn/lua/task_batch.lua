-- The batch task verbs on the ws index (nova-tools #3661, sweep #3647; part
-- of #3662). A task is a task card (02_card_move.lua, NS.task, nova-tools
-- #3778): task:<id> with the pointer where (waiting ready working merging
-- landed done parked, or '' in no set), stream and friend; its views are
-- ws:<stream>:<where> and friend:<friend>:cards:<where>, scored by the
-- task's created_at. Every row moves through NS.task.move, the one writer
-- of the pointer, the views, ws:log and the friend-queue shapes
-- (sprint:<S>:idx:<owner>:*, q:<owner>, q:blocked); this file writes none
-- of them. cancel is done/fail; the verbs' 'closed' is done.
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
-- move-stream and front keep every score (the task's age); front writes
-- front=1 and order 0 and requeues the ready entry on q:<owner>:front.
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

-- The verbs' 'closed' is the card's done (done/fail: a cancel).
TB.KNOWN = { waiting = true, ready = true, working = true, merging = true, landed = true, parked = true,
  closed = true, done = true }
TB.ORDERED = { waiting = true, ready = true, parked = true }
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

-- TB.read returns the row (id, stream, state = its where, owner, and the
-- fields sweep reads), or nil and why it cannot move (no record or stream).
function TB.read(id)
  local p = NS.task.read(id)
  if not p or p.stream == '' then
    return nil, 'no-stream'
  end
  local t = { id = id, stream = p.stream, owner = p.friend, kind = p.kind, ref = p.ref, pr = p.pr, sprint = p.sprint }
  local x = redis.call('HMGET', 'task:' .. id, 'head', 'repo')
  t.head, t.repo = TB.str(x[1]), TB.str(x[2])
  t.state = p.where
  if not p.placed then
    t.state = ({ open = 'ready', closed = 'done', cancelled = 'done', blocked = 'waiting' })[p.state] or p.state
  end
  return t
end

-- TB.target returns the where a verb moves the row to and the move's
-- options, or nil and why not. Every move is checked by NS.task.check
-- before any row is written.
function TB.target(ctx, t, why)
  local verb, param, st = ctx.verb, ctx.param, t.state
  local o = { by = ctx.by, why = why, sprint = ctx.S }
  if st == 'done' then
    return nil, 'closed'
  end
  if verb == 'cancel' then
    o.ok, o.fields = 'fail', { 'evidence', why }
    return 'done', o
  elseif verb == 'block' then
    if st == 'ready' or st == 'waiting' then
      o.fields = { 'blocked_on', ctx.on or '', 'blocked_reason', why, 'blocked_at', tostring(ctx.now) }
      return 'waiting', o
    end
  elseif verb == 'unblock' then
    if st == 'waiting' then
      return 'ready', o
    end
  elseif verb == 'move-state' then
    if param == 'closed' or param == 'done' then
      o.ok, o.fields = 'fail', { 'evidence', why }
      return 'done', o
    end
    if st == param then
      return nil, st .. '-to-' .. param
    end
    return param, o
  elseif verb == 'move-stream' then
    if t.stream == param then
      return nil, 'same-stream'
    end
    o.stream = param
    return st, o
  elseif verb == 'move-friend' then
    if t.owner == param then
      return nil, 'same-friend'
    end
    if TB.ORDERED[st] then
      o.friend = param
      return st, o
    end
  elseif verb == 'front' then
    if TB.ORDERED[st] then
      o.front, o.fields = '1', { 'order', '0' }
      return st, o
    end
  end
  return nil, verb .. '-from-' .. st
end

-- TB.apply moves one checked row through the one task move.
function TB.apply(ctx, t)
  local err = NS.task.move(t.id, t.to, t.o)
  if err then
    return err
  end
  if ctx.verb == 'unblock' then
    redis.call('HDEL', 'task:' .. t.id, 'blocked_on', 'blocked_reason', 'blocked_at')
  end
  ctx.streams[t.o.stream or t.stream] = true
  return nil
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
  local ctx = { verb = verb, S = S, by = by, param = param, now = TB.now(), streams = {} }
  if verb == 'block' then
    ctx.on = param
  end
  if why == nil or why == '' then
    why = verb
  end
  local rows, seen = {}, {}
  for _, id in ipairs(ids) do
    if not seen[id] then
      seen[id] = true
      local t, bad = TB.read(id)
      if not t then
        return { 'REFUSED', id, bad }
      end
      local to, o = TB.target(ctx, t, why)
      if not to then
        return { 'REFUSED', id, o }
      end
      bad = NS.task.check(id, to, o)
      if bad then
        return { 'REFUSED', id, bad }
      end
      t.to, t.o = to, o
      rows[#rows + 1] = t
    end
  end
  for _, t in ipairs(rows) do
    local err = TB.apply(ctx, t)
    if err then
      return { 'REFUSED', t.id, err }
    end
  end
  local fallback = '-'
  if args[6] == 'stream' then
    fallback = args[7]
  elseif verb == 'move-stream' then
    fallback = param
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
  local ctx = { verb = 'sweep', S = S, by = by, param = '', now = TB.now(), streams = {} }
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
        local why = 'sweep: head moved ' .. t.head .. ' -> ' .. head
        t.to, t.o = 'done', { by = by, why = why, ok = 'fail', sprint = S, fields = { 'evidence', why } }
        if TB.apply(ctx, t) then
          skipped = skipped + 1
        else
          cancelled = cancelled + 1
        end
      elseif pitstop[sprint] then
        local why = 'sweep: pitstop ' .. sprint
        t.to, t.o = 'waiting', { by = by, why = why, sprint = S,
          fields = { 'blocked_on', '', 'blocked_reason', why, 'blocked_at', tostring(ctx.now) } }
        if TB.apply(ctx, t) then
          skipped = skipped + 1
        else
          waiting = waiting + 1
        end
      end
    end
  end
  return { 'OK', cancelled + waiting, TB.stream_of(ctx, '-'), cancelled, waiting, skipped }
end

redis.register_function('ns_task_batch', TB.batch)
redis.register_function('ns_task_sweep', TB.sweep)
