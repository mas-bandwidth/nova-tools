-- Reconciler transitions for card reclaim, orphan-effect, idempotency keys and
-- the one-call assignment (#2930; #2756 3.2, 5.4, controls 8, 9, 23, 25).
-- Every function checks the reconciler fence (lease:reconciler token) before
-- it reads anything else, computes every guard before the first write, and
-- writes the state, the index move, the receipt and the idem key in the same
-- call. Windows are policy (milliseconds) passed by the caller; the clock is
-- Redis TIME read here, never a client time.

local function rr_reply(code, status, attempt, receipt)
  return tostring(code) .. '|' .. tostring(status) .. '|' .. tostring(attempt or '') .. '|' .. tostring(receipt or '')
end

local function rr_now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function rr_get(key, field)
  local v = redis.call('HGET', key, field)
  if not v then return '' end
  return tostring(v)
end

-- The reconciler lease is the only writer allowed here (5.1). A missing
-- lease or another instance's token refuses before any read or write.
local function rr_fenced(token)
  local held = redis.call('HGET', 'lease:reconciler', 'token')
  return (not held) or token == '' or held ~= token
end

local function rr_receipt(S, kind, id, from, to, attempt, token_sha, actor, reason, evidence, idem, at)
  return redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from, 'to', to,
    'attempt', tostring(attempt), 'token_sha', token_sha or '',
    'actor', actor, 'reason', reason, 'evidence', evidence or '',
    'idem', idem or '', 'at', tostring(at))
end

local function rr_move(S, label, from, to)
  redis.call('SREM', 's:' .. S .. ':idx:card:' .. from, label)
  redis.call('SADD', 's:' .. S .. ':idx:card:' .. to, label)
end

-- Requeue under a new attempt: the old token is fenced (cleared), the
-- reservation is freed, the label goes back to the pool. The next deal
-- increments attempt, so the same attempt is never launched twice.
local function rr_requeue(S, label, card, bench, member, from, reason, evidence, idem, at)
  local attempt = rr_get(card, 'attempt')
  local retries = tonumber(rr_get(card, 'retries')) or 0
  local priority = tonumber(rr_get(card, 'priority')) or 0
  redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
  redis.call('ZREM', 'bench:' .. bench .. ':living', member)
  redis.call('ZREM', 's:' .. S .. ':bench:' .. bench .. ':queue', label)
  rr_move(S, label, from, 'queued')
  redis.call('ZADD', 's:' .. S .. ':pool', priority, label)
  local r = rr_receipt(S, 'card', label, from, 'queued', attempt, rr_get(card, 'token_sha'),
    'reconciler', reason, evidence, idem, at)
  redis.call('HSET', card, 'state', 'queued', 'token', '', 'retries', tostring(retries + 1),
    'reason', reason, 'requeued_at', tostring(at))
  redis.call('HSET', 's:' .. S .. ':idem', idem, r)
  return rr_reply(0, 'QUEUED', attempt, r)
end

