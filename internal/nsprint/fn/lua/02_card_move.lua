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
-- ready, working, done, parked) with where_ok (ok|fail|abstain once done, -
-- before; abstain is a model's ABSTAIN, nova-tools#3919),
-- plus the dimensions that apply to it: bench (dealt bench or pin; _pool when
-- empty), stream (the card's STREAM: line) and owner (a friend holding it).
-- Each dimension is a view of the one place, a ZSET of card ids:
--
--   bench:<bench|_pool>:cards:<where>, and bench:<b>:cards:ok|fail|abstain while done
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
-- nova-sprint card fsck walks both directions (ns_card_fsck, ns_card_repair)
-- and every table set's members (ns_card_members, ns_card_members_repair: a
-- member that is not the id of a record is MEMBER-NOT-A-CARD, #4054).

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
  if p.stream ~= '' then v[#v + 1] = { t = 'z', k = 'ws:' .. p.stream .. ':' .. p.where, m = id, s = p.stream } end
  if p.owner ~= '' then v[#v + 1] = { t = 'z', k = 'friend:' .. p.owner .. ':cards:' .. p.where, m = id } end
  if p.where == 'ready' then v[#v + 1] = { t = 'l', k = 's:' .. S .. ':pool', m = label } end
  if p.where == 'waiting' then v[#v + 1] = { t = 's', k = 's:' .. S .. ':waiting', m = label } end
  return v
end

local function cm_has(e)
  if e.t == 's' then return redis.call('SISMEMBER', e.k, e.m) == 1 end
  return redis.call('ZSCORE', e.k, e.m) ~= false
end

-- A table set's member is a record (nova-tools#4054). Found 2026-09-25: a
-- friend's ready set held 32 members of the shape '<file path>:task:<id>' (a
-- grep with filenames in an import pipe), counted on the friend table for
-- hours, and no fsck flagged them because they are not records at all. A
-- member of a set that drives a table names an existing record: a card id
-- s:<S>:card:<label> whose record exists, or a task id <id> whose task:<id>
-- exists (rowan-new specs/ws-index.md, "Tasks are cards"). cm_record returns
-- that record's key, or nil.
local function cm_record(m)
  if type(m) ~= 'string' or m == '' then return nil end
  if cm_split(m) then
    if redis.call('EXISTS', m) == 1 then return m end
    return nil
  end
  if redis.call('EXISTS', 'task:' .. m) == 1 then return 'task:' .. m end
  return nil
end

-- PROBES NEVER RIDE THE CONSUMER SETS (nova-tools#4237). Seen 2026-09-26
-- 8:45-9:20 AM ET: after sprint clear zeroed both tables, a fleet roll ran
-- its pro+flash probe per bench on the card model and retired the probes
-- into bench:<b>:cards:ok and :fail, so every bench row showed done 2 again.
-- Glenn: "clearing the table also means the consume table is cleared. all
-- zeros except for status and load." A probe is a build check, not consumer
-- work: its result goes on the bench beat (bench:<b>:beat probe, written by
-- fleet build), and a record that names a probe in its probe field (fleet,
-- quack, route, lineup, ...) is a kind the consumer sets refuse.
--
-- cm_consumer_set(k): k is a consumer set, <bench|friend>:<name>:cards:<set>
-- (bench:_pool included: a probe is never dealt from there either).
-- cm_probe(m): the probe m's record names, else nil: a card id reads its
-- s:<S>:card record, a lease member <S>/<id>/<n> reads task:<id>, any other
-- id (a task, a copy <p>~<n>) task:<id>. A copy is never cut from a probe
-- (TM.cut), so a copy names one only when its own record was written so.
-- cm_probe_refused(k, m): the PROBE refusal when k is a consumer set and m a
-- probe, else nil. The moves call it before any write (card_create,
-- card_move, card_add, TK.create, TK.move, TM.cut), so a probe is refused
-- with its reason and nothing written.
-- cm_zadd(k, ...): the ONE ZADD into a table set in this file (the class
-- test in internal/nsprint/fn holds it). It raises the same refusal when a
-- probe would enter a consumer set: the last guard, never the path.
local function cm_consumer_set(k)
  if type(k) ~= 'string' then return false end
  local kind = string.match(k, '^(%l+):[^:]+:cards:%l+$')
  return kind == 'bench' or kind == 'friend'
end

local function cm_probe_of(key)
  local v = redis.pcall('HGET', key, 'probe')
  if type(v) == 'string' and v ~= '' then return v end
  return nil
end

local function cm_probe(m)
  if type(m) ~= 'string' or m == '' then return nil end
  if cm_split(m) then return cm_probe_of(m) end
  return cm_probe_of('task:' .. (string.match(m, '^[^/]+/(.+)/%d+$') or m))
end

local function cm_probe_refused(k, m)
  if not cm_consumer_set(k) then return nil end
  local v = cm_probe(m)
  if not v then return nil end
  return 'PROBE ' .. tostring(m) .. ' is a probe (probe=' .. v .. '); ' .. k ..
    ' holds consumer work only: a probe result goes on bench:<b>:beat probe (nova-tools#4237)'
end

local function cm_zadd(k, ...)
  local args = { ... }
  local why = cm_probe_refused(k, args[#args])
  if why then error(why) end
  return redis.call('ZADD', k, ...)
end

-- card_add(k, score, m): the one add into a table set (ws:<stream>:<where>,
-- bench:<b>:cards:*, friend:<f>:cards:*) outside the task move. card_move's
-- views add through it here, and another file adds as NS.card.add; the task
-- moves of 03_task_event.lua, deal_friend.lua and route_duty.lua go through
-- NS.task.move (TK, below), which adds only the views of the task:<id> record
-- it has read. It refuses a member that is not the id of an existing record:
-- NOTACARD <set> <member>, and nothing is written.
local function card_add(k, score, m)
  if not cm_record(m) then return 'NOTACARD ' .. k .. ' ' .. tostring(m) end
  local why = cm_probe_refused(k, m)
  if why then return why end
  cm_zadd(k, score, m)
  return nil
end

-- cm_register(stream): the one registration of a work stream, for the card
-- path and the task path alike (TK.register and ws.lua's W.place call it):
-- the stream is in ws:names and ranked last in ws:order when it has no rank.
-- Found 2026-09-26 on the live store: card push --dir of 32 cards with
-- STREAM: ci wrote ws:ci:ready and ws:ci:waiting but never ws:names or
-- ws:order, so stream ls, stream order, scope keep and the stream table did
-- not know the stream. Every ws:<stream>:<where> add of a card (cm_add of a
-- view that names its stream) registers the stream first.
local function cm_register(stream)
  if type(stream) ~= 'string' or stream == '' then return end
  redis.call('SADD', 'ws:names', stream)
  if not redis.call('ZSCORE', 'ws:order', stream) then
    redis.call('ZADD', 'ws:order', redis.call('ZCARD', 'ws:order') + 1, stream)
  end
end

-- cm_registered(stream): the stream is in ws:names and ranked in ws:order.
local function cm_registered(stream)
  return redis.call('SISMEMBER', 'ws:names', stream) == 1 and redis.call('ZSCORE', 'ws:order', stream) ~= false
end

-- The pool's members are labels (not a table set of ids): added as they are.
-- A card-id view goes through card_add; a refusal there leaves the view
-- unlinked, which card_move's after-check names (DRIFT-AFTER). A stream view
-- (e.s) registers its stream first: one state machine with the task path.
local function cm_add(e, score)
  if e.s then cm_register(e.s) end
  if e.t == 's' then
    redis.call('SADD', e.k, e.m)
  elseif e.t == 'l' then
    redis.call('ZADD', e.k, score, e.m)
  else
    card_add(e.k, score, e.m)
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
    add('z', 'bench:' .. b .. ':cards:abstain', id)
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
-- becomes done/fail on a refused result and never returns to the pool;
-- done/abstain (#3919) never returns to the pool and becomes done/ok only by
-- a merge, like done/fail.
local function cm_edge(from, fok, to, tok, state)
  if from == '' then
    if to == 'waiting' then return nil end
    return 'OFFGRAPH a new card starts in waiting'
  end
  if to == 'done' then
    if from == 'done' then
      if fok ~= 'ok' and tok == 'ok' and state ~= 'landed' then return 'OFFGRAPH done/' .. fok .. ' -> done/ok' end
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
    elseif state == 'refused' or state == 'superseded' then
      ok = 'fail'
    elseif c[2] == 'ABSTAIN' then
      ok = 'abstain'
    elseif c[2] ~= 'DONE' then
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
-- keeps it), o.ok is ok|fail|abstain (entering done; nil keeps it), o.bench the new
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
    if ok ~= 'ok' and ok ~= 'fail' and ok ~= 'abstain' then return 'OUTCOME done needs ok, fail or abstain' end
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
  -- a probe never enters a consumer set (nova-tools#4237): refused before any write
  for _, e in ipairs(new) do
    local why = e.t == 'z' and cm_probe_refused(e.k, e.m)
    if why then return why end
  end
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
    -- every placed card has a bench view (_pool when undealt): a probe
    -- never becomes a card (nova-tools#4237)
    if fields[i] == 'probe' and fields[i + 1] and fields[i + 1] ~= '' then
      return 'PROBE ' .. id .. ' is a probe (probe=' .. tostring(fields[i + 1]) ..
        '); a card is consumer work only: a probe result goes on bench:<b>:beat probe (nova-tools#4237)'
    end
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

-- cm_lease_member(m, w): m is a lease member of a working set, <S>/<id>/<attempt>
-- (the one lease ledger, #3998; NS.moves.hold) or the bench form
-- <S>/<label>/<sha>/<attempt>. A path-shaped junk id (/private/...) starts
-- with a slash and never matches.
local function cm_lease_member(m, w)
  return w == 'working' and string.match(m, '^[^/]+/.+/%d+$') ~= nil
end

-- cm_evict(k, m, stream, w, by): remove a table-set member that is not a
-- record, with its one ws:log receipt (id, stream, from = the set's where,
-- to '', set, by, why MEMBER-NOT-A-CARD, at). The one remover of such a
-- member, for the sprint walk and the member walk alike.
local function cm_evict(k, m, stream, w, by)
  redis.call('ZREM', k, m)
  redis.call('XADD', 'ws:log', 'MAXLEN', '~', '200000', '*', 'id', m, 'stream', stream or '',
    'from', w or '', 'to', '', 'set', k, 'by', by or '', 'why', 'MEMBER-NOT-A-CARD', 'at', tostring(cm_now()))
end

-- fsck of one sprint in one call, both directions. write repairs: adopts
-- every record the sprint's state indexes, waiting set and pool name that
-- sprint:<S>:cards lacks, rewrites a pointer its fine state contradicts, adds
-- a missing link and removes a stray one. A null card (where empty: created,
-- not yet placed) is in no table set and is counted as null. A stream with
-- members of the sprint that is missing from ws:names or ws:order is
-- UNREGISTERED stream=<s> members=<n>; repair registers it. Returns FSCK S
-- cards null waiting ready working done parked ok fail drift fixed
-- registered, then up to 50 drift lines.
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

  local n = { null = 0, waiting = 0, ready = 0, working = 0, done = 0, parked = 0, ok = 0, fail = 0, abstain = 0 }
  local want, benches, streams, owners = {}, { _pool = true }, {}, {}
  -- streams[s] counts the sprint's placed cards of stream s: its
  -- ws:<s>:<where> members (a null card is in no set and counts 0)
  local registered = 0
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
          if c.stream ~= '' then streams[c.stream] = streams[c.stream] or 0 end
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
          if c.stream ~= '' then streams[c.stream] = (streams[c.stream] or 0) + 1 end
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

  -- A member with no record is not a stray card but no card at all:
  -- MEMBER-NOT-A-CARD, removed with its ws:log receipt (#4054).
  local function sweep(k, stream, w)
    for _, id in ipairs(redis.call('ZRANGE', k, 0, -1)) do
      -- a friend-queue lease member <S>/<id>/<attempt> in a working set
      -- (the one lease ledger, #3998) is not a card and not a stray
      if not cm_lease_member(id, w) and string.sub(id, 1, #prefix) == prefix and not (want[k] and want[k][id]) then
        local card = cm_record(id) ~= nil
        if card then note('stray ' .. k .. ' ' .. id) else note('MEMBER-NOT-A-CARD ' .. k .. ' ' .. id) end
        if write then
          if card then redis.call('ZREM', k, id) else cm_evict(k, id, stream, w, 'card-repair') end
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
    for _, w in ipairs(CM_WHERE) do sweep('bench:' .. b .. ':cards:' .. w, '', w) end
    sweep('bench:' .. b .. ':cards:ok', '', 'done')
    sweep('bench:' .. b .. ':cards:fail', '', 'done')
    sweep('bench:' .. b .. ':cards:abstain', '', 'done')
  end
  for s in pairs(streams) do
    for _, w in ipairs(CM_WHERE) do sweep('ws:' .. s .. ':' .. w, s, w) end
  end
  -- A stream with members of this sprint that is not in ws:names or not
  -- ranked in ws:order (2026-09-26: card push never registered it) is
  -- UNREGISTERED; repair registers it (cm_register) and counts it registered.
  local snames = {}
  for s, m in pairs(streams) do
    if m > 0 then snames[#snames + 1] = s end
  end
  table.sort(snames)
  for _, s in ipairs(snames) do
    if not cm_registered(s) then
      note('UNREGISTERED stream=' .. s .. ' members=' .. streams[s])
      if write then
        cm_register(s)
        registered = registered + 1
        fixed = fixed + 1
      end
    end
  end
  for f in pairs(owners) do
    for _, w in ipairs(CM_WHERE) do sweep('friend:' .. f .. ':cards:' .. w, '', w) end
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
    tostring(n.done), tostring(n.parked), tostring(n.ok), tostring(n.fail), tostring(drift), tostring(fixed),
    tostring(registered) }
  for _, l in ipairs(lines) do out[#out + 1] = l end
  return out
end

-- Every set that drives a table, each with the stream its receipt names:
-- ws:<stream>:<where> for every stream in ws:order or ws:names (the task
-- places review, merging and landed included), bench:<b>:cards:<where> and
-- :ok|fail|abstain for every registered bench and _pool, and
-- friend:<f>:cards:<where> for every member of friends. Read from the three
-- registries, never a SCAN; sorted, so a walk reads in one order.
local CM_TABLE_WHERE = { 'waiting', 'ready', 'working', 'review', 'merging', 'landed', 'done', 'parked' }

local function cm_table_sets()
  local out, seen = {}, {}
  local function add(k, stream, w)
    if not seen[k] then
      seen[k] = true
      out[#out + 1] = { k = k, s = stream, w = w }
    end
  end
  local streams = redis.call('ZRANGE', 'ws:order', 0, -1)
  for _, s in ipairs(redis.call('SMEMBERS', 'ws:names')) do streams[#streams + 1] = s end
  table.sort(streams)
  for _, s in ipairs(streams) do
    for _, w in ipairs(CM_TABLE_WHERE) do add('ws:' .. s .. ':' .. w, s, w) end
  end
  local benches = redis.call('SMEMBERS', 'benches')
  table.sort(benches)
  benches[#benches + 1] = '_pool'
  for _, b in ipairs(benches) do
    for _, w in ipairs(CM_WHERE) do add('bench:' .. b .. ':cards:' .. w, '', w) end
    for _, w in ipairs({ 'ok', 'fail', 'abstain' }) do add('bench:' .. b .. ':cards:' .. w, '', 'done') end
  end
  local friends = redis.call('SMEMBERS', 'friends')
  table.sort(friends)
  for _, f in ipairs(friends) do
    for _, w in ipairs(CM_TABLE_WHERE) do add('friend:' .. f .. ':cards:' .. w, '', w) end
  end
  return out
end

-- card_members(write, by): the walk of every table set's members, the
-- direction a per-sprint fsck cannot see (a member that is no sprint's card
-- is in no sprint's sweep). A member that is not the id of an existing
-- record (cm_record) is the violation MEMBER-NOT-A-CARD <set> <member>;
-- write removes it with its ws:log receipt (cm_evict). A set of the wrong type
-- is named WRONGTYPE <set> and never written. Returns MEMBERS sets members
-- bad removed, then up to 50 lines.
local function card_members(write, by)
  local sets, members, bad, removed, lines = 0, 0, 0, 0, {}
  for _, e in ipairs(cm_table_sets()) do
    local got = redis.pcall('ZRANGE', e.k, 0, -1)
    if type(got) == 'table' and got.err then
      bad = bad + 1
      if #lines < 50 then lines[#lines + 1] = 'WRONGTYPE ' .. e.k end
    else
      sets = sets + 1
      for _, m in ipairs(got) do
        members = members + 1
        -- a friend-queue lease member <S>/<id>/<attempt> in a working set is
        -- the one lease ledger's (#3998), not a card: never a violation here
        if cm_lease_member(m, e.w) then
          -- the one lease ledger's member, kept
        elseif not cm_record(m) then
          bad = bad + 1
          if #lines < 50 then lines[#lines + 1] = 'MEMBER-NOT-A-CARD ' .. e.k .. ' ' .. m end
          if write then
            cm_evict(e.k, m, e.s, e.w, by)
            removed = removed + 1
          end
        end
      end
    end
  end
  local out = { 'MEMBERS', tostring(sets), tostring(members), tostring(bad), tostring(removed) }
  for _, l in ipairs(lines) do out[#out + 1] = l end
  return out
end

-- cm_sprint_member(m, live): m names a card of a sprint in sprint:order
-- (live, a set of sprint names): a card id s:<S>:card:<label> listed in
-- sprint:<S>:cards, or a task id whose task:<id> record exists and names a
-- live sprint (or none: TK.sprint's first of sprint:order).
local function cm_sprint_member(m, live)
  local S = cm_split(m)
  if S then return live[S] == true and redis.call('ZSCORE', 'sprint:' .. S .. ':cards', m) ~= false end
  if type(m) ~= 'string' or m == '' then return false end
  local sp = redis.call('HGET', 'task:' .. m, 'sprint')
  if sp == false then return redis.call('EXISTS', 'task:' .. m) == 1 and next(live) ~= nil end
  if sp == '' then return next(live) ~= nil end
  return live[sp] == true
end

-- ws_orphans(): every ws:<stream>:<where> set (streams of ws:order and
-- ws:names, cm_table_sets) that is non-empty and whose members all belong
-- to no card of any sprint in sprint:order (cm_sprint_member; a lease
-- member is the lease ledger's and is skipped) is ORPHAN-SET key=<k>
-- members=<n>. Read-only: #4334 owns purging. Returns ORPHANS sets orphans,
-- then up to 50 lines.
local function ws_orphans()
  local live = {}
  for _, S in ipairs(redis.call('ZRANGE', 'sprint:order', 0, -1)) do live[S] = true end
  local sets, orphans, lines = 0, 0, {}
  for _, e in ipairs(cm_table_sets()) do
    if string.sub(e.k, 1, 3) == 'ws:' then
      local got = redis.pcall('ZRANGE', e.k, 0, -1)
      if type(got) == 'table' and not got.err then
        sets = sets + 1
        local n, owned = 0, false
        for _, m in ipairs(got) do
          if not cm_lease_member(m, e.w) then
            n = n + 1
            if cm_sprint_member(m, live) then
              owned = true
              break
            end
          end
        end
        if n > 0 and not owned then
          orphans = orphans + 1
          if #lines < 50 then lines[#lines + 1] = 'ORPHAN-SET key=' .. e.k .. ' members=' .. #got end
        end
      end
    end
  end
  local out = { 'ORPHANS', tostring(sets), tostring(orphans) }
  for _, l in ipairs(lines) do out[#out + 1] = l end
  return out
end

-- ns_ws_orphans(): ws_orphans, read-only (card fsck prints its lines).
redis.register_function({ function_name = 'ns_ws_orphans', flags = { 'no-writes' },
  callback = function(keys, args) return ws_orphans() end })

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

-- ns_card_members(): the member walk, read-only. ns_card_members_repair(by):
-- the same walk, removing every member that is not a record (by names the
-- receipt's actor: card fsck, fsck-duty).
redis.register_function({ function_name = 'ns_card_members', flags = { 'no-writes' },
  callback = function(keys, args) return card_members(false, '') end })
redis.register_function('ns_card_members_repair', function(keys, args) return card_members(true, args[1]) end)

-- card_purge(S): the one place a card record is deleted, and only for a
-- control sprint (control-<id>), whose whole run nova-sprint drain --control
-- tears down (#3442). A real sprint's cards never disappear: any other S is
-- refused before a write. Each card on the roster leaves every view its
-- pointer names and its state index (shared views such as bench:_pool:* or
-- ws:<stream>:* keep every other member), then its record and result records
-- go; last the roster and the moves stream. Returns the cards purged, or nil
-- and the refusal.
local function card_purge(S)
  if type(S) ~= 'string' or not string.match(S, '^control%-[a-z0-9-]+$') then
    return nil, 'purge ' .. tostring(S) .. ': only a control-<id> sprint is torn down'
  end
  local all = 'sprint:' .. S .. ':cards'
  local n = 0
  for _, id in ipairs(redis.call('ZRANGE', all, 0, -1)) do
    local IS, label = cm_split(id)
    local p = IS == S and cm_read(id) or nil
    if p then
      for _, e in ipairs(cm_views(S, label, id, p)) do cm_rem(e) end
      if p.state then redis.call('SREM', cm_idx(S, p.state), label) end
      local attempts = tonumber(redis.call('HGET', id, 'attempt')) or 0
      for a = 1, attempts do redis.call('DEL', id .. ':result:a' .. a) end
      -- the body the wrapper ran (push stores it, nova-tools#4101)
      local sha = redis.call('HGET', id, 'payload_sha')
      if sha and sha ~= '' then redis.call('DEL', 's:' .. S .. ':body:sha256:' .. sha) end
      redis.call('DEL', id, id .. ':receipt', id .. ':lines', id .. ':rejected')
      n = n + 1
    end
  end
  redis.call('DEL', all, 'sprint:' .. S .. ':moves')
  return n
end

NS.card = { move = card_move, create = card_create, purge = card_purge, add = card_add, record = cm_record,
  register = cm_register }

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
--   waiting -> working                             card deal (a copy is cut)
--   ready -> waiting | working | parked | done     block, take, park, cancel
--   working -> merging | done | landed             done (with a PR / not), a merge
--   working -> ready                              the lease lapsed (a why)
--   working -> waiting                             a copy given back (card cancel, assign --revoke; a why)
--   working -> review                             card end --ok with a PR (its read copy cut in the same call)
--   working | review -> review                    a copy's fail (#4072; o.review, TM only)
--   review -> waiting | ready | landed             review post --verdict recut | redeal | drop (o.verdict)
--   review -> working | review                    review post --verdict reassign:<consumer> (a copy cut)
--   review -> merging | working | waiting         a read's score: 8+ | under 8 (a fix copy) | no author
--   review -> done | landed                       cancel, a merge
--   merging -> landed | done                       a merge, a PR closed unmerged
--   parked -> waiting | ready | done               unpark, cancel
--   landed -> done (ok only)                       table clear
--   working -> waiting                             a durable wait (a why)
--   done/ok -> merging | landed                    the PR of a closed task (a why)
-- landed needs the merge sha; a done that does not come from working needs
-- a why, and so does working -> ready. A task no friend holds (a primary)
-- enters working or merging only when the table-moves code names its copy
-- (TK.unread): card deal cuts it, a copy's card end returns it.
-- A take (-> working) writes lease_until = now + TK.LEASE; the child renews
-- it with ns_tcard_beat every 60 s; ns_tcard_expire moves a working task
-- whose lease lapsed back to ready (why=lease lapsed) and unlinks from the
-- working set an id whose record is not that friend's working task (TK.reap,
-- #3892), so a friend's working set never holds a task nobody holds
-- (Glenn 08:15 AM: rowan working=65 with two children alive). A same-where call re-places the task: a new stream (any where), a
-- new friend or the front flag (waiting, ready, parked).
-- A Redis Function cannot call another, so later files reach it as NS.task:
--   NS.task.move(id, to, o)          -> nil, info | refusal
--   NS.task.create(id, fields, o)    -> nil, info | refusal
--   NS.task.place(id, where, ok, o)  -> placed where | nil, why (migrate only)
-- o: by, why, ok (ok|fail entering done), friend, stream, sha (landed),
-- sprint (the legacy idx sets' sprint when the record names none), front
-- ('1' requeues on q:<f>:front), as (a take: the friend taking), state (the
-- fine state to write; default the where's friend-queue word), qscore (the
-- sprint ready queue score; default from priority and front), fields (more
-- HSET pairs, never a pointer field). info: from, to, xid (the ready entry).
local TK = {
  WHERE = { 'waiting', 'ready', 'working', 'review', 'merging', 'landed', 'done', 'parked' },
  IS = { waiting = true, ready = true, working = true, review = true, merging = true, landed = true,
    done = true, parked = true },
  STATE = { waiting = 'waiting', ready = 'open', working = 'working', review = 'review',
    merging = 'merging', landed = 'landed', done = 'closed', parked = 'parked' },
  -- the where of every fine state a task record carries (the sprint store's
  -- words, #3206, and friend-queue's); cancelled is done/fail
  WHERE_OF = { open = 'ready', ready = 'ready', claimed = 'working', working = 'working', waiting = 'waiting',
    blocked = 'waiting', ['waiting-ci'] = 'waiting', closed = 'done', cancelled = 'done', reading = 'review',
    merging = 'merging', landed = 'landed', parked = 'parked', ['reconcile-required'] = 'waiting', review = 'review' },
  IDX = { ready = 'open', working = 'working', review = 'closed', merging = 'closed', landed = 'closed', done = 'closed' },
  GRAPH = {
    [''] = { waiting = true, ready = true },
    waiting = { ready = true, parked = true, done = true, working = true },
    ready = { waiting = true, working = true, parked = true, done = true },
    working = { merging = true, done = true, landed = true, ready = true, waiting = true, review = true },
    -- review is one state (Glenn 2026-09-26): a card whose work is done and
    -- is being judged, by readers scoring its PR (read copies out) or by
    -- the coordinator's typed verdict on a copy's fail (TM.review, #4072).
    -- A score leaves it for merging, a low read cuts a fix copy and stays,
    -- a verdict goes to waiting | ready | working (reassign) | landed (drop).
    review = { merging = true, working = true, waiting = true, ready = true, done = true, landed = true, review = true },
    merging = { landed = true, done = true },
    parked = { waiting = true, ready = true, done = true },
    landed = { done = true },
    done = { merging = true, landed = true },
  },
  REPLACE = { waiting = true, ready = true, parked = true },
  POINTER = { where = true, where_ok = true, where_at = true, state = true, state_at = true, stream = true,
    friend = true, owner = true, created_at = true, sprint = true, queue = true, xid = true, cancelled = true,
    lease_until = true, copy = true, primary = true, reads = true },
  FIELDS = { 'where', 'where_ok', 'stream', 'friend', 'owner', 'created_at', 'sprint', 'state', 'title',
    'queue', 'xid', 'front', 'cancelled', 'pr', 'kind', 'ref', 'lease_until', 'beat_at', 'leased_at', 'where_at',
    'priority', 'dest', 'claimed_at', 'token', 'copy', 'attempts', 'reads', 'head', 'author' },
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

-- TK.copy_id: a consumer copy's id, <primary id>~<n> (table moves, below);
-- its record is task:<copy id> and only TM moves it.
function TK.copy_id(id)
  return type(id) == 'string' and string.match(id, '^%S+~%d+$') ~= nil
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
-- The card path's own registration (cm_register): one state machine.
function TK.register(stream)
  cm_register(stream)
end

-- TK.legacy keeps the friend-queue shapes of one move from cur to nxt and
-- returns the HSET pairs it owes the record (queue, xid) and whether they
-- are cleared.
function TK.legacy(id, S, cur, nxt, front, at, prio)
  local h, clear = {}, false
  if S ~= '' then
    -- The sprint store's ready queues (#3206, read by the take, the ranker,
    -- width and redistribute): s:<S>:open:<friend>, or s:<S>:ready for a
    -- task no friend holds, scored by the deal priority (negative at the
    -- front). Legacy views like the idx sets: written here only.
    local function q(f)
      if f == '' then return 's:' .. S .. ':ready' end
      return 's:' .. S .. ':open:' .. f
    end
    if cur.where == 'ready' then redis.call('ZREM', q(cur.friend), id) end
    if nxt.where == 'ready' then
      local pr = tonumber(prio) or 0
      if front == '1' then
        if pr == 0 then pr = 1 end
        pr = -pr
      end
      redis.call('ZADD', q(nxt.friend), tonumber(nxt.qscore) or pr, id)
    end
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

-- TK.unread(to, friend, o): the refusal when a friendless task (a primary)
-- would enter working or merging and the table-moves code names no copy
-- (o.copy nil): a primary advances only when a copy returns (rowan-new
-- specs/table-moves.md, ruling 2026-09-25 2:52 PM), so neither a harvest's
-- PR-open walk nor a hand task move takes it there unread. A friend take
-- (o.as, or a friend holding it) keeps its own path, and so does a step of
-- the one-call walk to landed at a merge (o.landing with the merge sha,
-- 03_task_event.lua), which never rests in working.
function TK.unread(to, friend, o)
  if o.landing and TK.str(o.sha) ~= '' then return nil end
  if (to == 'working' or to == 'merging') and friend == '' and o.copy == nil then
    if to == 'working' then return 'NOCOPY a primary enters working only as card deal cuts its copy' end
    return 'NOCOPY a primary enters merging only as a read copy\'s card end'
  end
  return nil
end

-- TK.pending(id): primary id waits in review for a typed verdict (a copy's
-- fail put it there, TM.evidence; no verdict since). A review primary
-- without one waits for its read instead (Glenn 2026-09-26: one review
-- state for both).
function TK.pending(id)
  local f = redis.call('HMGET', 'task:' .. id, 'review_at', 'reviewed_at')
  local at, done = tonumber(f[1]), tonumber(f[2])
  return at ~= nil and (done == nil or at > done)
end

-- TK.edge: nil when cur -> nxt is on the graph, else the refusal.
function TK.edge(id, cur, nxt, ok, o)
  local from, to = cur.where, nxt.where
  -- review (#4072): a copy's fail enters it (o.review, TM.finish), and a
  -- typed verdict (o.verdict, TM.review) is the only way out
  -- review has two doors in and two out: a copy's end enters it (card end
  -- --ok --pr cuts the read copies; card end --fail carries o.review) and a
  -- read copy's end (o.copy) or a typed verdict (o.verdict) leaves it.
  if from == 'review' and to ~= 'review' and to ~= 'done' and to ~= 'landed' and not o.verdict and o.copy == nil then
    return 'REVIEW task:' .. id .. ' leaves review by a read copy\'s card end or by nova-sprint review post --verdict recut|redeal|reassign:<consumer>|drop'
  end
  -- a fail's review (a verdict pending) is left by its verdict alone: no
  -- cancel, no hand move (a review waiting for its read may be cancelled)
  if from == 'review' and to ~= 'review' and not o.verdict and not o.clear and TK.str(o.copy) == '' and TK.pending(id) then
    return 'REVIEW task:' .. id .. ' waits for its verdict: nova-sprint review post --verdict recut|redeal|reassign:<consumer>|drop'
  end
  if to == 'review' and from ~= 'review' and not o.review and o.copy == nil then
    return 'OFFGRAPH ' .. (from == '' and 'null' or from) .. ' -> review is a copy\'s end (card end --ok --pr, or --fail)'
  end
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
  if to == 'landed' and TK.str(o.sha) == '' and not (from == 'review' and o.verdict == 'drop') then
    return 'SHA landed needs the merge sha'
  end
  if to == 'done' then
    if ok ~= 'ok' and ok ~= 'fail' then return 'OUTCOME done needs ok or fail' end
    if from == 'landed' and ok ~= 'ok' then return 'OFFGRAPH landed -> done/fail' end
    if from ~= 'working' and from ~= 'landed' and TK.str(o.why) == '' then
      return 'WHY ' .. from .. ' -> done needs a why'
    end
  end
  if from == 'working' and to == 'ready' and TK.str(o.why) == '' then return 'WHY working -> ready needs a why' end
  if from == 'working' and to == 'waiting' and TK.str(o.why) == '' then return 'WHY working -> waiting needs a why' end
  -- a primary enters and leaves working through its copy (table moves)
  if from == 'waiting' and to == 'working' and TK.str(o.copy) == '' then
    return 'OFFGRAPH waiting -> working is card deal (it cuts a copy)'
  end
  local unread = TK.unread(to, nxt.friend, o)
  if unread then return unread end
  if o.copy == nil then
    if TK.str(cur.copy) ~= '' and to ~= 'done' and to ~= 'landed' then
      return 'LIVECOPY task:' .. id .. ' moves when its copy ' .. cur.copy .. ' returns (card end, card cancel)'
    end
    if TK.str(cur.reads) ~= '' and to ~= 'done' and to ~= 'landed' then
      return 'LIVECOPY task:' .. id .. ' moves when a read copy (' .. cur.reads .. ') returns (card end, read post)'
    end
    -- (a sprint-store transition, NS.task.set, names its fine state: a
    -- friend-held task with no copy goes back to waiting that way, #3907)
    if from == 'working' and to == 'waiting' and TK.str(o.state) == '' then
      return 'OFFGRAPH working -> waiting is card end --fail'
    end
  end
  if from == 'review' and to == 'working' and TK.str(o.copy) == '' and not o.verdict then
    return 'OFFGRAPH review -> working cuts a fix copy'
  end
  if from == 'done' and (cur.ok ~= 'ok' or TK.str(o.why) == '') then
    return 'OFFGRAPH done/' .. cur.ok .. ' -> ' .. to .. ' (only done/ok, with a why)'
  end
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
  if TK.copy_id(id) then return 'BADID ' .. id .. ' is a consumer copy: card work|end|cancel' end
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
  local nxt = { where = to, stream = cur.stream, friend = cur.friend, qscore = o.qscore }
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
  -- a probe never enters a consumer set (nova-tools#4237): refused before any write
  if nxt.friend ~= '' then
    err = cm_probe_refused('friend:' .. nxt.friend .. ':cards:' .. to, id)
    if err then return err end
  end
  local front = TK.str(o.front)
  local state = o.state or TK.STATE[to]
  if TK.WHERE_OF[state] ~= to then return 'STATE ' .. TK.str(state) .. ' is not ' .. to end
  -- ONE PLACE, before any write: a violation is a refusal, never a write.
  if not adopted then
    err = TK.verify(id, cur, { nxt.stream }, { nxt.friend })
    if err then return 'DRIFT ' .. err .. ' task:' .. id .. '; run nova-sprint task fsck' end
  end
  if o.dry then return nil end
  if cur.where == to and nxt.stream == cur.stream and nxt.friend == cur.friend and front ~= '1' and ok == cur.ok and
      state == cur.state and (o.copy == nil or o.copy == cur.copy) then
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
    if not was[k] then cm_zadd(k, created, id) end
  end
  if nxt.stream ~= cur.stream then TK.register(nxt.stream) end
  local at = cm_now()
  local S = TK.sprint(cur, o)
  if front == '' then front = cur.front end
  local prio = cur.priority
  for i = 1, #fields, 2 do
    if fields[i] == 'priority' then prio = fields[i + 1] end
  end
  local lh, clear = TK.legacy(id, S, cur, nxt, front, at, prio)
  local h = { 'task:' .. id, 'where', to, 'where_ok', ok, 'state', state }
  -- The sprint's fine-state index (s:<S>:idx:task:<state>) follows the state.
  if S ~= '' and state ~= cur.state then
    if cur.state ~= '' then redis.call('SREM', 's:' .. S .. ':idx:task:' .. cur.state, id) end
    redis.call('SADD', 's:' .. S .. ':idx:task:' .. state, id)
  end
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
  if o.copy ~= nil and o.copy ~= cur.copy then put('copy', o.copy) end
  -- a primary dealt through a copy holds no lease: its copy does
  if to == 'working' and cur.where ~= 'working' and o.copy == nil then
    put('leased_at', tostring(at))
    put('lease_until', tostring(at + TK.LEASE))
  end
  if to == 'done' and (cur.where ~= 'done' or ok ~= cur.ok) then
    put('done_at', tostring(at))
    if ok == 'fail' then put('cancelled', '1') end
  end
  if to == 'landed' and cur.where ~= 'landed' then
    put('landed_at', tostring(at))
    if TK.str(o.sha) ~= '' then put('merge_sha', o.sha) end
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
  -- The review column (#4094): a primary (no friend) that enters review
  -- has its read copies cut in this same call, and one that leaves it
  -- retires the read copies still open (TK.hook is TM.after_move, below).
  local cut
  if TK.hook and nxt.friend == '' then
    local herr
    herr, cut = TK.hook(id, cur, to, o)
    if herr then return 'DRIFT-AFTER ' .. herr .. ' task:' .. id end
  end
  return nil, { from = cur.where, to = to, xid = xid, cut = cut }
end

-- TK.create(id, fields, o): a new record (where null) and its first move to
-- o.where (waiting, else ready). fields are HSET pairs (never a pointer
-- field); o.stream (else the title's STREAM: prefix), o.friend and o.sprint
-- are its dimensions.
function TK.create(id, fields, o)
  o = o or {}
  if type(id) ~= 'string' or id == '' or string.find(id, '%s') then return 'BADID ' .. TK.str(id) end
  if TK.card_id(id) then return 'BADID ' .. id .. ' is a card: card push' end
  if TK.copy_id(id) then return 'BADID ' .. id .. ' has the copy form <id>~<n>' end
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
  -- a probe never enters a consumer set (nova-tools#4237): refused before any write
  if TK.str(o.friend) ~= '' then
    for i = 1, #fields, 2 do
      if fields[i] == 'probe' and TK.str(fields[i + 1]) ~= '' then
        return 'PROBE ' .. id .. ' is a probe (probe=' .. TK.str(fields[i + 1]) .. '); friend:' .. TK.str(o.friend) ..
          ':cards:' .. where .. ' holds consumer work only: a probe result goes on bench:<b>:beat probe (nova-tools#4237)'
      end
    end
  end
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
  return TK.move(id, where, { by = o.by, why = o.why or 'push', front = o.front, sprint = o.sprint, state = o.state,
    qscore = o.qscore })
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
  local w = TK.WHERE_OF[st] or (TK.IS[st] and st ~= 'done' and st)
  if w == 'landed' then return 'landed', 'ok' end
  if w then return w, '-' end
  return '', '-'
end

-- TK.holder names the friend of a record that predates the card: its owner,
-- else its dest (the sprint store's queue), else the one sprint ready queue
-- (s:<S>:open:<f>, f in friends) that holds it.
function TK.holder(id, p, o)
  if p.friend ~= '' then return end
  if p.dest ~= '' then
    p.friend = p.dest
    return
  end
  local S = TK.sprint(p, o)
  if S == '' then return end
  for _, f in ipairs(redis.call('SMEMBERS', 'friends')) do
    if redis.call('ZSCORE', 's:' .. S .. ':open:' .. f, id) then
      p.friend = f
      return
    end
  end
end

-- TK.adopt links a record that predates the where field (a friend-queue
-- push, a pre-model ws move) at the place its facts imply, adding the views
-- it lacks. It refuses when any other set of its stream or friend holds it:
-- that is drift for task migrate, not a guess. dry writes nothing and
-- returns nil, where, ok.
function TK.adopt(id, p, o, dry)
  TK.holder(id, p, o)
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
  for _, k in ipairs(TK.views(q)) do cm_zadd(k, 'NX', created, id) end
  TK.register(p.stream)
  redis.call('HSET', 'task:' .. id, 'where', w, 'where_ok', ok, 'stream', p.stream, 'friend', p.friend,
    'owner', p.friend, 'created_at', string.format('%.0f', created))
  local S = TK.sprint(p, o)
  if S ~= '' then
    redis.call('SADD', 'sprint:' .. S .. ':tasks', id)
    if p.sprint == '' then redis.call('HSET', 'task:' .. id, 'sprint', S) end
  end
  return nil
end

-- TK.last_beat: the last sign a holder lives, ms: the lease less its span,
-- the beat, the take, the move into working; 0 when there is none.
function TK.last_beat(p)
  local last = 0
  local lease = tonumber(p.lease_until)
  if lease then last = lease - TK.LEASE end
  for _, v in ipairs({ p.beat_at, p.leased_at, p.claimed_at, p.where_at }) do
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
  if not p.placed then TK.holder(id, p, o) end
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
    -- a sprint store claim's old token is fenced: its done refuses FENCED
    if p.token ~= '' and p.token ~= '0' and p.token ~= 'fenced' then
      redis.call('HSET', 'task:' .. id, 'token', 'fenced')
    end
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
  for k in pairs(target) do cm_zadd(k, created, id) end
  TK.register(p.stream)
  local at = cm_now()
  local S = TK.sprint(p, o)
  if S ~= '' then
    -- out of every ready queue of its sprint; TK.legacy puts it back in one
    redis.call('ZREM', 's:' .. S .. ':ready', id)
    for _, f in ipairs(friends) do
      if f ~= '' then redis.call('ZREM', 's:' .. S .. ':open:' .. f, id) end
    end
  end
  local cur = { where = '', friend = p.friend, queue = p.queue, xid = p.xid }
  if p.placed then cur.where = p.where end
  local lh, clear = TK.legacy(id, S, cur, { where = where, friend = p.friend }, p.front, at, p.priority)
  -- a fine state that names this where stays (claimed, cancelled, ...)
  local state = TK.STATE[where] or ''
  if TK.WHERE_OF[p.state] == where and not (where == 'done' and (p.state == 'cancelled') ~= (ok == 'fail')) then
    state = p.state
  elseif where == 'done' and ok == 'fail' and p.state == 'cancelled' then
    state = 'cancelled'
  end
  if S ~= '' and state ~= p.state then
    if p.state ~= '' then redis.call('SREM', 's:' .. S .. ':idx:task:' .. p.state, id) end
  end
  if S ~= '' and state ~= '' then redis.call('SADD', 's:' .. S .. ':idx:task:' .. state, id) end
  local h = { 'task:' .. id, 'where', where, 'where_ok', ok, 'where_at', tostring(at), 'state', state,
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
-- tasks null, then one count per TK.WHERE (waiting ready working review
-- review merging landed done parked), unplaced drift,
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
      -- (a / is a friend-queue lease in the working set: TM.hold)
      if not TK.card_id(id) and not TK.copy_id(id) and not string.find(id, '/', 1, true) then
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
        if TK.WHERE_OF[p.state] ~= p.where then
          note('state task:' .. id .. ' state=' .. p.state .. ' where=' .. p.where)
        end
        if p.sprint ~= '' and p.state ~= '' and redis.call('SISMEMBER', 's:' .. p.sprint .. ':idx:task:' .. p.state, id) == 0 then
          note('unindexed s:' .. p.sprint .. ':idx:task:' .. p.state .. ' ' .. id)
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
-- sha, sprint, front, as, state, qscore are options; any other k is a record
-- field.
function TK.opts(args, from, o)
  local known = { ok = true, friend = true, stream = true, sha = true, sprint = true, front = true, as = true,
    state = true, qscore = true }
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
  if not named then ids = redis.call('ZRANGE', 'friend:' .. as .. ':cards:ready', 0, -1) end
  local out = { 'TAKEN', '0' }
  for _, id in ipairs(ids) do
    if not named and #out - 2 >= n then break end
    -- a consumer copy is card work's (the friend harness's task take works
    -- copies first, then takes friend-queue tasks)
    if named or not TK.copy_id(id) then
      local err = TK.move(id, 'working', { by = by, why = 'take', as = as })
      if err and named then return { 'REFUSED', err } end
      if not err then out[#out + 1] = id end
    end
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

-- TK.stray: why the link of task id in friend f's working set is stray, or
-- nil when the record says it is f's and working (a record that predates
-- the where field: where its facts imply, TK.derive). A finished task left
-- in the set (Glenn 2026-09-25 4:40 PM ET: "The friends table must be
-- accurate each second. It must not lie."; emma showed 17 working with 5
-- live children and 11 finished cards) is stray, and so is an id
-- with no record.
function TK.stray(id, f)
  local p = TK.read(id)
  if not p then return 'no record', nil end
  local w = p.where
  if not p.placed then w = TK.derive(id, p) or '' end
  if w ~= 'working' then return 'record is ' .. (w == '' and 'null' or w), p end
  if p.friend ~= f then return "record is " .. (p.friend == '' and 'nobody' or p.friend) .. "'s", p end
  return nil, p
end

-- TK.reap(f, by, now) sweeps friend f's working set (#3892): a task whose
-- record is not f's and working leaves the set (its own views are linked,
-- score created_at, so the record is still in exactly its places) with one
-- ws:log receipt, to=unlinked; a working task whose lease lapsed goes back
-- to ready through TK.move, why=lease lapsed (the lease rule). A card id
-- (s:<S>:card:) is the card move's: card fsck and the expire duty own it.
-- Returns the ids moved to ready and the ids unlinked.
function TK.reap(f, by, now)
  local fk = 'friend:' .. f .. ':cards:working'
  local expired, unlinked = {}, {}
  for _, id in ipairs(redis.call('ZRANGE', fk, 0, -1)) do
    if not TK.card_id(id) and not TK.copy_id(id) then
      local why, p = TK.stray(id, f)
      if why then
        if p and p.placed and TK.IS[p.where] then
          for _, k in ipairs(TK.views(p)) do cm_zadd(k, 'NX', p.created or cm_now(), id) end
        end
        redis.call('ZREM', fk, id)
        redis.call('XADD', 'ws:log', 'MAXLEN', '~', TK.LOG_MAX, '*', 'id', id, 'stream', p and p.stream or '',
          'from', 'working', 'to', 'unlinked', 'by', by, 'why', 'stray ' .. fk .. ': ' .. why, 'at', tostring(now))
        unlinked[#unlinked + 1] = id
      else
        local lease = tonumber(p.lease_until)
        if not lease then lease = TK.last_beat(p) + TK.LEASE end
        if p.state ~= 'reconcile-required' and lease < now then
          -- a sprint store claim is fenced too: its old token's done and
          -- beat refuse FENCED, and its lease leaves the friend's slots
          local c = redis.call('HMGET', 'task:' .. id, 'token', 'attempt', 'sprint')
          local token, attempt, S = TK.str(c[1]), TK.str(c[2]), TK.str(c[3])
          if not TK.move(id, 'ready', { by = by, why = 'lease lapsed', state = 'open' }) then
            if token ~= '' and token ~= '0' and token ~= 'fenced' then
              redis.call('HSET', 'task:' .. id, 'token', 'fenced')
              local identity = S .. '/' .. id .. '/' .. attempt
              NS.moves.drop('friend:' .. f, identity)
            end
            expired[#expired + 1] = id
          end
        end
      end
    end
  end
  return expired, unlinked
end

-- ns_tcard_expire(by[, friend...]) -> EXPIRED n m, then the n ids moved
-- back to ready, then the m ids unlinked: TK.reap over each named friend's
-- working set (else every member of friends). O(the working sets).
redis.register_function('ns_tcard_expire', function(keys, args)
  local by = args[1] or ''
  local friends = {}
  for i = 2, #args do friends[#friends + 1] = args[i] end
  if #friends == 0 then friends = redis.call('SMEMBERS', 'friends') end
  local now = cm_now()
  local expired, unlinked = {}, {}
  for _, f in ipairs(friends) do
    local e, u = TK.reap(f, by, now)
    for _, id in ipairs(e) do expired[#expired + 1] = id end
    for _, id in ipairs(u) do unlinked[#unlinked + 1] = id end
  end
  local out = { 'EXPIRED', tostring(#expired), tostring(#unlinked) }
  for _, id in ipairs(expired) do out[#out + 1] = id end
  for _, id in ipairs(unlinked) do out[#out + 1] = id end
  return out
end)

-- ns_tcard_land_stream(stream, sha, by, why) -> LANDED n refused, then per
-- landed member: id, repo#pr (its ref or pr), origin; then per refused
-- member: id, why. The stream lander's merge (Glenn 08:50 AM ET: merging ->
-- landed at the one merge sha, then the lander closes each member's PR and
-- origin issue with the CLOSE line): every member of ws:<stream>:merging
-- moves to landed through the one move, oldest first.
redis.register_function('ns_tcard_land_stream', function(keys, args)
  local stream, sha, by, why = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
  if why == '' then why = 'stream merged ' .. sha end
  local landed, refused = {}, {}
  for _, id in ipairs(redis.call('ZRANGE', 'ws:' .. stream .. ':merging', 0, -1)) do
    if not TK.card_id(id) then
      local err = TK.move(id, 'landed', { by = by, why = why, sha = sha })
      if err then
        refused[#refused + 1] = id
        refused[#refused + 1] = err
      else
        local f = redis.call('HMGET', 'task:' .. id, 'ref', 'repo', 'pr', 'origin')
        local ref = TK.str(f[1])
        if TK.str(f[3]) ~= '' and TK.str(f[3]) ~= '0' then ref = TK.str(f[2]) .. '#' .. TK.str(f[3]) end
        landed[#landed + 1] = id
        landed[#landed + 1] = ref
        landed[#landed + 1] = TK.str(f[4])
      end
    end
  end
  local out = { 'LANDED', tostring(#landed / 3), tostring(#refused / 2) }
  for _, v in ipairs(landed) do out[#out + 1] = v end
  for _, v in ipairs(refused) do out[#out + 1] = v end
  return out
end)

-- ns_tcard_fsck(S): TK.fsck, read-only.
redis.register_function({ function_name = 'ns_tcard_fsck', flags = { 'no-writes' },
  callback = function(keys, args) return TK.fsck(args[1] or '') end })

-- ===========================================================================
-- Table moves (nova-tools #3929; rowan-new specs/table-moves.md). Glenn
-- 2026-09-25 12:35 PM ET: "design a set of verbs in nova-sprint so that
-- moving cards across the host table is natural and easy, and stays
-- internally consistent." 12:45 PM: "the card in the stream table set is the
-- primary one -- the created friend cards, or swarm cards come from it -- and
-- RETURN TO IT when done." 12:55 PM: "this code is the same, whether the
-- consumer card is on a friend, or on the swarm." 1:25 PM: waiting -> working
-- -> review -> merging -> landed.
--
-- The PRIMARY is the task card above (task:<id>, in ws:<stream>:<where>); it
-- never leaves its stream. A CONSUMER is <kind>:<name> (bench:hetzner,
-- friend:emma), one shape for both kinds: its sets are <consumer>:cards:
-- ready | working | ok | fail and its capacity is the slots field of
-- <consumer>:desired. Nothing below branches on the kind; WHO names a
-- consumer by id or name, and 'swarm' names every bench.
--
-- card deal CUTS a COPY of a primary onto a consumer: the copy's record
-- task:<primary>~<n> names its primary (primary) and consumer, the primary
-- names its live work or fix copy (copy) or its live read copies (reads,
-- space-joined), and the primary moves waiting -> working (a WORK copy) or
-- stays review (a READ copy, a FIX copy). The copy walks ready ->
-- working (card work) -> ok | fail (card end, card cancel, a lapsed lease),
-- scored by the primary's created_at while live and by ended_at once
-- retired, so the consumer's ok and fail sets are its done count. card end
-- RETURNS the copy in the same call: the result is written onto the
-- primary, its copy pointer is cleared, and the primary moves:
--   work ok with a PR -> review (author = the consumer), ok with a
--   done-already sha -> landed, ok with neither -> done/ok, fail -> waiting
--   with the why (done/fail after TM.RETRIES);
--   read with --score N (or read post's SCORE line, TM.score): the SCORE
--   line goes on the PR record pr:<name>:<n>; N >= TM.PASS -> merging (the
--   other open copies retire), under it the copy ends ok with its finding,
--   the primary stays in review and ONE fix copy is cut on the author's
--   queue carrying the SCORE line (TM.fix_route; -> waiting when there is
--   none); read --fail: the copy retires and the primary stays in review;
--   fix ok (a new head) -> the open reads retire and fresh ones are cut;
--   fix fail -> review with no copy (done/fail after TM.RETRIES).
-- THE READING COLUMN (#4094, #4097): every move of a primary into review
-- (a work copy's ok with a PR, and any other way in) cuts its read copies
-- in the same call (TK.hook = TM.after_move -> TM.cut_reads): one copy on a
-- friend with the reader role and open slots, else TM.SWARM_READS copies on
-- the swarm's benches (TM.read_route), never on the author; each names the
-- primary and the head. A head move (the PR record's head is not the
-- primary's) retires the open copies and cuts fresh ones (TM.rehead: pr
-- record --head through ns_cm_head, read post, and TM.ensure on every deal
-- pass, which also re-cuts for a review primary left with no live copy).
--   work|fix ok with a PR -> review (author = the consumer) and a READ
--   copy cut for the best reader in the same call (review with no copy
--   when no reader has room: the deal cuts it), ok with a done-already sha
--   -> landed, ok with neither -> done/ok, fail -> waiting with the why
--   (done/fail after TM.RETRIES);
--   read with --score N: the SCORE line goes on the PR record pr:<name>:<n>
--   and N >= TM.PASS -> merging, under it -> working with the finding as
--   the why and a FIX copy cut on the author's consumer (hold-to-fix), or
--   -> waiting when there is no author.
-- A FAIL goes to review (nova-tools #4072, Glenn 2026-09-25 2:45 PM: "whenever
-- a card fails in either friend or swarms, this triggers intelligence
-- review"): a work, fix or read copy's fail (a crash, a wall, a refusal,
-- SLOTS REFUSED, a child's non-zero exit, a lapsed lease) and a second read
-- under TM.PASS (the author's copy then counts as a fail) move the primary
-- to review with the evidence on its record (TM.evidence) and the mechanical
-- first pass (REVIEW-JEV: the shape, the same-shape counts, a suggested
-- verdict, never a verdict). A copy given back (card cancel, assign
-- --revoke, a down consumer's ready copies: o.keep) is not a fail: its
-- primary returns to waiting (review for a read copy). The only way out of
-- review is TM.review, a typed verdict: recut -> waiting, redeal -> ready,
-- reassign:<consumer> -> a copy cut on that consumer, drop -> landed with
-- outcome=dropped. Its REVIEW line is carried to the next copy.
-- A copy's end is the ONLY event that moves a primary (Glenn 2026-09-25
-- 2:52 PM, rowan-new specs/table-moves.md). CI gates the READ copy, never
-- the primary: a read ends with a passing score only at a head whose CI is
-- OK (TM.ci_final; pending or red is refused, and a red head is a score
-- under TM.PASS with the failure as the finding). No CI verdict, duty or
-- hand verb moves a primary.
-- A primary with a live copy is never cut again; a copy with no primary
-- cannot be made; card fsck (TM.fsck) proves both links both ways. TM is the
-- only writer of the consumer sets and of the copy and primary pointers.
local TM = {
  COLS = { 'ready', 'working', 'ok', 'fail' },
  -- where a primary is while a copy of each leg is live (a fix copy cut
  -- before the review column may find its primary working)
  WANT = { work = { working = true }, read = { review = true }, fix = { review = true, working = true } },
  LIVE = { ready = true, working = true },
  -- a working copy's lease: three missed 60 s beats
  LEASE = 180000,
  -- the read score that moves a primary to merging
  PASS = 8,
  -- the read copies cut on the swarm when no friend reader has room (four
  -- cold readers per card)
  SWARM_READS = 4,
  -- a consumer beat this recent is live
  LIVE_MS = 90000,
  -- the primary's fields a copy carries for its consumer's brief
  CARRY = { 'kind', 'ref', 'origin', 'title', 'repo', 'pr', 'head', 'base', 'base_sha', 'paths', 'done_when', 'tier',
    'route', 'stream', 'review', 'body', 'branch' },
  -- a card end's result fields (written onto the primary and the copy)
  RESULT = { line1 = true, line2 = true, check = true, paths = true, branch = true, commit = true, pr = true,
    repo = true, head = true, base = true, base_sha = true, model = true, route = true, wall = true, evidence = true,
    gates = true, finding = true, tier = true, key = true, exit = true },
  -- the verdicts out of review and where each moves the primary (reassign
  -- cuts a copy: working, or review for a read)
  VERDICTS = { recut = 'waiting', redeal = 'ready', drop = 'landed', reassign = '' },
}

-- TM.holds: primary p names live copy id of leg (its copy, or one of its
-- reads) and is where that leg wants it (TM.WANT).
function TM.holds(p, id, leg)
  if not p then return false end
  leg = TK.str(leg) == '' and 'work' or leg
  local named = p.copy == id
  for w in string.gmatch(p.reads, '%S+') do named = named or w == id end
  return named and (TM.WANT[leg] or TM.WANT.work)[p.where] == true
end

-- TM.parse: kind, name of a consumer id; nil when it is not one.
function TM.parse(c)
  if type(c) ~= 'string' then return nil end
  local kind, name = string.match(c, '^(%l+):([A-Za-z0-9][A-Za-z0-9._-]*)$')
  if kind ~= 'bench' and kind ~= 'friend' then return nil end
  return kind, name
end

function TM.key(c, col) return c .. ':cards:' .. col end

-- TM.ci_legs: the CI legs running on the machine consumer c runs on, as
-- its beat's ci field counts them (<consumer>:beat, a bench's or a
-- friend's; nova-tools#4293: the Studio hosts friends and CI both), 0 for
-- an unmeasured beat. Each holds a slot while it runs.
function TM.ci_legs(c)
  return tonumber(redis.call('HGET', c .. ':beat', 'ci')) or 0
end

-- TM.set: the name list of a comma or space separated field, as a set; nil
-- when the field is empty (no restriction).
function TM.set(v)
  v = TK.str(v)
  if v == '' then return nil end
  local s = {}
  for n in string.gmatch(v, '[^%s,]+') do s[n] = true end
  return s
end

-- The three model types a card's ROUTE names and a worker advertises on its
-- desired tiers (cardhdr.Routes): frontier (the most recent Astra or Fable
-- model only), pro and flash. A worker advertising no tiers is
-- TM.DEFAULT_TIERS, the two swarm rungs, so a frontier card never goes to a
-- worker that did not advertise frontier. Nothing here names a worker.
TM.MODEL_TYPES = { frontier = true, pro = true, flash = true }
TM.DEFAULT_TIERS = 'flash,pro'

-- TM.list: a set's names as a sorted comma list.
function TM.list(s)
  local names = {}
  for n in pairs(s) do names[#names + 1] = n end
  table.sort(names)
  return table.concat(names, ',')
end

-- TM.desired: the consumer's declared capacity and filters, one HMGET.
-- tiers is what it advertises (TM.DEFAULT_TIERS when it declared none;
-- declared says which).
function TM.desired(c)
  local d = redis.call('HMGET', c .. ':desired', 'slots', 'paused', 'tiers', 'kinds')
  local tiers = TM.set(d[3])
  return { slots = tonumber(d[1]), paused = TK.str(d[2]) == '1', tiers = tiers or TM.set(TM.DEFAULT_TIERS),
    declared = tiers ~= nil, kinds = TM.set(d[4]) }
end

-- TM.names: whether c is named in the name set s (by id, by name, or as
-- 'swarm' for a bench).
function TM.names(s, c)
  local kind, name = TM.parse(c)
  return s[c] or s[name] or (kind == 'bench' and s['swarm']) or false
end

-- TM.admits: the WHO form (any | only a,b | except a,b) admits consumer c.
function TM.admits(who, c)
  who = TK.str(who)
  local mode, rest = string.match(who, '^%s*(%a+)%s*(.-)%s*$')
  if not mode or mode == 'any' then return true end
  local s = TM.set(rest) or {}
  if mode == 'only' then return TM.names(s, c) end
  if mode == 'except' then return not TM.names(s, c) end
  return false
end

-- TM.streams: every stream, in ws:order rank order, then unranked by name.
function TM.streams()
  local out, seen = {}, {}
  for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
    if not seen[s] then
      seen[s] = true
      out[#out + 1] = s
    end
  end
  local rest = redis.call('SMEMBERS', 'ws:names')
  table.sort(rest)
  for _, s in ipairs(rest) do
    if not seen[s] then
      seen[s] = true
      out[#out + 1] = s
    end
  end
  return out
end

function TM.log(id, stream, from, to, by, why, c)
  redis.call('XADD', 'ws:log', 'MAXLEN', '~', TK.LOG_MAX, '*', 'id', id, 'stream', TK.str(stream), 'from', from, 'to', to,
    'by', TK.str(by), 'why', TK.str(why), 'consumer', c, 'at', tostring(cm_now()))
end

-- TM.leg: the copy a primary is ready for: 'work' (waiting with no open
-- DEPENDS-ON, or ready) or 'read' (review with no live copy); else nil and
-- why not.
function TM.leg(id, p)
  if not p then return nil, 'NOTASK task:' .. id end
  if p.copy ~= '' then return nil, 'LIVECOPY task:' .. id .. ' has live copy ' .. p.copy end
  if p.reads ~= '' then return nil, 'LIVECOPY task:' .. id .. ' has live read copies ' .. p.reads end
  if p.friend ~= '' then return nil, 'OWNED task:' .. id .. ' is ' .. p.friend .. "'s friend-queue task, not a primary" end
  if p.where == 'review' then
    if TM.pending(id) then
      return nil, 'VERDICT task:' .. id .. ' is in review for its verdict: nova-sprint review post --verdict recut|redeal|reassign:<consumer>|drop'
    end
    return 'read'
  end
  if p.where == 'ready' then return 'work' end
  if p.where == 'waiting' or not p.placed then
    local dep = TK.str(redis.call('HGET', 'task:' .. id, 'blocked_on'))
    if dep ~= '' and dep ~= '-' and dep ~= 'none' then return nil, 'DEPENDS task:' .. id .. ' waits on ' .. dep end
    if p.where == 'waiting' then return 'work' end
  end
  return nil, 'WHERE task:' .. id .. ' is ' .. (p.where == '' and 'null' or p.where) .. ', not waiting or review'
end

-- TM.may: nil when consumer c (its desired d) may take primary id's leg,
-- else why not. A work copy honours the primary's WHO, a read copy its
-- read_who, the readers set when it has members, and never the author;
-- tiers and kinds on <c>:desired filter both. The card's tier is its tier
-- field, else its route when that names a model type (a cut card carries
-- ROUTE); a read is pro; a fix copy carries the primary's. A model-type
-- tier the consumer does not advertise is refused (advertising nothing is
-- TM.DEFAULT_TIERS); any other tier word is refused only against tiers the
-- consumer declared. author, when given, is the author a card end is about
-- to write (the record does not name it yet).
function TM.may(c, d, id, leg, author)
  local f = redis.call('HMGET', 'task:' .. id, 'who', 'read_who', 'author', 'tier', 'kind', 'route')
  local tier, kind = TK.str(f[4]), TK.str(f[5])
  if tier == '' and TM.MODEL_TYPES[TK.str(f[6])] then tier = TK.str(f[6]) end
  if leg == 'read' then
    author = TK.str(author) ~= '' and author or TK.str(f[3])
    if author ~= '' and (author == c or select(2, TM.parse(c)) == (select(2, TM.parse(author)) or author)) then
      return 'AUTHOR ' .. c .. ' wrote task:' .. id .. '; a read is never the author\'s'
    end
    if not TM.admits(f[2], c) then return 'WHO task:' .. id .. ' read_who is ' .. TK.str(f[2]) end
    local readers = redis.call('SMEMBERS', 'readers')
    if #readers > 0 then
      local s = {}
      for _, r in ipairs(readers) do s[r] = true end
      if not TM.names(s, c) then return 'READER ' .. c .. ' is not in readers' end
    end
    tier, kind = 'pro', 'read'
  elseif not TM.admits(f[1], c) then
    return 'WHO task:' .. id .. ' is ' .. TK.str(f[1])
  end
  if tier ~= '' and not d.tiers[tier] and (d.declared or TM.MODEL_TYPES[tier]) then
    return 'TIER ' .. c .. ' advertises ' .. TM.list(d.tiers) .. ', not ' .. tier
  end
  if d.kinds and kind ~= '' and not d.kinds[kind] then return 'KIND ' .. c .. ' takes no ' .. kind .. ' card' end
  return nil
end

-- TM.cut(c, id, leg, o): the copy of primary id for consumer c, in one
-- place (the one cut): the copy is created in c's ready set and the primary
-- names it. A work or fix copy is named by the primary's move (o.to:
-- working for a work copy, review for a fix copy); a read copy never moves
-- its primary, which is in review, and joins its reads list. o.fields are
-- more primary fields, o.extra more copy fields (the finding). o.dry checks
-- only. Returns nil and the copy id, or the refusal.
function TM.cut(c, id, leg, o)
  -- a probe is never cut onto a consumer (nova-tools#4237): refused before any write
  local probe = cm_probe_refused(TM.key(c, 'ready'), id)
  if probe then return probe end
  local n = (tonumber(redis.call('HGET', 'task:' .. id, 'copies')) or 0) + 1
  local cid = id .. '~' .. n
  if redis.call('EXISTS', 'task:' .. cid) == 1 then return 'DRIFT task:' .. cid .. ' exists; run nova-sprint card fsck' end
  if not o.verdict and TM.pending(id) then
    return 'VERDICT task:' .. id .. ' is in review for its verdict: nova-sprint review post --verdict recut|redeal|reassign:<consumer>|drop'
  end
  local fields = { 'copies', tostring(n) }
  for _, v in ipairs(o.fields or {}) do fields[#fields + 1] = v end
  if leg == 'read' then
    local f = redis.call('HMGET', 'task:' .. id, 'where', 'friend', 'owner', 'reads')
    if TK.str(f[1]) ~= 'review' then
      return 'WHERE task:' .. id .. ' is ' .. TK.str(f[1]) .. '; a read copy is cut for a primary in review'
    end
    if TK.str(f[2]) ~= '' or TK.str(f[3]) ~= '' then return 'OWNED task:' .. id .. ' is a friend-queue task, not a primary' end
    if o.dry then return nil end
    local reads = TM.words(f[4])
    reads[#reads + 1] = cid
    fields[#fields + 1] = 'reads'
    fields[#fields + 1] = table.concat(reads, ' ')
    redis.call('HSET', 'task:' .. id, unpack(fields))
  else
    local err = TK.move(id, o.to or 'working', { by = o.by, why = o.why or (leg .. ' copy to ' .. c), copy = cid,
      fields = fields, dry = o.dry, verdict = o.verdict })
    if err or o.dry then return err end
  end
  local p = redis.call('HMGET', 'task:' .. id, 'created_at', 'stream', unpack(TM.CARRY))
  local created, at = TK.ms(p[1]) or cm_now(), cm_now()
  local h = { 'task:' .. cid, 'card', 'copy', 'leg', leg, 'primary', id, 'consumer', c, 'where', 'ready',
    'where_at', tostring(at), 'cut_at', tostring(at), 'created_at', string.format('%.0f', created) }
  for i, f in ipairs(TM.CARRY) do
    local v = TK.str(p[i + 2])
    if v ~= '' then
      h[#h + 1] = f
      h[#h + 1] = v
    end
  end
  if leg == 'read' then
    h[#h + 1] = 'kind'
    h[#h + 1] = 'read'
    h[#h + 1] = 'tier'
    h[#h + 1] = 'pro'
    h[#h + 1] = 'route'
    h[#h + 1] = 'read'
  elseif leg == 'fix' then
    h[#h + 1] = 'kind'
    h[#h + 1] = 'fix'
  end
  for _, v in ipairs(o.extra or {}) do h[#h + 1] = v end
  redis.call('HSET', unpack(h))
  cm_zadd(TM.key(c, 'ready'), created, cid)
  TM.log(cid, p[2], '', c .. ':ready', o.by, leg .. ' copy of ' .. id, c)
  return nil, cid
end

-- TM.deal(c, by, k, stream, ids): named primaries (all or nothing), else
-- the k oldest primaries c may take, read legs first (they finish work in
-- flight), then work legs in stream rank order. Returns the reply.
function TM.deal(c, by, k, stream, ids)
  if not TM.parse(c) then return { 'REFUSED', 'CONSUMER ' .. TK.str(c) .. ' is not bench:<b> or friend:<f>' } end
  local d = TM.desired(c)
  local out = { 'DEALT', '0' }
  local function cut(id, leg)
    local err, cid = TM.cut(c, id, leg, { by = by })
    if err then return err end
    out[#out + 1] = id
    out[#out + 1] = cid
    return nil
  end
  if #ids > 0 then
    local legs, seen = {}, {}
    for i, id in ipairs(ids) do
      if seen[id] then return { 'REFUSED', 'TWICE ' .. id .. ' is named twice' } end
      seen[id] = true
      if TK.copy_id(id) then return { 'REFUSED', 'COPY ' .. id .. ' is a copy; deal its primary' } end
      local leg, why = TM.leg(id, TK.read(id))
      why = why or TM.may(c, d, id, leg) or TM.cut(c, id, leg, { by = by, dry = true })
      if why then return { 'REFUSED', why } end
      legs[i] = leg
    end
    for i, id in ipairs(ids) do
      local err = cut(id, legs[i])
      if err then return { 'REFUSED', 'DRIFT-AFTER ' .. err } end
    end
  else
    local streams = TM.streams()
    if stream ~= '' then streams = { stream } end
    local function pass(wheres, want)
      for _, s in ipairs(streams) do
        local rows = {}
        for _, w in ipairs(wheres) do
          local r = redis.call('ZRANGE', 'ws:' .. s .. ':' .. w, 0, -1, 'WITHSCORES')
          for i = 1, #r, 2 do rows[#rows + 1] = { r[i], tonumber(r[i + 1]) or 0 } end
        end
        table.sort(rows, function(a, b) return a[2] < b[2] or (a[2] == b[2] and a[1] < b[1]) end)
        for _, row in ipairs(rows) do
          if (#out - 2) / 2 >= k then return end
          local id = row[1]
          if not TK.card_id(id) and not TK.copy_id(id) then
            local leg = TM.leg(id, TK.read(id))
            if leg == want and not TM.may(c, d, id, leg) and not TM.cut(c, id, leg, { by = by, dry = true }) then
              cut(id, leg)
            end
          end
        end
      end
    end
    pass({ 'review' }, 'read')
    pass({ 'ready', 'waiting' }, 'work')
  end
  out[2] = tostring((#out - 2) / 2)
  return out
end

-- TM.work(c, by, k, fill, ids): consumer ready -> working, k = min(free,
-- |ready copies|) (fill; else also at most k), free = slots - ci -
-- |working|, ci the CI legs running on the consumer's machine as its beat
-- counts them (nova-tools#4293: a copy is never put beside a leg it would
-- slow); named copies all or nothing. Each starts a lease its holder renews with
-- card beat, and a token its end may present (a stale one is FENCED).
-- Returns the reply WORKED n free, then per copy its id and token.
function TM.work(c, by, k, fill, ids)
  if not TM.parse(c) then return { 'REFUSED', 'CONSUMER ' .. TK.str(c) .. ' is not bench:<b> or friend:<f>' } end
  local d = TM.desired(c)
  if not d.slots then return { 'REFUSED', 'SLOTS ' .. c .. ':desired has no slots; run nova-sprint capacity' } end
  local free = d.slots - TM.ci_legs(c) - redis.call('ZCARD', TM.key(c, 'working'))
  local take = {}
  if #ids > 0 then
    local seen = {}
    for _, id in ipairs(ids) do
      if seen[id] then return { 'REFUSED', 'TWICE ' .. id .. ' is named twice' } end
      seen[id] = true
      if not TK.copy_id(id) or not redis.call('ZSCORE', TM.key(c, 'ready'), id) then
        return { 'REFUSED', 'NOTREADY ' .. id .. ' is not a copy in ' .. TM.key(c, 'ready') }
      end
      take[#take + 1] = id
    end
    if #take > free then return { 'REFUSED', 'FULL ' .. c .. ' has ' .. free .. ' free of ' .. d.slots .. ' slots' } end
  else
    local n = free
    if not fill and k < n then n = k end
    if n > 0 then
      for _, id in ipairs(redis.call('ZRANGE', TM.key(c, 'ready'), 0, -1)) do
        if #take >= n then break end
        if TK.copy_id(id) then take[#take + 1] = id end
      end
    end
  end
  local at = cm_now()
  local out = { 'WORKED', tostring(#take), tostring(free - #take) }
  for _, id in ipairs(take) do
    local r = redis.call('HMGET', 'task:' .. id, 'consumer', 'where', 'created_at', 'stream')
    if r[1] ~= c or r[2] ~= 'ready' then return { 'REFUSED', 'DRIFT task:' .. id .. ' is ' .. TK.str(r[1]) .. ':' .. TK.str(r[2]) } end
    redis.call('ZREM', TM.key(c, 'ready'), id)
    cm_zadd(TM.key(c, 'working'), TK.ms(r[3]) or at, id)
    local token = id .. '@' .. tostring(at)
    redis.call('HSET', 'task:' .. id, 'where', 'working', 'where_at', tostring(at), 'leased_at', tostring(at),
      'lease_until', tostring(at + TM.LEASE), 'token', token)
    TM.log(id, r[4], c .. ':ready', c .. ':working', by, 'work', c)
    out[#out + 1] = id
    out[#out + 1] = token
  end
  return out
end

-- TM.pr_line: one typed line onto the PR record's two line stores (reads,
-- the lander's; :lines, read post's), once (the same store as
-- 03_task_event.lua's TE.line); posted: read post already pushed it to
-- :lines.
function TM.pr_line(repo, pr, line, posted)
  repo = string.match(TK.str(repo), '([^/]+)$') or ''
  if repo == '' or TK.str(pr) == '' or TK.str(pr) == '0' then return nil end
  local key = 'pr:' .. repo .. ':' .. pr
  local reads = TK.str(redis.call('HGET', key, 'reads'))
  if ('\n' .. reads .. '\n'):find('\n' .. line .. '\n', 1, true) == nil then
    if reads ~= '' then reads = reads .. '\n' end
    redis.call('HSET', key, 'reads', reads .. line)
  end
  if not posted and not redis.call('LPOS', key .. ':lines', line) then redis.call('RPUSH', key .. ':lines', line) end
  return key
end

-- TM.retire: copy id leaves c's live set w for its ok|fail set, scored by
-- ended_at, with the result on its record.
function TM.retire(id, c, w, outcome, why, fields, by, stream)
  local at = cm_now()
  redis.call('ZREM', TM.key(c, w), id)
  cm_zadd(TM.key(c, outcome), at, id)
  local h = { 'task:' .. id, 'where', outcome, 'where_at', tostring(at), 'ended_at', tostring(at), 'outcome', outcome,
    'why', TK.str(why) }
  for _, v in ipairs(fields or {}) do h[#h + 1] = v end
  redis.call('HSET', unpack(h))
  redis.call('HDEL', 'task:' .. id, 'lease_until')
  TM.log(id, stream, c .. ':' .. w, c .. ':' .. outcome, by, why, c)
end

-- TM.words: a space-joined list field as a list.
function TM.words(v)
  local out = {}
  for w in string.gmatch(TK.str(v), '%S+') do out[#out + 1] = w end
  return out
end

-- TM.without: list l less x.
function TM.without(l, x)
  local out = {}
  for _, v in ipairs(l) do
    if v ~= x then out[#out + 1] = v end
  end
  return out
end

-- TM.set_reads: the primary's reads list (its live read copies) is l.
function TM.set_reads(id, l)
  if #l == 0 then
    redis.call('HDEL', 'task:' .. id, 'reads')
  else
    redis.call('HSET', 'task:' .. id, 'reads', table.concat(l, ' '))
  end
end

-- TM.drop: copy cid, when it is live in its consumer's set, retires to
-- fail with why (a copy its primary no longer wants). Returns true when it
-- was live.
function TM.drop(cid, why, by)
  local r = redis.call('HMGET', 'task:' .. cid, 'consumer', 'where', 'stream')
  local c, w = TK.str(r[1]), TK.str(r[2])
  if TM.parse(c) and TM.LIVE[w] and redis.call('ZSCORE', TM.key(c, w), cid) then
    TM.retire(cid, c, w, 'fail', why, {}, by, r[3])
    return true
  end
  return false
end

-- TM.retire_reads: every open read copy of primary id retires to fail with
-- why, and the primary's reads list empties.
function TM.retire_reads(id, why, by)
  for _, cid in ipairs(TM.words(redis.call('HGET', 'task:' .. id, 'reads'))) do TM.drop(cid, why, by) end
  redis.call('HDEL', 'task:' .. id, 'reads')
end

-- TM.pr_head: the head the PR record of primary id holds now ('' when it
-- names no PR or the record has no head).
function TM.pr_head(id)
  local f = redis.call('HMGET', 'task:' .. id, 'repo', 'pr')
  local pr = TK.str(f[2])
  if pr == '' or pr == '0' then return '' end
  return TK.str(redis.call('HGET', 'pr:' .. TM.bare(f[1]) .. ':' .. pr, 'head'))
end

-- TM.prok: the checks an ok with a PR makes (#3488): the PR record exists
-- at the ended head, on the copy's base. Returns nil and the repo, or the
-- refusal.
function TM.prok(pid, get, base)
  if TK.str(get.head) == '' then return 'HEAD an ok with a PR names its --head' end
  local repo = TK.str(get.repo)
  if repo == '' then repo = TK.str(redis.call('HGET', 'task:' .. pid, 'repo')) end
  if repo == '' then return 'REPO an ok with a PR names its repo (--pr <repo>#<n>)' end
  local pk = 'pr:' .. TM.bare(repo) .. ':' .. get.pr
  local rec = redis.call('HMGET', pk, 'head', 'base')
  if not rec[1] then return 'NOPR ' .. pk .. ' has no record; record the PR first (nova-sprint pr record)' end
  if rec[1] ~= get.head then return 'PRHEAD ' .. pk .. ' head is ' .. rec[1] .. ', not --head ' .. get.head end
  base = TK.str(base)
  if base ~= '' and TK.str(rec[2]) ~= '' and rec[2] ~= base then
    return 'PRBASE ' .. pk .. ' base is ' .. rec[2] .. ', not the card base ' .. base
  end
  return nil, repo
end

-- TM.finish(id, o): card end of copy id. o.outcome ok|fail, o.why, o.fields
-- (result k v pairs; pr and repo name the PR), o.sha (done-already), o.score
-- and o.reader (a read), o.line (the SCORE line read post stored: it is the
-- line and the fix copy's finding), o.post (read post: a ready read copy
-- may end), o.by, o.dry, o.keep (a cancel's give-back: the primary returns
-- without counting an attempt). Returns nil and {copy, primary, from, to,
-- next (the copies cut, comma-joined)} or the refusal; with o.dry nil when
-- it would end.
function TM.finish(id, o)
  if not TK.copy_id(id) then return 'NOTCOPY ' .. TK.str(id) .. ' is not a consumer copy (<id>~<n>)' end
  local r = redis.call('HMGET', 'task:' .. id, 'primary', 'consumer', 'where', 'leg', 'stream', 'end_sig', 'token',
    'lease_until', 'base', 'head')
  local pid, c, w, leg = r[1], TK.str(r[2]), TK.str(r[3]), TK.str(r[4])
  if not pid then return 'NOCOPY task:' .. id end
  local f = o.fields or {}
  local get = {}
  for i = 1, #f - 1, 2 do
    if not TM.RESULT[f[i]] then return 'FIELD ' .. TK.str(f[i]) .. ' is not a result field' end
    get[f[i]] = f[i + 1]
  end
  -- #3488: a repeat end with the same evidence is ALREADY (no second move);
  -- other evidence on an ended copy is a CONFLICT; a stale token or lease
  -- is FENCED. A fix copy a new head ended (TM.rehead) takes its own ok at
  -- that head as the same end.
  local sig = TM.sig(o)
  if not TM.LIVE[w] then
    if TK.str(r[6]) == sig or (leg == 'fix' and w == 'ok' and o.outcome == 'ok' and TK.str(get.head) ~= '' and
        get.head == TK.str(r[10])) then
      return nil, { copy = id, primary = pid, from = w, to = 'already', next = '', already = true }
    end
    return 'CONFLICT copy ' .. id .. ' ended ' .. w .. ' with other evidence (' .. TK.str(r[6]) .. ')'
  end
  if TK.str(o.token) ~= '' then
    if TK.str(r[7]) ~= o.token then return 'FENCED copy ' .. id .. ' token is not ' .. o.token end
    if (tonumber(r[8]) or 0) < cm_now() then return 'FENCED copy ' .. id .. ' lease lapsed' end
  end
  if w == 'ready' and o.outcome ~= 'fail' and not (o.post and leg == 'read') then
    return 'READY copy ' .. id .. ' is ready: card work it first'
  end
  if not redis.call('ZSCORE', TM.key(c, w), id) then
    return 'DRIFT unlinked ' .. TM.key(c, w) .. ' ' .. id .. '; run nova-sprint card fsck --repair'
  end
  local p = TK.read(pid)
  if not p then return 'DRIFT copy ' .. id .. ' names no primary task:' .. pid end
  local reads = TM.words(p.reads)
  local isread = false
  for _, x in ipairs(reads) do isread = isread or x == id end
  if p.copy ~= id and not isread then
    return 'DRIFT task:' .. pid .. ' names copy ' .. (p.copy == '' and '-' or p.copy) .. ' and reads ' ..
      (p.reads == '' and '-' or p.reads) .. ', not ' .. id
  end
  -- a read or fix copy's primary is in review (a fix copy cut before the
  -- review column may find it working); a work copy's is working
  local want = { working = true }
  if leg == 'read' then want = { review = true } elseif leg == 'fix' then want = { review = true, working = true } end
  if not want[p.where] then
    return 'DRIFT task:' .. pid .. ' is ' .. p.where .. '; its ' .. leg .. ' copy wants ' ..
      (leg == 'work' and 'working' or 'review')
  end
  local pf = { 'last_copy', id }
  for _, v in ipairs(f) do pf[#pf + 1] = v end
  local cf = {}
  local to, pok, why, sha = nil, nil, TK.str(o.why), TK.str(o.sha)
  local outcome = o.outcome
  -- stay: the primary stays in review (its pointers change, it does not
  -- move); fix: the consumer a fix copy is cut on; recut: fresh read copies
  -- are cut after the open ones retire; rehead: the PR's head moved
  local line, fix, finding, stay, recut, rehead, score
  -- review: a fail goes to review (#4072) with the evidence; a copy given
  -- back (o.keep) is not a fail
  local review
  if leg == 'read' then
    if outcome == 'fail' then
      if o.keep then
        to, stay = 'review', true
        if why == '' then why = 'read given back' end
      else
        to, review = 'review', { consumer = c, copy = id }
        if why == '' then why = 'read failed' end
      end
    else
      score = tonumber(o.score)
      if not score or score < 1 or score > 10 or score ~= math.floor(score) then
        return 'SCORE a read copy ends with --score N/10, N 1-10 (or --fail <why>)'
      end
      local pr = redis.call('HMGET', 'task:' .. pid, 'head', 'repo', 'pr', 'author')
      local chead = TK.str(r[10])
      if chead == '' then chead = TK.str(pr[1]) end
      local head = TK.str(get.head)
      if head == '' then head = chead end
      local now = TM.pr_head(pid)
      if now ~= '' and chead ~= '' and now ~= chead then
        -- (#4094 c) the PR moved past the head this copy reads: its score
        -- does not count; the open copies retire and fresh ones are cut
        outcome, to, stay, rehead = 'fail', 'review', true, now
        why = 'head moved to ' .. string.sub(now, 1, 12)
      else
        local reader = TK.str(o.reader)
        if reader == '' then reader = select(2, TM.parse(c)) end
        local text = 'SCORE who=' .. reader .. ' head=' .. head .. ' score=' .. score .. '/10'
        if TK.str(get.gates) ~= '' then text = text .. ' gates=' .. get.gates end
        if TK.str(get.finding) ~= '' then text = text .. ': ' .. string.gsub(get.finding, '[\r\n]+', ' ') end
        if TK.str(o.line) ~= '' then text = o.line end
        line = { line = text, repo = pr[2], pr = pr[3] }
        pf[#pf + 1] = 'score'
        pf[#pf + 1] = tostring(score)
        pf[#pf + 1] = 'read_by'
        pf[#pf + 1] = c
        cf = { 'score', tostring(score) }
        if score >= TM.PASS then
          -- CI gates the read copy (#3093): a passing read is at a head whose
          -- CI is OK; the primary never takes a CI word by itself
          local final, cwhy = TM.ci_final(pr[2], pr[3], head)
          if final == '' then
            return 'CIPENDING ' .. string.sub(head, 1, 12) .. ' has no CI verdict yet; a read passes only at a head CI calls OK'
          elseif final ~= 'OK' then
            return 'CIRED ' .. string.sub(head, 1, 12) .. ' CI is ' .. final .. (cwhy ~= '' and (' (' .. cwhy .. ')') or '') ..
              '; end the read under ' .. TM.PASS .. ' with the failure as its --finding'
          end
          to = 'merging'
          if why == '' then why = 'read ' .. score .. '/10' end
        else
          -- #4097: the copy ends ok with its finding; the primary stays in
          -- review and one fix copy carries the SCORE line to the author
          finding = text
          if TK.str(get.finding) == '' then
            cf[#cf + 1] = 'finding'
            cf[#cf + 1] = text
          end
          why = TK.str(get.finding)
          if why == '' then why = text end
          -- a second read under TM.PASS is the author's fail (#4072): review
          local author = TK.str(pr[4])
          local low = (tonumber(redis.call('HGET', 'task:' .. pid, 'low_reads')) or 0) + 1
          pf[#pf + 1] = 'low_reads'
          pf[#pf + 1] = tostring(low)
          if low >= 2 then
            to, stay = 'review', false
            review = { consumer = author ~= '' and author or c, copy = TK.str(redis.call('HGET', 'task:' .. pid, 'last_copy')),
              shape = 'read-under-8-twice', read = tostring(score), demote = TM.parse(author) ~= nil }
          else
            to, stay = 'review', true
            if p.copy == '' or p.copy == id then
              fix = TM.fix_route(pid, author)
              if not fix then to, stay = 'waiting', false end
            end
          end
        end
      end
    end
  elseif leg == 'fix' and p.where == 'review' then
    if outcome == 'ok' then
      local prn = TK.str(get.pr)
      if sha ~= '' then
        to = 'landed'
        if why == '' then why = 'done-already ' .. sha end
      elseif prn ~= '' and prn ~= '0' then
        local err, repo = TM.prok(pid, get, r[9])
        if err then return err end
        pf[#pf + 1] = 'author'
        pf[#pf + 1] = c
        if why == '' then why = 'fix pr ' .. TM.bare(repo) .. '#' .. prn .. ' head ' .. string.sub(get.head, 1, 12) end
        to, stay, recut = 'review', true, true
      else
        if why == '' then why = 'fix ok' end
        to, stay, recut = 'review', true, true
      end
    else
      if why == '' then why = 'fix failed' end
      -- a fix copy's fail goes to review (#4072); given back, the primary
      -- stays in review
      to, stay = 'review', true
      if not o.keep then to, stay, review = 'review', false, { consumer = c, copy = id } end
    end
  elseif outcome == 'ok' then
    local prn = TK.str(get.pr)
    if sha ~= '' then
      to = 'landed'
      if why == '' then why = 'done-already ' .. sha end
    elseif prn ~= '' and prn ~= '0' then
      local err, repo = TM.prok(pid, get, r[9])
      if err then return err end
      pf[#pf + 1] = 'author'
      pf[#pf + 1] = c
      -- the build copy's ok is what moves the primary to review; its read
      -- copies are cut in this same call (TM.after_move; CI gates those
      -- reads, not this move)
      to = 'review'
      if why == '' then why = 'ok pr ' .. TM.bare(repo) .. '#' .. prn .. ' head ' .. string.sub(get.head, 1, 12) end
    else
      to, pok = 'done', 'ok'
    end
  else
    local n = tonumber(p.attempts) or 0
    if not o.keep then n = n + 1 end
    pf[#pf + 1] = 'attempts'
    pf[#pf + 1] = tostring(n)
    if why == '' then why = 'fail' end
    -- a fail goes to review (#4072); a copy given back returns its primary
    to = 'waiting'
    if not o.keep then to, review = 'review', { consumer = c, copy = id } end
  end
  -- the primary's copy pointer clears when it names this copy; a live fix
  -- copy stays named while a read of the same head ends
  local mo = { by = o.by, why = why, ok = pok, fields = pf, dry = o.dry, review = review ~= nil }
  if p.copy == id or not stay then mo.copy = '' end
  if sha ~= '' then mo.sha = sha end
  if score then mo.reads_why = 'superseded: ' .. id .. ' read ' .. score .. '/10' end
  if o.dry then
    if stay then return nil end
    return TK.move(pid, to, mo)
  end
  for _, v in ipairs(f) do cf[#cf + 1] = v end
  cf[#cf + 1] = 'end_sig'
  cf[#cf + 1] = sig
  TM.retire(id, c, w, outcome, why, cf, o.by, r[5])
  if isread then TM.set_reads(pid, TM.without(reads, id)) end
  if review then
    -- the evidence and the first pass go onto the primary with its move
    -- (mo.fields is pf); the author's copy of a twice-failed read is a fail
    for _, v in ipairs(TM.evidence(pid, review, leg, why)) do pf[#pf + 1] = v end
    if review.demote and review.copy ~= '' then
      TM.demote(review.copy, 'read under ' .. TM.PASS .. ' twice: ' .. why, o.by, r[5])
    end
  end
  local nxt, err = {}, nil
  if rehead then
    err, nxt = TM.rehead(pid, rehead, o.by)
  elseif stay then
    err = TK.move(pid, 'review', mo)
    if not err and fix then
      local n = (tonumber(redis.call('HGET', 'task:' .. pid, 'fix_rounds')) or 0) + 1
      local cid
      err, cid = TM.cut(fix, pid, 'fix', { by = o.by, why = why, to = 'review', fields = { 'fix_rounds', tostring(n) },
        extra = { 'finding', finding } })
      nxt = { cid }
    elseif not err and finding and p.copy ~= '' and p.copy ~= id then
      -- one fix copy per head: a second finding joins the live one's brief
      local old = TK.str(redis.call('HGET', 'task:' .. p.copy, 'finding'))
      redis.call('HSET', 'task:' .. p.copy, 'finding', old == '' and finding or (old .. '\n' .. finding))
    end
    if not err and recut then
      TM.retire_reads(pid, why, o.by)
      err, nxt = TM.cut_reads(pid, o.by, why)
    end
  else
    local info
    err, info = TK.move(pid, to, mo)
    if info and info.cut then nxt = info.cut end
    -- the first 8+ supersedes a fix copy still out on the same head
    if not err and to == 'merging' and p.copy ~= '' and p.copy ~= id then TM.drop(p.copy, mo.reads_why, o.by) end
  end
  if err then return 'DRIFT-AFTER ' .. err end
  if TK.str(get.pr) ~= '' and NS.tref then NS.tref.index(pid) end
  if line then TM.pr_line(line.repo, line.pr, line.line, o.line ~= nil) end
  return nil, { copy = id, primary = pid, from = p.where, to = to, next = table.concat(nxt or {}, ',') }
end

-- TM.shape: the failure shape of an end's evidence (#4072): the one word
-- the same-shape counts are kept by.
function TM.shape(why, exit, leg)
  if why == 'lease lapsed' then return 'lease-lapsed' end
  if exit ~= '' and exit ~= '0' then return 'exit-' .. exit end
  if leg == 'read' then return 'read-fail' end
  local w = string.lower(string.match(why, '^%s*([%w_.-]+)') or '')
  if w == '' then return 'fail' end
  return w
end

-- TM.evidence(pid, rv, leg, why): the review fields of primary pid, read
-- from the failed copy's record (rv.copy: its end's model, exit, typed
-- lines and wall) and the primary's PR and read, with the mechanical first
-- pass: the shape, its count for this card (task:<pid>:shapes) and for
-- this consumer (<consumer>:shapes), and a suggested verdict on a
-- REVIEW-JEV line. A suggestion is never a verdict: only TM.review moves.
function TM.evidence(pid, rv, leg, why)
  local cp = redis.call('HMGET', 'task:' .. rv.copy, 'model', 'exit', 'line1', 'line2', 'wall', 'leg')
  local pr = redis.call('HMGET', 'task:' .. pid, 'repo', 'pr', 'score')
  local line = TK.str(cp[4])
  if line == '' then line = TK.str(cp[3]) end
  local exit = TK.str(cp[2])
  local rleg = TK.str(cp[6])
  if rleg == '' then rleg = leg end
  local shape = rv.shape or TM.shape(why, exit, leg)
  local nc = redis.call('HINCRBY', 'task:' .. pid .. ':shapes', shape, 1)
  local nk = redis.call('HINCRBY', rv.consumer .. ':shapes', shape, 1)
  local suggest = 'redeal'
  if shape == 'read-under-8-twice' or nc >= 3 then
    suggest = 'recut'
  elseif nc >= 2 and nk >= 2 then
    suggest = 'reassign'
  end
  local prref = ''
  if TK.str(pr[2]) ~= '' and TK.str(pr[2]) ~= '0' then prref = TM.bare(pr[1]) .. '#' .. pr[2] end
  local jev = 'REVIEW-JEV id=' .. pid .. ' consumer=' .. rv.consumer .. ' shape=' .. shape .. ' same_card=' .. nc ..
    ' same_consumer=' .. nk .. ' suggest=' .. suggest
  return { 'review_at', tostring(cm_now()), 'review_copy', rv.copy, 'review_consumer', rv.consumer, 'review_leg', rleg,
    'review_model', TK.str(cp[1]), 'review_exit', exit, 'review_line', line, 'review_wall', TK.str(cp[5]),
    'review_why', why, 'review_pr', prref, 'review_read', rv.read or TK.str(pr[3]), 'review_shape', shape,
    'same_shape', tostring(nc), 'same_shape_consumer', tostring(nk), 'review_jev', jev }
end

-- TM.demote: a retired copy that ended ok moves to its consumer's fail set
-- (same score, its ended_at): the author's copy whose PR read under TM.PASS
-- twice (#4071: a friend's copy is ok only when its PR reads 8+).
function TM.demote(id, why, by, stream)
  local c = TK.str(redis.call('HGET', 'task:' .. id, 'consumer'))
  local score = TM.parse(c) and redis.call('ZSCORE', TM.key(c, 'ok'), id)
  if not score then return end
  redis.call('ZREM', TM.key(c, 'ok'), id)
  cm_zadd(TM.key(c, 'fail'), score, id)
  redis.call('HSET', 'task:' .. id, 'where', 'fail', 'outcome', 'fail', 'why', why)
  TM.log(id, stream, c .. ':ok', c .. ':fail', by, why, c)
end

-- TM.review(by, id, verdict, why): the one way out of review (#4072), a
-- typed verdict with a why: recut -> waiting, redeal -> ready, drop ->
-- landed (outcome=dropped), reassign:<consumer> -> a copy cut on that
-- consumer (a work copy, or a read copy when the failed copy was a read).
-- Its REVIEW line goes on the record, and TM.CARRY takes it to the next
-- copy. Returns REVIEWED id verdict where copy | REFUSED why.
function TM.review(by, id, verdict, why)
  verdict, why = TK.str(verdict), TK.str(why)
  local v, target = string.match(verdict, '^(%a+):?(.*)$')
  if not v or not TM.VERDICTS[v] or (v ~= 'reassign' and target ~= '') then
    return { 'REFUSED', 'VERDICT ' .. verdict .. ' is not recut|redeal|reassign:<consumer>|drop' }
  end
  if v == 'reassign' and not TM.parse(target) then
    return { 'REFUSED', 'VERDICT reassign names its consumer: reassign:bench:<b>|friend:<f>' }
  end
  if why == '' then return { 'REFUSED', 'WHY a review verdict needs a why' } end
  if type(id) ~= 'string' or id == '' or TK.copy_id(id) then
    return { 'REFUSED', 'COPY ' .. TK.str(id) .. ' is not a primary; review post names the primary' }
  end
  local p = TK.read(id)
  if not p then return { 'REFUSED', 'NOTASK task:' .. id } end
  if p.where ~= 'review' then
    return { 'REFUSED', 'NOTREVIEW task:' .. id .. ' is ' .. (p.where == '' and 'null' or p.where) .. ', not review' }
  end
  local line = 'REVIEW verdict=' .. verdict .. ' by=' .. TK.str(by) .. ': ' .. string.gsub(why, '[\r\n]+', ' ')
  local fields = { 'review', line, 'review_verdict', v, 'reviewed_by', TK.str(by), 'reviewed_at', tostring(cm_now()) }
  local mwhy = 'review ' .. v .. ': ' .. why
  if v == 'reassign' then
    local d = TM.desired(target)
    if not d.slots then return { 'REFUSED', 'SLOTS ' .. target .. ':desired has no slots' } end
    local leg = 'work'
    if TK.str(redis.call('HGET', 'task:' .. id, 'review_leg')) == 'read' then leg = 'read' end
    local err = TM.may(target, d, id, leg) or TM.cut(target, id, leg, { by = by, why = mwhy, fields = fields,
      verdict = v, dry = true })
    if err then return { 'REFUSED', err } end
    local cid
    err, cid = TM.cut(target, id, leg, { by = by, why = mwhy, fields = fields, verdict = v })
    if err then return { 'REFUSED', 'DRIFT-AFTER ' .. err } end
    return { 'REVIEWED', id, v, leg == 'read' and 'review' or 'working', cid }
  end
  if v == 'drop' then
    fields[#fields + 1] = 'outcome'
    fields[#fields + 1] = 'dropped'
  end
  local err = TK.move(id, TM.VERDICTS[v], { by = by, why = mwhy, copy = '', verdict = v, fields = fields })
  if err then return { 'REFUSED', err } end
  return { 'REVIEWED', id, v, TM.VERDICTS[v], '' }
end

-- TM.bare: the bare repository name of owner/name or name (prkey.Name).
function TM.bare(repo)
  return string.match(TK.str(repo), '([^/]+)$') or ''
end

-- TM.ci_final: the CI word for a PR head: FAIL when the PR record names
-- another head (a head change), else the request record's final (ci
-- run, #3597) or the ci cards' verdicts at the head (ci.lua): OK, FAIL, or
-- '' while pending. Returns the word and why.
function TM.ci_final(repo, pr, head)
  local bare = TM.bare(repo)
  local now = TK.str(redis.call('HGET', 'pr:' .. bare .. ':' .. TK.str(pr), 'head'))
  if now ~= '' and now ~= head then return 'FAIL', 'head moved to ' .. string.sub(now, 1, 12) end
  for _, r in ipairs({ bare, TK.str(repo) }) do
    local f = redis.call('HMGET', 'ci:' .. r .. ':' .. head, 'final', 'why')
    if TK.str(f[1]) == 'OK' or TK.str(f[1]) == 'FAIL' then return f[1], TK.str(f[2]) end
  end
  local word = ''
  for _, gid in ipairs(redis.call('SMEMBERS', 'ci:' .. TK.str(repo) .. ':' .. head .. ':gids')) do
    local v = TK.str(redis.call('HGET', 'ci:' .. TK.str(repo) .. ':' .. head .. ':' .. gid, 'verdict'))
    if v == 'FAIL' then return 'FAIL', 'ci card verdict FAIL' end
    if v == 'OK' then word = 'OK' end
  end
  return word, ''
end

-- TM.live: the consumer's beat (its bench beat, or its friend row) is
-- within TM.LIVE_MS.
function TM.live(c)
  local key = c
  if TM.parse(c) == 'bench' then key = c .. ':beat' end
  local at = TK.ms(redis.call('HGET', key, 'at'))
  return at ~= nil and math.abs(cm_now() - at) < TM.LIVE_MS
end

-- TM.role: friend name holds role in its friend:<f>:roles csv (reader,
-- builder, coordinator, may-hold).
function TM.role(name, role)
  for x in string.gmatch(TK.str(redis.call('HGET', 'friend:' .. name .. ':roles', 'roles')), '[^,%s]+') do
    if x == role then return true end
  end
  return false
end

-- TM.room: consumer c's desired slots less the CI legs running on it
-- (TM.ci_legs, nova-tools#4293) less its working and ready copies, and
-- whether it may be dealt at all (slots declared, not paused, not down, its
-- beat live).
function TM.room(c, d)
  if not d.slots or d.paused or redis.call('EXISTS', c .. ':down') == 1 or not TM.live(c) then return nil end
  return d.slots - TM.ci_legs(c) - redis.call('ZCARD', TM.key(c, 'working')) - redis.call('ZCARD', TM.key(c, 'ready'))
end

-- TM.read_route(id): the consumers primary id's read copies go to (#4094):
-- the friend with the reader role that may read it (never the author:
-- TM.may) with the most open slots, as one copy; else up to TM.SWARM_READS
-- copies over the swarm's live benches that may read it and have room
-- (slots less working less ready, #4270), most room first (by id on a
-- tie), round-robin, never more per bench than its room; fewer than
-- TM.SWARM_READS when the room runs out, {} when there is no reader with
-- room at all (the deal pass cuts them when room comes: TM.ensure).
function TM.read_route(id)
  local friend, froom, benches = nil, 0, {}
  for _, c in ipairs(TM.roster()) do
    local kind, name = TM.parse(c)
    if kind == 'bench' or TM.role(name, 'reader') then
      local d = TM.desired(c)
      local room = TM.room(c, d)
      if room and room > 0 and not TM.may(c, d, id, 'read') then
        if kind == 'friend' then
          if room > froom then friend, froom = c, room end
        else
          benches[#benches + 1] = { c = c, room = room }
        end
      end
    end
  end
  if friend then return { friend } end
  table.sort(benches, function(a, b) return a.room > b.room or (a.room == b.room and a.c < b.c) end)
  local out = {}
  while #out < TM.SWARM_READS do
    local any = false
    for _, b in ipairs(benches) do
      if #out < TM.SWARM_READS and b.room > 0 then
        out[#out + 1] = b.c
        b.room = b.room - 1
        any = true
      end
    end
    if not any then break end
  end
  return out
end

-- TM.fix_route(id, author): the consumer a fix copy of primary id goes to
-- (#4097): the author when a friend; for a swarm author (a bench) the swarm
-- route, the live bench with the most room that may take it (the author's
-- own bench when none has room); with no author, the coordinator (the first
-- friend with the coordinator role); nil when there is none.
function TM.fix_route(id, author)
  local kind = TM.parse(author)
  if kind == 'friend' then return author end
  if kind == 'bench' then
    local best, room = nil, 0
    for _, c in ipairs(TM.roster()) do
      if TM.parse(c) == 'bench' then
        local d = TM.desired(c)
        local r = TM.room(c, d)
        if r and r > room and not TM.may(c, d, id, 'fix') then best, room = c, r end
      end
    end
    return best or author
  end
  local friends = redis.call('SMEMBERS', 'friends')
  table.sort(friends)
  for _, f in ipairs(friends) do
    if TM.role(f, 'coordinator') then return 'friend:' .. f end
  end
  return nil
end

-- TM.cut_reads(id, by, why): primary id's read copies on its read route
-- (only consumers with room, at most their room each: #4270), through the
-- one cut. Returns nil and the copy ids, or the refusal.
function TM.cut_reads(id, by, why)
  local out = {}
  for _, c in ipairs(TM.read_route(id)) do
    local err, cid = TM.cut(c, id, 'read', { by = by, why = why })
    if err then return err end
    out[#out + 1] = cid
  end
  return nil, out
end

-- TM.after_move(id, cur, to, o) is TK.hook: TK.move calls it after every
-- move of a primary (a task no friend holds). A primary that enters
-- review has its read copies cut in the same call (#4094: by card end
-- --ok --pr, a harvest or a rebase copy returning, whichever way it comes
-- in); one that leaves review retires the read copies still open
-- (o.reads_why, else the move). Returns nil and the copies cut, or the
-- refusal.
function TM.after_move(id, cur, to, o)
  if cur.where == 'review' and to ~= 'review' then
    TM.retire_reads(id, o.reads_why or ('primary moved to ' .. to), o.by)
  end
  -- a copy's fail enters review for a verdict (o.review): no reads to cut
  if to == 'review' and cur.where ~= 'review' and not o.review then
    if NS.tref then NS.tref.index(id) end
    return TM.cut_reads(id, o.by, o.why)
  end
  return nil
end
TK.hook = TM.after_move

-- TM.rehead(id, head, by): the PR of primary id (in review) moved to head
-- (#4094 c): its live fix copy ends ok (the new head is that copy's
-- result), any other live copy and every open read copy retires to fail,
-- the primary takes the head, and fresh read copies are cut. Returns nil
-- and the copies cut, or the refusal.
function TM.rehead(id, head, by)
  local p = TK.read(id)
  if not p or p.where ~= 'review' then return nil, {} end
  local why = 'head moved to ' .. string.sub(head, 1, 12)
  if p.copy ~= '' then
    local r = redis.call('HMGET', 'task:' .. p.copy, 'consumer', 'where', 'leg', 'stream')
    local c, w = TK.str(r[1]), TK.str(r[2])
    if TM.parse(c) and TM.LIVE[w] and redis.call('ZSCORE', TM.key(c, w), p.copy) then
      if r[3] == 'fix' then
        TM.retire(p.copy, c, w, 'ok', 'new head ' .. string.sub(head, 1, 12), { 'head', head }, by, r[4])
      else
        TM.retire(p.copy, c, w, 'fail', why, {}, by, r[4])
      end
    end
  end
  TM.retire_reads(id, why, by)
  local err = TK.move(id, 'review', { by = by, why = why, copy = '', fields = { 'head', head } })
  if err then return err end
  return TM.cut_reads(id, by, why)
end

-- TM.pending: TK.pending (a fail's review awaiting its verdict).
TM.pending = TK.pending

-- TM.in_review(): every primary in some ws:<stream>:review set, in stream
-- rank order, oldest first.
function TM.in_review()
  local out = {}
  for _, s in ipairs(TM.streams()) do
    for _, id in ipairs(redis.call('ZRANGE', 'ws:' .. s .. ':review', 0, -1)) do
      if not TK.card_id(id) and not TK.copy_id(id) then out[#out + 1] = id end
    end
  end
  return out
end

-- TM.ensure(by): the review column's duty (#4094 DONE-WHEN 4), run on
-- every deal pass: a primary in review whose PR head moved is re-headed;
-- one with no live copy (a lapsed or failed read, a fix given back, no
-- reader when it came in) has its read copies cut. Returns READS n, then
-- per primary its id and the copies cut, comma-joined.
function TM.ensure(by)
  local out = { 'READS', '0' }
  for _, id in ipairs(TM.in_review()) do
    local p = TK.read(id)
    if p and p.friend == '' and p.where == 'review' and not TM.pending(id) then
      local now, err, cut = TM.pr_head(id), nil, nil
      if now ~= '' and p.head ~= '' and now ~= p.head then
        err, cut = TM.rehead(id, now, by)
      elseif p.copy == '' and p.reads == '' then
        err, cut = TM.cut_reads(id, by, 'review with no live copy')
      end
      if err then return { 'REFUSED', 'DRIFT-AFTER ' .. err } end
      if cut and #cut > 0 then
        out[#out + 1] = id
        out[#out + 1] = table.concat(cut, ',')
      end
    end
  end
  out[2] = tostring((#out - 2) / 2)
  return out
end

-- TM.pr_primaries(repo, n): the primaries in review whose PR is repo#n,
-- from the ref index (01_task_ref.lua; never a scan).
function TM.pr_primaries(repo, n)
  local out = {}
  if not NS.tref or not tonumber(n) then return out end
  n = tostring(tonumber(n))
  for _, id in ipairs(NS.tref.ids({ TM.bare(repo) .. '#' .. n })) do
    if not TK.card_id(id) and not TK.copy_id(id) then
      local p = TK.read(id)
      if p and p.friend == '' and p.where == 'review' and TK.str(redis.call('HGET', 'task:' .. id, 'pr')) == n then
        out[#out + 1] = p
        p.id = id
      end
    end
  end
  return out
end

-- TM.head(repo, n, by): pr record --head of repo#n: every primary of the PR
-- in review at another head is re-headed (TM.rehead). Returns REHEAD n,
-- then per primary its id and the copies cut, comma-joined.
function TM.head(repo, n, by)
  local out = { 'REHEAD', '0' }
  local now = TK.str(redis.call('HGET', 'pr:' .. TM.bare(repo) .. ':' .. TK.str(n), 'head'))
  if now == '' then return out end
  for _, p in ipairs(TM.pr_primaries(repo, n)) do
    if p.head ~= now then
      local err, cut = TM.rehead(p.id, now, by)
      if err then return { 'REFUSED', 'DRIFT-AFTER ' .. err } end
      out[#out + 1] = p.id
      out[#out + 1] = table.concat(cut, ',')
    end
  end
  out[2] = tostring((#out - 2) / 2)
  return out
end

-- TM.reader_copy(p, who, by): the live read copy of primary p that reader
-- who's SCORE ends: the one on who's consumer; else, for a who that is no
-- friend (a swarm reader posting under its model's name), the oldest on a
-- bench; else, for a friend, a read copy cut for it now (TM.may: never the
-- author, read_who, readers). Returns the copy id, or nil and why.
function TM.reader_copy(p, who, by)
  local ids = TM.words(p.reads)
  if p.copy ~= '' and TK.str(redis.call('HGET', 'task:' .. p.copy, 'leg')) == 'read' then ids[#ids + 1] = p.copy end
  local bench
  for _, cid in ipairs(ids) do
    local kind, name = TM.parse(TK.str(redis.call('HGET', 'task:' .. cid, 'consumer')))
    if name == who then return cid end
    if kind == 'bench' and not bench then bench = cid end
  end
  if redis.call('SISMEMBER', 'friends', who) == 0 then
    if bench then return bench end
    return nil, 'NOCOPY no live read copy for ' .. who
  end
  local c = 'friend:' .. who
  local err = TM.may(c, TM.desired(c), p.id, 'read')
  if err then return nil, err end
  local cid
  err, cid = TM.cut(c, p.id, 'read', { by = by, why = 'read post by ' .. who })
  if err then return nil, err end
  return cid
end

-- TM.score(repo, n, line, by): read post's SCORE line on repo#n (#4094 3,
-- #4097), in the same call as the post: for every primary of the PR in
-- review, a SCORE at the record head (re-heading the primary first when it
-- is behind) ends the poster's read copy through TM.finish (8+: the
-- primary -> merging; under 8: a fix copy); a SCORE at another head, by
-- jev, or with no score= moves nothing and is a note. Returns moved (the
-- primaries advanced), cut (the copies cut) and notes.
function TM.score(repo, n, line, by)
  local res = { moved = 0, cut = 0, notes = {} }
  local first = string.match(line, '^[^\n]*')
  local who = string.match(first, 'who=([^%s:;,]+)') or ''
  local head = string.match(first, 'head=(%x+)') or ''
  local score = tonumber(string.match(first, '%sscore=(%d+)'))
  local ps = TM.pr_primaries(repo, n)
  if #ps == 0 then return res end
  local now = TK.str(redis.call('HGET', 'pr:' .. TM.bare(repo) .. ':' .. TK.str(n), 'head'))
  for _, p in ipairs(ps) do
    local id = p.id
    local function note(s) res.notes[#res.notes + 1] = id .. ': ' .. s end
    if not score or who == '' or string.sub(who, 1, 3) == 'jev' then
      note('a SCORE moves a primary only with who= (not jev) and score=N')
    elseif #head < 7 or string.sub(now, 1, #head) ~= head then
      note('SCORE at ' .. string.sub(head, 1, 12) .. ' is not the record head ' .. string.sub(now, 1, 12))
    else
      local ok = true
      if p.head ~= now then
        local err, cut = TM.rehead(id, now, by)
        if err then
          note(err)
          ok = false
        else
          res.cut = res.cut + #cut
          p = TK.read(id)
          p.id = id
        end
      end
      if ok then
        local cid, why = TM.reader_copy(p, who, by)
        if not cid then
          note(why)
        else
          local err, info = TM.finish(cid, { outcome = 'ok', score = score, reader = who, post = true, line = first,
            by = by, fields = { 'head', now } })
          if err then
            note(err)
          else
            if info.to == 'merging' then res.moved = res.moved + 1 end
            for _ in string.gmatch(info.next, '[^,]+') do res.cut = res.cut + 1 end
          end
        end
      end
    end
  end
  return res
end

-- TM.assign(c, id, revoke, by, why): the primary's copy goes to consumer c
-- (#2940). A primary with a live copy is refused unless revoke: then, in
-- this one call, the live copy is given back (retired to fail, so its
-- holder's end or beat is refused: the fence; the slot frees) and a new
-- copy is cut on c.
function TM.assign(c, id, revoke, by, why)
  if not TM.parse(c) then return { 'REFUSED', 'CONSUMER ' .. TK.str(c) .. ' is not bench:<b> or friend:<f>' } end
  local p = TK.read(id)
  if not p then return { 'REFUSED', 'NOTASK task:' .. id } end
  local d = TM.desired(c)
  local old = p.copy
  if old ~= '' then
    if not revoke then
      return { 'REFUSED', 'LIVECOPY task:' .. id .. ' has live copy ' .. old .. '; assign --revoke moves it' }
    end
    local leg = TK.str(redis.call('HGET', 'task:' .. old, 'leg'))
    if leg ~= 'read' then leg = 'work' end
    local err = TM.may(c, d, id, leg) or TM.finish(old, { outcome = 'fail', keep = true, why = 'revoked: ' .. TK.str(why),
      by = by, dry = true })
    if err then return { 'REFUSED', err } end
    err = TM.finish(old, { outcome = 'fail', keep = true, why = 'revoked: ' .. TK.str(why), by = by })
    if err then return { 'REFUSED', err } end
    p = TK.read(id)
  end
  local leg, err = TM.leg(id, p)
  err = err or TM.may(c, d, id, leg)
  if not err then
    local cid
    err, cid = TM.cut(c, id, leg, { by = by, why = 'assign ' .. c })
    if not err then return { 'ASSIGNED', id, cid, old } end
  end
  if old ~= '' then return { 'REFUSED', 'DRIFT-AFTER ' .. err } end
  return { 'REFUSED', err }
end

-- TM.sig: an end's evidence, compared on a repeat end (#3488).
function TM.sig(o)
  local f, get = o.fields or {}, {}
  for i = 1, #f - 1, 2 do get[f[i]] = f[i + 1] end
  return table.concat({ TK.str(o.outcome), TK.str(get.repo), TK.str(get.pr), TK.str(get.head), TK.str(o.sha),
    TK.str(o.score), TK.str(o.why) }, '|')
end

-- TM.ends: TM.finish over ids, every one checked before any is written.
function TM.ends(ids, o)
  local seen = {}
  for _, id in ipairs(ids) do
    if seen[id] then return { 'REFUSED', 'TWICE ' .. id .. ' is named twice' } end
    seen[id] = true
    o.dry = true
    local err = TM.finish(id, o)
    o.dry = nil
    if err then return { 'REFUSED', err } end
  end
  local out = { 'ENDED', tostring(#ids) }
  for _, id in ipairs(ids) do
    local err, info = TM.finish(id, o)
    if info and info.already then info.to = 'already' end
    if err then return { 'REFUSED', err } end
    for _, v in ipairs({ info.copy, info.primary, info.from, info.to, info.next }) do out[#out + 1] = v end
  end
  return out
end

-- TM.cancel(by, why, ids): a copy is given back (copy -> fail, its primary
-- returns: waiting for a work or fix copy, review for a read copy, no
-- attempt counted); a primary is cancelled (done/fail with the why) and its
-- live copy, if any, retired to fail. All checked before any is written:
-- the batch is all or nothing (TM.cancel_each is the per-id form).
function TM.cancel(by, why, ids)
  if TK.str(why) == '' then return { 'REFUSED', 'WHY card cancel needs a why' } end
  local out = { 'CANCELLED', tostring(#ids) }
  for pass = 1, 2 do
    local dry = pass == 1
    for _, id in ipairs(ids) do
      local err, to, cp = nil, nil, ''
      if TK.copy_id(id) then
        local info
        err, info = TM.finish(id, { outcome = 'fail', why = 'cancel: ' .. why, by = by, keep = true, dry = dry })
        if info then to = info.to end
      else
        local p = TK.read(id)
        if not p then
          err = 'NOTASK task:' .. id
        else
          cp = p.copy
          local r = cp ~= '' and redis.call('HMGET', 'task:' .. cp, 'consumer', 'where', 'stream') or {}
          if cp ~= '' and not (r[1] and TM.LIVE[TK.str(r[2])] and redis.call('ZSCORE', TM.key(r[1], r[2]), cp)) then
            err = 'DRIFT task:' .. id .. ' names copy ' .. cp .. ' that is not live; run nova-sprint card fsck --repair'
          end
          err = err or TK.move(id, 'done', { by = by, why = why, ok = 'fail', copy = '', dry = dry })
          if not err and not dry and cp ~= '' then TM.retire(cp, r[1], r[2], 'fail', 'cancel: ' .. why, {}, by, r[3]) end
          to = 'done'
        end
      end
      if err then
        if dry then return { 'REFUSED', err } end
        return { 'REFUSED', 'DRIFT-AFTER ' .. err }
      end
      if not dry then
        out[#out + 1] = id
        out[#out + 1] = to or ''
      end
    end
  end
  return out
end

-- TM.cancel_each(by, why, ids) (#4309, card cancel --each): every id is
-- cancelled on its own, TM.cancel over one id at a time, so a refusal
-- names the id it is about and the rest still move (on superman a 32-id
-- batch refused as a whole and named nothing usable). Returns CANCEL ok
-- refused, then per id in order: id, CANCELLED <where> | REFUSED <why>.
function TM.cancel_each(by, why, ids)
  if TK.str(why) == '' then return { 'REFUSED', 'WHY card cancel needs a why' } end
  local out, ok, refused = { 'CANCEL', '0', '0' }, 0, 0
  for _, id in ipairs(ids) do
    local r = TM.cancel(by, why, { id })
    out[#out + 1] = id
    if r[1] == 'REFUSED' then
      refused = refused + 1
      out[#out + 1] = 'REFUSED'
      out[#out + 1] = r[2]
    else
      ok = ok + 1
      out[#out + 1] = 'CANCELLED'
      out[#out + 1] = r[4] or ''
    end
  end
  out[2], out[3] = tostring(ok), tostring(refused)
  return out
end

-- TM.sprint_clear(by, why, force): the sprint table to zeros (Glenn
-- 2026-09-26 8:40 AM ET: "reset the sprint table. zeros everywhere"). Every
-- primary in every ws:<s>:{waiting,ready,working,review,merging,landed} goes
-- to done: landed -> done/ok (what table clear did), the rest -> done/fail
-- with the why; a primary's live copies are retired to fail first; then
-- every consumer's ok and fail sets are deleted (the copies' records stay).
-- A ghost (a card or copy id in a ws set) is removed. Cards working or
-- merging are in flight: refused unless force. One call, one line back:
-- CLEARED streams cards copies consumers, then per stream its name and
-- count.
function TM.sprint_clear(by, why, force)
  if TK.str(why) == '' then return { 'REFUSED', 'WHY sprint clear needs a why' } end
  local streams = TM.streams()
  local inflight = 0
  for _, s in ipairs(streams) do
    for _, w in ipairs({ 'working', 'merging' }) do
      inflight = inflight + redis.call('ZCARD', 'ws:' .. s .. ':' .. w)
    end
  end
  if inflight > 0 and not force then
    return { 'REFUSED', 'INFLIGHT ' .. inflight .. ' cards working or merging; sprint clear --force cancels them' }
  end
  local cards, copies, per = 0, 0, {}
  for _, s in ipairs(streams) do
    local n = 0
    for _, w in ipairs({ 'waiting', 'ready', 'working', 'review', 'merging', 'landed' }) do
      local key = 'ws:' .. s .. ':' .. w
      for _, id in ipairs(redis.call('ZRANGE', key, 0, -1)) do
        local p = (not TK.card_id(id) and not TK.copy_id(id)) and TK.read(id) or nil
        if not p then
          redis.call('ZREM', key, id)
        else
          if p.copy ~= '' then
            local r = redis.call('HMGET', 'task:' .. p.copy, 'consumer', 'where', 'stream')
            if r[1] and TM.LIVE[TK.str(r[2])] and redis.call('ZSCORE', TM.key(r[1], r[2]), p.copy) then
              TM.retire(p.copy, r[1], r[2], 'fail', 'sprint clear: ' .. why, {}, by, r[3])
              copies = copies + 1
            end
          end
          if p.reads ~= '' then
            copies = copies + #TM.words(p.reads)
            TM.retire_reads(id, 'sprint clear: ' .. why, by)
          end
          local ok = w == 'landed' and 'ok' or 'fail'
          local err = TK.move(id, 'done', { by = by, why = why, ok = ok, copy = '', clear = true })
          if err then return { 'REFUSED', 'DRIFT-AFTER ' .. err } end
          n = n + 1
        end
      end
    end
    cards = cards + n
    per[#per + 1] = s
    per[#per + 1] = tostring(n)
  end
  local consumers = TM.roster()
  for _, c in ipairs(consumers) do
    -- a copy still live on a consumer (its primary gone, or dealt outside
    -- the streams) is retired; a live set member with no record is removed
    for _, w in ipairs({ 'ready', 'working' }) do
      for _, cid in ipairs(redis.call('ZRANGE', TM.key(c, w), 0, -1)) do
        if TK.copy_id(cid) and redis.call('EXISTS', 'task:' .. cid) == 1 then
          TM.retire(cid, c, w, 'fail', 'sprint clear: ' .. why, {}, by, TK.str(redis.call('HGET', 'task:' .. cid, 'stream')))
          copies = copies + 1
        else
          redis.call('ZREM', TM.key(c, w), cid)
        end
      end
    end
    redis.call('DEL', TM.key(c, 'ok'), TM.key(c, 'fail'))
  end
  local out = { 'CLEARED', tostring(#streams), tostring(cards), tostring(copies), tostring(#consumers) }
  for _, v in ipairs(per) do out[#out + 1] = v end
  return out
end

-- TM.beat(c, ids): the holder renews its working copies' leases. Named
-- copies all or nothing. Returns BEAT n lease_until.
function TM.beat(c, ids)
  local at = cm_now()
  for _, id in ipairs(ids) do
    if not TK.copy_id(id) or not redis.call('ZSCORE', TM.key(c, 'working'), id) then
      return { 'REFUSED', 'NOTWORKING ' .. TK.str(id) .. ' is not in ' .. TM.key(c, 'working') }
    end
  end
  for _, id in ipairs(ids) do
    redis.call('HSET', 'task:' .. id, 'lease_until', tostring(at + TM.LEASE), 'beat_at', tostring(at))
  end
  return { 'BEAT', tostring(#ids), tostring(at + TM.LEASE) }
end

-- TM.roster: every consumer: the consumers set, and bench:<b> for each of
-- benches, friend:<f> for each of friends.
function TM.roster()
  local out, seen = {}, {}
  local function add(c)
    if TM.parse(c) and not seen[c] then
      seen[c] = true
      out[#out + 1] = c
    end
  end
  for _, c in ipairs(redis.call('SMEMBERS', 'consumers')) do add(c) end
  for _, b in ipairs(redis.call('SMEMBERS', 'benches')) do add('bench:' .. b) end
  for _, f in ipairs(redis.call('SMEMBERS', 'friends')) do add('friend:' .. f) end
  table.sort(out)
  return out
end

-- TM.expire(by, consumers): every working copy whose lease lapsed returns to
-- its primary as a fail (why=lease lapsed). Returns EXPIRED n, then per copy
-- its id and the primary's new where.
function TM.expire(by, consumers)
  if #consumers == 0 then consumers = TM.roster() end
  -- only bench:<b> and friend:<f>: any other word is no key to read
  local named = {}
  for _, c in ipairs(consumers) do
    if TM.parse(c) then named[#named + 1] = c end
  end
  consumers = named
  local now = cm_now()
  local out = { 'EXPIRED', '0' }
  for _, c in ipairs(consumers) do
    -- a consumer whose beat is older than a lease holds no ready copy: each
    -- goes back to its primary (no attempt counted)
    local key = c
    if TM.parse(c) == 'bench' then key = c .. ':beat' end
    local at = TK.ms(redis.call('HGET', key, 'at'))
    if at and now - at > TM.LEASE then
      for _, id in ipairs(redis.call('ZRANGE', TM.key(c, 'ready'), 0, -1)) do
        if TK.copy_id(id) then
          local err, info = TM.finish(id, { outcome = 'fail', why = 'consumer down', keep = true, by = by })
          if not err then
            out[#out + 1] = id
            out[#out + 1] = info.to
          end
        end
      end
    end
    for _, id in ipairs(redis.call('ZRANGE', TM.key(c, 'working'), 0, -1)) do
      if TK.copy_id(id) then
        local lease = tonumber(redis.call('HGET', 'task:' .. id, 'lease_until')) or 0
        if lease < now then
          -- a lapsed READ is the reader's, not the card's: given back, the
          -- primary stays in review and the deal pass re-cuts its reads
          -- (#4094 c); a lapsed work or fix copy is a fail, to review (#4072)
          local keep = TK.str(redis.call('HGET', 'task:' .. id, 'leg')) == 'read'
          local err, info = TM.finish(id, { outcome = 'fail', why = 'lease lapsed', keep = keep, by = by })
          if not err then
            out[#out + 1] = id
            out[#out + 1] = info.to
          end
        end
      end
    end
  end
  out[2] = tostring((#out - 2) / 2)
  return out
end

-- TM.fsck(write): both links both ways, over every consumer's four sets and
-- every stream's primaries. A copy in a consumer set has a record naming
-- that consumer and set, scored by created_at while live and ended_at once
-- retired, in no other of its consumer's sets; a live copy's primary names
-- it and is working (review for a read copy); a primary naming a copy is
-- where that copy wants it and the copy is live in its set; a working
-- primary with no copy and no friend is drift. write repairs: a
-- stray is removed, a missing link added, a score reset, a copy whose
-- primary lost it retired to fail, a primary whose copy is lost returned
-- (working -> waiting, review keeps its where). Returns FSCK consumers
-- live retired primaries drift fixed, then up to 100 lines.
function TM.fsck(write)
  local drift, fixed, lines = 0, 0, {}
  local function note(s)
    drift = drift + 1
    if #lines < 100 then lines[#lines + 1] = s end
  end
  local live, retired, seen = 0, 0, {}
  local consumers = TM.roster()
  for _, c in ipairs(consumers) do
    for _, col in ipairs(TM.COLS) do
      local k = TM.key(c, col)
      local rows = redis.call('ZRANGE', k, 0, -1, 'WITHSCORES')
      for i = 1, #rows, 2 do
        local id = rows[i]
        local probe = cm_probe(id)
        if probe then
          -- a probe in a consumer set is drift whatever its shape (nova-tools#4237)
          note('probe ' .. k .. ' ' .. id .. ' (probe=' .. probe .. ')')
          if write then
            redis.call('ZREM', k, id)
            fixed = fixed + 1
          end
        elseif TK.copy_id(id) then
          local r = redis.call('HMGET', 'task:' .. id, 'primary', 'consumer', 'where', 'created_at', 'ended_at', 'leg')
          if not r[1] then
            note('gone ' .. k .. ' ' .. id)
            if write then
              redis.call('ZREM', k, id)
              fixed = fixed + 1
            end
          elseif r[2] ~= c or r[3] ~= col or seen[id] then
            note('stray ' .. k .. ' ' .. id .. ' (record: ' .. TK.str(r[2]) .. ':' .. TK.str(r[3]) .. ')')
            if write then
              redis.call('ZREM', k, id)
              fixed = fixed + 1
            end
          else
            seen[id] = true
            local want = TK.ms(r[4])
            if not TM.LIVE[col] then want = tonumber(r[5]) end
            if want and tonumber(rows[i + 1]) ~= want then
              note('score ' .. k .. ' ' .. id)
              if write then
                cm_zadd(k, want, id)
                fixed = fixed + 1
              end
            end
            if TM.LIVE[col] then
              live = live + 1
              local p = TK.read(r[1])
              if not TM.holds(p, id, r[6]) then
                note('orphan ' .. id .. ' (primary task:' .. r[1] .. ' ' ..
                  (p and (p.where .. ' copy=' .. p.copy .. ' reads=' .. p.reads) or 'gone') .. ')')
                if write then
                  TM.retire(id, c, col, 'fail', 'fsck: its primary does not name it', {}, 'fsck', '')
                  fixed = fixed + 1
                end
              end
            else
              retired = retired + 1
            end
          end
        end
      end
    end
  end
  local primaries = 0
  for _, s in ipairs(TM.streams()) do
    for _, w in ipairs(TK.WHERE) do
      for _, id in ipairs(redis.call('ZRANGE', 'ws:' .. s .. ':' .. w, 0, -1)) do
        if not TK.card_id(id) and not TK.copy_id(id) then
          local f = redis.call('HMGET', 'task:' .. id, 'copy', 'friend', 'owner', 'reads')
          local cp, friend = TK.str(f[1]), TK.str(f[2])
          if friend == '' then friend = TK.str(f[3]) end
          local reads = TM.words(f[4])
          if #reads > 0 then
            if cp == '' then primaries = primaries + 1 end
            local keep = {}
            for _, rid in ipairs(reads) do
              local r = redis.call('HMGET', 'task:' .. rid, 'primary', 'consumer', 'where', 'leg')
              if r[1] == id and r[4] == 'read' and TM.parse(r[2]) and TM.LIVE[TK.str(r[3])] and
                  redis.call('ZSCORE', TM.key(r[2], r[3]), rid) and w == 'review' then
                keep[#keep + 1] = rid
              else
                note('lost task:' .. id .. ' (' .. w .. ') names read copy ' .. rid .. ' (' .. TK.str(r[2]) .. ':' ..
                  TK.str(r[3]) .. ')')
              end
            end
            if write and #keep < #reads then
              TM.set_reads(id, keep)
              fixed = fixed + #reads - #keep
            end
          end
          if cp ~= '' then
            primaries = primaries + 1
            local r = redis.call('HMGET', 'task:' .. cp, 'primary', 'consumer', 'where', 'leg')
            local ok = r[1] == id and TM.parse(r[2]) and TM.LIVE[TK.str(r[3])] and
              redis.call('ZSCORE', TM.key(r[2], r[3]), cp) and (TM.WANT[TK.str(r[4])] or TM.WANT.work)[w]
            if not ok then
              note('lost task:' .. id .. ' (' .. w .. ') names copy ' .. cp .. ' (' .. TK.str(r[2]) .. ':' .. TK.str(r[3]) .. ')')
              if write then
                local to = w
                if w == 'working' then to = 'waiting' end
                if not TK.move(id, to, { by = 'fsck', why = 'fsck: copy ' .. cp .. ' lost', copy = '' }) then
                  fixed = fixed + 1
                end
              end
            end
          elseif w == 'working' and friend == '' then
            -- (review with no copy waits for its read deal; a working
            -- primary is always its copy's: nothing waits on CI there)
            note('bare task:' .. id .. ' is working with no copy and no friend')
            if write and not TK.move(id, 'waiting', { by = 'fsck', why = 'fsck: working with no copy', copy = '' }) then
              fixed = fixed + 1
            end
          end
        end
      end
    end
  end
  local out = { 'FSCK', tostring(#consumers), tostring(live), tostring(retired), tostring(primaries), tostring(drift),
    tostring(fixed) }
  for _, l in ipairs(lines) do out[#out + 1] = l end
  return out
end

-- TM.ids: args from i on, as a list.
function TM.ids(args, i)
  local ids = {}
  for j = i, #args do ids[#ids + 1] = args[j] end
  return ids
end

-- ns_cm_deal(consumer, by, k, stream, id...) -> DEALT n, then per copy the
-- primary and copy ids | REFUSED <why>.
redis.register_function('ns_cm_deal', function(keys, args)
  return TM.deal(args[1], TK.str(args[2]), tonumber(args[3]) or 0, TK.str(args[4]), TM.ids(args, 5))
end)

-- ns_cm_work(consumer, by, k|fill, id...) -> WORKED n free, then the ids |
-- REFUSED <why>.
redis.register_function('ns_cm_work', function(keys, args)
  return TM.work(args[1], TK.str(args[2]), tonumber(args[3]) or 0, args[3] == 'fill', TM.ids(args, 4))
end)

-- ns_cm_end(by, ok|fail, why, sha, score, reader, token, nfields, k, v...,
-- id...) -> ENDED n, then per copy: copy, primary, from, to (already: the
-- same end again, nothing moved), next copy | REFUSED <why> (CONFLICT,
-- FENCED, ...).
redis.register_function('ns_cm_end', function(keys, args)
  local outcome = args[2]
  if outcome ~= 'ok' and outcome ~= 'fail' then return { 'REFUSED', 'OUTCOME ok or fail' } end
  local nf = tonumber(args[8]) or 0
  local fields = {}
  for j = 9, 8 + nf * 2 do fields[#fields + 1] = args[j] end
  return TM.ends(TM.ids(args, 9 + nf * 2), { outcome = outcome, why = args[3], sha = args[4],
    score = TK.str(args[5]) ~= '' and args[5] or nil, reader = args[6], token = args[7], by = args[1], fields = fields })
end)

-- ns_cm_cancel(by, why, id...) -> CANCELLED n, then per id: id, where
-- (all or nothing).
redis.register_function('ns_cm_cancel', function(keys, args)
  return TM.cancel(TK.str(args[1]), args[2], TM.ids(args, 3))
end)

-- ns_cm_cancel_each(by, why, id...) -> CANCEL ok refused, then per id: id,
-- CANCELLED where | REFUSED why (#4309: each id on its own).
redis.register_function('ns_cm_cancel_each', function(keys, args)
  return TM.cancel_each(TK.str(args[1]), args[2], TM.ids(args, 3))
end)

-- ns_sprint_clear(by, why, force) -> CLEARED streams cards copies consumers,
-- then per stream: name, cards | REFUSED <why>.
redis.register_function('ns_sprint_clear', function(keys, args)
  return TM.sprint_clear(TK.str(args[1]), args[2], args[3] == '1')
end)

-- ns_cm_beat(consumer, id...) -> BEAT n lease_until | REFUSED <why>.
redis.register_function('ns_cm_beat', function(keys, args) return TM.beat(args[1], TM.ids(args, 2)) end)

-- ns_cm_expire(by, consumer...) -> EXPIRED n, then per copy: id, where.
redis.register_function('ns_cm_expire', function(keys, args) return TM.expire(TK.str(args[1]), TM.ids(args, 2)) end)

-- ns_cm_assign(consumer, id, revoke, by, why) -> ASSIGNED id copy revoked |
-- REFUSED <why>.
redis.register_function('ns_cm_assign', function(keys, args)
  return TM.assign(args[1], TK.str(args[2]), args[3] == '1', TK.str(args[4]), TK.str(args[5]))
end)

-- ns_cm_review(by, id, verdict, why) -> REVIEWED id verdict where copy |
-- REFUSED <why> (#4072: the only way out of review).
redis.register_function('ns_cm_review', function(keys, args)
  return TM.review(TK.str(args[1]), args[2], args[3], args[4])
end)

-- ns_cm_fsck() read-only; ns_cm_repair() the same walk, repairing.
redis.register_function({ function_name = 'ns_cm_fsck', flags = { 'no-writes' },
  callback = function(keys, args) return TM.fsck(false) end })
redis.register_function('ns_cm_repair', function(keys, args) return TM.fsck(true) end)

-- THE ONE LEASE LEDGER (#3998, #3877's other half; it replaces the
-- bench:<b>:starting|living and friend:<f>:starting|living ledgers, which
-- nothing writes or reads any more). A consumer's width in
-- use is ZCARD <consumer>:cards:working and nothing else: its working copies
-- (TM.work), its task cards (TK.move with a friend) and, while the older
-- paths still run, their leases. A bench's sprint card is already a member
-- there from its deal to its end (card_move: dealt, launched and running are
-- where=working), so the bench keeps no second ledger. A friend-queue take
-- (s:<S>:task:<id>, task_claim.lua) holds the member <S>/<id>/<attempt>
-- there from the take to its done, cancel, expiry or redistribute: TM.lease_hold
-- adds it once, scored by the take's time and never rescored (the task
-- record's beat_at is its beat), and TM.lease_drop removes it; the files that take
-- and close those tasks call them as NS.moves.hold and NS.moves.drop, so this
-- file stays the one writer of the set. TK.fsck and TM.fsck pass over these
-- members (a / in the member; no task id or copy id has one).
--
-- One task store (#3907): a friend-queue take is a task card, so the take's
-- own move (NS.task.set ... claimed, friend f) already put task <id> in
-- friend:<f>:cards:working. That member is the lease: TM.lease_hold adds no second
-- <S>/<id>/<attempt> for it (a take counts once against the width), and
-- TM.leases names it as <S>/<id>/<attempt> from the record, so the fence and
-- requeue paths read one identity either way.
function TM.lease_id(m) return type(m) == 'string' and string.find(m, '/', 1, true) ~= nil end

function TM.lease_hold(c, member, at)
  if not TM.parse(c) or not TM.lease_id(member) then return 0 end
  local id = string.match(member, '^[^/]+/(.+)/%d+$')
  if id and redis.call('ZSCORE', TM.key(c, 'working'), id) then return 0 end
  return cm_zadd(TM.key(c, 'working'), 'NX', at, member)
end

function TM.lease_drop(c, member)
  if not TM.parse(c) or not TM.lease_id(member) then return 0 end
  return redis.call('ZREM', TM.key(c, 'working'), member)
end

-- TM.held: the consumer's width in use (every member of its working set).
function TM.held(c) return redis.call('ZCARD', TM.key(c, 'working')) end

-- TM.leases: the consumer's friend-queue leases, oldest first.
function TM.leases(c)
  local out = {}
  for _, m in ipairs(redis.call('ZRANGE', TM.key(c, 'working'), 0, -1)) do
    if TM.lease_id(m) then
      out[#out + 1] = m
    elseif not TK.card_id(m) and not TK.copy_id(m) then
      -- a task card taken off a friend queue (one task store, #3907)
      local v = redis.call('HMGET', 'task:' .. m, 'sprint', 'attempt', 'state')
      if TK.str(v[1]) ~= '' and TK.str(v[2]) ~= '' and (v[3] == 'claimed' or v[3] == 'working') then
        out[#out + 1] = v[1] .. '/' .. m .. '/' .. v[2]
      end
    end
  end
  return out
end

-- NS.moves: what a later file calls: the lease ledger above (no CI word
-- moves a primary; a copy's end does, #3929).
NS.moves = { hold = TM.lease_hold, drop = TM.lease_drop, held = TM.held, leases = TM.leases }

-- ns_cm_head(repo, n, by) -> REHEAD n, then per primary re-headed: id,
-- copies (comma-joined) | REFUSED <why>: pr record --head's event (#4094).
redis.register_function('ns_cm_head', function(keys, args)
  return TM.head(TK.str(args[1]), TK.str(args[2]), TK.str(args[3]))
end)

-- ns_cm_reads(by) -> READS n, then per primary: id, copies | REFUSED <why>:
-- the review column's duty on every deal pass (TM.ensure).
redis.register_function('ns_cm_reads', function(keys, args) return TM.ensure(TK.str(args[1])) end)

-- read post's SCORE (03_task_event.lua) ends a read copy through the one
-- finish: NS.tm.score(repo, n, line, by) -> {moved, cut, notes}.
NS.tm = { score = TM.score }

NS.task = { move = TK.move, create = TK.create, place = TK.place, read = TK.read, stream_of = TK.stream_of,
  ms = TK.ms, where = TK.IS, where_of = TK.WHERE_OF, unread = TK.unread,
  -- set(id, state, o): the move to the where a fine state names (cancelled is
  -- done/fail), writing that state: the sprint store's transitions.
  -- renew(id, at): a live holder's beat renews the task's lease.
  renew = function(id, at)
    redis.call('HSET', 'task:' .. id, 'lease_until', tostring(at + TK.LEASE), 'beat_at', tostring(at))
  end,
  set = function(id, state, o)
    o = o or {}
    local to = TK.WHERE_OF[state]
    if not to then return 'STATE ' .. TK.str(state) end
    o.state = state
    if to == 'done' then o.ok = (state == 'cancelled') and 'fail' or (o.ok or 'ok') end
    return TK.move(id, to, o)
  end,
  check = function(id, to, o)
    o = o or {}
    o.dry = true
    local err = TK.move(id, to, o)
    o.dry = nil
    return err
  end }
