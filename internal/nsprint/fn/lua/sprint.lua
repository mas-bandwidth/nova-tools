-- Sprint open and status (#2939 rev 6). No shebang: loader.go prepends the
-- single library header. ns_sprint_begin and ns_sprint_open re-make the
-- existing-sprint decision atomically, so a close or a second open with
-- another file that lands between the verb's read and its write still wins;
-- ns_sprint_status is the one read-only round trip behind `sprint status`.

local STATUS_MAX_SPRINTS = 64

local function sha8(s)
  if s == nil or s == false or s == '' then
    return 'none'
  end
  return string.sub(s, 1, 8)
end

-- decide is the existing-sprint rule over s:<S>: closed refuses, an open or
-- opening sprint with another from_sha refuses, one with the same from_sha
-- resumes, and a hash with no status is a first open.
local function decide(S, from_sha)
  local cur = redis.call('HMGET', 's:' .. S, 'status', 'from_sha')
  local status, old = cur[1], cur[2] or ''
  if not status then
    return 'FIRST'
  end
  if status == 'closed' then
    return 'REFUSED', { 'REFUSED', 'closed' }
  end
  if old ~= from_sha then
    return 'REFUSED', { 'REFUSED', 'from_sha', sha8(old) }
  end
  return 'RESUME'
end

-- sprint_begin: args S, set id, from_sha, clock (ms). A first open writes
-- status=opening with its source; a resume writes nothing.
local function sprint_begin(keys, args)
  local S, from, from_sha, at = args[1], args[2], args[3], args[4]
  local verdict, reply = decide(S, from_sha)
  if verdict == 'REFUSED' then
    return reply
  end
  if verdict == 'RESUME' then
    return { 'RESUME' }
  end
  redis.call('HSET', 's:' .. S, 'status', 'opening', 'from', from, 'from_sha', from_sha, 'at', at)
  return { 'BEGUN' }
end

-- sprint_open: args S, set id, from_sha, clock (ms), then the unit ids. It
-- re-checks closed and from_sha, stamps opened_at once, sets status=open,
-- adds the units and places <S> in sprints and sprint:order.
local function sprint_open(keys, args)
  local S, from, from_sha, at = args[1], args[2], args[3], args[4]
  local verdict, reply = decide(S, from_sha)
  if verdict == 'REFUSED' then
    return reply
  end
  local key = 's:' .. S
  local first = redis.call('HSETNX', key, 'opened_at', at)
  local units = {}
  for i = 5, #args do
    units[#units + 1] = args[i]
  end
  redis.call('HSET', key, 'status', 'open', 'from', from, 'from_sha', from_sha,
    'units', tostring(#units), 'at', at)
  if #units > 0 then
    redis.call('SADD', key .. ':units', unpack(units))
  end
  redis.call('SADD', 'sprints', S)
  redis.call('ZADD', 'sprint:order', 'NX', tonumber(at), S)
  if first == 1 then
    return { 'OPENED' }
  end
  return { 'RESUMED' }
end

-- The task indexes that count toward y: every one but cancelled. The card
-- states are the ones the snapshot reads (append_pipeline in snapshot.lua).
local TASK_Y = { 'open', 'claimed', 'working', 'waiting', 'waiting-ci', 'parked',
  'closed', 'reconcile-required' }
local CARD_Y = { 'queued', 'dealt', 'running', 'ended', 'harvested', 'review-ready',
  'land-ready', 'landed', 'orphan-effect', 'reconcile-required' }

local function status_row(out, S)
  local prefix = 's:' .. S .. ':'
  local cur = redis.call('HMGET', 's:' .. S, 'status', 'opened_at')
  local x = redis.call('SCARD', prefix .. 'idx:task:closed') +
    redis.call('SCARD', prefix .. 'idx:card:landed')
  local y = 0
  for _, idx in ipairs(TASK_Y) do
    y = y + redis.call('SCARD', prefix .. 'idx:task:' .. idx)
  end
  for _, idx in ipairs(CARD_Y) do
    y = y + redis.call('SCARD', prefix .. 'idx:card:' .. idx)
  end
  out[#out + 1] = S
  out[#out + 1] = cur[1] or 'absent'
  out[#out + 1] = tostring(x)
  out[#out + 1] = tostring(y)
  out[#out + 1] = cur[2] or ''
end

-- sprint_status: args S (empty for every open sprint in sprint:order). Rows
-- of name, status, x, y, opened_at (ms). Bounded like ns_snapshot: more than
-- STATUS_MAX_SPRINTS in sprint:order is an error, checked before any read.
local function sprint_status(keys, args)
  local S = args[1] or ''
  local out = {}
  if S ~= '' then
    status_row(out, S)
    return out
  end
  if redis.call('ZCARD', 'sprint:order') > STATUS_MAX_SPRINTS then
    return redis.error_reply('sprint status: sprint:order holds more than ' ..
      STATUS_MAX_SPRINTS .. ' sprints; name one with --sprint')
  end
  for _, name in ipairs(redis.call('ZRANGE', 'sprint:order', 0, -1)) do
    if redis.call('HGET', 's:' .. name, 'status') == 'open' then
      status_row(out, name)
    end
  end
  return out
end

redis.register_function('ns_sprint_begin', sprint_begin)
redis.register_function('ns_sprint_open', sprint_open)
redis.register_function{ function_name = 'ns_sprint_status', callback = sprint_status,
  flags = { 'no-writes' } }
