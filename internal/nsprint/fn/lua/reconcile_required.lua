-- Reconciler transitions for card reclaim, orphan-effect, idempotency keys and
-- the one-call assignment (#2930; #2756 3.2, 5.4, controls 8, 9, 23, 25).
-- Every function but ns_idem_resolve (a friend's compare-and-set, see
-- rr_fenced) checks the reconciler fence (lease:reconciler token) before
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

-- The reconciler lease is the only writer allowed here (5.1), with one
-- fenced exception: ns_idem_resolve, the friend-run `nova-sprint idem
-- resolve`, holds no reconciler token and is fenced instead by
-- compare-and-set on --was (it writes only an ambiguous:* value equal to
-- --was byte for byte, and no machine function writes an ambiguous:* value
-- after ns_idem_ambiguous sets it). For every other function, a missing
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

-- Every card state write here is NS.card (02_card_move.lua): the reclaim
-- moves are working -> working, a requeue is working -> ready, a timeout is
-- working -> done/fail.
local CARD = NS.card

-- Requeue under a new attempt: the old token is fenced (cleared), the
-- reservation is freed, the label goes back to the pool. The next deal
-- increments attempt, so the same attempt is never launched twice.
local function rr_requeue(S, label, card, bench, member, from, reason, evidence, idem, at)
  local attempt = rr_get(card, 'attempt')
  local retries = tonumber(rr_get(card, 'retries')) or 0
  local token_sha = rr_get(card, 'token_sha')
  local refused = CARD.move(card, 'ready', { state = 'queued', by = 'reconciler', why = reason,
    fields = { 'token', '', 'retries', tostring(retries + 1), 'reason', reason, 'requeued_at', tostring(at) } })
  if refused then return rr_reply(2, 'STATE', attempt, '') end
  redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
  redis.call('ZREM', 'bench:' .. bench .. ':living', member)
  redis.call('ZREM', 's:' .. S .. ':bench:' .. bench .. ':queue', label)
  local r = rr_receipt(S, 'card', label, from, 'queued', attempt, token_sha,
    'reconciler', reason, evidence, idem, at)
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
      if CARD.move(card, 'working', { state = 'launched', by = 'reconciler', why = 'live-no-ack',
          fields = { 'launched_at', tostring(now) } }) then
        return rr_reply(2, 'STATE', attempt, '')
      end
      local r = rr_receipt(S, 'card', label, 'dealt', 'launched', attempt, rr_get(card, 'token_sha'),
        'reconciler', 'live-no-ack', member, 'launched:' .. identity, now)
      redis.call('HSET', card, 'launched_receipt', r)
      redis.call('HSET', 's:' .. S .. ':idem', 'launched:' .. identity, r)
      return rr_reply(0, 'LAUNCHED', attempt, r)
    end
    return rr_requeue(S, label, card, bench, member, 'dealt', 'spawn-timeout', '-', idem .. ':spawn-timeout', now)
  end

  if state == 'launched' or state == 'running' then
    local last = tonumber(rr_get(card, 'beat_at'))
    if not last then last = tonumber(rr_get(card, 'launched_at')) or 0 end
    if now - last < beat_ms then return rr_reply(0, 'NOTHING', attempt, '') end
    local token_sha = rr_get(card, 'token_sha')
    if CARD.move(card, 'working', { state = 'reconcile-required', by = 'reconciler', why = 'beat-lost',
        fields = { 'token', '', 'reason', 'beat-lost', 'required_at', tostring(now) } }) then
      return rr_reply(2, 'STATE', attempt, '')
    end
    redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
    redis.call('ZREM', 'bench:' .. bench .. ':living', member)
    local r = rr_receipt(S, 'card', label, state, 'reconcile-required', attempt, token_sha,
      'reconciler', 'beat-lost', identity, idem, now)
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
  if CARD.move(card, 'working', { state = 'orphan-effect', by = 'reconciler', why = 'orphan-effect',
      fields = { 'reason', 'orphan-effect', 'orphan_evidence', evidence, 'orphan_at', tostring(now) } }) then
    return rr_reply(2, 'STATE', attempt, '')
  end
  local r = rr_receipt(S, 'card', label, 'reconcile-required', 'orphan-effect', attempt,
    rr_get(card, 'token_sha'), 'reconciler', 'orphan-effect', evidence, idem, now)
  redis.call('HSETNX', 's:' .. S .. ':unresolved', label .. ':orphan-effect:' .. attempt,
    identity .. ' ' .. evidence)
  redis.call('HSET', 's:' .. S .. ':idem', idem, r)
  return rr_reply(0, 'ORPHAN', attempt, r)
end)

