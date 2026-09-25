-- Task beat, cancel and reconciler expiry for #2745. No shebang: the loader
-- (internal/nsprint/fn/loader.go) prepends the single library header and
-- embeds every lua/ file into one nova_sprint library. Each transition below
-- is one Redis Function call that checks the guard, reads server TIME, moves
-- the id between index sets, updates the global friend leases and appends
-- exactly one receipt, all atomically (spec #2756 2.1 rule 2, 3.1, 4.2).
--
-- The claim fence is #2929's stored `token` field: task_done (task_claim.lua)
-- refuses with exit 3 FENCED whenever `HGET key token` is not the token the
-- caller presents. On expiry and cancellation we replace that field with the
-- literal `fenced` before reopening, so the superseded attempt's existing
-- Done refuses and cannot be replayed. The existing Done code is not touched.

local function now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function receipt(S, kind, id, from_state, to_state, attempt, token_sha, actor, reason, evidence, idem, at)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
    'attempt', tostring(attempt or 0), 'token_sha', token_sha or '',
    'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
    'idem', idem or '', 'at', tostring(at))
end

-- A released global friend slot wakes the capacity reconciler. This event is
-- written in the same Function call as the task transition and its receipt.
local function slot_freed(S, id, friend, attempt, at)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', 'slot-freed', 'consumer', 'friend:' .. friend,
    'sprint', S, 'id', id, 'attempt', tostring(attempt), 'at', tostring(at))
end

-- requeue puts a reopened task back where its owner or the ready queue can
-- claim it again, through the one task move (NS.task, 02_card_move.lua):
-- the owner is kept (spec 3.1), so the work goes back to its friend's ready
-- queue at its priority; an ownerless task falls back to the sprint's.
local function requeue(S, id, why, actor, fields)
  return NS.task.set(id, 'open', { sprint = S, by = actor, why = why, fields = fields })
end

-- to_state is the one move into a fine state that keeps its where
-- (claimed -> working, working -> reconcile-required).
local function to_state(S, id, state, why, actor, fields)
  return NS.task.set(id, state, { sprint = S, by = actor, why = why, fields = fields })
end

-- task_beat: the process that started the child sends this every 60 s. The
-- first beat of a claimed task is the start acknowledgement (claimed ->
-- working); every later beat refreshes the working lease (working -> working).
-- A missing or superseded token refuses FENCED, so a stale wrapper can never
-- renew an attempt that the reconciler already reopened (spec 2.1 rule 8).
local function task_beat(keys, args)
  local S, id, token, actor, idem = args[1], args[2], args[3], args[4], args[5]
  local key = 'task:' .. id

  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  if redis.call('HGET', key, 'token') ~= token then
    return { 'FENCED' }
  end
  local state = redis.call('HGET', key, 'state')
  if state ~= 'claimed' and state ~= 'working' then
    return { 'INVALID' }
  end

  local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
  local friend = redis.call('HGET', key, 'owner') or ''
  local token_sha = redis.call('HGET', key, 'token_sha') or ''
  local identity = S .. '/' .. id .. '/' .. attempt
  local at = now_ms()

  if state == 'claimed' then
    to_state(S, id, 'working', 'start-ack', actor, { 'started_at', tostring(at), 'beat_at', tostring(at) })
    NS.task.renew(id, at)
    redis.call('ZREM', 'friend:' .. friend .. ':starting', identity)
    redis.call('ZADD', 'friend:' .. friend .. ':living', at, identity)
    receipt(S, 'task beat', id, 'claimed', 'working', attempt, token_sha, actor, 'start-ack', '', idem, at)
    return { 'WORKING', S, id, tostring(attempt) }
  end

  redis.call('HSET', key, 'beat_at', tostring(at))
  NS.task.renew(id, at)
  redis.call('ZADD', 'friend:' .. friend .. ':living', at, identity)
  receipt(S, 'task beat', id, 'working', 'working', attempt, token_sha, actor, '', '', idem, at)
  return { 'BEAT', S, id, tostring(attempt) }
end