-- ns_card_reclaim: the sweep's expiry for one card (3.2 rows 5 and 7).
--   dealt, no launched ack within start_ms and no live identity -> queued (spawn-timeout)
--   dealt, identity live on the bench -> launched (the ack was lost, the child runs)
--   launched or running, no beat within beat_ms -> reconcile-required (beat-lost)
-- A lost beat never requeues: the child may have pushed or opened a PR.
-- args: sprint, label, reconciler token, start_ms, beat_ms
redis.register_function('ns_card_reclaim', function(keys, args)
  local S, label, rtoken = args[1], args[2], args[3] or ''
  local start_ms, beat_ms = tonumber(args[4]), tonumber(args[5])
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if not start_ms or not beat_ms or start_ms <= 0 or beat_ms <= 0 then return rr_reply(1, 'USAGE', '', '') end
  local card = 's:' .. S .. ':card:' .. label
  local state = rr_get(card, 'state')
  if state == '' then return rr_reply(5, 'NOTFOUND', '', '') end
  local attempt = rr_get(card, 'attempt')
  local bench = rr_get(card, 'bench')
  local identity = rr_get(card, 'identity')
  local member = S .. '/' .. label .. '/' .. attempt
  local idem = 'launch:' .. S .. '/' .. label .. '/' .. attempt
  local now = rr_now()

  if state == 'dealt' then
    local dealt_at = tonumber(rr_get(card, 'dealt_at')) or 0
    if now - dealt_at < start_ms then return rr_reply(0, 'NOTHING', attempt, '') end
    if redis.call('SISMEMBER', 'bench:' .. bench .. ':live', member) == 1 then
      local r = rr_receipt(S, 'card', label, 'dealt', 'launched', attempt, rr_get(card, 'token_sha'),
        'reconciler', 'live-no-ack', member, 'launched:' .. identity, now)
      redis.call('HSET', card, 'state', 'launched', 'launched_at', tostring(now), 'launched_receipt', r)
      rr_move(S, label, 'dealt', 'launched')
      redis.call('HSET', 's:' .. S .. ':idem', 'launched:' .. identity, r)
      return rr_reply(0, 'LAUNCHED', attempt, r)
    end
    return rr_requeue(S, label, card, bench, member, 'dealt', 'spawn-timeout', '-', idem .. ':spawn-timeout', now)
  end

  if state == 'launched' or state == 'running' then
    local last = tonumber(rr_get(card, 'beat_at'))
    if not last then last = tonumber(rr_get(card, 'launched_at')) or 0 end
    if now - last < beat_ms then return rr_reply(0, 'NOTHING', attempt, '') end
    redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
    redis.call('ZREM', 'bench:' .. bench .. ':living', member)
    rr_move(S, label, state, 'reconcile-required')
    local r = rr_receipt(S, 'card', label, state, 'reconcile-required', attempt, rr_get(card, 'token_sha'),
      'reconciler', 'beat-lost', identity, idem, now)
    redis.call('HSET', card, 'state', 'reconcile-required', 'token', '', 'reason', 'beat-lost',
      'required_at', tostring(now))
    redis.call('HSET', 's:' .. S .. ':idem', idem, r)
    return rr_reply(0, 'REQUIRED', attempt, r)
  end

  return rr_reply(0, 'NOTHING', attempt, '')
end)

-- ns_card_required: resolve a reconcile-required card that has NO end record
-- for its identity (the caller tried card resolve first).
--   mode effect: a branch, a PR or a live process -> orphan-effect, never DONE
--   mode absent: proven absence of every effect -> queued under a new attempt (lost)
-- args: sprint, label, reconciler token, mode, evidence
redis.register_function('ns_card_required', function(keys, args)
  local S, label, rtoken, mode, evidence = args[1], args[2], args[3] or '', args[4] or '', args[5] or ''
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if (mode ~= 'effect' and mode ~= 'absent') or (mode == 'effect' and evidence == '') then
    return rr_reply(1, 'USAGE', '', '')
  end
  local card = 's:' .. S .. ':card:' .. label
  local state = rr_get(card, 'state')
  if state == '' then return rr_reply(5, 'NOTFOUND', '', '') end
  local attempt = rr_get(card, 'attempt')
  local identity = rr_get(card, 'identity')
  local idem = 'required:' .. identity .. ':' .. mode
  local prev = rr_get('s:' .. S .. ':idem', idem)
  if prev ~= '' then return rr_reply(0, 'OK', attempt, prev) end
  if state ~= 'reconcile-required' then return rr_reply(2, 'STATE', attempt, '') end
  local now = rr_now()
  if mode == 'absent' then
    local member = S .. '/' .. label .. '/' .. attempt
    return rr_requeue(S, label, card, rr_get(card, 'bench'), member, 'reconcile-required', 'lost', '-', idem, now)
  end
  rr_move(S, label, 'reconcile-required', 'orphan-effect')
  local r = rr_receipt(S, 'card', label, 'reconcile-required', 'orphan-effect', attempt,
    rr_get(card, 'token_sha'), 'reconciler', 'orphan-effect', evidence, idem, now)
  redis.call('HSET', card, 'state', 'orphan-effect', 'reason', 'orphan-effect',
    'orphan_evidence', evidence, 'orphan_at', tostring(now))
  redis.call('HSETNX', 's:' .. S .. ':unresolved', label .. ':orphan-effect:' .. attempt,
    identity .. ' ' .. evidence)
  redis.call('HSET', 's:' .. S .. ':idem', idem, r)
  return rr_reply(0, 'ORPHAN', attempt, r)
end)

