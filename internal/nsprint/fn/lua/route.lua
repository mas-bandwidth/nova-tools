-- ok-to-friend handlers (#2756 4.5, 3.2 rows ended(DONE) -> harvest task and
-- harvested -> review-ready; controls 7 and 14; nova-tools #2933). No shebang:
-- loader.go prepends the single library header. Each handler is one Redis
-- Function call that checks the idempotency key `ok-to-friend:<event id>`,
-- guards the card state, writes the tasks create-only with their index moves
-- and receipts, records the result under the key and XACKs the event, all
-- atomically (spec 2.1 rule 2, 5.4: a redelivered event returns the stored
-- result and acks). The locals are scoped to this block so the library's one
-- chunk does not carry them.
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function receipt(S, kind, id, from_state, to_state, attempt, actor, reason, evidence, idem, at)
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
      'attempt', tostring(attempt or 0), 'token_sha', '',
      'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
      'idem', idem or '', 'at', tostring(at))
  end

  -- seen returns the stored result of an event already handled, after acking
  -- it again (the ack of the first handling may have been lost).
  local function seen(S, group, event_id)
    local prev = redis.call('HGET', 's:' .. S .. ':idem', group .. ':' .. event_id)
    if prev then
      redis.call('XACK', 's:' .. S .. ':log', group, event_id)
      return { 'DUP', prev }
    end
    return nil
  end

  local function finish(S, group, event_id, result)
    redis.call('HSET', 's:' .. S .. ':idem', group .. ':' .. event_id, result)
    redis.call('XACK', 's:' .. S .. ':log', group, event_id)
  end

  -- create_task is the create-only push of spec 3.1 row 1 with the same hash
  -- shape as ns_task_push; the payload sha is computed by the Go caller with
  -- task.PayloadSHA so a later `task push` of the same id compares equal.
  local function create_task(S, id, kind, title, effects, repo, pr, head, ref, to, front, priority, payload_sha, actor, idem, at)
    local key = 's:' .. S .. ':task:' .. id
    local existing = redis.call('HGET', key, 'payload_sha')
    if existing then
      if existing ~= payload_sha then
        return 'CONFLICT'
      end
      local state = redis.call('HGET', key, 'state')
      if state == 'closed' or state == 'cancelled' then
        return 'CLOSED'
      end
      return 'EXISTS'
    end
    redis.call('HSET', key,
      'kind', kind, 'repo', repo, 'ref', ref, 'pr', pr, 'head', head,
      'title', title, 'effects', effects, 'owner', '', 'priority', tostring(priority),
      'state', 'open', 'attempt', '0', 'token', '0', 'payload_sha', payload_sha,
      'reason', '', 'evidence', '', 'claimed_at', '', 'started_at', '',
      'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '')
    local score = tonumber(priority)
    if front then
      score = -score
    end
    redis.call('ZADD', 's:' .. S .. ':open:' .. to, score, id)
    redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
    receipt(S, 'task push', id, '', 'open', 0, actor, 'ok-to-friend', '', idem, at)
    return 'CREATED'
  end

  -- ns_okfriend_harvest: a card ended DONE -> the harvest task
  -- `harvest-<label>` in `open:harvest:<bench>` (harvest work, never review).
  -- Guard: the card is ended, outcome DONE, at the event's attempt, on the
  -- named bench, and the bench is registered.
  local function okfriend_harvest(keys, args)
    local S, group, event_id, label, attempt, bench = args[1], args[2], args[3], args[4], args[5], args[6]
    local id, title, effects, repo, ref = args[7], args[8], args[9], args[10], args[11]
    local front, priority, payload_sha, actor = args[12] == '1', args[13], args[14], args[15]
    local dup = seen(S, group, event_id)
    if dup then
      return dup
    end
    local card = 's:' .. S .. ':card:' .. label
    local state = redis.call('HGET', card, 'state')
    local result
    if state ~= 'ended' then
      result = 'SKIP state ' .. tostring(state)
    elseif redis.call('HGET', card, 'outcome') ~= 'DONE' then
      result = 'SKIP not-done'
    elseif redis.call('HGET', card, 'attempt') ~= attempt then
      result = 'SKIP stale-attempt'
    elseif redis.call('HGET', card, 'bench') ~= bench then
      result = 'SKIP bench'
    elseif redis.call('SISMEMBER', 'benches', bench) == 0 then
      result = 'SKIP bench-unregistered'
      redis.call('HSETNX', 's:' .. S .. ':unresolved', label .. ':harvest-bench:' .. bench, event_id)
    else
      local at = now_ms()
      local status = create_task(S, id, 'harvest', title, effects, repo, '0', '', ref,
        'harvest:' .. bench, front, priority, payload_sha, actor, group .. ':' .. event_id, at)
      if status == 'CONFLICT' then
        redis.call('HSETNX', 's:' .. S .. ':unresolved', label .. ':harvest-conflict:' .. attempt, event_id)
      end
      result = status .. ' ' .. id
    end
    finish(S, group, event_id, result)
    return { 'OK', result }
  end

  -- ns_okfriend_review: a card harvested at a verified head -> one review
  -- task per required reader at exactly that head, and the card to
  -- review-ready, in one call (spec 3.2 row harvested -> review-ready).
  -- Guard: the card is harvested at the event's attempt, pr and head, and the
  -- head equals the pushed sha. A reader not registered refuses the whole
  -- call with RETRY and writes nothing, so the event stays pending.
  -- review-ready means every required read exists at the exact head, so every
  -- read id is checked before anything is written:
  --   absent            -> created;
  --   same payload, open or leased -> EXISTS (that read is already routed);
  --   same payload, closed -> CLOSED: that reader already read this exact
  --                        head (the payload pins repo, pr, head and reader),
  --                        so the read exists and counts;
  --   same payload, cancelled -> no usable read;
  --   different payload -> CONFLICT: the id is taken by another payload and
  --                        the required read cannot be created.
  -- Any cancelled or CONFLICT id refuses the whole call with BLOCKED: no task
  -- is created, the card stays harvested, the event is not acked (it stays
  -- pending and is retried each pass), and one blocking unresolved item
  -- `<label>:review-<conflict|cancelled>:<id>` names the id a human clears.
  local function okfriend_review(keys, args)
    local S, group, event_id, label, attempt = args[1], args[2], args[3], args[4], args[5]
    local repo, pr, head, actor = args[6], args[7], args[8], args[9]
    local n = tonumber(args[10])
    local dup = seen(S, group, event_id)
    if dup then
      return dup
    end
    local card = 's:' .. S .. ':card:' .. label
    local state = redis.call('HGET', card, 'state')
    local result
    if state ~= 'harvested' then
      result = 'SKIP state ' .. tostring(state)
    elseif redis.call('HGET', card, 'attempt') ~= attempt then
      result = 'SKIP stale-attempt'
    elseif redis.call('HGET', card, 'pr') ~= pr or redis.call('HGET', card, 'head') ~= head or
        redis.call('HGET', card, 'pushed_sha') ~= head or head == '' or pr == '' or pr == '0' then
      result = 'SKIP unverified-head'
    elseif n == nil or n < 1 then
      return { 'RETRY', 'no readers' }
    else
      local base = 11
      for i = 0, n - 1 do
        local friend = args[base + i * 5 + 1]
        if redis.call('SISMEMBER', 'friends', friend) == 0 then
          return { 'RETRY', 'unregistered ' .. friend }
        end
      end
      for i = 0, n - 1 do
        local id = args[base + i * 5]
        local payload_sha = args[base + i * 5 + 4]
        local key = 's:' .. S .. ':task:' .. id
        local existing = redis.call('HGET', key, 'payload_sha')
        local why = nil
        if existing and existing ~= payload_sha then
          why = 'conflict'
        elseif existing and redis.call('HGET', key, 'state') == 'cancelled' then
          why = 'cancelled'
        end
        if why then
          redis.call('HSETNX', 's:' .. S .. ':unresolved', label .. ':review-' .. why .. ':' .. id, event_id)
          return { 'BLOCKED', why .. ' ' .. id }
        end
      end
      local at = now_ms()
      local ids = {}
      local idem = group .. ':' .. event_id
      local ref = redis.call('HGET', card, 'identity') or ''
      for i = 0, n - 1 do
        local id = args[base + i * 5]
        local friend = args[base + i * 5 + 1]
        local title = args[base + i * 5 + 2]
        local priority = args[base + i * 5 + 3]
        local payload_sha = args[base + i * 5 + 4]
        local status = create_task(S, id, 'review', title, 'none', repo, pr, head, ref,
          friend, true, priority, payload_sha, actor, idem, at)
        ids[#ids + 1] = status .. ' ' .. id
      end
      redis.call('HSET', card, 'state', 'review-ready', 'review_at', tostring(at))
      redis.call('SREM', 's:' .. S .. ':idx:card:harvested', label)
      redis.call('SADD', 's:' .. S .. ':idx:card:review-ready', label)
      receipt(S, 'card', label, 'harvested', 'review-ready', attempt, actor, 'reads', table.concat(ids, ','), idem, at)
      result = table.concat(ids, ',')
    end
    finish(S, group, event_id, result)
    return { 'OK', result }
  end

  -- ns_okfriend_skip: an event this consumer handles with no transition (a
  -- card that did not end DONE, a ci card, a stale attempt, an unverified
  -- head). It records the result, optionally one unresolved item under its
  -- dedup key, and acks.
  local function okfriend_skip(keys, args)
    local S, group, event_id, reason, unresolved = args[1], args[2], args[3], args[4], args[5]
    local dup = seen(S, group, event_id)
    if dup then
      return dup
    end
    if unresolved ~= '' then
      redis.call('HSETNX', 's:' .. S .. ':unresolved', unresolved, event_id)
    end
    finish(S, group, event_id, 'SKIP ' .. reason)
    return { 'OK', 'SKIP ' .. reason }
  end

  -- ns_okfriend_pass: the proc line (spec 2.2 proc:<name>) with server TIME.
  local function okfriend_pass(keys, args)
    local name, took_ms, n, err = args[1], args[2], args[3], args[4]
    local at = now_ms()
    redis.call('HSET', 'proc:' .. name, 'pass_at', tostring(at), 'took_ms', took_ms,
      'n', n, 'err', err, 'at', tostring(at))
    return { 'OK' }
  end

  redis.register_function('ns_okfriend_harvest', okfriend_harvest)
  redis.register_function('ns_okfriend_review', okfriend_review)
  redis.register_function('ns_okfriend_skip', okfriend_skip)
  redis.register_function('ns_okfriend_pass', okfriend_pass)

  -- ns_read_evidence S repo pr label head suggest findings floor
  -- Writes swarm read evidence into s:<S>:readev:<repo>:<pr>
  redis.register_function('ns_read_evidence', function(keys, args)
    local S, repo, pr = args[1] or '', args[2] or '', args[3] or ''
    local label, head = args[4] or '', args[5] or ''
    local suggest, findings, floor = args[6] or '', args[7] or '', args[8] or ''
    if S == '' or repo == '' or pr == '' or label == '' or head == '' then
      return 'USAGE'
    end
    local key = 's:' .. S .. ':readev:' .. repo .. ':' .. pr
    local field = label .. '@' .. head
    local val = string.format('%s %s %s authority=swarm counted=0', suggest, findings, floor)
    redis.call('HSET', key, field, val)
    return 'OK'
  end)
end
