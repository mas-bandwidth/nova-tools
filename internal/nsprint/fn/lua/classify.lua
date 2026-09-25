-- Atomic non-DONE classification (#3038 rev 6). One ns_classify function
-- owns the decision, charge, transition, receipt, idempotency record and ack.
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function has_word(list, word)
    for item in string.gmatch(list or '', '%S+') do
      if item == word then return true end
    end
    return false
  end

  local function add_word(list, word)
    if word == nil or word == '' or has_word(list, word) then return list or '' end
    if list == nil or list == '' then return word end
    return list .. ' ' .. word
  end

  local function runs(legs, leg)
    if not leg or leg == '' or not legs or legs == '' then return true end
    for item in string.gmatch(legs, '[^, ]+') do
      if item == leg then return true end
    end
    return false
  end

  -- Every card state write here is NS.card (02_card_move.lua): REQUEUE is
  -- done/fail -> ready, WAIT done/fail -> waiting, RECUT supersedes (done ->
  -- done/fail), and a FIX or RECUT follow-up is created and moved to ready.
  local CARD = NS.card

  local function finish(S, event_id, result)
    redis.call('HSET', 's:' .. S .. ':idem', 'classify:' .. event_id, result)
    redis.call('XACK', 's:' .. S .. ':log', 'classify', event_id)
    return { 'OK', result }
  end

  local function record(S, ckey, classkey, label, event_id, action, actor, at)
    redis.call('HSET', ckey, 'classified', action, 'classified_at', tostring(at),
      'classified_event', event_id)
    redis.call('HSET', classkey, 'last_event', event_id, 'last_action', action, 'at', tostring(at))
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'card-classify', 'id', label, 'from', 'ended', 'to', action,
      'attempt', redis.call('HGET', ckey, 'attempt') or '0', 'token_sha', '',
      'actor', actor or '', 'reason', redis.call('HGET', ckey, 'reason') or '',
      'evidence', '', 'idem', 'classify:' .. event_id, 'at', tostring(at))
  end

  local function unresolved(S, ckey, classkey, label, root, reason, base_sha,
      event_id, actor, at, why)
    local marker = root .. ':' .. reason .. ':' .. string.sub(base_sha or '', 1, 8)
    redis.call('HSETNX', 's:' .. S .. ':unresolved', marker, event_id)
    if why and why ~= '' then
      redis.call('HSET', ckey, 'why', why, 'why_at', tostring(at))
    end
    record(S, ckey, classkey, label, event_id, 'UNRESOLVED', actor, at)
    return finish(S, event_id, 'UNRESOLVED')
  end

  local function pr_class(S, repo, n)
    if repo == nil or repo == '' then return 'unknown', 'no repo' end
    local state = redis.call('HGET', 's:' .. S .. ':pr:' .. repo .. ':' .. n, 'state')
    if state == 'landed' then return 'landed', '' end
    if state == 'closed' then return 'dead', 'can no longer land: closed without merge' end
    if state == 'opened' or state == 'reading' or state == 'landable' or state == 'landing' or state == 'dropped' then
      return 'live', ''
    end
    return 'unknown', 'unknown: no PR record'
  end

  local function dep_class(S, blocked_repo, typ, a, b)
    if typ == 'r' then
      local class, why = pr_class(S, a, b)
      return class, a .. '#' .. b .. ': ' .. why
    end
    local label = a
    local key = 's:' .. S .. ':card:' .. label
    if redis.call('EXISTS', key) == 0 then
      return 'dead', label .. ': can no longer land: no such card in sprint ' .. S
    end
    local d = redis.call('HMGET', key, 'state', 'outcome', 'repo', 'base', 'pr', 'pushed_sha')
    local state, outcome, repo, pr, pushed = d[1] or '', d[2] or '', d[3] or '', tonumber(d[5]) or 0, d[6] or ''
    if state == 'landed' then return 'landed', '' end
    if pr > 0 then
      if repo == '' then repo = blocked_repo or '' end
      if repo == '' then return 'unknown', label .. ': unknown: PR #' .. pr .. ' has no repo' end
      local class, why = pr_class(S, repo, tostring(pr))
      return class, label .. ': ' .. why
    end
    if state == 'ended' and outcome == 'DONE' and pushed == '' then return 'landed', '' end
    if state == 'cancelled' or state == 'superseded' then
      return 'dead', label .. ': can no longer land: card ' .. state .. ' without a PR'
    end
    if state == 'queued' or state == 'dealt' or state == 'launched' or state == 'running' then
      return 'live', ''
    end
    return 'unknown', label .. ': unknown: card ' .. state .. '/' .. outcome .. ', no PR record'
  end

  local function dealable(pin, avoid, leg)
    local candidates = {}
    if pin and pin ~= '' then candidates = { pin } else candidates = redis.call('SMEMBERS', 'benches') end
    for _, bench in ipairs(candidates) do
      if redis.call('SISMEMBER', 'benches', bench) == 1 and not has_word(avoid, bench) then
        local legs = redis.call('HGET', 'bench:' .. bench .. ':desired', 'legs')
        if runs(legs, leg) then return true end
      end
    end
    return false
  end

  local function classify(keys, args)
    if args[1] == 'pass' then
      local at = now_ms()
      redis.call('HSET', 'proc:classify', 'pass_at', tostring(at), 'took_ms', args[2] or '0',
        'n', args[3] or '0', 'err', args[4] or '', 'at', tostring(at))
      return { 'OK', 'PASS' }
    end
    if args[1] ~= 'event' then return redis.error_reply('ns_classify: mode event or pass') end

    local S, event_id, label, attempt, actor = args[2], args[3], args[4], args[5], args[6]
    local deps_raw, dep_n = args[7] or '', tonumber(args[8]) or 0
    local tail = 9 + dep_n * 3
    local expect_n, new_label = tonumber(args[tail]) or 0, args[tail + 1] or ''
    local new_base, payload, title = args[tail + 2] or '', args[tail + 3] or '', args[tail + 4] or ''
    local idem = redis.call('HGET', 's:' .. S .. ':idem', 'classify:' .. event_id)
    if idem then
      redis.call('XACK', 's:' .. S .. ':log', 'classify', event_id)
      return { 'DUP', idem }
    end

    local ckey = 's:' .. S .. ':card:' .. label
    local guard = redis.call('HMGET', ckey, 'state', 'end_receipt', 'attempt')
    if guard[1] ~= 'ended' or guard[2] ~= event_id or guard[3] ~= attempt then
      return finish(S, event_id, 'SKIP stale')
    end
    local card = redis.call('HMGET', ckey, 'kind', 'outcome', 'reason', 'root', 'base_sha',
      'bench', 'pin', 'avoid', 'leg', 'repo', 'base', 'paths', 'priority', 'author', 'tier')
    local kind, outcome, reason = card[1] or '', card[2] or '', card[3] or ''
    if kind == 'ci' then return finish(S, event_id, 'SKIP ci') end
    local root = card[4] or ''
    if root == '' then root = label end
    local base_sha, bench, pin, avoid, leg = card[5] or '', card[6] or '', card[7] or '', card[8] or '', card[9] or ''
    local action, counter, policy, default = 'UNRESOLVED', '', '', 0
    if outcome == 'DONE' then return finish(S, event_id, 'SKIP stale') end
    if outcome == 'FAILED' and (reason == 'crash' or reason == 'timeout' or reason == 'idle-killed') then
      action, counter, policy, default = 'REQUEUE', 'fail', 'retry_fail', 2
    elseif outcome == 'BLOCKED' and reason == 'env' then
      action, counter, policy, default = 'REQUEUE', 'env', 'retry_env', 1
    elseif outcome == 'BLOCKED' and reason == 'base-moved' then
      action, counter, policy, default = 'RECUT', 'recut', 'retry_recut', 1
    elseif outcome == 'BLOCKED' and reason == 'deps' then
      action, counter, policy, default = 'WAIT', 'deps', 'retry_deps', 1
    elseif outcome == 'FAILED' and reason == 'tests-red' then
      action, counter, policy, default = 'FIX', 'fix', 'retry_fix', 1
    end
    if reason == '' then reason = 'other' end
    local classkey = 's:' .. S .. ':classify:' .. root
    local used, budget = 0, 0
    if counter ~= '' then
      used = tonumber(redis.call('HGET', classkey, counter)) or 0
      budget = tonumber(redis.call('HGET', 's:' .. S .. ':policy', policy))
      if budget == nil or budget < 0 then budget = default end
    end

    if action == 'WAIT' and used < budget then
      if redis.call('HGET', ckey, 'depends_on') ~= deps_raw then return { 'RETRY', 'deps changed' } end
    end
    if (action == 'FIX' or action == 'RECUT') and used < budget then
      if used + 1 ~= expect_n or new_label == '' or redis.call('EXISTS', 's:' .. S .. ':card:' .. new_label) == 1 then
        return { 'RETRY', 'follow-up changed' }
      end
    end

    local at = now_ms()
    if counter ~= '' then redis.call('HINCRBY', classkey, counter, 1) end
    if counter ~= '' and used >= budget then
      local why = ''
      if action == 'WAIT' then why = 'deps over budget' end
      return unresolved(S, ckey, classkey, label, root, reason, base_sha, event_id, actor, at, why)
    end

    if action == 'REQUEUE' then
      if reason == 'env' then
        redis.call('HSET', ckey, 'why', 'env ' .. leg, 'why_at', tostring(at))
        redis.call('HSETNX', 's:' .. S .. ':unresolved', 'bench:' .. bench .. ':env:' .. leg, event_id)
        if pin ~= '' then
          redis.call('HINCRBY', classkey, counter, -1)
          return unresolved(S, ckey, classkey, label, root, reason, base_sha, event_id, actor, at, '')
        end
      end
      local next_avoid = avoid
      if pin == '' then next_avoid = add_word(avoid, bench) end
      if not dealable(pin, next_avoid, leg) then
        return unresolved(S, ckey, classkey, label, root, reason, base_sha, event_id, actor, at, '')
      end
      local refused = CARD.move(ckey, 'ready', { state = 'queued', bench = pin, by = actor, why = 'classify REQUEUE',
        pool_score = at - 10000000000000, fields = { 'token', '', 'avoid', next_avoid } })
      if refused then return { 'RETRY', refused } end
      record(S, ckey, classkey, label, event_id, 'REQUEUE', actor, at)
      return finish(S, event_id, 'REQUEUE')
    end

    if action == 'WAIT' then
      local live, bad = 0, {}
      for i = 0, dep_n - 1 do
        local p = 9 + i * 3
        local class, why = dep_class(S, card[10] or '', args[p], args[p + 1], args[p + 2])
        if class == 'live' then live = live + 1
        elseif class == 'dead' or class == 'unknown' then bad[#bad + 1] = why end
      end
      if #bad > 0 then
        return unresolved(S, ckey, classkey, label, root, reason, base_sha, event_id, actor, at, 'deps ' .. table.concat(bad, '; '))
      end
      if live == 0 then
        return unresolved(S, ckey, classkey, label, root, reason, base_sha, event_id, actor, at, 'deps landed')
      end
      local refused = CARD.move(ckey, 'waiting', { state = 'queued', bench = pin, by = actor, why = 'classify WAIT',
        fields = { 'token', '' } })
      if refused then return { 'RETRY', refused } end
      record(S, ckey, classkey, label, event_id, 'WAIT', actor, at)
      return finish(S, event_id, 'WAIT')
    end

    if action == 'FIX' or action == 'RECUT' then
      local nkey = 's:' .. S .. ':card:' .. new_label
      local new_kind = card[1] or 'model'
      if action == 'FIX' then new_kind = 'fix' end
      if action == 'RECUT' then
        local refused = CARD.move(ckey, 'done', { state = 'superseded', ok = 'fail', by = actor, why = 'classify RECUT',
          fields = { 'superseded_by', new_label } })
        if refused then return { 'RETRY', refused } end
      end
      local lineage = redis.call('HMGET', ckey, 'stream', 'origin')
      local nfields = { 'label', new_label, 'kind', new_kind, 'repo', card[10] or '',
        'base', card[11] or '', 'base_sha', new_base, 'paths', card[12] or '', 'depends_on', '',
        'priority', card[13] or '0', 'payload_sha', payload, 'attempt', '0',
        'retries', '0', 'root', root, 'tier', 'priority', 'front', '1', 'title', title }
      if lineage[2] then
        nfields[#nfields + 1] = 'origin'
        nfields[#nfields + 1] = lineage[2]
      end
      if action == 'FIX' then
        nfields[#nfields + 1] = 'fixes'
        nfields[#nfields + 1] = label
        redis.call('HSET', ckey, 'fixed_by', new_label)
      else
        nfields[#nfields + 1] = 'supersedes'
        nfields[#nfields + 1] = label
      end
      local nerr = CARD.create(nkey, nfields, { stream = lineage[1] or '', by = actor })
      if not nerr then
        nerr = CARD.move(nkey, 'ready', { by = actor, why = 'classify ' .. action, pool_score = at - 10000000000000 })
      end
      if nerr then return redis.error_reply('ns_classify: ' .. nerr) end
      record(S, ckey, classkey, label, event_id, action, actor, at)
      return finish(S, event_id, action)
    end

    return unresolved(S, ckey, classkey, label, root, reason, base_sha, event_id, actor, at, '')
  end

  redis.register_function('ns_classify', classify)
end
