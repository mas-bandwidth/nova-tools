-- snapshot.lua: ns_snapshot, the single consistent read behind one rendered
-- tick. Section 6 of #2756.
--
-- The function runs atomically and reads the server clock itself, so the
-- table is one instant: a pipeline of reads is not one consistent instant.
-- It is read-only (no-writes) and bounded: it uses no SCAN, KEYS or XLEN and
-- refuses a registry larger than its bound with `snapshot: bound exceeded`.
--
-- Rows are returned as a flat tagged list so the Go side decodes with no
-- nested map ambiguity. Every value is a string.
--
--   time     <unix seconds>
--   error    <message>
--   bench    name up desired missing starting living stale leased queue done why
--   friend   name up desired missing starting living stale leased queue done why
--   pipeline sprint queued dealt running ended harvested review-ready
--            land-ready landed pool waiting backpressure orphan reconcile
--   proc     name up age why

local MAX_SPRINTS = 4
local MAX_BENCHES = 64
local MAX_FRIENDS = 16
local MAX_MACHINES = 16
local MAX_CI_CARDS = 64
local LIVING_FRESH_MS = 120000

local function zcard(k) return tonumber(redis.call('ZCARD', k)) end
local function scard(k) return tonumber(redis.call('SCARD', k)) end
local function exists(k) return redis.call('EXISTS', k) end

local function desired_of(prefix)
  local v = redis.call('HGET', prefix .. ':desired', 'slots')
  if not v then return 0, 1 end
  return tonumber(v) or 0, 0
end

-- A bench and a friend row are one shape (6.4): width is derived from
-- starting + living, never stored.
local function count_row(bucket, name, sprints, now_ms)
  local up = exists(bucket .. ':' .. name .. ':beat')
  local desired, missing = desired_of(bucket .. ':' .. name)
  local starting = zcard(bucket .. ':' .. name .. ':starting')
  local living = redis.call('ZCOUNT', bucket .. ':' .. name .. ':living', now_ms - LIVING_FRESH_MS, '+inf')
  local total = zcard(bucket .. ':' .. name .. ':living')
  local leased = starting + total
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
  return up, desired, missing, starting, tonumber(living), total - tonumber(living),
    leased, queue, done
end

