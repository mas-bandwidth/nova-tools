-- pr-to-read handlers (#2756 4.5, 3.2 rows review-ready to land-ready;
-- controls 6, 32; nova-tools #2941, prread.go).
-- Every function that writes checks lease:route:<S> instance against the caller's
-- instance. If they differ, it returns { 'LEASE', holder } and writes nothing.
do
  -- The land-ready write is NS.card (02_card_move.lua): done -> done.
  local CARD = NS.card
  -- A first read is pushed by NS.fq (friend_queue.lua), the one writer of a
  -- friend queue task (#3773).
  local FQ = NS.fq

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
      local tkey = 'task:' .. tid
      local tstate = redis.call('HGET', tkey, 'state')
      if tstate == 'open' or tstate == 'leased' then
        NS.task.set(tid, 'cancelled', { sprint = S, by = actor, why = 'head-change', fields = { 'closed_at', at } })
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
      local tkey = 'task:' .. tid
      if redis.call('EXISTS', tkey) == 0 then
        NS.task.create(tid, {
          'kind', 'review', 'repo', repo, 'ref', '', 'pr', pr, 'head', head,
          'title', 'review ' .. repo .. '#' .. pr .. ' at ' .. head, 'effects', 'none',
          'priority', priority, 'attempt', '0', 'token', '0',
          'payload_sha', payload_sha, 'reason', '', 'evidence', '', 'claimed_at', '',
          'started_at', '', 'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '', 'dest', fid },
          { where = 'ready', state = 'open', friend = fid, sprint = S, qscore = tonumber(priority) or 0,
            by = actor, why = 'head-change' })
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

  -- ns_pr_first_read(S, instance, event_id, repo, pr, head, label, stream,
  -- need, authors, actor, event_at, ref): the first read of a card
  -- PR (#3739). The first `pr head` event of a card PR (prev empty; harvest
  -- writes it when it opens the PR) queues need read tasks at head, one per
  -- distinct UP friend (friend:<f> up=1, no friend:<f>:down), never an
  -- author or jev, least-loaded first (friend:<f> ready + working, then
  -- name). Each read is pushed by FQ.push, exactly as `friend-queue push
  -- --kind read --ref <PR URL> --title 'STREAM: <stream> | ...' --head
  -- <head>` pushes it (#3773: a hash written here with state=ready was
  -- invisible to friend-queue list, counts, take and cancel). The first reader's id is
  -- read-<n>-<head8> (the id ns_route_read and ns_route_pr_read use, so an
  -- existing one counts as that owner's read); a second distinct reader
  -- gets read-<n>-<head8>-<friend>. s:<S>:reads:<repo>#<n>@<head> (friend
  -- -> id) makes it idempotent on (n, head, friend). Short of need, the
  -- member <repo>#<n>@<head> stays in s:<S>:reads:pending (score = event
  -- time) for the next pass, and one `read pending` log entry is written
  -- once per member. event_id '' is a retry of a pending member: no ack.
  -- Reply: { status, need, have, first_pending, friend, id, ... } with
  -- status OK | PENDING | STALE | DUP | LEASE.
  local function pr_first_read(keys, args)
    local S, instance, event_id = args[1], args[2], args[3] or ''
    local holder = check_lease(S, instance)
    if holder then
      return { 'LEASE', holder }
    end
    local log = 's:' .. S .. ':log'
    local idem = 's:' .. S .. ':idem'
    if event_id ~= '' and redis.call('HGET', idem, 'pr-to-read:' .. event_id) then
      redis.call('XACK', log, 'pr-to-read', event_id)
      return { 'DUP' }
    end
    local repo, pr, head, label, stream = args[4], args[5], args[6], args[7], args[8]
    local need, actor = tonumber(args[9]) or 1, args[11] or 'pr-to-read'
    -- authors: the PR's authors, space-separated; none of them reads it.
    local authors = { jev = true }
    for a in string.gmatch(args[10] or '', '%S+') do authors[a] = true end
    local event_at, ref = args[12] or '0', args[13] or ''
    local member = repo .. '#' .. pr .. '@' .. head
    local pending = 's:' .. S .. ':reads:pending'
    local function finish(reply)
      if event_id ~= '' then
        redis.call('HSET', idem, 'pr-to-read:' .. event_id, reply[1])
        redis.call('XACK', log, 'pr-to-read', event_id)
      end
      return reply
    end
    local card_head = redis.call('HGET', 's:' .. S .. ':card:' .. label, 'head')
    if card_head ~= head then
      redis.call('ZREM', pending, member)
      return finish({ 'STALE', tostring(need), '0', '0' })
    end
    local rkey = 's:' .. S .. ':reads:' .. member
    local head8 = string.sub(head, 1, 8)
    local name = string.match(repo, '([^/]+)$') or repo
    local base = 'read-' .. pr .. '-' .. head8
    if name ~= 'nova-tools' then
      base = base .. '-' .. name
    end
    -- A read another route already pushed at this head counts as its owner's.
    local owner = redis.call('HGET', 'task:' .. base, 'owner')
    if owner and owner ~= '' then
      redis.call('HSETNX', rkey, owner, base)
    end
    local have = {}
    local nhave = 0
    for _, f in ipairs(redis.call('HKEYS', rkey)) do
      have[f] = true
      nhave = nhave + 1
    end
    local pool = {}
    if nhave < need then
      for _, f in ipairs(redis.call('SMEMBERS', 'friends')) do
        if not authors[f] and not have[f] and redis.call('EXISTS', 'friend:' .. f .. ':down') == 0 then
          local r = redis.call('HMGET', 'friend:' .. f, 'up', 'ready', 'working')
          if r[1] == '1' then
            pool[#pool + 1] = { f, (tonumber(r[2] or '0') or 0) + (tonumber(r[3] or '0') or 0) }
          end
        end
      end
      table.sort(pool, function(a, b)
        if a[2] ~= b[2] then return a[2] < b[2] end
        return a[1] < b[1]
      end)
    end
    local at = now_ms()
    local out = {}
    local i = 1
    while nhave < need and i <= #pool do
      local f, load = pool[i][1], pool[i][2]
      i = i + 1
      local id = base
      if redis.call('EXISTS', 'task:' .. id) == 1 then
        id = base .. '-' .. f
      end
      local title = 'STREAM: ' .. stream .. ' | read ' .. name .. '#' .. pr .. ' at ' .. head8 .. ' (' .. label .. ')'
      local xid, why = FQ.push(S, id, f, 'read', ref, title, head, false, at)
      if xid then
        redis.call('XADD', log, '*', 'kind', 'read queued', 'id', id, 'repo', repo, 'pr', pr,
          'head', head, 'to', f, 'card', label, 'load', tostring(load), 'actor', actor,
          'idem', 'first-read:' .. member .. ':' .. f, 'at', tostring(at))
        out[#out + 1] = f
        out[#out + 1] = id
      end
      -- A read already pushed under this id counts; a refused push does not.
      if xid or why == 'exists' then
        redis.call('HSETNX', rkey, f, id)
        have[f] = true
        nhave = nhave + 1
      end
    end
    if nhave < need then
      redis.call('ZADD', pending, 'NX', tonumber(event_at) or at, member)
      local first = redis.call('HSETNX', idem, 'read-pending:' .. member, tostring(at))
      if first == 1 then
        redis.call('XADD', log, '*', 'kind', 'read pending', 'repo', repo, 'pr', pr, 'head', head,
          'card', label, 'need', tostring(need), 'have', tostring(nhave), 'actor', actor, 'at', tostring(at))
      end
      local reply = { 'PENDING', tostring(need), tostring(nhave), tostring(first) }
      for _, v in ipairs(out) do reply[#reply + 1] = v end
      return finish(reply)
    end
    redis.call('ZREM', pending, member)
    local reply = { 'OK', tostring(need), tostring(nhave), '0' }
    for _, v in ipairs(out) do reply[#reply + 1] = v end
    return finish(reply)
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
      local at = tostring(now_ms())
      if cur_state == 'review-ready' and not CARD.move(card_key, 'done', { state = 'land-ready',
          by = actor, why = 'reads', fields = { 'land_ready_at', at } }) then
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
  redis.register_function('ns_pr_first_read', pr_first_read)
  redis.register_function('ns_pr_evaluate', pr_evaluate)
  redis.register_function('ns_reads_short_delete_ended', reads_short_delete_ended)
  redis.register_function('ns_reads_short_delete_left', reads_short_delete_left)
  redis.register_function('ns_pr_pass', pr_pass)
end