-- task_cancel: the owner gives claimed or working work back (spec 3.1 row 8).
-- Effects none or idempotent reopen the task; an external-effect task becomes
-- reconcile-required with an unresolved item and nothing is replayed (spec 3.1
-- row 9). The stored token is replaced with the fence marker first, so the old
-- token's Done and any later beat refuse with exit 3 (spec 2.1 rule 8).
local function task_cancel(keys, args)
  local S, id, token, reason, actor, idem = args[1], args[2], args[3], args[4], args[5], args[6]
  local key = 'task:' .. id

  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  if redis.call('HGET', key, 'token') ~= token then
    return { 'FENCED' }
  end
  local state = redis.call('HGET', key, 'state')
  if state ~= 'claimed' and state ~= 'working' then
    return { 'INVALID' }
  end

  local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
  local friend = redis.call('HGET', key, 'owner') or ''
  local effects = redis.call('HGET', key, 'effects') or 'none'
  local token_sha = redis.call('HGET', key, 'token_sha') or ''
  local identity = S .. '/' .. id .. '/' .. attempt
  local at = now_ms()

  redis.call('HSET', key, 'token', 'fenced')
  redis.call('ZREM', 'friend:' .. friend .. ':starting', identity)
  redis.call('ZREM', 'friend:' .. friend .. ':living', identity)
  slot_freed(S, id, friend, attempt, at)

  if effects == 'external' then
    to_state(S, id, 'reconcile-required', 'cancel ' .. (reason or ''), actor, { 'reason', reason })
    redis.call('HSET', 's:' .. S .. ':unresolved', id .. ':cancel-external:',
      'state=' .. state .. ' attempt=' .. tostring(attempt) .. ' token_sha=' .. token_sha ..
      ' reason=' .. reason .. ' at=' .. tostring(at))
    receipt(S, 'task cancel', id, state, 'reconcile-required', attempt, token_sha, actor, reason, '', idem, at)
    return { 'RECONCILE' }
  end

  requeue(S, id, 'cancel ' .. ((reason and reason ~= '') and reason or 'given back'), actor)
  receipt(S, 'task cancel', id, state, 'open', attempt, token_sha, actor, reason, '', idem, at)
  return { 'OPEN' }
end

-- task_expire: reconciler-only (not replayed for external effects). A claimed
-- task whose start acknowledgement never came within 60 s is a failed spawn;
-- it reopens with reason spawn-timeout (spec 3.1 row 4). A working task whose
-- last beat is older than 180 s expires; effects none or idempotent reopen it,
-- an external-effect task becomes reconcile-required with an unresolved item
-- and nothing is replayed (spec 3.1 rows 5 and 6). Age is measured against
-- Redis TIME inside the function; clients never send a time (spec 2.1 rule 3).
-- The stored token is fenced before the reopen so the old attempt's Done
-- refuses with exit 3.
local function task_expire(keys, args)
  local S, id, actor, idem = args[1], args[2], args[3], args[4]
  local key = 'task:' .. id

  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  local state = redis.call('HGET', key, 'state')
  local at = now_ms()

  if state == 'claimed' then
    local claimed_at = tonumber(redis.call('HGET', key, 'claimed_at') or '0')
    if claimed_at <= 0 or at - claimed_at < 60000 then
      return { 'FRESH' }
    end
    local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
    local friend = redis.call('HGET', key, 'owner') or ''
    local token_sha = redis.call('HGET', key, 'token_sha') or ''
    redis.call('HSET', key, 'token', 'fenced')
    redis.call('ZREM', 'friend:' .. friend .. ':starting', S .. '/' .. id .. '/' .. attempt)
    slot_freed(S, id, friend, attempt, at)
    requeue(S, id, 'spawn-timeout', actor)
    receipt(S, 'task expire', id, 'claimed', 'open', attempt, token_sha, actor, 'spawn-timeout', '', idem, at)
    return { 'REOPENED' }
  end

  if state == 'working' then
    local beat_at = tonumber(redis.call('HGET', key, 'beat_at') or '0')
    if beat_at <= 0 or at - beat_at < 180000 then
      return { 'FRESH' }
    end
    local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
    local friend = redis.call('HGET', key, 'owner') or ''
    local token_sha = redis.call('HGET', key, 'token_sha') or ''
    local effects = redis.call('HGET', key, 'effects') or 'none'
    redis.call('HSET', key, 'token', 'fenced')
    redis.call('ZREM', 'friend:' .. friend .. ':living', S .. '/' .. id .. '/' .. attempt)
    slot_freed(S, id, friend, attempt, at)

    if effects == 'external' then
      to_state(S, id, 'reconcile-required', 'beat-timeout', actor, { 'reason', 'beat-timeout' })
      redis.call('HSET', 's:' .. S .. ':unresolved', id .. ':beat-timeout:',
        'attempt=' .. tostring(attempt) .. ' token_sha=' .. token_sha ..
        ' effects=external at=' .. tostring(at))
      receipt(S, 'task expire', id, 'working', 'reconcile-required', attempt, token_sha, actor, 'beat-timeout', '', idem, at)
      return { 'RECONCILE' }
    end

    requeue(S, id, 'beat-timeout', actor)
    receipt(S, 'task expire', id, 'working', 'open', attempt, token_sha, actor, 'beat-timeout', '', idem, at)
    return { 'EXPIRED' }
  end

  return { 'INVALID' }
end

redis.register_function('ns_task_beat', task_beat)
redis.register_function('ns_task_cancel', task_cancel)
redis.register_function('ns_task_expire', task_expire)
