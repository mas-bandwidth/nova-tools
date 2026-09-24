-- land_publish.lua: the publisher's fenced steps (Issue #3139 rev 7, 7.1-7.2, B7).
-- land.lua's ns_pub_state and ns_land stay as they are; these two add the
-- lease fence to the steps a publisher writes between the intent and ns_land,
-- so a resumed stale publisher writes nothing (L28 iii). No new top-level
-- locals: every lua/ file shares one chunk.

-- ns_pub_step: pushed, verified or dead on an unresolved intent, under the
-- publisher fence (writer owner nova-sprint, lease equal, lease gen = writer gen).
-- STALE when the fence fails or the intent is already resolved (landed or dead).
redis.register_function('ns_pub_step', function(keys, args)
  local repo, base, batch_id, state, lease_val = args[1], args[2], args[3], args[4], args[5]
  if state ~= 'pushed' and state ~= 'verified' and state ~= 'dead' then
    return redis.error_reply('ns_pub_step: state must be pushed, verified or dead')
  end
  local pre = 'land:' .. repo .. ':' .. base
  local writer = redis.call('HMGET', pre .. ':writer', 'gen', 'owner')
  if writer[2] ~= 'nova-sprint' then return 'STALE' end
  if redis.call('GET', pre .. ':lease') ~= lease_val then return 'STALE' end
  if string.match(lease_val or '', '^([^:]+):') ~= writer[1] then return 'STALE' end
  local pub_key = pre .. ':pub:' .. batch_id
  local cur = redis.call('HGET', pub_key, 'state')
  if not cur or cur == 'dead' then return 'STALE' end
  local t = redis.call('TIME')
  local now = string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
  redis.call('HSET', pub_key, 'state', state, 'at_' .. state, now)
  if state == 'dead' and redis.call('GET', pre .. ':pub:active') == batch_id then
    redis.call('DEL', pre .. ':pub:active')
  end
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', string.upper(state), 'repo', repo, 'base', base, 'batch', batch_id, 'at', now)
  return 'OK'
end)

-- ns_tip: the tip record from a read of the remote (by=fetch), under the same
-- fence: after a verified-descendant landing or a dead intent (7.2).
redis.register_function('ns_tip', function(keys, args)
  local repo, base, sha, by, lease_val = args[1], args[2], args[3], args[4], args[5]
  local pre = 'land:' .. repo .. ':' .. base
  local writer = redis.call('HMGET', pre .. ':writer', 'gen', 'owner')
  if writer[2] ~= 'nova-sprint' then return 'STALE' end
  if redis.call('GET', pre .. ':lease') ~= lease_val then return 'STALE' end
  if string.match(lease_val or '', '^([^:]+):') ~= writer[1] then return 'STALE' end
  local t = redis.call('TIME')
  local now = string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
  redis.call('HSET', pre .. ':tip', 'sha', sha, 'at', now, 'by', by)
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'TIP', 'repo', repo, 'base', base, 'sha', sha, 'by', by, 'at', now)
  return 'OK'
end)
