-- pr-to-read handlers (#2756 4.5, 3.2 rows review-ready to land-ready;
-- controls 6, 32; nova-tools #2941, prread.go).
-- Every function that writes checks lease:route:<S> instance against the caller's
-- instance. If they differ, it returns { 'LEASE', holder } and writes nothing.
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function check_lease(S, instance)
    local holder = redis.call('HGET', 'lease:route:' .. S, 'instance')
    if holder ~= instance then
      return holder or ''
    end
    return nil
  end

  -- ns_pr_head(S, instance, repo, pr, head, source): updates head on
  -- s:<S>:pr:<repo>:<n> and emits a 'pr head' event on s:<S>:log when changed.
  local function pr_head(keys, args)
    local S, instance, repo, pr, head, source = args[1], args[2], args[3], args[4], args[5], args[6]
    local holder = check_lease(S, instance)
    if holder then
      return { 'LEASE', holder }
    end
    local key = 's:' .. S .. ':pr:' .. repo .. ':' .. pr
    local cur_head = redis.call('HGET', key, 'head')
    if cur_head == head then
      return { 'OK', 'NOOP' }
    end
    local at = tostring(now_ms())
    redis.call('HSET', key, 'head', head, 'head_prev', cur_head or '', 'head_at', at, 'head_source', source)
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'pr head', 'repo', repo, 'pr', pr, 'head', head,
      'prev', cur_head or '', 'source', source, 'at', at)
    return { 'OK', 'UPDATED' }
  end

  -- ns_pr_head_change(S, instance, event_id, repo, pr, head, prev, actor, cancel_count, ...cancels, push_count, ...pushes)
  local function pr_head_change(keys, args)
    local S, instance, event_id = args[1], args[2], args[3]
    local holder = check_lease(S, instance)
    if holder then
      return { 'LEASE', holder }
    end
    local prev_res = redis.call('HGET', 's:' .. S .. ':idem', 'pr-to-read:' .. event_id)
    if prev_res then
      redis.call('XACK', 's:' .. S .. ':log', 'pr-to-read', event_id)
      return { 'DUP', prev_res }
    end

    local repo, pr, head, prev, actor = args[4], args[5], args[6], args[7], args[8]
    local cancel_count = tonumber(args[9]) or 0
    local idx = 10
    local at = tostring(now_ms())
    for i = 1, cancel_count do
      local f = args[idx]
      local tid = args[idx + 1]
      idx = idx + 2
      local tkey = 's:' .. S .. ':task:' .. tid
      local tstate = redis.call('HGET', tkey, 'state')
      if tstate == 'open' or tstate == 'leased' then
        redis.call('HSET', tkey, 'state', 'cancelled', 'closed_at', at)
        if f and f ~= '' then
          redis.call('ZREM', 's:' .. S .. ':open:' .. f, tid)
        end
        redis.call('SREM', 's:' .. S .. ':idx:task:open', tid)
        redis.call('XADD', 's:' .. S .. ':log', '*',
          'kind', 'task cancel', 'id', tid, 'from', tstate, 'to', 'cancelled',
          'attempt', '0', 'token_sha', '', 'actor', actor, 'reason', 'head-change',
          'evidence', '', 'idem', 'pr-to-read:' .. event_id, 'at', at)
      end
    end

    local push_count = tonumber(args[idx]) or 0
    idx = idx + 1
    for i = 1, push_count do
      local fid = args[idx]
      local tid = args[idx + 1]
      local priority = args[idx + 2]
      local payload_sha = args[idx + 3]
      idx = idx + 4
      local tkey = 's:' .. S .. ':task:' .. tid
      local existing = redis.call('HGET', tkey, 'payload_sha')
      if not existing then
        redis.call('HSET', tkey,
          'kind', 'review', 'repo', repo, 'ref', '', 'pr', pr, 'head', head,
          'title', 'review ' .. repo .. '#' .. pr .. ' at ' .. head, 'effects', 'none',
          'owner', '', 'priority', priority, 'state', 'open', 'attempt', '0', 'token', '0',
          'payload_sha', payload_sha, 'reason', '', 'evidence', '', 'claimed_at', '',
          'started_at', '', 'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '')
        redis.call('ZADD', 's:' .. S .. ':open:' .. fid, tonumber(priority) or 0, tid)
        redis.call('SADD', 's:' .. S .. ':idx:task:open', tid)
        redis.call('XADD', 's:' .. S .. ':log', '*',
          'kind', 'task push', 'id', tid, 'from', '', 'to', 'open',
          'attempt', '0', 'token_sha', '', 'actor', actor, 'reason', 'head-change',
          'evidence', '', 'idem', 'pr-to-read:' .. event_id, 'at', at)
      end
    end

    -- HDEL the field for old head A
    if prev and prev ~= '' then
      local prev12 = string.sub(prev, 1, 12)
      redis.call('HDEL', 's:' .. S .. ':unresolved', repo .. '#' .. pr .. ':reads-short:' .. prev12)
    end

    redis.call('HSET', 's:' .. S .. ':idem', 'pr-to-read:' .. event_id, 'OK')
    redis.call('XACK', 's:' .. S .. ':log', 'pr-to-read', event_id)
    return { 'OK' }
  end

  -- ns_pr_evaluate(S, instance, label, repo, pr, head, action, pass_id, actor, reason, attempt)
  local function pr_evaluate(keys, args)
    local S, instance = args[1], args[2]
    local holder = check_lease(S, instance)
    if holder then
      return { 'LEASE', holder }
    end
    local label, repo, pr, head = args[3], args[4], args[5], args[6]
    local action, pass_id, actor = args[7], args[8], args[9]
    local reason, attempt = args[10], args[11]

    local head12 = string.sub(head, 1, 12)
    local short_field = repo .. '#' .. pr .. ':reads-short:' .. head12
    local unres_key = 's:' .. S .. ':unresolved'

    if action == 'short' then
      redis.call('HSETNX', unres_key, short_field, pass_id)
      return { 'OK', 'SHORT' }
    elseif action == 'recovered' then
      redis.call('HDEL', unres_key, short_field)
      return { 'OK', 'RECOVERED' }
    elseif action == 'land-ready' then
      redis.call('HDEL', unres_key, short_field)
      local card_key = 's:' .. S .. ':card:' .. label
      local cur_state = redis.call('HGET', card_key, 'state')
      if cur_state == 'review-ready' then
        local at = tostring(now_ms())
        redis.call('HSET', card_key, 'state', 'land-ready', 'land_ready_at', at)
        redis.call('SREM', 's:' .. S .. ':idx:card:review-ready', label)
        redis.call('SADD', 's:' .. S .. ':idx:card:land-ready', label)
        redis.call('XADD', 's:' .. S .. ':log', '*',
          'kind', 'card', 'id', label, 'from', 'review-ready', 'to', 'land-ready',
          'attempt', tostring(attempt or 0), 'token_sha', '', 'actor', actor or '',
          'reason', 'reads', 'evidence', '', 'idem', '', 'at', at)
        return { 'OK', 'LAND_READY' }
      end
      return { 'OK', 'SKIPPED' }
    end
    return { 'OK', 'NOOP' }
  end

  -- ns_reads_short_delete_ended(S, instance, field): deletes reads-short field
  -- when PR is landed, dropped, or closed.
  local function reads_short_delete_ended(keys, args)
    local S, instance, field = args[1], args[2], args[3]
    local holder = check_lease(S, instance)
    if holder then
      return { 'LEASE', holder }
    end
    redis.call('HDEL', 's:' .. S .. ':unresolved', field)
    return { 'OK' }
  end

  -- ns_reads_short_delete_left(S, instance, field): deletes reads-short field
  -- when card leaves review-ready while PR is still open.
  local function reads_short_delete_left(keys, args)
    local S, instance, field = args[1], args[2], args[3]
    local holder = check_lease(S, instance)
    if holder then
      return { 'LEASE', holder }
    end
    redis.call('HDEL', 's:' .. S .. ':unresolved', field)
    return { 'OK' }
  end

  -- ns_pr_pass(S, instance, name, took_ms, n, err): writes proc line fenced by lease.
  local function pr_pass(keys, args)
    local S, instance, name, took_ms, n, err = args[1], args[2], args[3], args[4], args[5], args[6]
    local holder = check_lease(S, instance)
    if holder then
      return { 'LEASE', holder }
    end
    local at = tostring(now_ms())
    redis.call('HSET', 'proc:' .. name,
      'pass_at', at, 'took_ms', tostring(took_ms), 'n', tostring(n),
      'err', err or '', 'instance', instance, 'at', at)
    return { 'OK' }
  end

  redis.register_function('ns_pr_head', pr_head)
  redis.register_function('ns_pr_head_change', pr_head_change)
  redis.register_function('ns_pr_evaluate', pr_evaluate)
  redis.register_function('ns_reads_short_delete_ended', reads_short_delete_ended)
  redis.register_function('ns_reads_short_delete_left', reads_short_delete_left)
  redis.register_function('ns_pr_pass', pr_pass)
end