-- ns_card_required_timeout: age out a reconcile-required card that no
-- evidence resolved (nova-tools #3803: probe-nova-card-570c7852 sat 12 h in
-- reconcile-required on a bench that gave none). Past max_ms since
-- required_at (Redis TIME) the card ends through the one move, working ->
-- done/fail, state ended, outcome FAILED, reason reconcile-timeout, with a
-- why naming the window; it is never requeued (the child may have left an
-- effect) and never enters the bench's ended set (there is nothing to
-- harvest). A card with no required_at gets one stamped now (NOTHING): its
-- window runs from its first sighting, never from an absent time.
-- args: sprint, label, reconciler token, max_ms
redis.register_function('ns_card_required_timeout', function(keys, args)
  local S, label, rtoken = args[1], args[2] or '', args[3] or ''
  local max_ms = tonumber(args[4] or '')
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if label == '' or not max_ms or max_ms <= 0 then return rr_reply(1, 'USAGE', '', '') end
  local card = 's:' .. S .. ':card:' .. label
  local state = rr_get(card, 'state')
  if state == '' then return rr_reply(5, 'NOTFOUND', '', '') end
  local attempt = rr_get(card, 'attempt')
  local identity = rr_get(card, 'identity')
  local idem = 'required:' .. identity .. ':timeout'
  local prev = rr_get('s:' .. S .. ':idem', idem)
  if prev ~= '' then return rr_reply(0, 'OK', attempt, prev) end
  if state ~= 'reconcile-required' then return rr_reply(2, 'STATE', attempt, '') end
  local now = rr_now()
  local since = tonumber(rr_get(card, 'required_at'))
  if not since then
    redis.call('HSET', card, 'required_at', tostring(now))
    return rr_reply(0, 'NOTHING', attempt, '')
  end
  if now - since < max_ms then return rr_reply(0, 'NOTHING', attempt, '') end
  local bench = rr_get(card, 'bench')
  local why = 'reconcile-required ' .. tostring(math.floor((now - since) / 1000)) .. ' s (since '
    .. tostring(since) .. ') past max_required_s ' .. tostring(math.floor(max_ms / 1000))
    .. ' with no evidence from bench ' .. bench .. '; ended, not requeued'
  if CARD.move(card, 'done', { state = 'ended', ok = 'fail', by = 'reconciler', why = 'reconcile-timeout',
      fields = { 'token', '', 'outcome', 'FAILED', 'reason', 'reconcile-timeout', 'why', why,
        'why_at', tostring(now), 'ended_at', tostring(now) } }) then
    return rr_reply(2, 'STATE', attempt, '')
  end
  local member = S .. '/' .. label .. '/' .. attempt
  redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
  redis.call('ZREM', 'bench:' .. bench .. ':living', member)
  local r = rr_receipt(S, 'card', label, 'reconcile-required', 'ended', attempt,
    rr_get(card, 'token_sha'), 'reconciler', 'reconcile-timeout', why, idem, now)
  redis.call('HSET', card, 'end_receipt', r)
  redis.call('HSET', 's:' .. S .. ':idem', idem, r)
  return rr_reply(0, 'TIMEOUT', attempt, r)
end)

-- The pending index: every pending:* idem key, scored by its begin time
-- (Redis TIME ms). The expire sweep reads it past open_ms; commit,
-- ambiguous, rejected and resolve each clear the key's member.
local function rr_pending_idx(S)
  return 's:' .. S .. ':idx:idem:pending'
end

-- The s:<S>:unresolved field an ambiguous key raises (pr-ambiguous:<repo>:<branch>).
local function rr_unresolved_field(key)
  if string.sub(key, 1, 3) == 'pr:' then return 'pr-ambiguous:' .. string.sub(key, 4) end
  return 'ambiguous:' .. key
end

-- who of a pending:<who>:<at_ms> value (a value from before at_ms carried
-- only pending:<who>).
local function rr_pending_who(v)
  local rest = string.sub(v, 9)
  local who = string.match(rest, '^(.*):%d+$')
  return who or rest
end