local function append_row(out, bucket, name, sprints, now_ms)
  local up, desired, missing, starting, living, stale, leased, queue, done =
    count_row(bucket, name, sprints, now_ms)
  local why = {}
  if missing == 1 then why[#why + 1] = 'missing: desired' end
  if up == 0 then why[#why + 1] = 'down: beat' end
  if stale > 0 then why[#why + 1] = 'stale: living >120s' end
  out[#out + 1] = bucket
  out[#out + 1] = name
  out[#out + 1] = tostring(up)
  out[#out + 1] = tostring(desired)
  out[#out + 1] = tostring(missing)
  out[#out + 1] = tostring(starting)
  out[#out + 1] = tostring(living)
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

-- The section 6 machine, ci, clock and process lines (#3045). They share one
-- server instant with the rows above when ns_snapshot is asked for them by
-- passing the literal arg `lines`: the renderer calls FCALL_RO ns_snapshot
-- <sprint> lines, and the function reads server TIME once and answers every
-- line from that instant, never a pipeline of separate reads.

local function consumer_machine(bucket, name)
  return redis.call('HGET', bucket .. ':' .. name .. ':desired', 'machine')
end

local function desired_sum_on(m, benches, friends)
  local sum = 0
  for _, name in ipairs(benches) do
    if consumer_machine('bench', name) == m then
      sum = sum + (tonumber(redis.call('HGET', 'bench:' .. name .. ':desired', 'slots')) or 0)
    end
  end
  for _, name in ipairs(friends) do
    if consumer_machine('friend', name) == m then
      sum = sum + (tonumber(redis.call('HGET', 'friend:' .. name .. ':desired', 'slots')) or 0)
    end
  end
  return sum
end

local function living_sum_on(m, benches, friends, now_ms)
  local sum = 0
  for _, name in ipairs(benches) do
    if consumer_machine('bench', name) == m then
      sum = sum + redis.call('ZCOUNT', 'bench:' .. name .. ':living', now_ms - LIVING_FRESH_MS, '+inf')
    end
  end
  for _, name in ipairs(friends) do
    if consumer_machine('friend', name) == m then
      sum = sum + redis.call('ZCOUNT', 'friend:' .. name .. ':living', now_ms - LIVING_FRESH_MS, '+inf')
    end
  end
  return sum
end

-- append_procs_lines renders the process lines of section 6 for the lines cut:
-- reconciler, router (the consumers), backpressure and one harvest worker per
-- bench. A stale reconciler pass reads state `?` with its age; a harvest pass
-- whose start item is older than 60 s is a red line.
local function append_procs_lines(out, now, benches)
  local procs = { 'reconciler', 'router', 'backpressure' }
  for _, bench in ipairs(benches) do
    procs[#procs + 1] = 'harvest:' .. bench
  end
  table.sort(procs)
  for _, p in ipairs(procs) do
    local key = 'proc:' .. p
    local pass = tonumber(redis.call('HGET', key, 'pass_at'))
    local state, age, why, red = 'down', -1, '', 0
    if pass then
      age = math.max(0, now - math.floor(pass / 1000))
      if age <= 20 then
        state = 'up'
      else
        state = '?'
      end
    else
      why = 'missing: pass'
    end
    if string.sub(p, 1, 8) == 'harvest:' then
      local start_at = tonumber(redis.call('HGET', key, 'start_at'))
      if start_at then
        local start_age = math.max(0, now - math.floor(start_at / 1000))
        if start_age > 60 then
          red = 1
          why = 'start ' .. tostring(start_age) .. 's'
        end
      end
    end
    out[#out + 1] = 'proc'
    out[#out + 1] = p
    out[#out + 1] = state
    out[#out + 1] = tostring(age)
    out[#out + 1] = why
    out[#out + 1] = tostring(red)
  end
end

-- snapshot_lines answers the machine, ci, clock and process lines as one flat
-- tagged list. Records:
--
--   machine <name> <ceiling> <desired-sum> <living-sum> <load1>
--   ci      <cut> <running> <PENDING> <OK> <FAIL> <FLAKY> <MISSING-at-head>
--           <blocked-no-alternate-bench> <oldest-age>
--   clock   <sprint> <oldest-age> <breach>
--   proc    <name> <state> <age> <why> <red>
local function snapshot_lines(now, now_ms)
  if scard('machines') > MAX_MACHINES or scard('ci:cards') > MAX_CI_CARDS then
    return { 'time', tostring(now), 'error', 'snapshot: bound exceeded' }
  end

  local machines = redis.call('SMEMBERS', 'machines')
  local benches = redis.call('SMEMBERS', 'benches')
  local friends = redis.call('SMEMBERS', 'friends')
  local sprints = redis.call('SMEMBERS', 'sprints')
  table.sort(machines)
  table.sort(benches)
  table.sort(friends)
  table.sort(sprints)

  local out = { 'time', tostring(now) }

  for _, m in ipairs(machines) do
    local ceiling = tonumber(redis.call('HGET', 'machine:' .. m .. ':ceiling', 'slots')) or 0
    local load1 = redis.call('HGET', 'machine:' .. m .. ':ceiling', 'load1')
    out[#out + 1] = 'machine'
    out[#out + 1] = m
    out[#out + 1] = tostring(ceiling)
    out[#out + 1] = tostring(desired_sum_on(m, benches, friends))
    out[#out + 1] = tostring(living_sum_on(m, benches, friends, now_ms))
    out[#out + 1] = load1 or '?'
  end

  local ci_states = { 'cut', 'running', 'PENDING', 'OK', 'FAIL', 'FLAKY',
    'MISSING-at-head', 'blocked-no-alternate-bench' }
  local counts = {}
  for _, s in ipairs(ci_states) do counts[s] = 0 end
  local oldest_at = nil
  for _, id in ipairs(redis.call('SMEMBERS', 'ci:cards')) do
    local key = 'ci:' .. id
    local st = redis.call('HGET', key, 'state')
    if counts[st] then counts[st] = counts[st] + 1 end
    local at = tonumber(redis.call('HGET', key, 'at'))
    if at and (not oldest_at or at < oldest_at) then oldest_at = at end
  end
  out[#out + 1] = 'ci'
  for _, s in ipairs(ci_states) do out[#out + 1] = tostring(counts[s]) end
  if oldest_at then
    out[#out + 1] = tostring(math.max(0, now - math.floor(oldest_at / 1000)))
  else
    out[#out + 1] = '-1'
  end

  for _, sp in ipairs(sprints) do
    local status = redis.call('HGET', 's:' .. sp, 'status')
    if status == 'open' or status == 'paused' then
      local min = redis.call('ZRANGE', 's:' .. sp .. ':clock', 0, 0, 'WITHSCORES')
      local age = -1
      if #min >= 2 then
        age = math.max(0, now - math.floor(tonumber(min[2]) / 1000))
      end
      local limit = tonumber(redis.call('HGET', 's:' .. sp, 'clock')) or 0
      local breach = 0
      if age >= 0 and limit > 0 and age > limit then breach = 1 end
      out[#out + 1] = 'clock'
      out[#out + 1] = sp
      out[#out + 1] = tostring(age)
      out[#out + 1] = tostring(breach)
    end
  end

  append_procs_lines(out, now, benches)
  return out
end

redis.register_function{
  function_name = 'ns_snapshot',
  flags = { 'no-writes' },
  callback = function(keys, args)
    local t = redis.call('TIME')
    local now = tonumber(t[1])
    local now_ms = now * 1000 + math.floor(tonumber(t[2]) / 1000)
    if (args[2] or '') == 'lines' then
      return snapshot_lines(now, now_ms)
    end
    local out = { 'time', tostring(now) }

    -- Bounds first: a registry larger than its bound is an error, and the
    -- bound is checked before any member is read (6.3).
    if scard('sprints') > MAX_SPRINTS or scard('benches') > MAX_BENCHES
      or scard('friends') > MAX_FRIENDS then
      return { 'time', tostring(now), 'error', 'snapshot: bound exceeded' }
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
