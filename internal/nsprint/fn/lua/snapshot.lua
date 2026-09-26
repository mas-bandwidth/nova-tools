-- snapshot.lua: ns_snapshot, the single consistent read behind one rendered
-- tick. Section 6 of #2756.
--
-- The function runs atomically and reads the server clock itself, so the
-- table is one instant: a pipeline of reads is not one consistent instant.
-- It is read-only (no-writes) and bounded: it uses no SCAN, KEYS or XLEN and
-- refuses a registry larger than its bound with one line that names it:
-- `snapshot: bound exceeded: bound=<name> value=<n> limit=<n> remedy=<flag>`
-- (#3893).
--
-- Rows are returned as a flat tagged list so the Go side decodes with no
-- nested map ambiguity. Every value is a string.
--
--   time     <unix seconds>
--   error    <message>
--   bench    name up desired missing ready working stale leased queue done why
--   friend   name up desired missing ready working stale leased queue done why
--   pipeline sprint queued dealt running ended harvested review-ready
--            land-ready landed pool waiting backpressure orphan reconcile
--   proc     name up age why

-- `sprints` is the registry of every sprint ever made (a closed sprint stays
-- in it), so its bound is the registry's: one HGET of status per member. The
-- rows the snapshot reads grow with the open sprints, bounded apart (#3893:
-- a bound of 4 on the registry refused the fleet at 9 sprints, 4 of them
-- closed). At every bound the read stays under a millisecond of server time.
local MAX_SPRINTS = 1024
local MAX_OPEN_SPRINTS = 16
local MAX_BENCHES = 64
local MAX_FRIENDS = 16
local BOUND_REMEDY = '--layout live'

local function zcard(k) return tonumber(redis.call('ZCARD', k)) end
local function scard(k) return tonumber(redis.call('SCARD', k)) end
local function exists(k) return redis.call('EXISTS', k) end

local function desired_of(prefix)
  local v = redis.call('HGET', prefix .. ':desired', 'slots')
  if not v then return 0, 1 end
  return tonumber(v) or 0, 0
end

-- A bench and a friend row are one shape (6.4), read from the consumer's
-- sets (#3998): ready is ZCARD <consumer>:cards:ready, leased is ZCARD
-- <consumer>:cards:working (the one lease ledger: the width in use, derived,
-- never stored), stale the working copies whose lease (task:<copy>
-- lease_until) has lapsed, and working the rest.
local function count_row(bucket, name, sprints, now_ms)
  local c = bucket .. ':' .. name
  local up = exists(c .. ':beat')
  local desired, missing = desired_of(c)
  -- the consumer's sets under the current epoch (#4238)
  local e = NS.card.epoch()
  local ready = zcard(NS.card.ckey(e, c, 'ready'))
  local working_key = NS.card.ckey(e, c, 'working')
  local leased = zcard(working_key)
  local stale = 0
  for _, id in ipairs(redis.call('ZRANGE', working_key, 0, -1)) do

    if string.match(id, '^%S+~%d+$') then
      local lease = tonumber(redis.call('HGET', 'task:' .. id, 'lease_until'))
      if lease and lease < now_ms then stale = stale + 1 end
    end
  end
  local queue, done = 0, 0
  for _, sp in ipairs(sprints) do
    if bucket == 'bench' then
      queue = queue + zcard('s:' .. sp .. ':bench:' .. name .. ':queue')
      done = done + scard('s:' .. sp .. ':bench:' .. name .. ':ended')
    else
      queue = queue + zcard('s:' .. sp .. ':open:' .. name)
      done = done + scard('s:' .. sp .. ':done:' .. name)
    end
  end
  return up, desired, missing, ready, leased - stale, stale, leased, queue, done
end

local function append_row(out, bucket, name, sprints, now_ms)
  local up, desired, missing, ready, working, stale, leased, queue, done =
    count_row(bucket, name, sprints, now_ms)
  local why = {}
  if missing == 1 then why[#why + 1] = 'missing: desired' end
  if up == 0 then why[#why + 1] = 'down: beat' end
  if stale > 0 then why[#why + 1] = 'stale: lease lapsed' end
  if bucket == 'friend' then
    -- friend:<f>:state (out-of-credits from the keeper, down from the
    -- redistribute tick, #3047) is printed on the row it describes.
    local state = redis.call('HGET', 'friend:' .. name .. ':state', 'state')
    if state then why[#why + 1] = 'state: ' .. state end
  end
  out[#out + 1] = bucket
  out[#out + 1] = name
  out[#out + 1] = tostring(up)
  out[#out + 1] = tostring(desired)
  out[#out + 1] = tostring(missing)
  out[#out + 1] = tostring(ready)
  out[#out + 1] = tostring(working)
  out[#out + 1] = tostring(stale)
  out[#out + 1] = tostring(leased)
  out[#out + 1] = tostring(queue)
  out[#out + 1] = tostring(done)
  out[#out + 1] = table.concat(why, '; ')
end

local function append_pipeline(out, sprint)
  local prefix = 's:' .. sprint .. ':'
  local states = {
    'queued', 'dealt', 'running', 'ended',
    'harvested', 'review-ready', 'land-ready', 'landed',
  }
  local rec = { 'pipeline', sprint }
  for _, state in ipairs(states) do
    rec[#rec + 1] = tostring(scard(prefix .. 'idx:card:' .. state))
  end
  rec[#rec + 1] = tostring(zcard(prefix .. 'pool'))
  rec[#rec + 1] = tostring(scard(prefix .. 'waiting'))
  local bp = redis.call('HGET', prefix .. 'backpressure', 'state')
  rec[#rec + 1] = bp == 'ON' and '1' or '0'
  rec[#rec + 1] = tostring(scard(prefix .. 'idx:card:orphan-effect'))
  rec[#rec + 1] = tostring(scard(prefix .. 'idx:card:reconcile-required'))
  for _, v in ipairs(rec) do out[#out + 1] = v end
end

local function append_procs(out, now, benches)
  local procs = { 'reconciler', 'ok-to-friend', 'pr-to-read',
    'hold-to-fix', 'backpressure' }
  for _, bench in ipairs(benches) do
    procs[#procs + 1] = 'harvest:' .. bench
  end
  table.sort(procs)
  for _, p in ipairs(procs) do
    local at = tonumber(redis.call('HGET', 'proc:' .. p, 'pass_at'))
    local age = -1
    if at then age = math.max(0, now - math.floor(at / 1000)) end
    local up = at and age <= 20 and 1 or 0
    local why = ''
    if not at then
      why = 'missing: pass'
    elseif age > 20 then
      why = 'stale: pass ' .. tostring(age) .. 's'
    end
    out[#out + 1] = 'proc'
    out[#out + 1] = p
    out[#out + 1] = tostring(up)
    out[#out + 1] = tostring(age)
    out[#out + 1] = why
  end
end

local function bound_exceeded(now, name, value, limit)
  return { 'time', tostring(now), 'error', 'snapshot: bound exceeded: bound=' .. name
    .. ' value=' .. tostring(value) .. ' limit=' .. tostring(limit)
    .. ' remedy=' .. BOUND_REMEDY }
end

redis.register_function{
  function_name = 'ns_snapshot',
  flags = { 'no-writes' },
  callback = function(keys, args)
    local t = redis.call('TIME')
    local now = tonumber(t[1])
    local now_ms = now * 1000 + math.floor(tonumber(t[2]) / 1000)
    local out = { 'time', tostring(now) }

    -- Bounds first: a registry larger than its bound is an error, and the
    -- bound is checked before any member is read (6.3). The refusal names
    -- the bound, its value, its limit and the flag that renders anyway.
    for _, b in ipairs({ { 'sprints', MAX_SPRINTS }, { 'benches', MAX_BENCHES },
      { 'friends', MAX_FRIENDS } }) do
      local n = scard(b[1])
      if n > b[2] then return bound_exceeded(now, b[1], n, b[2]) end
    end

    local requested = args[1] or ''
    local all_sprints = redis.call('SMEMBERS', 'sprints')
    local sprints = {}
    for _, sp in ipairs(all_sprints) do
      local status = redis.call('HGET', 's:' .. sp, 'status')
      local control = string.sub(sp, 1, 8) == 'control-'
      if (status == 'open' or status == 'paused') and (not control or requested == sp) then
        sprints[#sprints + 1] = sp
      end
    end
    if #sprints > MAX_OPEN_SPRINTS then
      return bound_exceeded(now, 'open_sprints', #sprints, MAX_OPEN_SPRINTS)
    end
    local benches = redis.call('SMEMBERS', 'benches')
    local friends = redis.call('SMEMBERS', 'friends')
    table.sort(sprints)
    table.sort(benches)
    table.sort(friends)

    -- Width, queue and done for a bench or friend have exactly one writer,
    -- ns_snapshot, derived from the index sets. A stored sidecar key is a
    -- second writer and the snapshot reports it (6.5). Rows are still
    -- rendered so the defect is visible beside the table, and the command
    -- exits non-zero for `--check`.
    local writers = {}
    for _, name in ipairs(benches) do
      for _, field in ipairs({ 'width', 'queue', 'done' }) do
        local k = 'bench:' .. name .. ':' .. field
        if exists(k) == 1 then writers[#writers + 1] = k end
      end
    end
    for _, name in ipairs(friends) do
      for _, field in ipairs({ 'width', 'queue', 'done' }) do
        local k = 'friend:' .. name .. ':' .. field
        if exists(k) == 1 then writers[#writers + 1] = k end
      end
      local old_slots = 'friend:' .. name .. ':slots'
      if exists(old_slots) == 1 then writers[#writers + 1] = old_slots end
    end

    for _, name in ipairs(benches) do
      append_row(out, 'bench', name, sprints, now_ms)
    end
    for _, name in ipairs(friends) do
      append_row(out, 'friend', name, sprints, now_ms)
    end
    for _, sp in ipairs(sprints) do
      append_pipeline(out, sp)
    end
    append_procs(out, now, benches)
    for _, k in ipairs(writers) do
      out[#out + 1] = 'error'
      out[#out + 1] = 'two writers: ' .. k
    end
    return out
  end,
}