-- ns_idem_begin: record the intent to make an external effect under key,
-- create-only: pending:<who>:<at_ms> (Redis TIME) and the key in the pending
-- index. BEGUN returns the value it wrote, which the caller passes to
-- ns_idem_ambiguous or ns_idem_rejected. An existing key returns its stored
-- value: a URL (done), pending:* (in flight; the caller never opens again)
-- or ambiguous:* (terminal for the machine until a friend resolves it).
-- Fenced like every machine function here: a stale or missing reconciler
-- token refuses before the idem hash is read or written.
-- args: sprint, key, who, reconciler token
redis.register_function('ns_idem_begin', function(keys, args)
  local S, key, who, rtoken = args[1], args[2], args[3] or '', args[4] or ''
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if key == '' or who == '' then return rr_reply(1, 'USAGE', '', '') end
  local idem = 's:' .. S .. ':idem'
  local prev = rr_get(idem, key)
  if prev ~= '' then return rr_reply(0, 'EXISTS', '', prev) end
  local now = rr_now()
  local v = 'pending:' .. who .. ':' .. tostring(now)
  redis.call('HSET', idem, key, v)
  redis.call('ZADD', rr_pending_idx(S), now, key)
  return rr_reply(0, 'BEGUN', '', v)
end)

-- ns_idem_commit: record the effect's result once, with one receipt, and
-- clear the key from the pending index. The same value again returns the
-- stored receipt; a different value, or an ambiguous key (only a friend's
-- ns_idem_resolve moves it), is 4 CONFLICT with no write.
-- A stale or missing reconciler token refuses before any read or write.
-- args: sprint, key, value, actor, reason, reconciler token
redis.register_function('ns_idem_commit', function(keys, args)
  local S, key, value, actor, reason = args[1], args[2], args[3] or '', args[4] or '', args[5] or ''
  local rtoken = args[6] or ''
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if key == '' or value == '' or string.sub(value, 1, 8) == 'pending:' or string.sub(value, 1, 10) == 'ambiguous:' then
    return rr_reply(1, 'USAGE', '', '')
  end
  local idem = 's:' .. S .. ':idem'
  local prev = rr_get(idem, key)
  if prev ~= '' and string.sub(prev, 1, 8) ~= 'pending:' then
    if prev == value then return rr_reply(0, 'OK', '', rr_get(idem, key .. ':receipt')) end
    return rr_reply(4, 'CONFLICT', '', prev)
  end
  local now = rr_now()
  local r = rr_receipt(S, 'effect', key, prev, 'recorded', 0, '', actor, reason, value, key, now)
  redis.call('HSET', idem, key, value, key .. ':receipt', r)
  redis.call('ZREM', rr_pending_idx(S), key)
  return rr_reply(0, 'OK', '', r)
end)

-- ns_idem_ambiguous: a pending key whose outcome cannot be known from Redis
-- (the forge answered a 422 that does not prove "no PR", or the open is
-- pending past open_ms) becomes ambiguous:<who>:<at_ms>, terminal for the
-- machine: the key leaves the pending index, one pr-ambiguous item is raised
-- (HSETNX) and one receipt written. was, when given, must equal the stored
-- pending value. A replay on the ambiguous key returns the first receipt;
-- a URL or an absent key is 2 STATE with no write.
-- before_ms, when given (the expire sweep: Redis TIME minus open_ms), must be
-- at or after the pending value's at_ms: a key begun again since the sweep
-- read the pending index is a live open and is 2 STATE with no write.
-- args: sprint, key, reconciler token, [was], [before_ms]
redis.register_function('ns_idem_ambiguous', function(keys, args)
  local S, key, rtoken, was = args[1], args[2] or '', args[3] or '', args[4] or ''
  local before = tonumber(args[5] or '')
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if key == '' then return rr_reply(1, 'USAGE', '', '') end
  local idem = 's:' .. S .. ':idem'
  local prev = rr_get(idem, key)
  if string.sub(prev, 1, 10) == 'ambiguous:' then
    return rr_reply(0, 'AMBIGUOUS', '', rr_get(idem, key .. ':receipt'))
  end
  if string.sub(prev, 1, 8) ~= 'pending:' or (was ~= '' and was ~= prev) then
    return rr_reply(2, 'STATE', '', prev)
  end
  if before then
    local began = tonumber(string.match(prev, ':(%d+)$') or '')
    if began and began > before then return rr_reply(2, 'STATE', '', prev) end
  end
  local now = rr_now()
  local v = 'ambiguous:' .. rr_pending_who(prev) .. ':' .. tostring(now)
  local r = rr_receipt(S, 'effect', key, prev, 'ambiguous', 0, '', 'reconciler', 'pr-ambiguous', v, key, now)
  redis.call('HSET', idem, key, v, key .. ':receipt', r)
  redis.call('ZREM', rr_pending_idx(S), key)
  redis.call('HSETNX', 's:' .. S .. ':unresolved', rr_unresolved_field(key), r)
  return rr_reply(0, 'AMBIGUOUS', '', r)
end)