-- ns_idem_begin: record the intent to make an external effect under key,
-- create-only. Returns the stored value when the key already exists: a URL
-- (done), or pending:<who> (a crash between the effect and its record, which
-- the caller must resolve by lookup, never by a second effect).
-- args: sprint, key, who
redis.register_function('ns_idem_begin', function(keys, args)
  local S, key, who = args[1], args[2], args[3] or ''
  if key == '' or who == '' then return rr_reply(1, 'USAGE', '', '') end
  local idem = 's:' .. S .. ':idem'
  local prev = rr_get(idem, key)
  if prev ~= '' then return rr_reply(0, 'EXISTS', '', prev) end
  redis.call('HSET', idem, key, 'pending:' .. who)
  return rr_reply(0, 'BEGUN', '', '')
end)

-- ns_idem_commit: record the effect's result once, with one receipt.
-- The same value again returns the stored receipt; a different value is 4.
-- args: sprint, key, value, actor, reason
redis.register_function('ns_idem_commit', function(keys, args)
  local S, key, value, actor, reason = args[1], args[2], args[3] or '', args[4] or '', args[5] or ''
  if key == '' or value == '' or string.sub(value, 1, 8) == 'pending:' then return rr_reply(1, 'USAGE', '', '') end
  local idem = 's:' .. S .. ':idem'
  local prev = rr_get(idem, key)
  if prev ~= '' and string.sub(prev, 1, 8) ~= 'pending:' then
    if prev == value then return rr_reply(0, 'OK', '', rr_get(idem, key .. ':receipt')) end
    return rr_reply(4, 'CONFLICT', '', prev)
  end
  local now = rr_now()
  local r = rr_receipt(S, 'effect', key, prev, 'recorded', 0, '', actor, reason, value, key, now)
  redis.call('HSET', idem, key, value, key .. ':receipt', r)
  return rr_reply(0, 'OK', '', r)
end)

-- ns_task_assign: route one ready task to one consumer (5.2 sweep, 5.3).
-- Guard, owner, queue move, receipt and idem key in one call: two concurrent
-- refills produce one assignment and one receipt, and there is no point
-- between the route state and the XADD at which a crash can land (control 9;
-- the refill.go defect wrote them in two calls).
-- args: sprint, id, consumer, reconciler token
redis.register_function('ns_task_assign', function(keys, args)
  local S, id, consumer, rtoken = args[1], args[2], args[3] or '', args[4] or ''
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if consumer == '' then return rr_reply(1, 'USAGE', '', '') end
  local task = 's:' .. S .. ':task:' .. id
  if redis.call('EXISTS', task) == 0 then return rr_reply(5, 'NOTFOUND', '', '') end
  local attempt = rr_get(task, 'attempt')
  local idem = 'route:' .. id .. ':' .. attempt
  local prev = rr_get('s:' .. S .. ':idem', idem)
  if prev ~= '' then
    local owner = rr_get(task, 'owner')
    if owner == consumer then return rr_reply(0, 'OK', attempt, prev) end
    return rr_reply(4, 'CONFLICT', attempt, owner)
  end
  if rr_get(task, 'state') ~= 'open' or rr_get(task, 'owner') ~= '' then return rr_reply(2, 'STATE', attempt, '') end
  local score = redis.call('ZSCORE', 's:' .. S .. ':ready', id)
  if not score then return rr_reply(2, 'STATE', attempt, '') end
  local registered
  if string.sub(consumer, 1, 8) == 'harvest:' then
    registered = redis.call('SISMEMBER', 'benches', string.sub(consumer, 9))
  else
    registered = redis.call('SISMEMBER', 'friends', consumer)
  end
  if registered == 0 then return rr_reply(2, 'CONSUMER', attempt, '') end
  local now = rr_now()
  redis.call('ZREM', 's:' .. S .. ':ready', id)
  redis.call('ZADD', 's:' .. S .. ':open:' .. consumer, score, id)
  redis.call('HSET', task, 'owner', consumer)
  local r = rr_receipt(S, 'task', id, 'ready', 'open:' .. consumer, attempt, '', 'reconciler', 'route', consumer, idem, now)
  redis.call('HSET', 's:' .. S .. ':idem', idem, r)
  return rr_reply(0, 'OK', attempt, r)
end)
