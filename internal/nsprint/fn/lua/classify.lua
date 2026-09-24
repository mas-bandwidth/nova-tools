-- ok-to-friend 3.3 classification (#2756 3.3, control 16; nova-tools
-- #3038). No shebang: loader.go prepends the single library header. One call
-- per `ended` event of a card that did not end DONE: it checks the
-- idempotency key `<group>:<event id>`, guards the card (ended, at the
-- event's attempt, not DONE), checks the row's retry budget against the
-- card's `retry_<class>` counter, writes the action with its index moves and
-- receipts, records the result under the key and XACKs the event, all
-- atomically (spec 2.1 rule 2, 5.4). Unresolved items are written create-only
-- under the dedup key `<label>:<reason>:<base_sha>`, so repeated events yield
-- one item. The locals are scoped to this block.
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function receipt(S, id, from_state, to_state, attempt, actor, reason, evidence, idem, at)
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'card', 'id', id, 'from', from_state, 'to', to_state,
      'attempt', tostring(attempt or 0), 'token_sha', '',
      'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
      'idem', idem or '', 'at', tostring(at))
  end

  local function finish(S, group, event_id, result)
    redis.call('HSET', 's:' .. S .. ':idem', group .. ':' .. event_id, result)
    redis.call('XACK', 's:' .. S .. ':log', group, event_id)
    return { 'OK', result }
  end

  -- pool_score: the front of the pool is negative, as in open:<c>.
  local function pool_score(priority, front)
    local p = tonumber(priority) or 0
    if front then
      return -math.abs(p) - 1
    end
    return p
  end

  local function move(S, label, from_state, to_state)
    redis.call('SREM', 's:' .. S .. ':idx:card:' .. from_state, label)
    redis.call('SADD', 's:' .. S .. ':idx:card:' .. to_state, label)
  end

  local function unresolve(S, card, label, attempt, reason, base_sha, item, actor, idem, at)
    redis.call('HSETNX', 's:' .. S .. ':unresolved', label .. ':' .. reason .. ':' .. base_sha, item)
    redis.call('HSET', card, 'state', 'unresolved', 'unresolved_at', tostring(at))
    move(S, label, 'ended', 'unresolved')
    receipt(S, label, 'ended', 'unresolved', attempt, actor, reason, item, idem, at)
    return 'UNRESOLVED ' .. label .. ':' .. reason .. ':' .. base_sha
  end

  local function add_word(list, word)
    if not list then
      list = ''
    end
    for w in string.gmatch(list, '%S+') do
      if w == word then
        return list
      end
    end
    if list == '' then
      return word
    end
    return list .. ' ' .. word
  end

  local copied = { 'kind', 'leg', 'tier', 'repo', 'base', 'paths', 'priority', 'author',
    'retry_crash', 'retry_env', 'retry_base_moved', 'retry_tests_red' }

  -- ns_classify_end: args S, group, event_id, label, attempt, action, class,
  -- budget, reason, new_label, new_base_sha, new_payload_sha, new_title,
  -- actor.
  local function classify_end(keys, args)
    local S, group, event_id, label, attempt = args[1], args[2], args[3], args[4], args[5]
    local action, class, budget, reason = args[6], args[7], tonumber(args[8]) or 0, args[9]
    local new_label, new_base, new_payload, new_title, actor = args[10], args[11], args[12], args[13], args[14]
    local idem = group .. ':' .. event_id
    local prev = redis.call('HGET', 's:' .. S .. ':idem', idem)
    if prev then
      redis.call('XACK', 's:' .. S .. ':log', group, event_id)
      return { 'DUP', prev }
    end
    local card = 's:' .. S .. ':card:' .. label
    local state = redis.call('HGET', card, 'state')
    if action == '' then
      return finish(S, group, event_id, 'SKIP not-classified')
    elseif state ~= 'ended' then
      return finish(S, group, event_id, 'SKIP state ' .. tostring(state))
    elseif redis.call('HGET', card, 'attempt') ~= attempt then
      return finish(S, group, event_id, 'SKIP stale-attempt')
    elseif redis.call('HGET', card, 'outcome') == 'DONE' then
      return finish(S, group, event_id, 'SKIP done')
    end
    local at = now_ms()
    local base_sha = redis.call('HGET', card, 'base_sha') or ''
    local bench = redis.call('HGET', card, 'bench') or ''
    local outcome = redis.call('HGET', card, 'outcome') or ''
    local item = 'outcome=' .. outcome .. ' reason=' .. reason .. ' attempt=' .. attempt ..
      ' bench=' .. bench .. ' event=' .. event_id .. ' for=cutter'

    local counter = 'retry_' .. class
    if class ~= '' then
      local used = tonumber(redis.call('HGET', card, counter)) or 0
      if used >= budget then
        return finish(S, group, event_id, unresolve(S, card, label, attempt, reason, base_sha,
          item .. ' budget=' .. used .. '/' .. budget, actor, idem, at))
      end
    end

    if action == 'requeue' or action == 'requeue-env' then
      local priority = redis.call('HGET', card, 'priority') or '0'
      local avoid = add_word(redis.call('HGET', card, 'avoid_benches'), bench)
      redis.call('HINCRBY', card, counter, 1)
      redis.call('HINCRBY', card, 'retries', 1)
      redis.call('HSET', card, 'state', 'queued', 'bench', '', 'front', '1',
        'avoid_benches', avoid, 'requeued_at', tostring(at))
      redis.call('ZADD', 's:' .. S .. ':pool', pool_score(priority, true), label)
      move(S, label, 'ended', 'queued')
      if action == 'requeue-env' then
        local leg = redis.call('HGET', card, 'leg') or ''
        redis.call('HSET', 'bench:' .. bench .. ':why', 'env ' .. leg,
          'why: env ' .. leg .. ' ' .. S .. '/' .. label .. '/' .. attempt .. ' at ' .. at)
        redis.call('HSETNX', 's:' .. S .. ':unresolved', 'bench:' .. bench .. ':env:' .. leg, item)
      end
      receipt(S, label, 'ended', 'queued', attempt, actor, reason, 'avoid ' .. avoid, idem, at)
      return finish(S, group, event_id, 'REQUEUED ' .. label .. ' avoid ' .. avoid)
    end

    if action == 'waiting' then
      local dep = redis.call('HGET', card, 'blocked_on') or ''
      if dep == '' then
        return finish(S, group, event_id, unresolve(S, card, label, attempt, reason, base_sha,
          item .. ' dependency=unnamed', actor, idem, at))
      end
      local deps = redis.call('HGET', card, 'depends_on') or ''
      for d in string.gmatch(dep, '[^%s,]+') do
        deps = add_word(deps, d)
      end
      redis.call('HSET', card, 'state', 'queued', 'bench', '', 'depends_on', deps, 'requeued_at', tostring(at))
      redis.call('SADD', 's:' .. S .. ':waiting', label)
      move(S, label, 'ended', 'queued')
      receipt(S, label, 'ended', 'queued', attempt, actor, reason, 'waiting on ' .. deps, idem, at)
      return finish(S, group, event_id, 'WAITING ' .. label .. ' on ' .. deps)
    end

    if action == 'fix' or action == 'recut' then
      local new = 's:' .. S .. ':card:' .. new_label
      local existing = redis.call('HGET', new, 'payload_sha')
      local status = 'CREATED'
      if existing then
        if existing ~= new_payload then
          return finish(S, group, event_id, unresolve(S, card, label, attempt, reason, base_sha,
            item .. ' conflict=' .. new_label, actor, idem, at))
        end
        status = 'EXISTS'
      else
        local fields = { 'state', 'queued', 'attempt', '0', 'retries', '0', 'front', '1',
          'base_sha', new_base, 'depends_on', '', 'payload_sha', new_payload, 'parent', label,
          'title', new_title, 'cut_reason', reason, 'queued_at', tostring(at) }
        if new_base == '' then
          fields[#fields + 1] = 'cut_at_deal'
          fields[#fields + 1] = '1'
        end
        local values = redis.call('HMGET', card, unpack(copied))
        for i, f in ipairs(copied) do
          if values[i] and f ~= counter then
            fields[#fields + 1] = f
            fields[#fields + 1] = values[i]
          end
        end
        local used = tonumber(redis.call('HGET', card, counter)) or 0
        fields[#fields + 1] = counter
        fields[#fields + 1] = tostring(used + 1)
        redis.call('HSET', new, unpack(fields))
        local priority = redis.call('HGET', card, 'priority') or '0'
        redis.call('ZADD', 's:' .. S .. ':pool', pool_score(priority, true), new_label)
        redis.call('SADD', 's:' .. S .. ':idx:card:queued', new_label)
        receipt(S, new_label, '', 'queued', 0, actor, action .. ' ' .. reason, 'parent ' .. label, idem, at)
      end
      redis.call('HSET', card, 'state', 'superseded', 'superseded_by', new_label, 'superseded_at', tostring(at))
      move(S, label, 'ended', 'superseded')
      receipt(S, label, 'ended', 'superseded', attempt, actor, reason, 'by ' .. new_label, idem, at)
      return finish(S, group, event_id, status .. ' ' .. new_label)
    end

    return finish(S, group, event_id, unresolve(S, card, label, attempt, reason, base_sha, item, actor, idem, at))
  end

  -- ns_classify_pass: the proc line (spec 2.2 proc:<name>) with server TIME.
  local function classify_pass(keys, args)
    local name, took_ms, n, err = args[1], args[2], args[3], args[4]
    local at = now_ms()
    redis.call('HSET', 'proc:' .. name, 'pass_at', tostring(at), 'took_ms', took_ms,
      'n', n, 'err', err, 'at', tostring(at))
    return { 'OK' }
  end

  redis.register_function('ns_classify_end', classify_end)
  redis.register_function('ns_classify_pass', classify_pass)
end
