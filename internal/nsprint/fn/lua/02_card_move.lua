-- ONE PLACE: the card model (rowan-new specs/ws-index.md, "The card model";
-- nova-tools#3692).
--
-- Glenn 2026-09-24 11:35 PM ET: "cards are not allowed to disappear. They go
-- from ready -> working -> done, and either OK or fail. they cannot DISAPPEAR."
-- 11:40 PM: "The data structure of cards in redis should enforce that cards
-- can only move, and not disappear."
-- 11:50 PM: "Instead of making it look like cards move, have a card be a THING
-- that exists, and points to where it is. That way it can't disappear."
-- 12:10 AM: "A card is a real thing. It exists in redis, it points to the
-- issue in github it came from (today), or eventually, the work item it came
-- from in the nova-work sexp. The card starts in waiting per-stream, and moves
-- by changing a pointer to which set it is in on the card itself."
-- 12:12 AM: "The sets are actual sorted sets of cards, perhaps pointing to the
-- card by id. do this all in redis. it's obvious."
-- 12:20 AM: "change one, you change the other."
-- 12:22 AM: "it's a double link, you can go either way. it's YOUR JOB to make
-- sure that these links are always valid. the sorted order should be in order
-- of age of the card, uniformly."
-- 12:25 AM: "then in redis we have these tables, and the values are just the
-- sizes of the sets |s|. we are just moving between sets. doubly linked."
-- 12:28 AM: "a card can only ever be in no set, or one of these sets that drive
-- the tables. it can only be one place. this is invariant."
--
-- 12:32 AM: "the card can also NOT be in any set. it can be null. it starts
-- this way, before you assign it to the correct work stream 'waiting' set. so
-- a card is either in no set, or in one of the sets backing the tables."
--
-- ONE PLACE: where is null (empty: in no table set) or exactly one set
-- backing the tables, per dimension that applies to the card.
--
-- The card is its record, s:<S>:card:<label>; that key is the card id and the
-- record is never deleted. Create writes it with where empty and adds the id
-- to sprint:<S>:cards (every card of the sprint, ever; the roster, not a
-- table set); push then moves it to waiting. The record points to its one
-- place: where (waiting,
-- ready, working, done, parked) with where_ok (ok|fail once done, - before),
-- plus the dimensions that apply to it: bench (dealt bench or pin; _pool when
-- empty), stream (the card's STREAM: line) and owner (a friend holding it).
-- Each dimension is a view of the one place, a ZSET of card ids:
--
--   bench:<bench|_pool>:cards:<where>, and bench:<b>:cards:ok|fail while done
--   ws:<stream>:<where>               when the card has a stream
--   friend:<owner>:cards:<where>      when the card has an owner
--   s:<S>:pool (ZSET of labels at the deal score) while ready, and
--   s:<S>:waiting (SET of labels) while waiting: the dealer's lists
--
-- Every score, in every view and in sprint:<S>:cards, is the card's
-- created_at (ms), so each list reads oldest first and a move never rescores.
-- The fine state (queued, dealt, ..., landed) stays on the record as state,
-- with its SET index s:<S>:idx:card:<state> (members are labels).
--
-- card_move below is the ONLY writer of state, where, where_ok, bench and of
-- every view (the dealer's pool and waiting lists included) and state index.
-- Before any write it checks the double link: the id is in each view its
-- pointer names and in no other set it could be in (every place and outcome
-- of every registered bench, _pool and the bench it moves to; its stream's
-- and owner's other places; the pool and waiting lists), and it refuses a
-- move off the graph. A violation is a refusal, never a write. Then, in the
-- same call, it sets the pointer, removes the old views, adds the new ones,
-- SMOVEs the state index, checks the new link and writes one receipt to
-- sprint:<S>:moves (under the bench seat's ACL, which has no ws:* pattern; a
-- card with a stream needs ~ws:* there). A Redis Function cannot call
-- another, so later files reach it as NS.card (hence the 02_ name: it loads
-- first).
-- nova-sprint card fsck walks both directions (ns_card_fsck, ns_card_repair).

local CM_WHERE = { 'waiting', 'ready', 'working', 'done', 'parked' }
local CM_IS_WHERE = { waiting = true, ready = true, working = true, done = true, parked = true }
-- fine state -> where (queued is also waiting or parked)
local CM_FINE = {
  queued = 'ready', dealt = 'working', launched = 'working', running = 'working',
  ['reconcile-required'] = 'working', ['orphan-effect'] = 'working',
  ended = 'done', harvested = 'done', refused = 'done', ['review-ready'] = 'done',
  ['land-ready'] = 'done', landed = 'done', superseded = 'done',
}
-- the pointer is written only through card_move's own arguments
local CM_POINTER = { state = true, where = true, where_ok = true, bench = true, created_at = true, where_at = true }

local function cm_now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function cm_split(id)
  if type(id) ~= 'string' then return nil end
  return string.match(id, '^s:([-a-z0-9]+):card:([A-Za-z0-9][A-Za-z0-9._-]*)$')
end

local function cm_idx(S, state)
  return 's:' .. S .. ':idx:card:' .. state
end

local function cm_bench(b)
  if b == nil or b == '' then return '_pool' end
  return b
end

-- The record's pointer; nil when the record does not exist.
local function cm_read(id)
  local c = redis.call('HMGET', id, 'state', 'where', 'where_ok', 'bench', 'stream', 'owner', 'created_at', 'priority')
  if not c[1] then return nil end
  local S = cm_split(id)
  return {
    state = c[1], where = c[2] or '', ok = c[3] or '-', bench = c[4] or '',
    stream = c[5] or '', owner = c[6] or '', created = tonumber(c[7]) or 0, priority = tonumber(c[8]) or 0,
    linked = S ~= nil and redis.call('ZSCORE', 'sprint:' .. S .. ':cards', id) ~= false,
  }
end

-- The views one pointer names. A view entry is t = 'z' (a ZSET of card
-- ids scored by created_at), 'p' (the sprint's pool: the dealer's ZSET of
-- labels at their deal score) or 's' (the sprint's waiting SET of labels):
-- the dealer's two lists are views of ready and waiting like any other.
local function cm_views(S, label, id, p)
  local v = {}
  if p.where == '' then return v end
  local b = cm_bench(p.bench)
  v[#v + 1] = { t = 'z', k = 'bench:' .. b .. ':cards:' .. p.where, m = id }
  if p.where == 'done' then v[#v + 1] = { t = 'z', k = 'bench:' .. b .. ':cards:' .. p.ok, m = id } end
  if p.stream ~= '' then v[#v + 1] = { t = 'z', k = 'ws:' .. p.stream .. ':' .. p.where, m = id } end
  if p.owner ~= '' then v[#v + 1] = { t = 'z', k = 'friend:' .. p.owner .. ':cards:' .. p.where, m = id } end
  if p.where == 'ready' then v[#v + 1] = { t = 'p', k = 's:' .. S .. ':pool', m = label } end
  if p.where == 'waiting' then v[#v + 1] = { t = 's', k = 's:' .. S .. ':waiting', m = label } end
  return v
end

local function cm_has(e)
  if e.t == 's' then return redis.call('SISMEMBER', e.k, e.m) == 1 end
  return redis.call('ZSCORE', e.k, e.m) ~= false
end

local function cm_add(e, score, pscore)
  if e.t == 's' then
    redis.call('SADD', e.k, e.m)
  elseif e.t == 'p' then
    redis.call('ZADD', e.k, pscore, e.m)
  else
    redis.call('ZADD', e.k, score, e.m)
  end
end

local function cm_rem(e)
  if e.t == 's' then
    redis.call('SREM', e.k, e.m)
  else
    redis.call('ZREM', e.k, e.m)
  end
end

-- Every other set the card could be in: each place and outcome of every
-- registered bench, of _pool and of the benches named in extra (the bench a
-- move goes to), the other places of its stream and owner, and the pool and
-- waiting lists when neither is its place.
local function cm_others(S, label, id, p, extra)
  local mine, seen, v = {}, {}, {}
  for _, e in ipairs(cm_views(S, label, id, p)) do mine[e.k] = true end
  local function add(t, k, m)
    if not mine[k] and not seen[k] then
      seen[k] = true
      v[#v + 1] = { t = t, k = k, m = m }
    end
  end
  local benches = redis.call('SMEMBERS', 'benches')
  benches[#benches + 1] = '_pool'
  benches[#benches + 1] = cm_bench(p.bench)
  for _, b in ipairs(extra or {}) do benches[#benches + 1] = cm_bench(b) end
  for _, b in ipairs(benches) do
    for _, w in ipairs(CM_WHERE) do add('z', 'bench:' .. b .. ':cards:' .. w, id) end
    add('z', 'bench:' .. b .. ':cards:ok', id)
    add('z', 'bench:' .. b .. ':cards:fail', id)
  end
  for _, w in ipairs(CM_WHERE) do
    if p.stream ~= '' then add('z', 'ws:' .. p.stream .. ':' .. w, id) end
    if p.owner ~= '' then add('z', 'friend:' .. p.owner .. ':cards:' .. w, id) end
  end
  add('p', 's:' .. S .. ':pool', label)
  add('s', 's:' .. S .. ':waiting', label)
  return v
end

-- nil when the double link holds for p: the id is in every view p names and
-- in no other set it could be in (extra: benches a move is about to name);
-- else what is wrong.
local function cm_verify(S, label, id, p, extra)
  for _, e in ipairs(cm_views(S, label, id, p)) do
    if not cm_has(e) then return 'unlinked ' .. e.k end
  end
  for _, e in ipairs(cm_others(S, label, id, p, extra)) do
    if cm_has(e) then return 'twice ' .. e.k end
  end
  return nil
end

local function cm_fits(state, where)
  if CM_FINE[state] == where then return true end
  return state == 'queued' and (where == 'waiting' or where == 'parked')
end

-- The graph: waiting <-> ready -> working -> done; working -> ready (a
-- reservation returned: undeal, bench reset, requeue); any -> done/fail
-- (refusal, crash, wall); any not done -> done/ok only by a merge (landed);
-- done/fail -> waiting or ready (a retry); waiting, ready <-> parked; done/ok
-- becomes done/fail on a refused result and never returns to the pool.
local function cm_edge(from, fok, to, tok, state)
  if from == '' then
    if to == 'waiting' then return nil end
    return 'OFFGRAPH a new card starts in waiting'
  end
  if to == 'done' then
    if from == 'done' then
      if fok == 'fail' and tok == 'ok' and state ~= 'landed' then return 'OFFGRAPH done/fail -> done/ok' end
      return nil
    end
    if from == 'working' or tok == 'fail' or state == 'landed' then return nil end
    return 'OFFGRAPH ' .. from .. ' -> done/ok without a run or a merge'
  end
  if from == to then return nil end
  if from == 'done' then
    if fok == 'fail' and (to == 'ready' or to == 'waiting') then return nil end
    return 'OFFGRAPH done/' .. fok .. ' -> ' .. to
  end
  local ok = {
    waiting = { ready = true, parked = true },
    ready = { waiting = true, working = true, parked = true },
    working = { ready = true },
    parked = { waiting = true, ready = true },
  }
  if ok[from] and ok[from][to] then return nil end
  return 'OFFGRAPH ' .. from .. ' -> ' .. to
end

-- The place a record's fine state and outcome imply (fsck and adoption).
-- The pointer's own choice stands where the fine state allows more than one
-- place: a queued card's waiting, ready or parked, and a done card's fail
-- (fail is always a legal outcome: a refused result, a refusal).
local function cm_derive(S, label, id)
  local c = redis.call('HMGET', id, 'state', 'outcome', 'verdict', 'ci_head', 'attempt', 'where', 'where_ok')
  local state = c[1]
  local w = state and CM_FINE[state]
  if not w then return nil end
  if state == 'queued' then
    if c[6] == 'waiting' or c[6] == 'ready' or c[6] == 'parked' then
      w = c[6]
    elseif redis.call('SISMEMBER', 's:' .. S .. ':waiting', label) == 1 then
      w = 'waiting'
    end
  end
  local ok = '-'
  if w == 'done' then
    if state == 'landed' then
      ok = 'ok'
    elseif state == 'refused' or state == 'superseded' or c[2] ~= 'DONE' then
      ok = 'fail'
    elseif c[4] and c[4] ~= '' and c[3] ~= 'OK' then
      ok = 'fail'
    elseif redis.call('HGET', id .. ':result:a' .. tostring(c[5] or ''), 'valid') == '0' then
      ok = 'fail'
    else
      ok = 'ok'
    end
    if c[6] == 'done' and c[7] == 'fail' then ok = 'fail' end
  end
  return w, ok
end

-- Link a record that predates this model (no created_at, not in
-- sprint:<S>:cards): its place from its fine state, every view, its state
-- index. The only writer that adds without a remove, and only for an id no
-- set holds yet.
local function cm_adopt(id, S, label)
  local w, ok = cm_derive(S, label, id)
  if not w then return 'STATE ' .. tostring(redis.call('HGET', id, 'state')) .. ' has no place' end
  local c = redis.call('HMGET', id, 'created_at', 'cut_at', 'bench', 'stream', 'owner', 'state', 'priority')
  local created = tonumber(c[1])
  if not created then
    created = tonumber(c[2]) or cm_now()
    if created < 100000000000 then created = created * 1000 end
  end
  local p = { where = w, ok = ok, bench = c[3] or '', stream = c[4] or '', owner = c[5] or '' }
  redis.call('ZADD', 'sprint:' .. S .. ':cards', created, id)
  for _, e in ipairs(cm_views(S, label, id, p)) do
    if not cm_has(e) then cm_add(e, created, tonumber(c[7]) or 0) end
  end
  if w ~= 'ready' then redis.call('ZREM', 's:' .. S .. ':pool', label) end
  if w ~= 'waiting' then redis.call('SREM', 's:' .. S .. ':waiting', label) end
  redis.call('SADD', cm_idx(S, c[6]), label)
  redis.call('HSET', id, 'created_at', string.format('%.0f', created), 'where', w, 'where_ok', ok)
  return nil
end

-- card_move(id, to, o): the one move. o.state is the new fine state (nil
-- keeps it), o.ok is ok|fail (entering done; nil keeps it), o.bench the new
-- bench (nil keeps it), o.pool_score the card's deal score when it enters
-- the pool (nil: its priority), o.fields more HSET pairs (never a pointer field),
-- o.by and o.why the receipt. Returns nil when moved, else the refusal; a
-- refusal writes nothing but the one-time adoption of a record that
-- predates the model.
local function card_move(id, to, o)
  o = o or {}
  local S, label = cm_split(id)
  if not S then return 'BADID ' .. tostring(id) end
  if not CM_IS_WHERE[to] then return 'WHERE ' .. tostring(to) end
  local fields = o.fields or {}
  for i = 1, #fields, 2 do
    if CM_POINTER[fields[i]] then return 'FIELD ' .. fields[i] .. ' is the pointer' end
  end
  local cur = cm_read(id)
  if not cur then return 'NOCARD ' .. id end
  if not cur.linked then
    local err = cm_adopt(id, S, label)
    if err then return err end
    cur = cm_read(id)
  end
  local state = o.state or cur.state
  if not cm_fits(state, to) then return 'OFFGRAPH state ' .. state .. ' is not ' .. to end
  local ok = '-'
  if to == 'done' then
    ok = o.ok or cur.ok
    if ok ~= 'ok' and ok ~= 'fail' then return 'OUTCOME done needs ok or fail' end
  end
  local err = cm_edge(cur.where, cur.ok, to, ok, state)
  if err then return err end
  local nxt = { where = to, ok = ok, bench = cur.bench, stream = cur.stream, owner = cur.owner }
  if o.bench ~= nil then nxt.bench = o.bench end
  -- Before any write: the current link holds and the id is in no other set
  -- it could be in, the new bench's included, so every set the move adds it
  -- to is empty of it. A violation is a refusal, never a write.
  err = cm_verify(S, label, id, cur, { nxt.bench })
  if not err and state ~= cur.state and redis.call('SISMEMBER', cm_idx(S, cur.state), label) == 0 then
    err = 'unindexed ' .. cm_idx(S, cur.state)
  end
  if err then return 'DRIFT ' .. err .. ' ' .. id .. '; run nova-sprint card fsck --sprint ' .. S .. ' --repair' end

  local pscore = o.pool_score or cur.priority
  local old, new = cm_views(S, label, id, cur), cm_views(S, label, id, nxt)
  local was, keep = {}, {}
  for _, e in ipairs(old) do was[e.k] = true end
  for _, e in ipairs(new) do keep[e.k] = true end
  for _, e in ipairs(old) do
    if not keep[e.k] then cm_rem(e) end
  end
  for _, e in ipairs(new) do
    if not was[e.k] then cm_add(e, cur.created, pscore) end
  end
  if state ~= cur.state then redis.call('SMOVE', cm_idx(S, cur.state), cm_idx(S, state), label) end
  local at = cm_now()
  local h = { id, 'state', state, 'where', to, 'where_ok', ok }
  if nxt.bench ~= cur.bench then
    h[#h + 1] = 'bench'
    h[#h + 1] = nxt.bench
  end
  if to ~= cur.where then
    h[#h + 1] = 'where_at'
    h[#h + 1] = tostring(at)
  end
  for i = 1, #fields do h[#h + 1] = fields[i] end
  redis.call('HSET', unpack(h))
  -- After: the new views hold the id and the dropped ones do not (the
  -- before-check already proved every other set empty of it).
  for _, e in ipairs(new) do
    if not cm_has(e) then return 'DRIFT-AFTER unlinked ' .. e.k .. ' ' .. id end
  end
  for _, e in ipairs(old) do
    if not keep[e.k] and cm_has(e) then return 'DRIFT-AFTER twice ' .. e.k .. ' ' .. id end
  end
  if to ~= cur.where or ok ~= cur.ok then
    local from = cur.where
    if from == 'done' then from = 'done/' .. cur.ok end
    local dest = to
    if to == 'done' then dest = 'done/' .. ok end
    redis.call('XADD', 'sprint:' .. S .. ':moves', 'MAXLEN', '~', '100000', '*',
      'id', id, 'stream', cur.stream, 'from', from, 'to', dest,
      'by', o.by or '', 'why', o.why or state, 'at', tostring(at))
  end
  return nil
end

-- card_create(id, fields, o): a new record and its first move, '' ->
-- waiting. fields are its HSET pairs (never a pointer field); o.bench,
-- o.stream and o.owner are its dimensions. Returns nil or the refusal. The
-- caller has checked the id is new.
local function card_create(id, fields, o)
  o = o or {}
  local S, label = cm_split(id)
  if not S then return 'BADID ' .. tostring(id) end
  for i = 1, #fields, 2 do
    if CM_POINTER[fields[i]] then return 'FIELD ' .. fields[i] .. ' is the pointer' end
  end
  local created = cm_now()
  local h = { id }
  for i = 1, #fields do h[#h + 1] = fields[i] end
  for _, f in ipairs({ 'bench', 'stream', 'owner' }) do
    if o[f] ~= nil and o[f] ~= '' then
      h[#h + 1] = f
      h[#h + 1] = o[f]
    end
  end
  h[#h + 1] = 'state'
  h[#h + 1] = 'queued'
  h[#h + 1] = 'where'
  h[#h + 1] = ''
  h[#h + 1] = 'where_ok'
  h[#h + 1] = '-'
  h[#h + 1] = 'created_at'
  h[#h + 1] = tostring(created)
  redis.call('HSET', unpack(h))
  redis.call('ZADD', 'sprint:' .. S .. ':cards', created, id)
  redis.call('SADD', cm_idx(S, 'queued'), label)
  return card_move(id, 'waiting', { by = o.by, why = 'push' })
end

-- fsck of one sprint in one call, both directions. write repairs: adopts
-- every record the sprint's state indexes, waiting set and pool name that
-- sprint:<S>:cards lacks, rewrites a pointer its fine state contradicts, adds
-- a missing link and removes a stray one. A null card (where empty: created,
-- not yet placed) is in no table set and is counted as null. Returns FSCK S
-- cards null waiting ready working done parked ok fail drift fixed, then up
-- to 50 drift lines.
local function card_fsck(S, write)
  local drift, fixed, lines = 0, 0, {}
  local function note(s)
    drift = drift + 1
    if #lines < 50 then lines[#lines + 1] = s end
  end
  local all = 'sprint:' .. S .. ':cards'
  local prefix = 's:' .. S .. ':card:'
  local seen = {}
  for fine in pairs(CM_FINE) do
    for _, l in ipairs(redis.call('SMEMBERS', cm_idx(S, fine))) do seen[l] = true end
  end
  for _, l in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':waiting')) do seen[l] = true end
  for _, l in ipairs(redis.call('ZRANGE', 's:' .. S .. ':pool', 0, -1)) do seen[l] = true end
  for l in pairs(seen) do
    local id = prefix .. l
    if cm_split(id) and redis.call('EXISTS', id) == 1 and not redis.call('ZSCORE', all, id) then
      note('unlisted ' .. id)
      if write and not cm_adopt(id, S, l) then fixed = fixed + 1 end
    end
  end

  local n = { null = 0, waiting = 0, ready = 0, working = 0, done = 0, parked = 0, ok = 0, fail = 0 }
  local want, benches, streams, owners = {}, { _pool = true }, {}, {}
  for _, b in ipairs(redis.call('SMEMBERS', 'benches')) do benches[b] = true end
  for _, id in ipairs(redis.call('ZRANGE', all, 0, -1)) do
    do
      local _, label = cm_split(id)
      local c = cm_read(id)
      if not c or not label or string.sub(id, 1, #prefix) ~= prefix then
        note('gone ' .. id)
      else
        local w, ok = cm_derive(S, label, id)
        if c.where == '' and c.state == 'queued' then
          -- null: created, not yet placed; in no table set (Glenn 12:32 AM)
          n.null = n.null + 1
          benches[cm_bench(c.bench)] = true
          if c.stream ~= '' then streams[c.stream] = true end
          if c.owner ~= '' then owners[c.owner] = true end
        elseif not w then
          note('state ' .. id .. ' ' .. tostring(c.state))
        else
          if c.where ~= w or c.ok ~= ok then
            note('pointer ' .. id .. ' where=' .. c.where .. '/' .. c.ok .. ' state=' .. c.state .. ' implies ' .. w .. '/' .. ok)
            if write then
              redis.call('HSET', id, 'where', w, 'where_ok', ok)
              fixed = fixed + 1
            end
            c.where, c.ok = w, ok
          end
          n[w] = n[w] + 1
          if w == 'done' then n[ok] = n[ok] + 1 end
          benches[cm_bench(c.bench)] = true
          if c.stream ~= '' then streams[c.stream] = true end
          if c.owner ~= '' then owners[c.owner] = true end
          for _, e in ipairs(cm_views(S, label, id, c)) do
            want[e.k] = want[e.k] or {}
            want[e.k][e.m] = true
            if not cm_has(e) then
              note('unlinked ' .. e.k .. ' ' .. e.m)
              if write then
                cm_add(e, c.created, c.priority)
                fixed = fixed + 1
              end
            end
          end
          if redis.call('SISMEMBER', cm_idx(S, c.state), label) == 0 then
            note('unindexed ' .. cm_idx(S, c.state) .. ' ' .. label)
            if write then
              redis.call('SADD', cm_idx(S, c.state), label)
              fixed = fixed + 1
            end
          end
        end
      end
    end
  end

  local function sweep(k)
    for _, id in ipairs(redis.call('ZRANGE', k, 0, -1)) do
      if string.sub(id, 1, #prefix) == prefix and not (want[k] and want[k][id]) then
        note('stray ' .. k .. ' ' .. id)
        if write then
          redis.call('ZREM', k, id)
          fixed = fixed + 1
        end
      end
    end
  end
  -- The dealer's lists: the pool holds exactly the ready cards and the
  -- waiting set exactly the waiting ones (members are labels).
  local pool, waiting = 's:' .. S .. ':pool', 's:' .. S .. ':waiting'
  for _, l in ipairs(redis.call('ZRANGE', pool, 0, -1)) do
    if not (want[pool] and want[pool][l]) then
      note('stray ' .. pool .. ' ' .. l)
      if write then
        redis.call('ZREM', pool, l)
        fixed = fixed + 1
      end
    end
  end
  for _, l in ipairs(redis.call('SMEMBERS', waiting)) do
    if not (want[waiting] and want[waiting][l]) then
      note('stray ' .. waiting .. ' ' .. l)
      if write then
        redis.call('SREM', waiting, l)
        fixed = fixed + 1
      end
    end
  end
  for b in pairs(benches) do
    for _, w in ipairs(CM_WHERE) do sweep('bench:' .. b .. ':cards:' .. w) end
    sweep('bench:' .. b .. ':cards:ok')
    sweep('bench:' .. b .. ':cards:fail')
  end
  for s in pairs(streams) do
    for _, w in ipairs(CM_WHERE) do sweep('ws:' .. s .. ':' .. w) end
  end
  for f in pairs(owners) do
    for _, w in ipairs(CM_WHERE) do sweep('friend:' .. f .. ':cards:' .. w) end
  end
  for fine in pairs(CM_FINE) do
    for _, l in ipairs(redis.call('SMEMBERS', cm_idx(S, fine))) do
      local st = redis.call('HGET', prefix .. l, 'state')
      if st and st ~= fine then
        note('stray ' .. cm_idx(S, fine) .. ' ' .. l .. ' (state ' .. st .. ')')
        if write then
          redis.call('SREM', cm_idx(S, fine), l)
          fixed = fixed + 1
        end
      end
    end
  end

  local total = redis.call('ZCARD', all)
  local sum = n.null + n.waiting + n.ready + n.working + n.done + n.parked
  if sum ~= total then note('sum ' .. sum .. ' (null + places) ~= ' .. total .. ' cards') end
  local out = { 'FSCK', S, tostring(total), tostring(n.null), tostring(n.waiting), tostring(n.ready), tostring(n.working),
    tostring(n.done), tostring(n.parked), tostring(n.ok), tostring(n.fail), tostring(drift), tostring(fixed) }
  for _, l in ipairs(lines) do out[#out + 1] = l end
  return out
end

-- ns_card_move(id, where[, ok[, by[, why]]]): a move that keeps the fine
-- state (waiting <-> ready <-> parked, done/ok -> done/fail); the dealer's
-- pool and waiting lists move with it, as with every move. OK or
-- REFUSED <why>.
redis.register_function('ns_card_move', function(keys, args)
  local ok = args[3]
  if ok == '' then ok = nil end
  local err = card_move(args[1], args[2], { ok = ok, by = args[4], why = args[5] })
  if err then return 'REFUSED ' .. err end
  return 'OK'
end)

-- ns_card_fsck(S): the read-only walk. ns_card_repair(S): the same walk,
-- repairing (and adopting every record that predates the model: the one-time
-- reindex).
redis.register_function({ function_name = 'ns_card_fsck', flags = { 'no-writes' },
  callback = function(keys, args) return card_fsck(args[1], false) end })
redis.register_function('ns_card_repair', function(keys, args) return card_fsck(args[1], true) end)

-- ns_card_counts(S): every table cell as a ZCARD in one reply, read-only:
-- {cards, n}, then {stream, name, waiting, ready, working, done, parked} per
-- ws:order stream, {bench, name, waiting, ready, working, done, parked, ok,
-- fail} per registered bench and _pool, {friend, name, waiting, ready,
-- working, done, parked} per member of friends.
redis.register_function({ function_name = 'ns_card_counts', flags = { 'no-writes' },
  callback = function(keys, args)
    local out = { { 'cards', tostring(redis.call('ZCARD', 'sprint:' .. tostring(args[1]) .. ':cards')) } }
    for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
      local row = { 'stream', s }
      for _, w in ipairs(CM_WHERE) do row[#row + 1] = tostring(redis.call('ZCARD', 'ws:' .. s .. ':' .. w)) end
      out[#out + 1] = row
    end
    local benches = redis.call('SMEMBERS', 'benches')
    table.sort(benches)
    benches[#benches + 1] = '_pool'
    for _, b in ipairs(benches) do
      local row = { 'bench', b }
      for _, w in ipairs(CM_WHERE) do row[#row + 1] = tostring(redis.call('ZCARD', 'bench:' .. b .. ':cards:' .. w)) end
      row[#row + 1] = tostring(redis.call('ZCARD', 'bench:' .. b .. ':cards:ok'))
      row[#row + 1] = tostring(redis.call('ZCARD', 'bench:' .. b .. ':cards:fail'))
      out[#out + 1] = row
    end
    local friends = redis.call('SMEMBERS', 'friends')
    table.sort(friends)
    for _, f in ipairs(friends) do
      local row = { 'friend', f }
      for _, w in ipairs(CM_WHERE) do row[#row + 1] = tostring(redis.call('ZCARD', 'friend:' .. f .. ':cards:' .. w)) end
      out[#out + 1] = row
    end
    return out
  end })

NS.card = { move = card_move, create = card_create }
