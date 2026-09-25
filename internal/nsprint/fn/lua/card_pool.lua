-- A dependency that is not landed keeps the card in waiting. Release moves
-- that one card into the pool only when every dependency has state landed
-- and a 40-hex merge commit. It does not copy the waiting set across.

local function safe_id(s)
  return type(s) == 'string' and string.match(s, '^[A-Za-z0-9][A-Za-z0-9._-]*$') ~= nil
end

local function is_merge(s)
  return type(s) == 'string' and #s == 40 and string.match(s, '^[0-9a-f]+$') ~= nil
end

local function now_s()
  local t = redis.call('TIME')
  return t[1]
end

-- keys: card, pool, waiting, log, idx queued
-- args: label, payload_sha, priority, base, base_sha, paths, repo, kind,
--       depends_on, card_type (the optional TYPE: line, nova-tools#3091;
--       empty: not stored), depends_on_typed, ready (0|1), route (the
--       card's ROUTE: pro|flash tier; empty or absent: not stored), bench
--       (the card's BENCH: line, nova-tools#3650; empty or absent: any bench,
--       not stored). A bench not in the benches set returns NOBENCH and
--       writes nothing; a named bench is stored as the card's bench field,
--       the pin ns_card_deal honours (pin == '' or pin == bench).
--       card's ROUTE: pro|flash tier; empty or absent: not stored), est
--       (the card's EST: line in minutes, #3653; empty or absent: not stored).
-- priority is the card's PRIORITY: line (0 when absent) and its pool score.
-- ready is resolved by the Go caller (card.Push); a caller that omits it
-- (the pre-#3503 ten-argument shape) gets pool only when depends_on is empty.
-- The card also gets cut_at, Redis TIME seconds at this push (nova-tools#3091).
redis.register_function('ns_card_push', function(keys, args)
  local card, pool, waiting, log, idx = keys[1], keys[2], keys[3], keys[4], keys[5]
  local label, payload, priority = args[1], args[2], args[3]
  local base, base_sha, paths = args[4], args[5], args[6]
  local repo, kind, depends_on = args[7], args[8], args[9]
  local card_type = args[10]
  local depends_on_typed, ready_arg = args[11], args[12]
  local route = args[13]
  local bench = args[14]
  local est = args[15]
  -- test (#3689): the card's TEST line, which the wrapper runs at card end.
  local test = args[16]
  if type(depends_on) ~= 'string' then
    depends_on = ''
  end
  if redis.call('EXISTS', card) == 1 then
    if redis.call('HGET', card, 'payload_sha') == payload then
      return 'EXISTS'
    end
    return 'CONFLICT'
  end
  if type(bench) ~= 'string' then
    bench = ''
  end
  if bench ~= '' and redis.call('SISMEMBER', 'benches', bench) == 0 then
    return 'NOBENCH'
  end
  if type(depends_on_typed) ~= 'string' then
    depends_on_typed = ''
  end
  local ready = ready_arg == '1'
  if ready_arg == nil and depends_on == '' then
    ready = true
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
    'depends_on_typed', depends_on_typed,
    'priority', priority,
    'payload_sha', payload,
    'state', 'queued',
    'cut_at', now_s()}
  if type(card_type) == 'string' and card_type ~= '' then
    table.insert(fields, 'card_type')
    table.insert(fields, card_type)
  end
  if type(route) == 'string' and route ~= '' then
    table.insert(fields, 'route')
    table.insert(fields, route)
  end
  if bench ~= '' then
    table.insert(fields, 'bench')
    table.insert(fields, bench)
  end
  if type(est) == 'string' and est ~= '' then
    table.insert(fields, 'est')
    table.insert(fields, est)
  end
  if type(test) == 'string' and test ~= '' then
    table.insert(fields, 'test')
    table.insert(fields, test)
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
-- args: sprint, then the labels whose typed dependencies the caller resolved
--       in this pass (GitHub reads are cached by the caller).
redis.register_function('ns_card_release', function(keys, args)
  local waiting, pool, log = keys[1], keys[2], keys[3]
  local sprint = args[1]
  if type(sprint) ~= 'string' or string.match(sprint, '^[-a-z0-9]+$') == nil or #sprint > 40 or #sprint < 1 then
    return redis.error_reply('bad sprint')
  end
  local releasable = {}
  for i = 2, #args do
    if safe_id(args[i]) then
      releasable[args[i]] = true
    end
  end
  local labels = redis.call('SMEMBERS', waiting)
  local moved, still = 0, 0
  for _, label in ipairs(labels) do
    if not safe_id(label) then
      still = still + 1
    else
      local card = 's:' .. sprint .. ':card:' .. label
      if releasable[label] and redis.call('HGET', card, 'state') == 'queued' then
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

-- ns_card_resume is drain's resume step (#3035), a Function since #3419: the
-- card package sends no EVAL/EVALSHA on any path. It takes the member out of
-- the paused set and moves each parked card that is still queued into the
-- pool at its own priority. A parked label whose card is gone or no longer
-- queued (landed, cancelled) leaves the parked set and is not pooled.
-- keys: paused, parked, pool, log; args: member, sprint
-- Returns {was_paused, moved, dropped}.
redis.register_function('ns_card_resume', function(keys, args)
  local paused, parked, pool, log = keys[1], keys[2], keys[3], keys[4]
  local member, sprint = args[1], args[2]
  local was = redis.call('SREM', paused, member)
  local moved, dropped = 0, 0
  for _, label in ipairs(redis.call('SMEMBERS', parked)) do
    redis.call('SREM', parked, label)
    local card = 's:' .. sprint .. ':card:' .. label
    if safe_id(label) and redis.call('HGET', card, 'state') == 'queued' then
      local priority = redis.call('HGET', card, 'priority')
      if type(priority) ~= 'string' or priority == '' then
        priority = '0'
      end
      redis.call('ZADD', pool, priority, label)
      redis.call('XADD', log, '*',
        'kind', 'card', 'id', label, 'from', 'queued', 'to', 'queued',
        'place', 'pool', 'actor', 'resume', 'reason', 'resume ' .. member, 'at', now_s())
      moved = moved + 1
    else
      dropped = dropped + 1
    end
  end
  return { was, moved, dropped }
end)

-- ns_card_import_receipt writes drain's one import receipt for a card (#3035),
-- a Function since #3419. The fence is the label in s:<S>:drain:imported: a
-- re-run after a crash between the receipt and the file removal finds it and
-- writes nothing.
-- keys: imported, log; args: label, payload_sha, file, place
-- Returns 1 when it wrote the receipt, 0 when the fence already held it.
redis.register_function('ns_card_import_receipt', function(keys, args)
  if redis.call('HSETNX', keys[1], args[1], args[2]) == 0 then
    return 0
  end
  redis.call('XADD', keys[2], '*',
    'kind', 'card', 'id', args[1], 'actor', 'drain-import', 'reason', 'import',
    'file', args[3], 'place', args[4], 'payload_sha', args[2], 'at', now_s())
  return 1
end)
