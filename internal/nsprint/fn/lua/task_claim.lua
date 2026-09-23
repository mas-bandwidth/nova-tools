-- Task transitions for control 3 (push is create-only) and control 4 (one
-- concurrent take wins). No shebang: loader.go prepends the single library
-- header. Every transition below is one Redis Function call that checks the
-- guard, moves the id between index sets, reads server TIME and appends one
-- receipt, all atomically (spec #2756 2.1 rule 2, 3.1, 4.2).

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

local function is_review(kind)
  return kind == 'read' or kind == 'review'
end

-- task_push: create-only (spec 3.1 row 1, 4.2). Same id and identical
-- payload_sha writes nothing and exits 0 EXISTS; a different payload exits 4
-- CONFLICT; a terminal id with the same payload exits 0 CLOSED and never
-- reopens (repaired rowan-tools #145).
local function task_push(keys, args)
  local S, id = args[1], args[2]
  local kind, title, effects = args[3], args[4], args[5]
  local repo, pr, head, ref = args[6], args[7], args[8], args[9]
  local to, front = args[10], args[11] == '1'
  local priority, payload_sha = tonumber(args[12]), args[13]
  local actor, idem = args[14], args[15]
  local key = 's:' .. S .. ':task:' .. id

  local existing = redis.call('HGET', key, 'payload_sha')
  if existing then
    if existing ~= payload_sha then
      return { 'CONFLICT' }
    end
    local state = redis.call('HGET', key, 'state')
    if state == 'closed' or state == 'cancelled' then
      return { 'CLOSED' }
    end
    return { 'EXISTS' }
  end

  if is_review(kind) then
    if repo == '' or pr == '' or head == '' or
        redis.call('HGET', 's:' .. S .. ':pr:' .. repo .. ':' .. pr, 'head') ~= head then
      return { 'INVALID' }
    end
  end
  if to ~= '' then
    local registered
    if string.sub(to, 1, 8) == 'harvest:' then
      registered = redis.call('SISMEMBER', 'benches', string.sub(to, 9))
    else
      registered = redis.call('SISMEMBER', 'friends', to)
    end
    if registered == 0 then
      return { 'INVALID' }
    end
  end

  local at = now_ms()
  redis.call('HSET', key,
    'kind', kind, 'repo', repo, 'ref', ref, 'pr', pr, 'head', head,
    'title', title, 'effects', effects, 'owner', '', 'priority', tostring(priority),
    'state', 'open', 'attempt', '0', 'token', '0', 'payload_sha', payload_sha,
    'reason', '', 'evidence', '', 'claimed_at', '', 'started_at', '',
    'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '')
  local score = priority
  if front then
    score = -priority
  end
  if to ~= '' then
    redis.call('ZADD', 's:' .. S .. ':open:' .. to, score, id)
  else
    redis.call('ZADD', 's:' .. S .. ':ready', score, id)
  end
  redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
  receipt(S, 'task push', id, '', 'open', 0, '', actor, '', '', idem, at)
  return { 'CREATED' }
end

-- task_take: open -> claimed(attempt, token) (spec 3.1 row 2, 4.2). The guard,
-- the index move, the server TIME and the one receipt are atomic, so two
-- concurrent takes of one task yield exactly one owner and one receipt
-- (#2756 control 4). attempt increments; token is <attempt>.<128 random bits
-- hex> where the random half is supplied by the Go RNG (spec 2.1 rule 8).
local function task_take(keys, args)
  local S, id, friend = args[1], args[2], args[3]
  local expected_attempt, token, token_sha = tonumber(args[4]), args[5], args[6]
  local actor, idem = args[7], args[8]
  local key = 's:' .. S .. ':task:' .. id

  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  if redis.call('HGET', key, 'state') ~= 'open' then
    return { 'NONE' }
  end
  if redis.call('HGET', 's:' .. S, 'status') ~= 'open' then
    return { 'NONE' }
  end

  -- A friend takes only from its assigned queue. The ready queue is for
  -- routing unassigned work, never an implicit claim by an arbitrary friend.
  local in_open = redis.call('ZSCORE', 's:' .. S .. ':open:' .. friend, id)
  if not in_open then
    return { 'NONE' }
  end

  -- Presence and capacity are global, shared by every open sprint.
  if redis.call('SISMEMBER', 'friends', friend) == 0 or
      redis.call('EXISTS', 'friend:' .. friend .. ':beat') == 0 then
    return { 'DOWN' }
  end
  local desired_key = 'friend:' .. friend .. ':desired'
  local desired = tonumber(redis.call('HGET', desired_key, 'slots') or '0')
  if redis.call('HGET', desired_key, 'paused') == '1' then
    return { 'DOWN' }
  end
  if desired <= 0 then
    return { 'FULL' }
  end
  local leased = redis.call('ZCARD', 'friend:' .. friend .. ':starting') +
    redis.call('ZCARD', 'friend:' .. friend .. ':living')
  if leased >= desired then
    return { 'FULL' }
  end

  local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0') + 1
  if expected_attempt ~= attempt or string.sub(token, 1, #tostring(attempt) + 1) ~= tostring(attempt) .. '.' or
      not string.match(token, '^%d+%.[0-9a-f]+$') or #token ~= #tostring(attempt) + 33 or
      not string.match(token_sha, '^[0-9a-f]+$') or #token_sha ~= 12 then
    return { 'RETRY' }
  end
  local at = now_ms()
  redis.call('HSET', key, 'state', 'claimed', 'owner', friend,
    'attempt', tostring(attempt), 'token', token, 'token_sha', token_sha, 'claimed_at', tostring(at))
  redis.call('ZREM', 's:' .. S .. ':ready', id)
  redis.call('ZREM', 's:' .. S .. ':open:' .. friend, id)
  redis.call('SREM', 's:' .. S .. ':idx:task:open', id)
  redis.call('SADD', 's:' .. S .. ':idx:task:claimed', id)
  redis.call('ZADD', 'friend:' .. friend .. ':starting', at, S .. '/' .. id .. '/' .. attempt)
  receipt(S, 'task take', id, 'open', 'claimed', attempt, token_sha, actor, '', '', idem, at)
  return { 'CLAIMED', S, id, tostring(attempt), token }
end

-- task_done: claimed/working -> closed (spec 3.1 row 7, 4.2). The token must
-- match or the call refuses; a review task's evidence must name a verdict, a
-- score and a head equal to the task head. A repeated identical done exits 0;
-- different evidence on a closed task exits 4.
local function task_done(keys, args)
  local S, id, token = args[1], args[2], args[3]
  local evidence, verdict, score, head = args[4], args[5], args[6], args[7]
  local actor, idem = args[8], args[9]
  local key = 's:' .. S .. ':task:' .. id

  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  if redis.call('HGET', key, 'token') ~= token then
    return { 'FENCED' }
  end
  local state = redis.call('HGET', key, 'state')
  if state == 'closed' then
    if redis.call('HGET', key, 'evidence') == evidence then
      return { 'CLOSED' }
    end
    return { 'CONFLICT' }
  end
  if state == 'cancelled' then
    return { 'CONFLICT' }
  end
  if state ~= 'claimed' and state ~= 'working' then
    return { 'INVALID' }
  end
  if evidence == '' then
    return { 'NOEVIDENCE' }
  end
  local kind = redis.call('HGET', key, 'kind')
  if is_review(kind) then
    if verdict == '' or score == '' or redis.call('HGET', key, 'head') ~= head then
      return { 'INVALID' }
    end
  end

  local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
  local friend = redis.call('HGET', key, 'owner')
  local at = now_ms()
  redis.call('HSET', key, 'state', 'closed', 'evidence', evidence,
    'verdict', verdict, 'score', score, 'closed_at', tostring(at))
  redis.call('SREM', 's:' .. S .. ':idx:task:claimed', id)
  redis.call('SREM', 's:' .. S .. ':idx:task:working', id)
  redis.call('SADD', 's:' .. S .. ':idx:task:closed', id)
  local identity = S .. '/' .. id .. '/' .. attempt
  redis.call('ZREM', 'friend:' .. friend .. ':starting', identity)
  redis.call('ZREM', 'friend:' .. friend .. ':living', identity)
  redis.call('SADD', 's:' .. S .. ':done:' .. friend, id)
  if is_review(kind) then
    local repo = redis.call('HGET', key, 'repo')
    local pr = redis.call('HGET', key, 'pr')
    redis.call('HSET', 's:' .. S .. ':disp:' .. repo .. ':' .. pr,
      friend .. '@' .. head, verdict .. ' ' .. score .. ' ' .. evidence)
  end
  receipt(S, 'task done', id, state, 'closed', attempt, redis.call('HGET', key, 'token_sha'), actor, '', evidence, idem, at)
  return { 'DONE' }
end

redis.register_function('ns_task_push', task_push)
redis.register_function('ns_task_take', task_take)
redis.register_function('ns_task_done', task_done)
