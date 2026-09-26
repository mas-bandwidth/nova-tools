-- No ghost cards (nova-tools#3925). Glenn 2026-09-25 12:25 PM ET: "we
-- should not have ghost cards"; "What do we have to do to stop ghost cards
-- showing up, and make sure that the fleet table is accurate?"
--
-- Found that day: bench:<b>:cards:ready on six benches held cards of a
-- closed sprint forever, and bench:hetzner's old lease ledger held three leases of
-- fleet-probe cards nothing ever released, so the bench ran 2 of 5 slots.
-- This file is the three things that retire them (the record is the truth):
--
--   retire     every card of a closed sprint that is not done moves to
--              done/fail (state superseded, reason sprint-closed, token
--              cleared so a still-running wrapper's next beat is FENCED)
--              through NS.card.move (which takes it out of its bench's
--              one lease ledger, bench:<b>:cards:working, #3998); sprint close
--              (sprint.lua) calls it in the same call, and ns_sprint_retire
--              is the fenced duty form for a sprint closed by any path.
--   lease reap ns_lease_reap walks the one lease ledger of every registered
--              bench, bench:<b>:cards:working (#3998: the two old lease
--              ledgers are folded into it; only the move writes it), and
--              frees the slot of a card that sits in a closed sprint by
--              retiring the card through NS.card.move; fenced by the
--              reconciler token, one cap:log slot-freed receipt per bench.
--              A member whose record is gone or names another bench is card
--              fsck's repair (the fsck duty), and a live card with no beat
--              is the expire sweep's beat-lost reclaim: neither is a lease
--              this file may drop, since it writes no table set.
--   discovery  ns_fsck_sprints names every sprint the fsck duty walks: the
--              open ones (sprints), every one ever opened (sprint:order) and
--              every sprint a card id in a bench, stream or friend view
--              names, each with its status, read-only.
--
-- This file writes no table set: the card moves only through NS.card.move.
do
local CARD = NS.card
local CG_PLACES = { 'waiting', 'ready', 'working', 'done', 'parked' }
-- Fine states that can never hold one again (the card moved on).
local CG_PAST = {
  queued = true, ended = true, harvested = true, refused = true, ['review-ready'] = true,
  ['land-ready'] = true, landed = true, superseded = true, ['reconcile-required'] = true,
  ['orphan-effect'] = true,
}

local function cg_now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function cg_fenced(token)
  local held = redis.call('HGET', 'lease:reconciler', 'token')
  return (not held) or token == nil or token == '' or held ~= token
end

-- An open sprint deals and runs; closed, folded or any other written status
-- is past it. A sprint with no status hash is not judged closed.
local function cg_closed(S)
  local st = redis.call('HGET', 's:' .. S, 'status')
  return st ~= false and st ~= nil and st ~= 'open' and st ~= 'opening'
end

-- Retire one card of a closed sprint: its bench queue entry goes, and a card
-- not yet done moves to done/fail (the move takes it out of its bench's
-- cards:working, its lease). A null card (created, in no set) is placed in
-- waiting first.
-- Returns moved (bool), refusal (nil or text).
local function cg_retire_card(S, id, by, at)
  local label = string.sub(id, #('s:' .. S .. ':card:') + 1)
  local c = redis.call('HMGET', id, 'where', 'bench', 'attempt', 'state')
  if not c[4] then return false, 'NOCARD ' .. id end
  local bench = c[2] or ''
  if bench ~= '' then
    redis.call('ZREM', 's:' .. S .. ':bench:' .. bench .. ':queue', label)
  end
  if c[1] == 'done' then return false, nil end
  if (c[1] or '') == '' and c[4] == 'queued' then
    local err = CARD.move(id, 'waiting', { by = by, why = 'sprint-closed' })
    if err then return false, err end
  end
  local err = CARD.move(id, 'done', { state = 'superseded', ok = 'fail', by = by, why = 'sprint-closed',
    fields = { 'token', '', 'reason', 'sprint-closed', 'retired_at', tostring(at) } })
  if err then return false, err end
  return true, nil
end

-- Retire every card of sprint S (its roster, sprint:<S>:cards). Returns the
-- number moved to done and up to 20 refusal lines.
local function cg_retire(S, by)
  local at = cg_now()
  local n, refused = 0, {}
  for _, id in ipairs(redis.call('ZRANGE', 'sprint:' .. S .. ':cards', 0, -1)) do
    local moved, err = cg_retire_card(S, id, by, at)
    if moved then n = n + 1 end
    if err and #refused < 20 then refused[#refused + 1] = err end
  end
  return n, refused
end

-- ns_lease_reap(token, stale_ms): free every closed sprint's lease on every
-- registered bench: a sprint card in bench:<b>:cards:working whose sprint is
-- closed is retired (done/fail superseded, sprint-closed) through the move.
-- stale_ms is checked for the caller's contract; the beat-lost reclaim is the
-- expire sweep's. {'REAPED', n, '<bench> working <card> <why>'...} or
-- {'FENCED'}.
local function lease_reap(keys, args)
  if cg_fenced(args[1]) then return { 'FENCED' } end
  local stale_ms = tonumber(args[2])
  if not stale_ms or stale_ms <= 0 then return redis.error_reply('ns_lease_reap: stale_ms must be positive') end
  local now = cg_now()
  local out, n = { 'REAPED', '0' }, 0
  local benches = redis.call('SMEMBERS', 'benches')
  table.sort(benches)
  for _, b in ipairs(benches) do
    local freed = 0
    for _, id in ipairs(redis.call('ZRANGE', 'bench:' .. b .. ':cards:working', 0, -1)) do
      local S = string.match(id, '^s:([-a-z0-9]+):card:[A-Za-z0-9][A-Za-z0-9._-]*$')
      if S and cg_closed(S) then
        local st = redis.call('HGET', id, 'state')
        if st and not CG_PAST[st] then
          local moved = cg_retire_card(S, id, 'lease-reap', now)
          if moved then
            freed = freed + 1
            if #out < 52 then out[#out + 1] = b .. ' working ' .. id .. ' sprint-closed' end
          end
        end
      end
    end
    if freed > 0 then
      n = n + freed
      redis.call('XADD', 'cap:log', 'MAXLEN', '~', '100000', '*',
        'kind', 'slot-freed', 'target', 'bench:' .. b, 'slots', tostring(freed),
        'reason', 'lease-reap', 'actor', 'reconciler', 'idem', '', 'at', tostring(now))
    end
  end
  out[2] = tostring(n)
  return out
end

-- ns_sprint_retire(S, token): the fenced duty form of the close's retire,
-- for a sprint closed by any path. An open sprint is refused. {'RETIRED', n,
-- refusal...}, {'OPEN'} or {'FENCED'}.
local function sprint_retire(keys, args)
  local S = args[1] or ''
  if cg_fenced(args[2]) then return { 'FENCED' } end
  if not string.match(S, '^[-a-z0-9]+$') then return redis.error_reply('ns_sprint_retire: bad sprint name') end
  if not cg_closed(S) then return { 'OPEN' } end
  local n, refused = cg_retire(S, 'fsck-duty')
  local out = { 'RETIRED', tostring(n) }
  for _, r in ipairs(refused) do out[#out + 1] = r end
  return out
end

-- ns_fsck_sprints(): every sprint the fsck duty walks, sorted, as name,
-- status pairs ('' when s:<S> has none).
local function fsck_sprints(keys, args)
  local seen = {}
  for _, s in ipairs(redis.call('SMEMBERS', 'sprints')) do seen[s] = true end
  for _, s in ipairs(redis.call('ZRANGE', 'sprint:order', 0, -1)) do seen[s] = true end
  local function ids(k)
    for _, id in ipairs(redis.call('ZRANGE', k, 0, -1)) do
      local S = string.match(id, '^s:([-a-z0-9]+):card:')
      if S then seen[S] = true end
    end
  end
  -- the current epoch's sets: the ones the tables read (#4238)
  local e = NS.card.epoch()
  local benches = redis.call('SMEMBERS', 'benches')
  benches[#benches + 1] = '_pool'
  for _, b in ipairs(benches) do
    for _, w in ipairs(CG_PLACES) do ids(NS.card.ckey(e, 'bench:' .. b, w)) end
    ids(NS.card.ckey(e, 'bench:' .. b, 'ok'))
    ids(NS.card.ckey(e, 'bench:' .. b, 'fail'))
  end
  for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
    for _, w in ipairs(CG_PLACES) do ids(NS.card.wskey(e, s, w)) end
  end
  for _, f in ipairs(redis.call('SMEMBERS', 'friends')) do
    for _, w in ipairs(CG_PLACES) do ids(NS.card.ckey(e, 'friend:' .. f, w)) end
  end

  local names = {}
  for s in pairs(seen) do names[#names + 1] = s end
  table.sort(names)
  local out = {}
  for _, s in ipairs(names) do
    out[#out + 1] = s
    out[#out + 1] = redis.call('HGET', 's:' .. s, 'status') or ''
  end
  return out
end

-- ns_fsck_finding(token, fixed, retired, text): the fsck duty's record on
-- proc:reconciler, fenced: fsck_at every run, and a repair over 0 is a
-- finding (a writer broke the invariant) kept until the next one.
local function fsck_finding(keys, args)
  if cg_fenced(args[1]) then return { 'FENCED' } end
  local fixed, retired = tonumber(args[2]) or 0, tonumber(args[3]) or 0
  local at = tostring(cg_now())
  redis.call('HSET', 'proc:reconciler', 'fsck_at', at, 'fsck_fixed', tostring(fixed), 'fsck_retired', tostring(retired))
  if fixed + retired > 0 then
    redis.call('HSET', 'proc:reconciler', 'finding', 'fsck ' .. (args[4] or ''), 'finding_at', at)
  end
  return { 'OK', at }
end

redis.register_function('ns_lease_reap', lease_reap)
redis.register_function('ns_sprint_retire', sprint_retire)
redis.register_function{ function_name = 'ns_fsck_sprints', callback = fsck_sprints, flags = { 'no-writes' } }
redis.register_function('ns_fsck_finding', fsck_finding)

NS.ghost = { retire = cg_retire }
end
