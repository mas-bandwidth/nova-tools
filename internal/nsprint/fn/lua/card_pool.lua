-- A dependency that is not landed keeps the card in waiting. Release moves
-- that one card into the pool only when every dependency has state landed
-- and a 40-hex merge commit. It does not copy the waiting set across.

local function safe_id(s)
  return type(s) == 'string' and string.match(s, '^[A-Za-z0-9][A-Za-z0-9._-]*$') ~= nil
end

local function is_merge(s)
  return type(s) == 'string' and #s == 40 and string.match(s, '^[0-9a-f]+$') ~= nil
end

local function is_landed(key)
  local st = redis.call('HGET', key, 'state')
  local merge = redis.call('HGET', key, 'merge_sha')
  return st == 'landed' and is_merge(merge)
end

local function now_s()
  local t = redis.call('TIME')
  return t[1]
end

-- keys: card, pool, waiting, log, idx queued, then one card hash per dependency
-- args: label, payload_sha, priority, base, base_sha, paths, repo, kind, depends_on,
-- card_type (the optional TYPE: line, nova-tools#3091; empty: not stored)
-- The card also gets cut_at, Redis TIME seconds at this push (nova-tools#3091).
redis.register_function('ns_card_push', function(keys, args)
  local card, pool, waiting, log, idx = keys[1], keys[2], keys[3], keys[4], keys[5]
  local label, payload, priority = args[1], args[2], args[3]
  local base, base_sha, paths = args[4], args[5], args[6]
  local repo, kind, depends_on = args[7], args[8], args[9]
  local card_type = args[10]
  if type(depends_on) ~= 'string' then
    depends_on = ''
  end
  if redis.call('EXISTS', card) == 1 then
    if redis.call('HGET', card, 'payload_sha') == payload then
      return 'EXISTS'
    end
    return 'CONFLICT'
  end
  local dep_count = 0
  if depends_on ~= '' then
    for _ in string.gmatch(depends_on, '[^,]+') do
      dep_count = dep_count + 1
    end
  end
  if #keys - 5 ~= dep_count then
    return redis.error_reply('dependency keys do not match depends_on')
  end
  local ready = true
  for i = 6, #keys do
    if not is_landed(keys[i]) then
      ready = false
    end
  end
  local place = 'waiting'
  if ready then
    place = 'pool'
  end
  local fields = {
    'label', label,
    'kind', kind,
    'repo', repo,
    'base', base,
    'base_sha', base_sha,
    'paths', paths,
    'depends_on', depends_on,
    'priority', priority,
    'payload_sha', payload,
    'state', 'queued',
    'cut_at', now_s()}
  if type(card_type) == 'string' and card_type ~= '' then
    table.insert(fields, 'card_type')
    table.insert(fields, card_type)
  end
  redis.call('HSET', card, unpack(fields))
  redis.call('SADD', idx, label)
  if place == 'pool' then
    redis.call('ZADD', pool, priority, label)
  else
    redis.call('SADD', waiting, label)
  end
  redis.call('XADD', log, '*',
    'kind', 'card',
    'id', label,
    'from', '',
    'to', 'queued',
    'place', place,
    'actor', 'card-push',
    'reason', 'push',
    'at', now_s())
  return 'OK place=' .. place
end)

-- keys: waiting, pool, log
-- args: sprint
redis.register_function('ns_card_release', function(keys, args)
  local waiting, pool, log = keys[1], keys[2], keys[3]
  local sprint = args[1]
  if type(sprint) ~= 'string' or string.match(sprint, '^[-a-z0-9]+$') == nil or #sprint > 40 or #sprint < 1 then
    return redis.error_reply('bad sprint')
  end
  local labels = redis.call('SMEMBERS', waiting)
  local moved, still = 0, 0
  for _, label in ipairs(labels) do
    if not safe_id(label) then
      still = still + 1
    else
      local card = 's:' .. sprint .. ':card:' .. label
      local deps = redis.call('HGET', card, 'depends_on')
      if type(deps) ~= 'string' then
        deps = ''
      end
      local ready = true
      for dep in string.gmatch(deps, '[^,]+') do
        if not safe_id(dep) or not is_landed('s:' .. sprint .. ':card:' .. dep) then
          ready = false
          break
        end
      end
      if ready then
        redis.call('SREM', waiting, label)
        local priority = redis.call('HGET', card, 'priority')
        if type(priority) ~= 'string' or priority == '' then
          priority = '0'
        end
        redis.call('ZADD', pool, priority, label)
        redis.call('XADD', log, '*',
          'kind', 'card',
          'id', label,
          'from', 'queued',
          'to', 'queued',
          'place', 'pool',
          'actor', 'card-release',
          'reason', 'release',
          'at', now_s())
        moved = moved + 1
      else
        still = still + 1
      end
    end
  end
  return 'moved=' .. tostring(moved) .. ' waiting=' .. tostring(still)
end)

-- keys: card, pool, waiting, log, idx queued, idx landed
-- args: label, merge_sha
-- The merge commit is the landing. This does not move dependents; release does.
redis.register_function('ns_card_land', function(keys, args)
  local card, pool, waiting = keys[1], keys[2], keys[3]
  local log, idx_q, idx_l = keys[4], keys[5], keys[6]
  local label, merge = args[1], args[2]
  if redis.call('EXISTS', card) == 0 then
    return 'ABSENT'
  end
  if not is_merge(merge) then
    return 'REFUSED'
  end
  local st = redis.call('HGET', card, 'state')
  if type(st) ~= 'string' then
    st = ''
  end
  local old = redis.call('HGET', card, 'merge_sha')
  if st == 'landed' and old == merge then
    return 'OK'
  end
  if st == 'landed' then
    return 'CONFLICT'
  end
  redis.call('HSET', card, 'state', 'landed', 'merge_sha', merge)
  redis.call('ZREM', pool, label)
  redis.call('SREM', waiting, label)
  redis.call('SREM', idx_q, label)
  redis.call('SADD', idx_l, label)
  redis.call('XADD', log, '*',
    'kind', 'card',
    'id', label,
    'from', st,
    'to', 'landed',
    'actor', 'card-land',
    'reason', 'merged',
    'evidence', merge,
    'at', now_s())
  return 'OK'
end)