-- ns_idem_rejected: the forge refused the open (a 422 on the validation
-- allowlist), so no PR exists: the key and its pending-index member go, with
-- one rejected-open receipt (status, msg). Nothing is raised in
-- s:<S>:unresolved. was is the pending value this open began; the receipt
-- is remembered under <key>:rejected as "<was> <receipt>", so a replay for
-- the same was returns the first receipt and writes nothing, while a later
-- begin on the key (the caller fixed its request) is a new open.
-- An absent key or any other value is 2 STATE with no write.
-- args: sprint, key, was, status, msg, reconciler token
redis.register_function('ns_idem_rejected', function(keys, args)
  local S, key, was, status, msg = args[1], args[2] or '', args[3] or '', args[4] or '', args[5] or ''
  local rtoken = args[6] or ''
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if key == '' or string.sub(was, 1, 8) ~= 'pending:' then return rr_reply(1, 'USAGE', '', '') end
  local idem = 's:' .. S .. ':idem'
  local done = rr_get(idem, key .. ':rejected')
  if string.sub(done, 1, #was + 1) == was .. ' ' then
    return rr_reply(0, 'REJECTED', '', string.sub(done, #was + 2))
  end
  local prev = rr_get(idem, key)
  if prev ~= was then return rr_reply(2, 'STATE', '', prev) end
  local now = rr_now()
  local r = redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', 'effect', 'id', key, 'from', prev, 'to', 'rejected-open',
    'attempt', '0', 'token_sha', '', 'actor', 'reconciler', 'reason', 'rejected-open',
    'evidence', msg, 'idem', key, 'key', key, 'status', status, 'msg', msg, 'at', tostring(now))
  redis.call('HDEL', idem, key, key .. ':receipt')
  redis.call('HSET', idem, key .. ':rejected', was .. ' ' .. r)
  redis.call('ZREM', rr_pending_idx(S), key)
  return rr_reply(0, 'REJECTED', '', r)
end)

-- ns_idem_resolve: a friend's typed resolution of an ambiguous key
-- (`nova-sprint idem resolve`), the one writer of the ambiguous-to-URL or
-- ambiguous-to-absent transition and the one function in this file that
-- takes no reconciler token (see the header). Its fence is compare-and-set:
-- it writes only when the stored value is ambiguous:* and equals was byte for
-- byte. mode url records the URL (one resolved receipt, the pr-ambiguous item
-- deleted); mode none deletes the key and the item (one receipt), so the next
-- EnsurePR begins fresh. Any other stored value, including an ambiguous:*
-- that differs from was, is 2 STATE with the stored value and no write.
-- args: sprint, key, was, mode (url|none), url, who
redis.register_function('ns_idem_resolve', function(keys, args)
  local S, key, was, mode = args[1], args[2] or '', args[3] or '', args[4] or ''
  local url, who = args[5] or '', args[6] or ''
  if key == '' or was == '' or who == '' or (mode ~= 'url' and mode ~= 'none')
    or (mode == 'url' and (url == '' or string.sub(url, 1, 8) == 'pending:' or string.sub(url, 1, 10) == 'ambiguous:'))
    or (mode == 'none' and url ~= '') then
    return rr_reply(1, 'USAGE', '', '')
  end
  local idem = 's:' .. S .. ':idem'
  local prev = rr_get(idem, key)
  if string.sub(prev, 1, 10) ~= 'ambiguous:' or prev ~= was then
    return rr_reply(2, 'STATE', '', prev)
  end
  local now = rr_now()
  local evidence = url
  if mode == 'none' then evidence = 'none' end
  local r = rr_receipt(S, 'effect', key, prev, 'resolved', 0, '', who, 'resolve-' .. mode, evidence, key, now)
  if mode == 'url' then
    redis.call('HSET', idem, key, url, key .. ':receipt', r)
  else
    redis.call('HDEL', idem, key, key .. ':receipt')
  end
  redis.call('HDEL', 's:' .. S .. ':unresolved', rr_unresolved_field(key))
  redis.call('ZREM', rr_pending_idx(S), key)
  return rr_reply(0, 'RESOLVED', '', r)
end)

-- ns_card_retry: feed a crashed card back once (#2930 rev 5, replaces
-- rowan-tools bin/sprint-requeue). It acts only on a card that is ended with
-- outcome FAILED, reason idle-killed or crash with exit -1, no pushed sha
-- (empty or the wrapper's no-commit '-'), and
-- retries below retry_max: the child died before it could leave any effect,
-- so a new attempt cannot make a second one. The card goes back to the pool
-- (queued, reason retry:<reason>; the next deal takes attempt+1), with
-- retry_of=<attempt>, one receipt and the idem key retry:<S>/<label>/<attempt>.
-- A replay returns the first receipt. Any other card is NOTHING, no write.
-- The end fields stay the card wrapper's: this function never writes them.
-- args: sprint, label, reconciler token, retry_max
redis.register_function('ns_card_retry', function(keys, args)
  local S, label, rtoken = args[1], args[2] or '', args[3] or ''
  local retry_max = tonumber(args[4] or '')
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if label == '' or not retry_max or retry_max < 0 then return rr_reply(1, 'USAGE', '', '') end
  local card = 's:' .. S .. ':card:' .. label
  local state = rr_get(card, 'state')
  if state == '' then return rr_reply(5, 'NOTFOUND', '', '') end
  local attempt = rr_get(card, 'attempt')
  local idem = 'retry:' .. S .. '/' .. label .. '/' .. attempt
  local prev = rr_get('s:' .. S .. ':idem', idem)
  if prev ~= '' then return rr_reply(0, 'OK', attempt, prev) end
  -- pushed_sha '-' is the wrapper's no-commit mark (card.NoCommit): every
  -- non-DONE end carries it, and it is no effect, the same as empty.
  local pushed = rr_get(card, 'pushed_sha')
  if state ~= 'ended' or rr_get(card, 'outcome') ~= 'FAILED' or (pushed ~= '' and pushed ~= '-') then
    return rr_reply(0, 'NOTHING', attempt, '')
  end
  local reason = rr_get(card, 'reason')
  if not (reason == 'idle-killed' or (reason == 'crash' and rr_get(card, 'exit') == '-1')) then
    return rr_reply(0, 'NOTHING', attempt, '')
  end
  if (tonumber(rr_get(card, 'retries')) or 0) >= retry_max then
    return rr_reply(0, 'NOTHING', attempt, '')
  end
  local now = rr_now()
  local bench = rr_get(card, 'bench')
  redis.call('SREM', 's:' .. S .. ':bench:' .. bench .. ':ended', label)
  local res = rr_requeue(S, label, card, bench, S .. '/' .. label .. '/' .. attempt,
    'ended', 'retry:' .. reason, '-', idem, now)
  redis.call('HSET', card, 'retry_of', attempt, 'retried_at', tostring(now))
  return res
end)

-- ns_expire_stamp: the one writer of proc:reconciler expire_at:<S>, the Redis
-- TIME ms of the last expire sweep of sprint S (the sweep's self-gate against
-- s:<S>:policy expire_every_ms). Fenced: a deposed instance stamps nothing, so
-- it cannot delay the holder's next sweep.
-- args: reconciler token, sprint
redis.register_function('ns_expire_stamp', function(keys, args)
  local rtoken, S = args[1] or '', args[2] or ''
  if rr_fenced(rtoken) then return rr_reply(3, 'FENCED', '', '') end
  if S == '' then return rr_reply(1, 'USAGE', '', '') end
  local now = rr_now()
  redis.call('HSET', 'proc:reconciler', 'expire_at:' .. S, tostring(now))
  return rr_reply(0, 'STAMPED', '', tostring(now))
end)

-- ns_expire_read: the expire duty's read of one card index (nova-tools
-- #3620), in place of SORT <idx> BY nosort GET # GET s:<S>:card:*-><field>:
-- SORT is @dangerous and the coordinator ACL refuses it. Read-only, so the
-- duty calls it with FCALL_RO, one per sprint and state, all in its one read
-- pipeline. The reply is SORT GET's shape: label, then each field's value
-- ('' for a missing field or card), per member of s:<S>:idx:card:<state>.
-- args: sprint, state, field...
redis.register_function{
  function_name = 'ns_expire_read',
  flags = { 'no-writes' },
  callback = function(keys, args)
    local S, state = args[1] or '', args[2] or ''
    if S == '' or state == '' then
      return redis.error_reply('ns_expire_read: sprint and state are required')
    end
    local fields = {}
    for i = 3, #args do fields[#fields + 1] = args[i] end
    local out = {}
    for _, label in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':idx:card:' .. state)) do
      out[#out + 1] = label
      if #fields > 0 then
        local vals = redis.call('HMGET', 's:' .. S .. ':card:' .. label, unpack(fields))
        for i = 1, #fields do out[#out + 1] = vals[i] or '' end
      end
    end
    return out
  end,
}

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
