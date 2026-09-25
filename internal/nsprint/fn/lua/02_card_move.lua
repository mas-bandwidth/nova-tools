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
--   s:<S>:pool (ZSET of labels) while ready, and
--   s:<S>:waiting (SET of labels) while waiting: the dealer's lists
--
-- Every score, in every view (the pool included) and in sprint:<S>:cards,
-- is the card's created_at (ms), uniformly, so each list reads oldest first
-- and a move never rescores; fsck checks every score and --repair re-scores.
-- The deal priority is not a score: it is the record's priority field
-- (lower deals first; front and ci cards are negative), which the move
-- writes when a card enters ready with o.priority, and the dealer reads the
-- pool oldest first and deals by that field, age breaking ties.
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
-- ids), 'l' (the sprint's pool: a ZSET of labels) or 's' (the sprint's
-- waiting SET of labels); every ZSET is scored by created_at. The dealer's
-- two lists are views of ready and waiting like any other.
local function cm_views(S, label, id, p)
  local v = {}
  if p.where == '' then return v end
  local b = cm_bench(p.bench)
  v[#v + 1] = { t = 'z', k = 'bench:' .. b .. ':cards:' .. p.where, m = id }
  if p.where == 'done' then v[#v + 1] = { t = 'z', k = 'bench:' .. b .. ':cards:' .. p.ok, m = id } end
  if p.stream ~= '' then v[#v + 1] = { t = 'z', k = 'ws:' .. p.stream .. ':' .. p.where, m = id } end
  if p.owner ~= '' then v[#v + 1] = { t = 'z', k = 'friend:' .. p.owner .. ':cards:' .. p.where, m = id } end
  if p.where == 'ready' then v[#v + 1] = { t = 'l', k = 's:' .. S .. ':pool', m = label } end
  if p.where == 'waiting' then v[#v + 1] = { t = 's', k = 's:' .. S .. ':waiting', m = label } end
  return v
end

local function cm_has(e)
  if e.t == 's' then return redis.call('SISMEMBER', e.k, e.m) == 1 end
  return redis.call('ZSCORE', e.k, e.m) ~= false
end

local function cm_add(e, score)
  if e.t == 's' then
    redis.call('SADD', e.k, e.m)
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
  add('l', 's:' .. S .. ':pool', label)
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
  -- A pre-model pool scored the card by its deal priority: that score moves
  -- to the record's priority field (when it has none) and the pool is
  -- re-scored by created_at like every view.
  local legacy = redis.call('ZSCORE', 's:' .. S .. ':pool', label)
  if legacy and not c[7] then redis.call('HSET', id, 'priority', tostring(legacy)) end
  redis.call('ZADD', 'sprint:' .. S .. ':cards', created, id)
  for _, e in ipairs(cm_views(S, label, id, p)) do cm_add(e, created) end
  if w ~= 'ready' then redis.call('ZREM', 's:' .. S .. ':pool', label) end
  if w ~= 'waiting' then redis.call('SREM', 's:' .. S .. ':waiting', label) end
  redis.call('SADD', cm_idx(S, c[6]), label)
  redis.call('HSET', id, 'created_at', string.format('%.0f', created), 'where', w, 'where_ok', ok)
  return nil
end

-- card_move(id, to, o): the one move. o.state is the new fine state (nil
-- keeps it), o.ok is ok|fail (entering done; nil keeps it), o.bench the new
-- bench (nil keeps it), o.priority the card's deal priority as it enters
-- ready (written to the record's priority field; nil keeps the field),
-- o.fields more HSET pairs (never a pointer field),
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

  local old, new = cm_views(S, label, id, cur), cm_views(S, label, id, nxt)
  local was, keep = {}, {}
  for _, e in ipairs(old) do was[e.k] = true end
  for _, e in ipairs(new) do keep[e.k] = true end
  for _, e in ipairs(old) do
    if not keep[e.k] then cm_rem(e) end
  end
  for _, e in ipairs(new) do
    if not was[e.k] then cm_add(e, cur.created) end
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
  if o.priority ~= nil and to == 'ready' then
    h[#h + 1] = 'priority'
    h[#h + 1] = tostring(o.priority)
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
        if tonumber(redis.call('ZSCORE', all, id)) ~= c.created then
          note('score ' .. all .. ' ' .. id)
          if write then
            redis.call('ZADD', all, c.created, id)
            fixed = fixed + 1
          end
        end
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
                cm_add(e, c.created)
                fixed = fixed + 1
              end
            elseif e.t ~= 's' and tonumber(redis.call('ZSCORE', e.k, e.m)) ~= c.created then
              -- every ZSET is scored by the card's created_at, uniformly
              note('score ' .. e.k .. ' ' .. e.m)
              if write then
                cm_add(e, c.created)
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

-- ===========================================================================
-- Tasks are cards (nova-tools #3778; rowan-new specs/ws-index.md, "Tasks are
-- cards", Glenn 2026-09-25 07:50 AM ET: "All of the above will be fixed by
-- making the card real in redis and actually move between sets"; 07:52 AM:
-- "You should not have to continually remember to maintain the correct
-- structure, it should just happen. This is the goal.").
--
-- A sprint task is a card. Its record is task:<id> (the key every reader
-- already knows), never deleted, with the pointer
--   where      '' (null: in no set) | waiting | ready | working | merging |
--              landed | done | parked
--   where_ok   ok | fail once done (landed is ok), - before
--   where_at   ms of the last move that changed where
--   stream     the work stream (ws:names), '' when it has none
--   friend     the friend holding it ('' for none); owner mirrors it
--   created_at ms; the score of the task in every set, uniformly
--   state      the friend-queue word where maps to (TK.STATE), written for
--              one release so rowan-tools bin/friend-queue keeps working
-- beside origin, kind, ref, title, head, pr, repo, sprint, why, evidence.
-- Its views are ZSETs of ids scored by created_at:
--   ws:<stream>:<where>            while it has a stream (the stream table)
--   friend:<friend>:cards:<where>  while a friend holds it (the friend table)
-- and, for one release, the friend-queue shapes: sprint:<S>:idx:<f>:open |
-- working | closed (TK.IDX), the ready entry on q:<f> (q:<f>:front when
-- front=1; its stream and entry id are the record's queue and xid),
-- q:blocked while waiting or parked, and the roster sprint:<S>:tasks.
--
-- TK.move below is the ONLY writer of the pointer and of every view (the
-- fn rule in taskwriter_test.go fails on a second one). Before any write it
-- checks the double link (ONE PLACE): the id is in each view its pointer
-- names and in no other place of its stream or friend, nor of the stream or
-- friend the move goes to; a violation is a refusal, never a write. The
-- graph (TK.GRAPH):
--   '' -> waiting | ready                          push
--   waiting -> ready | parked | done               unblock, park, cancel
--   ready -> waiting | working | parked | done     block, take, park, cancel
--   working -> merging | done | landed             done (with a PR / not), a merge
--   working -> ready                              the lease lapsed (a why)
--   merging -> landed | done                       a merge, a PR closed unmerged
--   parked -> waiting | ready | done               unpark, cancel
--   landed -> done (ok only)                       table clear
-- landed needs the merge sha; a done that does not come from working needs
-- a why, and so does working -> ready.
-- A take (-> working) writes lease_until = now + TK.LEASE; the child renews
-- it with ns_tcard_beat every 60 s; ns_tcard_expire moves a working task
-- whose lease lapsed back to ready (why=lease lapsed), so a friend's working
-- column (ZCARD friend:<f>:cards:working) never counts a task nobody holds
-- (Glenn 08:15 AM: rowan working=65 with two children alive). A same-where call re-places the task: a new stream (any where), a
-- new friend or the front flag (waiting, ready, parked).
-- A Redis Function cannot call another, so later files reach it as NS.task:
--   NS.task.move(id, to, o)          -> nil, info | refusal
--   NS.task.create(id, fields, o)    -> nil, info | refusal
--   NS.task.place(id, where, ok, o)  -> placed where | nil, why (migrate only)
-- o: by, why, ok (ok|fail entering done), friend, stream, sha (landed),
-- sprint (the legacy idx sets' sprint when the record names none), front
-- ('1' requeues on q:<f>:front), as (a take: the friend taking), fields (more
-- HSET pairs, never a pointer field). info: from, to, xid (the ready entry).
local TK = {
  WHERE = { 'waiting', 'ready', 'working', 'merging', 'landed', 'done', 'parked' },
  IS = { waiting = true, ready = true, working = true, merging = true, landed = true, done = true, parked = true },
  STATE = { waiting = 'waiting', ready = 'open', working = 'working', merging = 'merging', landed = 'landed',
    done = 'closed', parked = 'parked' },
  IDX = { ready = 'open', working = 'working', merging = 'closed', landed = 'closed', done = 'closed' },
  GRAPH = {
    [''] = { waiting = true, ready = true },
    waiting = { ready = true, parked = true, done = true },
    ready = { waiting = true, working = true, parked = true, done = true },
    working = { merging = true, done = true, landed = true, ready = true },
    merging = { landed = true, done = true },
    parked = { waiting = true, ready = true, done = true },
    landed = { done = true },
    done = {},
  },
  REPLACE = { waiting = true, ready = true, parked = true },
  POINTER = { where = true, where_ok = true, where_at = true, state = true, state_at = true, stream = true,
    friend = true, owner = true, created_at = true, sprint = true, queue = true, xid = true, cancelled = true,
    lease_until = true },
  FIELDS = { 'where', 'where_ok', 'stream', 'friend', 'owner', 'created_at', 'sprint', 'state', 'title',
    'queue', 'xid', 'front', 'cancelled', 'pr', 'kind', 'ref', 'lease_until', 'beat_at', 'leased_at', 'where_at' },
  LOG_MAX = '200000',
  -- a take's lease: three missed 60 s beats
  LEASE = 180000,
  -- task migrate: a working task with no beat in this long goes back to ready
  STALE = 600000,
}

function TK.str(v)
  if v == nil or v == false then return '' end
  return tostring(v)
end

-- TK.days: days from 1970-01-01 to y-m-d (proleptic Gregorian).
function TK.days(y, m, d)
  if m <= 2 then y = y - 1 end
  local era = math.floor(y / 400)
  local yoe = y - era * 400
  local doy = math.floor((153 * ((m + 9) % 12) + 2) / 5) + d - 1
  return era * 146097 + yoe * 365 + math.floor(yoe / 4) - math.floor(yoe / 100) + doy - 719468
end

-- TK.ms reads created_at as epoch ms: a number (seconds when under 1e11) or
-- friend-queue's YYYY-MM-DDTHH:MM:SSZ; nil for anything else.
function TK.ms(v)
  if not v or v == '' then return nil end
  local n = tonumber(v)
  if n then
    if n < 100000000000 then n = n * 1000 end
    return n
  end
  local y, mo, d, h, mi, se = string.match(v, '^(%d%d%d%d)-(%d%d)-(%d%d)T(%d%d):(%d%d):(%d%d)')
  if not y then return nil end
  return ((TK.days(tonumber(y), tonumber(mo), tonumber(d)) * 24 + tonumber(h)) * 60 + tonumber(mi)) * 60000 +
    tonumber(se) * 1000
end

-- TK.stream_of: the stream field, else the title's "STREAM: <s> |" prefix.
function TK.stream_of(stream, title)
  if stream and stream ~= '' then return stream end
  if title then
    local s = string.match(title, '^%s*STREAM:%s*(.-)%s*|')
    if s and s ~= '' then return s end
  end
  return ''
end

function TK.valid_stream(s)
  return s == '' or (#s <= 64 and not string.find(s, '[%c|]'))
end

function TK.card_id(id)
  return string.match(id, '^s:[-a-z0-9]+:card:') ~= nil
end

-- TK.read: the record's pointer and legacy fields; nil when there is no
-- record. placed is false for a record that predates the where field.
function TK.read(id)
  if redis.call('EXISTS', 'task:' .. id) == 0 then return nil end
  local c = redis.call('HMGET', 'task:' .. id, unpack(TK.FIELDS))
  local p = {}
  for i, f in ipairs(TK.FIELDS) do p[f] = TK.str(c[i]) end
  p.placed = c[1] ~= false and c[1] ~= nil
  p.ok = p.where_ok
  if p.ok == '' then p.ok = '-' end
  if p.friend == '' then p.friend = p.owner end
  p.stream = TK.stream_of(p.stream, p.title)
  p.created = TK.ms(p.created_at)
  return p
end

-- TK.views: the sets pointer p names.
function TK.views(p)
  local v = {}
  if p.where == '' then return v end
  if p.stream ~= '' then v[#v + 1] = 'ws:' .. p.stream .. ':' .. p.where end
  if p.friend ~= '' then v[#v + 1] = 'friend:' .. p.friend .. ':cards:' .. p.where end
  return v
end

-- TK.verify: nil when the double link holds for p: the id is in every view
-- p names and in no other place of p's stream and friend, nor of the extra
-- streams and friends (where a move is about to put it); else what is wrong.
function TK.verify(id, p, streams, friends)
  local mine = {}
  for _, k in ipairs(TK.views(p)) do
    mine[k] = true
    if not redis.call('ZSCORE', k, id) then return 'unlinked ' .. k end
  end
  local seen = {}
  local function each(prefix, name)
    if name == '' or seen[prefix .. name] then return nil end
    seen[prefix .. name] = true
    for _, w in ipairs(TK.WHERE) do
      local k = prefix .. name .. ':' .. w
      if prefix == 'friend:' then k = 'friend:' .. name .. ':cards:' .. w end
      if not mine[k] and redis.call('ZSCORE', k, id) then return 'twice ' .. k end
    end
    return nil
  end
  local err = each('ws:', p.stream) or each('friend:', p.friend)
  if err then return err end
  for _, s in ipairs(streams or {}) do
    err = each('ws:', s)
    if err then return err end
  end
  for _, f in ipairs(friends or {}) do
    err = each('friend:', f)
    if err then return err end
  end
  return nil
end

-- TK.sprint: the sprint whose legacy idx sets the task is in: the record's,
-- else the caller's, else the first of sprint:order.
function TK.sprint(p, o)
  if p.sprint ~= '' then return p.sprint end
  if o and o.sprint and o.sprint ~= '' then return o.sprint end
  return TK.str(redis.call('ZRANGE', 'sprint:order', 0, 0)[1])
end

-- TK.register: a stream the task enters is in ws:names and ranked last in
-- ws:order when it has no rank.
function TK.register(stream)
  if stream == '' then return end
  redis.call('SADD', 'ws:names', stream)
  if not redis.call('ZSCORE', 'ws:order', stream) then
    redis.call('ZADD', 'ws:order', redis.call('ZCARD', 'ws:order') + 1, stream)
  end
end

-- TK.legacy keeps the friend-queue shapes of one move from cur to nxt and
-- returns the HSET pairs it owes the record (queue, xid) and whether they
-- are cleared.
function TK.legacy(id, S, cur, nxt, front, at)
  local h, clear = {}, false
  if S ~= '' then
    local ix = 'sprint:' .. S .. ':idx:'
    if cur.friend ~= '' then
      for _, st in ipairs({ 'open', 'working', 'closed' }) do redis.call('SREM', ix .. cur.friend .. ':' .. st, id) end
      if cur.where == 'working' and cur.xid ~= '' then redis.call('SREM', ix .. cur.friend .. ':leased', cur.xid) end
    end
    if nxt.friend ~= '' and TK.IDX[nxt.where] then
      redis.call('SADD', ix .. nxt.friend .. ':' .. TK.IDX[nxt.where], id)
    end
    redis.call('SADD', 'sprint:' .. S .. ':tasks', id)
  end
  local requeue = nxt.where == 'ready' and nxt.friend ~= '' and
    (cur.where ~= 'ready' or nxt.friend ~= cur.friend or front == '1')
  if cur.queue ~= '' and cur.xid ~= '' and (cur.where == 'working' or
      (cur.where == 'ready' and (nxt.where ~= 'ready' or requeue))) then
    if cur.friend ~= '' then redis.pcall('XACK', cur.queue, cur.friend, cur.xid) end
    redis.call('XDEL', cur.queue, cur.xid)
    clear = true
  end
  if requeue then
    local q = 'q:' .. nxt.friend
    if front == '1' then q = q .. ':front' end
    h = { 'queue', q, 'xid', redis.call('XADD', q, '*', 'id', id) }
    clear = false
  end
  if nxt.where == 'waiting' or nxt.where == 'parked' then
    redis.call('ZADD', 'q:blocked', 'NX', at, id)
  else
    redis.call('ZREM', 'q:blocked', id)
  end
  if nxt.where ~= 'waiting' then redis.call('ZREM', 'q:waiting', id) end
  return h, clear
end

-- TK.edge: nil when cur -> nxt is on the graph, else the refusal.
function TK.edge(id, cur, nxt, ok, o)
  local from, to = cur.where, nxt.where
  if from == to then
    if nxt.friend ~= cur.friend and not TK.REPLACE[to] then
      return 'OFFGRAPH a ' .. to .. ' task keeps its friend'
    end
    return nil
  end
  if not (TK.GRAPH[from] and TK.GRAPH[from][to]) then
    if from == '' then return 'OFFGRAPH a new task starts in waiting or ready' end
    return 'OFFGRAPH ' .. from .. ' -> ' .. to
  end
  if to == 'landed' and TK.str(o.sha) == '' then return 'SHA landed needs the merge sha' end
  if to == 'done' then
    if ok ~= 'ok' and ok ~= 'fail' then return 'OUTCOME done needs ok or fail' end
    if from == 'landed' and ok ~= 'ok' then return 'OFFGRAPH landed -> done/fail' end
    if from ~= 'working' and from ~= 'landed' and TK.str(o.why) == '' then
      return 'WHY ' .. from .. ' -> done needs a why'
    end
  end
  if from == 'working' and to == 'ready' and TK.str(o.why) == '' then return 'WHY working -> ready needs a why' end
  if to == 'working' and o.as and o.as ~= '' and cur.friend ~= '' and cur.friend ~= o.as then
    return 'OWNER task:' .. id .. ' is ' .. cur.friend .. "'s, not " .. o.as .. "'s"
  end
  return nil
end

-- TK.move(id, to, o): the one move. Returns nil and {from, to, xid} when
-- moved (to == from with nothing to change is a no-op: info.same, which
-- writes only o.fields), else the refusal; a refusal writes nothing but the
-- one-time adoption of a record that predates the where field. With
-- o.dry it writes nothing at all and returns nil when the move would be
-- made (NS.task.check: a batch checks every row before it moves any).
function TK.move(id, to, o)
  o = o or {}
  if type(id) ~= 'string' or id == '' or string.find(id, '%s') then return 'BADID ' .. TK.str(id) end
  if TK.card_id(id) then return 'BADID ' .. id .. ' is a card: ns_card_move' end
  if not TK.IS[to] then return 'WHERE ' .. TK.str(to) end
  local fields = o.fields or {}
  for i = 1, #fields, 2 do
    if TK.POINTER[fields[i]] then return 'FIELD ' .. fields[i] .. ' is the pointer' end
  end
  local cur = TK.read(id)
  if not cur then return 'NOTASK task:' .. id end
  local adopted = false
  if not cur.placed then
    local err, w, k = TK.adopt(id, cur, o, o.dry)
    if err then return err end
    if o.dry then
      cur.where, cur.ok, adopted = w, k, true
    else
      cur = TK.read(id)
    end
  end
  local nxt = { where = to, stream = cur.stream, friend = cur.friend }
  if o.stream ~= nil then nxt.stream = o.stream end
  if o.friend ~= nil then nxt.friend = o.friend end
  if to == 'working' and nxt.friend == '' and o.as and o.as ~= '' then nxt.friend = o.as end
  if not TK.valid_stream(nxt.stream) then return 'STREAM bad name ' .. nxt.stream end
  local ok = '-'
  if to == 'landed' then ok = 'ok' end
  if to == 'done' then
    ok = o.ok or ''
    if ok == '' and cur.where == 'done' then ok = cur.ok end
  end
  local err = TK.edge(id, cur, nxt, ok, o)
  if err then return err end
  local front = TK.str(o.front)
  -- ONE PLACE, before any write: a violation is a refusal, never a write.
  if not adopted then
    err = TK.verify(id, cur, { nxt.stream }, { nxt.friend })
    if err then return 'DRIFT ' .. err .. ' task:' .. id .. '; run nova-sprint task fsck' end
  end
  if o.dry then return nil end
  if cur.where == to and nxt.stream == cur.stream and nxt.friend == cur.friend and front ~= '1' and ok == cur.ok then
    if #fields > 0 then redis.call('HSET', 'task:' .. id, unpack(fields)) end
    return nil, { from = to, to = to, same = true }
  end

  local created = cur.created or cm_now()
  local old, new = TK.views(cur), TK.views(nxt)
  local was, keep = {}, {}
  for _, k in ipairs(old) do was[k] = true end
  for _, k in ipairs(new) do keep[k] = true end
  for _, k in ipairs(old) do
    if not keep[k] then redis.call('ZREM', k, id) end
  end
  for _, k in ipairs(new) do
    if not was[k] then redis.call('ZADD', k, created, id) end
  end
  if nxt.stream ~= cur.stream then TK.register(nxt.stream) end
  local at = cm_now()
  local S = TK.sprint(cur, o)
  if front == '' then front = cur.front end
  local lh, clear = TK.legacy(id, S, cur, nxt, front, at)
  local h = { 'task:' .. id, 'where', to, 'where_ok', ok, 'state', TK.STATE[to] }
  local function put(k, v)
    h[#h + 1] = k
    h[#h + 1] = v
  end
  if to ~= cur.where then
    put('where_at', tostring(at))
    put('state_at', tostring(at))
  end
  if cur.created_at ~= tostring(created) then put('created_at', string.format('%.0f', created)) end
  if nxt.stream ~= cur.stream or TK.str(redis.call('HGET', 'task:' .. id, 'stream')) ~= nxt.stream then
    put('stream', nxt.stream)
  end
  if nxt.friend ~= cur.friend or cur.owner ~= nxt.friend then
    put('friend', nxt.friend)
    put('owner', nxt.friend)
  end
  if cur.sprint == '' and S ~= '' then put('sprint', S) end
  if TK.str(o.why) ~= '' then put('why', o.why) end
  if TK.str(o.front) ~= '' then put('front', o.front) end
  if to == 'working' and cur.where ~= 'working' then
    put('leased_at', tostring(at))
    put('lease_until', tostring(at + TK.LEASE))
  end
  if to == 'done' and (cur.where ~= 'done' or ok ~= cur.ok) then
    put('done_at', tostring(at))
    if ok == 'fail' then put('cancelled', '1') end
  end
  if to == 'landed' and cur.where ~= 'landed' then
    put('landed_at', tostring(at))
    put('merge_sha', o.sha)
  end
  for _, v in ipairs(lh) do h[#h + 1] = v end
  for i = 1, #fields do h[#h + 1] = fields[i] end
  redis.call('HSET', unpack(h))
  if clear then redis.call('HDEL', 'task:' .. id, 'queue', 'xid') end
  if cur.where == 'working' and to ~= 'working' then redis.call('HDEL', 'task:' .. id, 'lease_until') end
  -- After: the new views hold the id and the dropped ones do not.
  for _, k in ipairs(new) do
    if not redis.call('ZSCORE', k, id) then return 'DRIFT-AFTER unlinked ' .. k .. ' task:' .. id end
  end
  for _, k in ipairs(old) do
    if not keep[k] and redis.call('ZSCORE', k, id) then return 'DRIFT-AFTER twice ' .. k .. ' task:' .. id end
  end
  local from, dest = cur.where, to
  if from == 'done' then from = 'done/' .. cur.ok end
  if to == 'done' then dest = 'done/' .. ok end
  local log = { 'id', id, 'stream', nxt.stream, 'from', from, 'to', dest, 'by', TK.str(o.by), 'why', TK.str(o.why),
    'at', tostring(at) }
  if nxt.stream ~= cur.stream then
    log[#log + 1] = 'from_stream'
    log[#log + 1] = cur.stream
  end
  if nxt.friend ~= cur.friend then
    log[#log + 1] = 'friend'
    log[#log + 1] = nxt.friend
  end
  redis.call('XADD', 'ws:log', 'MAXLEN', '~', TK.LOG_MAX, '*', unpack(log))
  local xid = ''
  if #lh > 0 then xid = lh[4] end
  return nil, { from = cur.where, to = to, xid = xid }
end

-- TK.create(id, fields, o): a new record (where null) and its first move to
-- o.where (waiting, else ready). fields are HSET pairs (never a pointer
-- field); o.stream (else the title's STREAM: prefix), o.friend and o.sprint
-- are its dimensions.
function TK.create(id, fields, o)
  o = o or {}
  if type(id) ~= 'string' or id == '' or string.find(id, '%s') then return 'BADID ' .. TK.str(id) end
  if TK.card_id(id) then return 'BADID ' .. id .. ' is a card: card push' end
  if redis.call('EXISTS', 'task:' .. id) == 1 then return 'EXISTS task:' .. id end
  local title = ''
  for i = 1, #fields, 2 do
    if TK.POINTER[fields[i]] then return 'FIELD ' .. fields[i] .. ' is the pointer' end
    if fields[i] == 'title' then title = fields[i + 1] end
  end
  local stream = TK.stream_of(o.stream, title)
  if not TK.valid_stream(stream) then return 'STREAM bad name ' .. stream end
  local where = o.where or 'waiting'
  if where ~= 'waiting' and where ~= 'ready' then return 'OFFGRAPH a new task starts in waiting or ready' end
  local h = { 'task:' .. id }
  for i = 1, #fields do h[#h + 1] = fields[i] end
  for _, kv in ipairs({ { 'where', '' }, { 'where_ok', '-' }, { 'state', '' }, { 'stream', stream },
    { 'friend', TK.str(o.friend) }, { 'owner', TK.str(o.friend) }, { 'sprint', TK.str(o.sprint) },
    { 'created_at', tostring(o.created or cm_now()) } }) do
    h[#h + 1] = kv[1]
    h[#h + 1] = kv[2]
  end
  redis.call('HSET', unpack(h))
  TK.register(stream)
  return TK.move(id, where, { by = o.by, why = o.why or 'push', front = o.front, sprint = o.sprint })
end

-- TK.derive: the place a record's own facts imply, for adoption and
-- migrate: its where field when it has one; else the one ws set of its
-- stream that holds it; else the friend-queue shapes, in bin/friend-queue's
-- own order: q:waiting or q:blocked -> waiting; closed or cancelled -> done
-- (fail when cancelled); the friend's idx set (working, open -> ready,
-- closed -> done: the index wins over a hash a crashed take left open);
-- else the state (open or ready -> ready, blocked -> waiting, a ws word ->
-- itself). Returns where, ok, or nil and why.
function TK.derive(id, p)
  if p.placed then return p.where, p.ok end
  if p.stream ~= '' then
    local found
    for _, w in ipairs(TK.WHERE) do
      if redis.call('ZSCORE', 'ws:' .. p.stream .. ':' .. w, id) then
        if found then return nil, 'twice ws:' .. p.stream .. ':' .. found .. ' and :' .. w end
        found = w
      end
    end
    if found == 'done' then
      if p.cancelled == '1' then return 'done', 'fail' end
      return 'done', 'ok'
    end
    if found == 'landed' then return 'landed', 'ok' end
    if found then return found, '-' end
  end
  local st = p.state
  if redis.call('ZSCORE', 'q:waiting', id) or redis.call('ZSCORE', 'q:blocked', id) then return 'waiting', '-' end
  if p.cancelled == '1' or st == 'cancelled' then return 'done', 'fail' end
  if st == 'closed' then return 'done', 'ok' end
  local S = TK.sprint(p, nil)
  if S ~= '' and p.friend ~= '' then
    local ix = 'sprint:' .. S .. ':idx:' .. p.friend .. ':'
    if redis.call('SISMEMBER', ix .. 'working', id) == 1 then return 'working', '-' end
    if redis.call('SISMEMBER', ix .. 'open', id) == 1 then return 'ready', '-' end
    if redis.call('SISMEMBER', ix .. 'closed', id) == 1 then return 'done', 'ok' end
  end
  if st == 'open' or st == 'ready' then return 'ready', '-' end
  if st == 'blocked' then return 'waiting', '-' end
  if st == 'landed' then return 'landed', 'ok' end
  if TK.IS[st] and st ~= 'done' then return st, '-' end
  return '', '-'
end

-- TK.adopt links a record that predates the where field (a friend-queue
-- push, a pre-model ws move) at the place its facts imply, adding the views
-- it lacks. It refuses when any other set of its stream or friend holds it:
-- that is drift for task migrate, not a guess. dry writes nothing and
-- returns nil, where, ok.
function TK.adopt(id, p, o, dry)
  local w, ok = TK.derive(id, p)
  if not w then return 'DRIFT ' .. ok .. ' task:' .. id .. '; run nova-sprint task migrate' end
  local q = { where = w, ok = ok, stream = p.stream, friend = p.friend }
  local mine = {}
  for _, k in ipairs(TK.views(q)) do mine[k] = true end
  for _, w2 in ipairs(TK.WHERE) do
    for _, k in ipairs(TK.views({ where = w2, stream = p.stream, friend = p.friend })) do
      if not mine[k] and redis.call('ZSCORE', k, id) then
        return 'DRIFT twice ' .. k .. ' task:' .. id .. '; run nova-sprint task migrate'
      end
    end
  end
  if dry then return nil, w, ok end
  local created = p.created or cm_now()
  for _, k in ipairs(TK.views(q)) do redis.call('ZADD', k, 'NX', created, id) end
  TK.register(p.stream)
  redis.call('HSET', 'task:' .. id, 'where', w, 'where_ok', ok, 'stream', p.stream, 'friend', p.friend,
    'owner', p.friend, 'created_at', string.format('%.0f', created))
  local S = TK.sprint(p, o)
  if S ~= '' then redis.call('SADD', 'sprint:' .. S .. ':tasks', id) end
  return nil
end

-- TK.last_beat: the last sign a holder lives, ms: the lease less its span,
-- the beat, the take, the move into working; 0 when there is none.
function TK.last_beat(p)
  local last = 0
  local lease = tonumber(p.lease_until)
  if lease then last = lease - TK.LEASE end
  for _, v in ipairs({ p.beat_at, p.leased_at, p.where_at }) do
    local ms = TK.ms(v)
    if ms and ms > last then last = ms end
  end
  return last
end

-- TK.place(id, where, ok, o): task migrate's writer, once: the record at
-- where/ok (nil where: TK.derive), o.stream when the record names none; the
-- id leaves every other set of every stream in ws:names and every friend in
-- friends, joins its views, and the legacy shapes follow. Returns the where
-- placed, or nil and why it was skipped.
function TK.place(id, where, ok, o)
  o = o or {}
  if TK.card_id(id) then return nil, 'card' end
  local p = TK.read(id)
  if not p then return nil, 'norecord' end
  if p.kind == '' and p.title == '' and p.state == '' and p.ref == '' and not p.placed then return nil, 'notatask' end
  if p.stream == '' and TK.str(o.stream) ~= '' then p.stream = o.stream end
  if not TK.valid_stream(p.stream) then return nil, 'badstream' end
  if not where or where == '' then
    local w, k = TK.derive(id, { placed = false, stream = p.stream, state = p.state, cancelled = p.cancelled,
      friend = p.friend, sprint = TK.sprint(p, o), where = '' })
    if not w then
      -- in two sets: the furthest along the graph wins
      local rank = { parked = 1, waiting = 1, ready = 2, working = 3, merging = 4, done = 5, landed = 6 }
      w, k = nil, '-'
      for _, x in ipairs(TK.WHERE) do
        if p.stream ~= '' and redis.call('ZSCORE', 'ws:' .. p.stream .. ':' .. x, id) and
            (not w or rank[x] > rank[w]) then
          w = x
        end
      end
      if w == 'done' then k = 'ok' end
      if w == 'landed' then k = 'ok' end
    end
    if p.placed and p.where ~= '' then w, k = p.where, p.ok end
    where, ok = w or '', k
  end
  if where ~= '' and not TK.IS[where] then return nil, 'where-' .. where end
  local now = cm_now()
  if where == 'working' and now - TK.last_beat(p) > TK.STALE then
    where = 'ready'
    o.why = 'lease lapsed (migrate: no beat in 10 min)'
  end
  if where == 'landed' then ok = 'ok' end
  if where == 'done' and ok ~= 'fail' then ok = 'ok' end
  if where ~= 'done' and where ~= 'landed' then ok = '-' end
  local created = p.created or cm_now()
  local target = {}
  for _, k in ipairs(TK.views({ where = where, stream = p.stream, friend = p.friend })) do target[k] = true end
  local streams = redis.call('SMEMBERS', 'ws:names')
  streams[#streams + 1] = p.stream
  local friends = redis.call('SMEMBERS', 'friends')
  friends[#friends + 1] = p.friend
  if p.owner ~= '' then friends[#friends + 1] = p.owner end
  for _, w in ipairs(TK.WHERE) do
    for _, s in ipairs(streams) do
      local k = 'ws:' .. s .. ':' .. w
      if s ~= '' and not target[k] then redis.call('ZREM', k, id) end
    end
    for _, f in ipairs(friends) do
      local k = 'friend:' .. f .. ':cards:' .. w
      if f ~= '' and not target[k] then redis.call('ZREM', k, id) end
    end
  end
  for k in pairs(target) do redis.call('ZADD', k, created, id) end
  TK.register(p.stream)
  local at = cm_now()
  local S = TK.sprint(p, o)
  local cur = { where = '', friend = p.friend, queue = p.queue, xid = p.xid }
  if p.placed then cur.where = p.where end
  local lh, clear = TK.legacy(id, S, cur, { where = where, friend = p.friend }, '0', at)
  local h = { 'task:' .. id, 'where', where, 'where_ok', ok, 'where_at', tostring(at), 'state', TK.STATE[where] or '',
    'stream', p.stream, 'friend', p.friend, 'owner', p.friend, 'created_at', string.format('%.0f', created),
    'migrated_at', tostring(at) }
  if p.sprint == '' and S ~= '' then
    h[#h + 1] = 'sprint'
    h[#h + 1] = S
  end
  if where == 'done' and ok == 'fail' then
    h[#h + 1] = 'cancelled'
    h[#h + 1] = '1'
  end
  if TK.str(o.why) ~= '' then
    h[#h + 1] = 'why'
    h[#h + 1] = o.why
  end
  if where == 'working' and not tonumber(p.lease_until) then
    h[#h + 1] = 'lease_until'
    h[#h + 1] = tostring(TK.last_beat(p) + TK.LEASE)
  end
  for _, v in ipairs(lh) do h[#h + 1] = v end
  redis.call('HSET', unpack(h))
  if clear then redis.call('HDEL', 'task:' .. id, 'queue', 'xid') end
  if where ~= 'working' then redis.call('HDEL', 'task:' .. id, 'lease_until') end
  if cur.where ~= where then
    redis.call('XADD', 'ws:log', 'MAXLEN', '~', TK.LOG_MAX, '*', 'id', id, 'stream', p.stream, 'from', cur.where,
      'to', where, 'by', TK.str(o.by), 'why', TK.str(o.why) ~= '' and o.why or 'migrate', 'at', tostring(at))
  end
  return where
end

-- TK.fsck(S): both directions, read-only. Every id in a ws:<stream>:<where>
-- set (streams in ws:names and ws:order) or a friend:<f>:cards:<where> set
-- (friends in friends) that is not a card has a record whose pointer names
-- that set and is scored by its created_at; every record among those ids and
-- the roster sprint:<S>:tasks with a where is in exactly its views, its
-- state mirrors where, and its legacy idx set (sprint:<S>:idx:<friend>:*)
-- agrees; a roster record with no where field is unplaced. Returns FSCK S
-- tasks null waiting ready working merging landed done parked unplaced drift,
-- then up to 100 drift lines.
function TK.fsck(S)
  local drift, lines = 0, {}
  local function note(s)
    drift = drift + 1
    if #lines < 100 then lines[#lines + 1] = s end
  end
  local ids, seen = {}, {}
  local function want(id)
    if not seen[id] then
      seen[id] = true
      ids[#ids + 1] = id
    end
  end
  local streams, sseen = {}, {}
  for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
    if not sseen[s] then
      sseen[s] = true
      streams[#streams + 1] = s
    end
  end
  for _, s in ipairs(redis.call('SMEMBERS', 'ws:names')) do
    if not sseen[s] then
      sseen[s] = true
      streams[#streams + 1] = s
    end
  end
  local friends = redis.call('SMEMBERS', 'friends')
  local recs = {}
  local function rec(id)
    if recs[id] == nil then recs[id] = TK.read(id) or false end
    return recs[id]
  end
  local function sweep(k, dim, name, w)
    local rows = redis.call('ZRANGE', k, 0, -1, 'WITHSCORES')
    for i = 1, #rows, 2 do
      local id = rows[i]
      if not TK.card_id(id) then
        want(id)
        local p = rec(id)
        if not p then
          note('gone ' .. k .. ' ' .. id)
        elseif p[dim] ~= name or p.where ~= w then
          note('stray ' .. k .. ' ' .. id .. ' (record: ' .. dim .. '=' .. p[dim] .. ' where=' .. p.where .. ')')
        elseif p.created and tonumber(rows[i + 1]) ~= p.created then
          note('score ' .. k .. ' ' .. id)
        end
      end
    end
  end
  for _, s in ipairs(streams) do
    for _, w in ipairs(TK.WHERE) do sweep('ws:' .. s .. ':' .. w, 'stream', s, w) end
  end
  for _, f in ipairs(friends) do
    for _, w in ipairs(TK.WHERE) do sweep('friend:' .. f .. ':cards:' .. w, 'friend', f, w) end
  end
  for _, id in ipairs(redis.call('SMEMBERS', 'sprint:' .. S .. ':tasks')) do want(id) end
  local n = { null = 0, unplaced = 0 }
  for _, w in ipairs(TK.WHERE) do n[w] = 0 end
  local total = 0
  for _, id in ipairs(ids) do
    local p = rec(id)
    if p then
      total = total + 1
      if not p.placed then
        n.unplaced = n.unplaced + 1
        note('unplaced task:' .. id .. ' (no where; run nova-sprint task migrate)')
      elseif p.where == '' then
        n.null = n.null + 1
      elseif not TK.IS[p.where] then
        note('where task:' .. id .. ' ' .. p.where)
      else
        n[p.where] = n[p.where] + 1
        for _, k in ipairs(TK.views(p)) do
          if not redis.call('ZSCORE', k, id) then note('unlinked ' .. k .. ' ' .. id) end
        end
        if p.state ~= TK.STATE[p.where] then
          note('state task:' .. id .. ' state=' .. p.state .. ' where=' .. p.where)
        end
        local LS = TK.sprint(p, { sprint = S })
        if p.friend ~= '' and LS ~= '' then
          local ix = 'sprint:' .. LS .. ':idx:' .. p.friend .. ':'
          for _, st in ipairs({ 'open', 'working', 'closed' }) do
            local has = redis.call('SISMEMBER', ix .. st, id) == 1
            if has ~= (TK.IDX[p.where] == st) then
              note((has and 'stray ' or 'unindexed ') .. ix .. st .. ' ' .. id)
            end
          end
        end
      end
    end
  end
  local out = { 'FSCK', S, tostring(total), tostring(n.null) }
  for _, w in ipairs(TK.WHERE) do out[#out + 1] = tostring(n[w]) end
  out[#out + 1] = tostring(n.unplaced)
  out[#out + 1] = tostring(drift)
  for _, l in ipairs(lines) do out[#out + 1] = l end
  return out
end

-- TK.opts reads the k v pairs after the fixed args: ok, friend, stream,
-- sha, sprint, front, as are options; any other k is a record field.
function TK.opts(args, from, o)
  local known = { ok = true, friend = true, stream = true, sha = true, sprint = true, front = true, as = true }
  o.fields = o.fields or {}
  for i = from, #args - 1, 2 do
    local k, v = args[i], args[i + 1]
    if known[k] then
      if v ~= '' or k == 'friend' or k == 'stream' then o[k] = v end
    else
      o.fields[#o.fields + 1] = k
      o.fields[#o.fields + 1] = v
    end
  end
  return o
end

function TK.reply(err, info)
  if err then return 'REFUSED ' .. err end
  if info.same then return 'SAME ' .. info.to end
  return 'MOVED ' .. (info.from == '' and '-' or info.from) .. ' ' .. info.to
end

-- ns_tcard_move(id, to, by, why[, k, v]...) -> MOVED <from> <to> |
-- SAME <where> | REFUSED <why>. The keys: ok friend stream sha sprint front
-- as, else a record field.
redis.register_function('ns_tcard_move', function(keys, args)
  local o = TK.opts(args, 5, { by = args[3], why = args[4] })
  return TK.reply(TK.move(args[1], args[2], o))
end)

-- ns_tcard_push(id, where, by, why[, k, v]...) -> PUSHED <where> <xid> |
-- REFUSED <why>. where is waiting or ready; stream, friend, sprint and front
-- are options, every other k a record field (kind, ref, origin, title, head,
-- pr, repo, ...).
redis.register_function('ns_tcard_push', function(keys, args)
  local o = TK.opts(args, 5, { by = args[3], why = args[4] })
  o.where = args[2]
  local fields = o.fields
  o.fields = nil
  local err, info = TK.create(args[1], fields, o)
  if err then return 'REFUSED ' .. err end
  return 'PUSHED ' .. info.to .. ' ' .. (info.xid ~= '' and info.xid or '-')
end)

-- ns_tcard_take(as, n, by, id...) -> TAKEN k then the ids, or REFUSED <why>
-- for a named id. No id: the n oldest of friend:<as>:cards:ready.
redis.register_function('ns_tcard_take', function(keys, args)
  local as, n, by = args[1], tonumber(args[2]) or 1, args[3]
  local ids = {}
  for i = 4, #args do ids[#ids + 1] = args[i] end
  local named = #ids > 0
  if not named then ids = redis.call('ZRANGE', 'friend:' .. as .. ':cards:ready', 0, n - 1) end
  local out = { 'TAKEN', '0' }
  for _, id in ipairs(ids) do
    local err = TK.move(id, 'working', { by = by, why = 'take', as = as })
    if err and named then return { 'REFUSED', err } end
    if not err then out[#out + 1] = id end
  end
  out[2] = tostring(#out - 2)
  return out
end)

-- ns_tcard_done(id, by, evidence[, pr]) -> MOVED working merging|done |
-- REFUSED <why>: working -> merging when the task names a PR (its pr field,
-- or pr given), else working -> done/ok.
redis.register_function('ns_tcard_done', function(keys, args)
  local id, by, evidence, pr = args[1], args[2], args[3] or '', args[4] or ''
  local fields = { 'evidence', evidence }
  if pr ~= '' then
    fields[#fields + 1] = 'pr'
    fields[#fields + 1] = pr
  else
    pr = TK.str(redis.call('HGET', 'task:' .. id, 'pr'))
  end
  local to, ok = 'done', 'ok'
  if pr ~= '' and pr ~= '0' then to, ok = 'merging', nil end
  return TK.reply(TK.move(id, to, { by = by, why = 'done', ok = ok, fields = fields }))
end)

-- ns_tcard_beat(id, as) -> BEAT <lease_until> | REFUSED <why>: the holder
-- of a working task renews its lease (the child's beat, every 60 s).
redis.register_function('ns_tcard_beat', function(keys, args)
  local id, as = args[1], args[2] or ''
  local p = TK.read(id)
  if not p then return 'REFUSED NOTASK task:' .. id end
  if p.where ~= 'working' then return 'REFUSED NOTWORKING task:' .. id .. ' is ' .. p.where end
  if as == '' or p.friend ~= as then return 'REFUSED OWNER task:' .. id .. ' is ' .. p.friend .. "'s" end
  local at = cm_now()
  redis.call('HSET', 'task:' .. id, 'lease_until', tostring(at + TK.LEASE), 'beat_at', tostring(at))
  return 'BEAT ' .. tostring(at + TK.LEASE)
end)

-- ns_tcard_expire(by[, friend...]) -> EXPIRED n then the ids: every working
-- task of the named friends (else every member of friends) whose lease
-- lapsed goes back to ready, why=lease lapsed. O(the working sets).
redis.register_function('ns_tcard_expire', function(keys, args)
  local by = args[1] or ''
  local friends = {}
  for i = 2, #args do friends[#friends + 1] = args[i] end
  if #friends == 0 then friends = redis.call('SMEMBERS', 'friends') end
  local now = cm_now()
  local out = { 'EXPIRED', '0' }
  for _, f in ipairs(friends) do
    for _, id in ipairs(redis.call('ZRANGE', 'friend:' .. f .. ':cards:working', 0, -1)) do
      if not TK.card_id(id) then
        local p = TK.read(id)
        local lease = p and tonumber(p.lease_until)
        if p and not lease then lease = TK.last_beat(p) + TK.LEASE end
        if p and lease < now and not TK.move(id, 'ready', { by = by, why = 'lease lapsed' }) then
          out[#out + 1] = id
        end
      end
    end
  end
  out[2] = tostring(#out - 2)
  return out
end)

-- ns_tcard_place(by, sprint, [id, where, ok, stream]...) -> per id: the
-- where placed, or 'skip <why>'. task migrate's one-time writer.
redis.register_function('ns_tcard_place', function(keys, args)
  local by, S = args[1], args[2]
  local out = {}
  for i = 3, #args - 3, 4 do
    local w, why = TK.place(args[i], args[i + 1], args[i + 2], { by = by, sprint = S, stream = args[i + 3] })
    if w then out[#out + 1] = w else out[#out + 1] = 'skip ' .. why end
  end
  return out
end)

-- ns_tcard_fsck(S): TK.fsck, read-only.
redis.register_function({ function_name = 'ns_tcard_fsck', flags = { 'no-writes' },
  callback = function(keys, args) return TK.fsck(args[1] or '') end })

NS.task = { move = TK.move, create = TK.create, place = TK.place, read = TK.read, stream_of = TK.stream_of,
  ms = TK.ms, where = TK.IS,
  check = function(id, to, o)
    o = o or {}
    o.dry = true
    local err = TK.move(id, to, o)
    o.dry = nil
    return err
  end }
