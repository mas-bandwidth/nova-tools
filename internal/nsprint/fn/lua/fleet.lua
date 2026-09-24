-- Fleet presence and state machine (#2046 rev 3).
--
-- Global keys:
--   bench:<b>:state   hash (state, since, misses, oks, build, reason, at), no TTL
--   bench:<b>:hold    hash (by, why, at), no TTL
--   cfg:fleet         hash (down_after, up_after, at), no TTL
--   cap:log           stream for fleet-state changes

local function fl_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function fl_caplog(kind, subject, reason, actor, idem, at)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', kind, 'subject', subject, 'reason', reason or '',
    'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
end

-- ns_fleet_config([down_after, up_after])
-- Reads or updates cfg:fleet. Refuses N < 1 with error.
local function fleet_config(keys, args)
  local cur = redis.call('HMGET', 'cfg:fleet', 'down_after', 'up_after')
  local down_after = tonumber(cur[1]) or 30
  local up_after = tonumber(cur[2]) or 10
  local changed = false

  if args[1] and args[1] ~= '' then
    local d = tonumber(args[1])
    if not d or d < 1 then
      return redis.error_reply('down_after must be >= 1')
    end
    down_after = d
    changed = true
  end

  if args[2] and args[2] ~= '' then
    local u = tonumber(args[2])
    if not u or u < 1 then
      return redis.error_reply('up_after must be >= 1')
    end
    up_after = u
    changed = true
  end

  if changed then
    local at = fl_now_ms()
    redis.call('HSET', 'cfg:fleet',
      'down_after', tostring(down_after),
      'up_after', tostring(up_after),
      'at', tostring(at))
  end

  return { 'OK', tostring(down_after), tostring(up_after) }
end

-- ns_fleet_hold(bench, why, by)
local function fleet_hold(keys, args)
  local bench = args[1]
  local why = args[2]
  local by = args[3] or ''
  if not bench or bench == '' or not why or why == '' then
    return { 'USAGE' }
  end
  if redis.call('SISMEMBER', 'benches', bench) == 0 then
    return { 'UNREGISTERED' }
  end

  local cur = redis.call('HMGET', 'bench:' .. bench .. ':state', 'state', 'since', 'build')
  local cur_state = cur[1]
  local cur_since = cur[2]
  local from = (cur_state and cur_state ~= '') and cur_state or 'DOWN'
  local at = fl_now_ms()

  redis.call('HSET', 'bench:' .. bench .. ':hold',
    'by', by,
    'why', why,
    'at', tostring(at))

  local since = (cur_state == 'HELD' and cur_since and cur_since ~= '') and cur_since or tostring(at)
  local state_key = 'bench:' .. bench .. ':state'
  redis.call('HSET', state_key,
    'state', 'HELD',
    'since', since,
    'reason', why,
    'at', tostring(at))

  if cur_state ~= 'HELD' then
    fl_caplog('fleet-state', bench, from .. '->HELD ' .. why, by, '', at)
  end

  return { 'OK' }
end

-- ns_fleet_release(bench)
-- Exits with NOTHELD if not held.
local function fleet_release(keys, args)
  local bench = args[1]
  if not bench or bench == '' then
    return { 'USAGE' }
  end
  if redis.call('SISMEMBER', 'benches', bench) == 0 then
    return { 'UNREGISTERED' }
  end

  local cur_state = redis.call('HGET', 'bench:' .. bench .. ':state', 'state')
  if cur_state ~= 'HELD' then
    return { 'NOTHELD' }
  end

  local at = fl_now_ms()
  redis.call('DEL', 'bench:' .. bench .. ':hold')
  local state_key = 'bench:' .. bench .. ':state'
  redis.call('HSET', state_key,
    'state', 'PROBING',
    'since', tostring(at),
    'oks', '0',
    'misses', '0',
    'reason', '',
    'at', tostring(at))

  fl_caplog('fleet-state', bench, 'HELD->PROBING', '', '', at)
  return { 'OK' }
end

-- ns_fleet_read([bench])
-- Read-only function.
local function fleet_read(keys, args)
  local bench = args[1] or ''
  if bench ~= '' then
    if redis.call('SISMEMBER', 'benches', bench) == 0 then
      return { 'UNREGISTERED' }
    end
    local f = redis.call('HMGET', 'bench:' .. bench .. ':state', 'state', 'since', 'build', 'reason')
    local state = f[1] or ''
    if state == '' then state = 'DOWN' end
    local since = f[2] or ''
    local build = f[3] or ''
    local reason = f[4] or ''
    return { 'OK', bench, state, since, build, reason }
  end

  local benches = redis.call('SMEMBERS', 'benches')
  table.sort(benches)
  local out = { 'OK' }
  for _, b in ipairs(benches) do
    local f = redis.call('HMGET', 'bench:' .. b .. ':state', 'state', 'since', 'build', 'reason')
    local state = f[1] or ''
    if state == '' then state = 'DOWN' end
    local since = f[2] or ''
    local build = f[3] or ''
    local reason = f[4] or ''
    out[#out + 1] = b
    out[#out + 1] = state
    out[#out + 1] = since
    out[#out + 1] = build
    out[#out + 1] = reason
  end
  return out
end

-- ns_fleet_step([bench...])
-- Evaluates UP/DOWN/PROBING/HELD for all benches (or passed benches).
local function fleet_step(keys, args)
  local cfg = redis.call('HMGET', 'cfg:fleet', 'down_after', 'up_after')
  local down_after = tonumber(cfg[1]) or 30
  local up_after = tonumber(cfg[2]) or 10
  local at = fl_now_ms()

  local benches = {}
  if #args > 0 then
    for _, b in ipairs(args) do
      if b and b ~= '' then
        benches[#benches + 1] = b
      end
    end
  else
    benches = redis.call('SMEMBERS', 'benches')
  end
  table.sort(benches)

  for _, b in ipairs(benches) do
    local has_beat = redis.call('EXISTS', 'bench:' .. b .. ':beat') == 1
    local beat_build = ''
    if has_beat then
      beat_build = redis.call('HGET', 'bench:' .. b .. ':beat', 'build') or ''
    end

    local cur = redis.call('HMGET', 'bench:' .. b .. ':state', 'state', 'since', 'misses', 'oks', 'build', 'reason')
    local cur_state = cur[1]
    local cur_since = cur[2]
    local cur_misses = tonumber(cur[3]) or 0
    local cur_oks = tonumber(cur[4]) or 0
    local cur_build = cur[5] or ''
    local cur_reason = cur[6] or ''

    local new_state, new_oks, new_misses, new_since, new_build, new_reason

    if not cur_state or cur_state == '' then
      -- no record: first sight; this pass's beat is not counted
      new_state = 'DOWN'
      new_oks = 0
      new_misses = 0
      new_since = tostring(at)
      new_build = ''
      new_reason = ''
    elseif cur_state == 'DOWN' then
      if has_beat then
        new_oks = 1
        new_misses = 0
        if new_oks >= up_after then
          new_state = 'UP'
        else
          new_state = 'PROBING'
        end
        new_reason = ''
        new_since = tostring(at)
        new_build = beat_build ~= '' and beat_build or cur_build
      else
        new_state = 'DOWN'
        new_oks = 0
        new_misses = cur_misses + 1
        new_since = (cur_since and cur_since ~= '') and cur_since or tostring(at)
        new_build = cur_build
        new_reason = cur_reason
      end
    elseif cur_state == 'PROBING' then
      if has_beat then
        new_oks = cur_oks + 1
        new_misses = 0
        new_build = beat_build ~= '' and beat_build or cur_build
        if new_oks >= up_after then
          new_state = 'UP'
          new_since = tostring(at)
        else
          new_state = 'PROBING'
          new_since = (cur_since and cur_since ~= '') and cur_since or tostring(at)
        end
        new_reason = ''
      else
        new_state = 'DOWN'
        new_oks = 0
        new_misses = 0
        new_since = tostring(at)
        new_build = cur_build
        new_reason = ''
      end
    elseif cur_state == 'UP' then
      if has_beat then
        new_state = 'UP'
        new_misses = 0
        new_oks = cur_oks + 1
        new_since = (cur_since and cur_since ~= '') and cur_since or tostring(at)
        new_build = beat_build ~= '' and beat_build or cur_build
        new_reason = cur_reason
      else
        new_misses = cur_misses + 1
        new_oks = cur_oks
        if new_misses >= down_after then
          new_state = 'DOWN'
          new_oks = 0
          new_since = tostring(at)
          new_reason = ''
        else
          new_state = 'UP'
          new_since = (cur_since and cur_since ~= '') and cur_since or tostring(at)
          new_reason = cur_reason
        end
        new_build = cur_build
      end
    elseif cur_state == 'HELD' then
      new_state = 'HELD'
      new_since = (cur_since and cur_since ~= '') and cur_since or tostring(at)
      new_oks = cur_oks
      new_misses = cur_misses
      new_build = (has_beat and beat_build ~= '') and beat_build or cur_build
      new_reason = cur_reason
    end

    local state_key = 'bench:' .. b .. ':state'
    redis.call('HSET', state_key,
      'state', new_state,
      'since', new_since,
      'misses', tostring(new_misses),
      'oks', tostring(new_oks),
      'build', new_build,
      'reason', new_reason,
      'at', tostring(at))

    if cur_state and cur_state ~= '' and cur_state ~= new_state then
      local why_str = (new_reason and new_reason ~= '') and (' ' .. new_reason) or ''
      fl_caplog('fleet-state', b, cur_state .. '->' .. new_state .. why_str, '', '', at)
    end
  end

  return { 'OK' }
end

redis.register_function('ns_fleet_config', fleet_config)
redis.register_function('ns_fleet_hold', fleet_hold)
redis.register_function('ns_fleet_release', fleet_release)
redis.register_function{
  function_name = 'ns_fleet_read',
  flags = { 'no-writes' },
  callback = fleet_read,
}
redis.register_function('ns_fleet_step', fleet_step)
