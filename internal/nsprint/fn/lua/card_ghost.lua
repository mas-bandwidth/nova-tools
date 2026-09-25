-- No ghost cards (nova-tools#3925). Glenn 2026-09-25 12:25 PM ET: "we
-- should not have ghost cards"; "What do we have to do to stop ghost cards
-- showing up, and make sure that the fleet table is accurate?"
--
-- Found that day: bench:<b>:cards:ready on six benches held cards of a
-- closed sprint forever, and bench:hetzner:living held three leases of
-- fleet-probe cards nothing ever released, so the bench ran 2 of 5 slots.
-- This file is the three things that retire them (the record is the truth):
--
--   retire     every card of a closed sprint that is not done moves to
--              done/fail (state superseded, reason sprint-closed, token
--              cleared so a still-running wrapper's next beat is FENCED)
--              through NS.card.move, and its bench leases go; sprint close
--              (sprint.lua) calls it in the same call, and ns_sprint_retire
--              is the fenced duty form for a sprint closed by any path.
--   lease reap ns_lease_reap walks bench:<b>:starting and :living of every
--              registered bench and drops an entry whose card record is gone,
--              names another attempt, is past running, sits in a closed
--              sprint, or has not beaten for stale_ms (90 s; a DOWN bench
--              keeps the leases of its live cards); fenced by the reconciler
--              token, one cap:log slot-freed receipt per bench.
--   discovery  ns_fsck_sprints names every sprint the fsck duty walks: the
--              open ones (sprints), every one ever opened (sprint:order) and
--              every sprint a card id in a bench, stream or friend view
--              names, each with its status, read-only.
--
-- The lease sets are not yet views of the one move (nova-tools#3929 folds
-- them into working): lease_drop below is the only place this file writes
-- them. The card itself moves only through NS.card.move.
do
local CARD = NS.card
local CG_PLACES = { 'waiting', 'ready', 'working', 'done', 'parked' }
-- Fine states that hold a lease slot; any other state's lease is a ghost.
local CG_LIVE = { dealt = true, launched = true, running = true }
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

local function lease_drop(bench, member)
  return redis.call('ZREM', 'bench:' .. bench .. ':starting', member) +
    redis.call('ZREM', 'bench:' .. bench .. ':living', member)
end

-- Retire one card of a closed sprint: its leases on its bench (every
-- attempt) and its bench queue entry go, and a card not yet done moves to
-- done/fail. A null card (created, in no set) is placed in waiting first.
-- Returns moved (bool), refusal (nil or text).
local function cg_retire_card(S, id, by, at)
  local label = string.sub(id, #('s:' .. S .. ':card:') + 1)
  local c = redis.call('HMGET', id, 'where', 'bench', 'attempt', 'state')
  if not c[4] then return false, 'NOCARD ' .. id end
  local bench = c[2] or ''
  if bench ~= '' then
    for a = 1, tonumber(c[3]) or 0 do lease_drop(bench, S .. '/' .. label .. '/' .. a) end
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

-- Why one lease entry is a ghost, or nil when it holds a live slot.
local function cg_ghost(bench, member, score, stale_ms, now, down)
  local S, label, attempt = string.match(member, '^([-a-z0-9]+)/([A-Za-z0-9][A-Za-z0-9._-]*)/([1-9][0-9]*)$')
  -- A member in any other shape is not judged: no evidence is not negative
  -- evidence.
  if not S then return nil end
  local id = 's:' .. S .. ':card:' .. label
  local c = redis.call('HMGET', id, 'state', 'attempt', 'bench', 'beat_at', 'launched_at', 'dealt_at')
  if not c[1] then return 'no-record' end
  if cg_closed(S) then
    if not CG_PAST[c[1]] then cg_retire_card(S, id, 'lease-reap', now) end
    return 'sprint-closed'
  end
  if c[2] ~= attempt then return 'attempt ' .. tostring(c[2]) end
  if (c[3] or '') ~= bench then return 'bench ' .. tostring(c[3]) end
  if CG_PAST[c[1]] then return 'state ' .. c[1] end
  if not CG_LIVE[c[1]] or down then return nil end
  local last = tonumber(c[4]) or tonumber(c[5]) or tonumber(c[6]) or tonumber(score) or 0
  if now - last >= stale_ms then return 'no-beat ' .. tostring(math.floor((now - last) / 1000)) .. 's' end
  return nil
end

-- ns_lease_reap(token, stale_ms): drop every ghost lease of every registered
-- bench. {'REAPED', n, '<bench> <member> <why>'...} or {'FENCED'}.
local function lease_reap(keys, args)
  if cg_fenced(args[1]) then return { 'FENCED' } end
  local stale_ms = tonumber(args[2])
  if not stale_ms or stale_ms <= 0 then return redis.error_reply('ns_lease_reap: stale_ms must be positive') end
  local now = cg_now()
  local out, n = { 'REAPED', '0' }, 0
  local benches = redis.call('SMEMBERS', 'benches')
  table.sort(benches)
  for _, b in ipairs(benches) do
    local down = string.upper(redis.call('HGET', 'bench:' .. b .. ':state', 'state') or '') == 'DOWN'
    local freed = 0
    for _, set in ipairs({ 'starting', 'living' }) do
      local members = redis.call('ZRANGE', 'bench:' .. b .. ':' .. set, 0, -1, 'WITHSCORES')
      for i = 1, #members, 2 do
        -- Still there (a retire above may have dropped this card's other
        -- entries), then judged; a closed sprint's card is retired in place.
        local key = 'bench:' .. b .. ':' .. set
        local why = redis.call('ZSCORE', key, members[i]) and cg_ghost(b, members[i], members[i + 1], stale_ms, now, down)
        if why then
          redis.call('ZREM', key, members[i])
          freed = freed + 1
          if #out < 52 then out[#out + 1] = b .. ' ' .. set .. ' ' .. members[i] .. ' ' .. why end
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
  local benches = redis.call('SMEMBERS', 'benches')
  benches[#benches + 1] = '_pool'
  for _, b in ipairs(benches) do
    for _, w in ipairs(CG_PLACES) do ids('bench:' .. b .. ':cards:' .. w) end
    ids('bench:' .. b .. ':cards:ok')
    ids('bench:' .. b .. ':cards:fail')
  end
  for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
    for _, w in ipairs(CG_PLACES) do ids('ws:' .. s .. ':' .. w) end
  end
  for _, f in ipairs(redis.call('SMEMBERS', 'friends')) do
    for _, w in ipairs(CG_PLACES) do ids('friend:' .. f .. ':cards:' .. w) end
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
