-- The sole Redis writer for nova-sprint land flaky observe (#3098).
-- ARGV: sprint, record key, lane, token, optional issue number|post|abort.
local function now_parts()
  local t = redis.call('TIME')
  local sec, usec = tonumber(t[1]), tonumber(t[2])
  local days, sod = math.floor(sec / 86400), sec % 86400
  local z = days + 719468
  local era = math.floor(z / 146097)
  local doe = z - era * 146097
  local yoe = math.floor((doe - math.floor(doe / 1460) + math.floor(doe / 36524) - math.floor(doe / 146096)) / 365)
  local year = yoe + era * 400
  local doy = doe - (365 * yoe + math.floor(yoe / 4) - math.floor(yoe / 100))
  local mp = math.floor((5 * doy + 2) / 153)
  local day = doy - math.floor((153 * mp + 2) / 5) + 1
  local month = mp + (mp < 10 and 3 or -9)
  if month <= 2 then year = year + 1 end
  local hour, minute, second = math.floor(sod / 3600), math.floor((sod % 3600) / 60), sod % 60
  local iso = string.format('%04d-%02d-%02dT%02d:%02d:%02d.%06dZ', year, month, day, hour, minute, second, usec)
  return iso, sec * 1000 + math.floor(usec / 1000)
end

local function fields(key, status, extra)
  local v = redis.call('HMGET', key, 'first_seen', 'lanes_hit', 'issue', 'last_at', 'last_lane', 'at', 'token')
  return { status, v[1] or '', v[2] or '0', v[3] or '0', v[4] or '', v[5] or '', v[6] or '', v[7] or '', extra or '' }
end

local function owned_delete(key, token)
  if redis.call('GET', key) == token then redis.call('DEL', key) end
end

local function flaky_observe(keys, args)
  local S, key, lane, token, arg = args[1], args[2], args[3], args[4], args[5]
  if not S or S == '' or not key or key == '' or not lane or lane == '' or not token or token == '' then
    return redis.error_reply('ns_flaky_observe: sprint, key, lane and token are required')
  end
  local suffix = string.sub(key, 7)
  local intent, lock = 'flaky:intent:' .. suffix, 'flaky:lock:' .. suffix
  local now, now_ms = now_parts()

  if arg == 'abort' then
    owned_delete(lock, token)
    if redis.call('HGET', intent, 'token') == token then redis.call('DEL', intent) end
    return { 'ABORTED' }
  end

  if redis.call('EXISTS', key) == 1 then
    if arg and arg ~= '' and arg ~= 'post' then
      local issue = tonumber(arg)
      if not issue then return redis.error_reply('ns_flaky_observe: issue must be numeric') end
      local kept, owner = tonumber(redis.call('HGET', key, 'issue') or '0'), redis.call('HGET', key, 'token')
      if kept == issue and owner == token then return fields(key, 'FILED') end
      redis.call('XADD', 's:' .. S .. ':log', '*', 'kind', 'flaky', 'id', key, 'reason', 'duplicate', 'evidence', '#' .. tostring(issue))
      return fields(key, 'DUPLICATE', tostring(issue))
    end
    if arg == 'post' then return fields(key, 'SEEN') end
    redis.call('HINCRBY', key, 'lanes_hit', 1)
    redis.call('HSET', key, 'last_at', now, 'last_lane', lane, 'at', now)
    redis.call('XADD', 's:' .. S .. ':log', '*', 'kind', 'flaky', 'id', key, 'reason', 'seen', 'evidence', '#' .. (redis.call('HGET', key, 'issue') or '0'))
    return fields(key, 'SEEN')
  end

  if arg == 'post' then
    if redis.call('GET', lock) ~= token then return { 'FILING' } end
    redis.call('HSET', intent, 'token', token, 'lane', lane, 'at', now, 'at_ms', tostring(now_ms))
    return { 'POST', now, tostring(now_ms) }
  end

  if arg and arg ~= '' then
    local issue = tonumber(arg)
    if not issue then return redis.error_reply('ns_flaky_observe: issue must be numeric') end
    if redis.call('GET', lock) ~= token then return { 'FILING' } end
    redis.call('HSET', key, 'first_seen', now, 'lanes_hit', 1, 'issue', issue,
      'last_at', now, 'last_lane', lane, 'at', now, 'token', token)
    redis.call('SADD', 'flaky:idx', key)
    local reconciled = redis.call('HGET', intent, 'reconcile') == '1'
    redis.call('DEL', intent)
    owned_delete(lock, token)
    redis.call('XADD', 's:' .. S .. ':log', '*', 'kind', 'flaky', 'id', key,
      'reason', reconciled and 'reconciled' or 'first', 'evidence', '#' .. tostring(issue))
    return fields(key, 'FILED', reconciled and 'reconciled' or '')
  end

  if not redis.call('SET', lock, token, 'NX', 'PX', 120000) then return { 'FILING' } end
  if redis.call('EXISTS', intent) == 1 then
    local intent_time = redis.call('HMGET', intent, 'at', 'at_ms')
    local at, at_ms = intent_time[1], intent_time[2]
    redis.call('HSET', intent, 'token', token, 'lane', lane, 'reconcile', '1')
    return { 'NEW', '1', at or '', at_ms or '0', tostring(now_ms - tonumber(at_ms or now_ms)) }
  end
  redis.call('HSET', intent, 'token', token, 'lane', lane, 'at', now, 'at_ms', tostring(now_ms), 'reconcile', '0')
  return { 'NEW', '0', now, tostring(now_ms), '0' }
end

redis.register_function('ns_flaky_observe', flaky_observe)
