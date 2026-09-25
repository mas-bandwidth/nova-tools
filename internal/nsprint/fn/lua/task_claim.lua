-- Task transitions for control 3 (push is create-only) and control 4 (one
-- concurrent take wins). No shebang: loader.go prepends the single library
-- header. Every transition below is one Redis Function call that checks the
-- guard, moves the id between index sets, reads server TIME and appends one
-- receipt, all atomically (spec #2756 2.1 rule 2, 3.1, 4.2).

-- DEP is the DEPENDS-ON machinery of #3206 PR A (ruling nova-tools#3516:
-- there is no blocked state; a task with an unmet dependency is waiting and
-- never ready). It is declared here, in the first task file of the one
-- library chunk, so push and done below can call it; task_queue.lua fills it
-- in through NS.DEP (loader.go wraps every file in its own do-block). Every
-- call happens at run time, after the whole chunk has loaded.
local DEP = {}
NS.DEP = DEP

-- HD is hold.lua's table (it sorts before this file); ns_task_done calls
-- HD.ingest, HD.resolve_who and HD.write_disp at run time.
local HD = NS.HD

local function now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- for_friend is the effective friend of the transition (#2929): --to on a
-- push, --as on a take and a denied take. actor is recorded as given and never
-- judged here; the CLI verbs own the initiator rule.
local function receipt(S, kind, id, from_state, to_state, attempt, token_sha, actor, for_friend, reason, evidence, idem, at)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
    'attempt', tostring(attempt or 0), 'token_sha', token_sha or '',
    'actor', actor or '', 'for', for_friend or '', 'reason', reason or '', 'evidence', evidence or '',
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
  local est = args[16] or ''
  -- needs (#2939): space-separated ids in <S> that must be closed before
  -- this task can be claimed; written only when non-empty.
  local needs = args[17] or ''
  local key = 's:' .. S .. ':task:' .. id

  -- #2929 rev 5: a down friend gets nothing. The marker is read first, before
  -- any other read, so a re-push of an existing id is refused too; it is only
  -- read, never written. The ready pool and harvest targets are not checked.
  if to ~= '' and string.sub(to, 1, 8) ~= 'harvest:' then
    local down = DEP.down(to)
    if down then
      return { 'DOWN', down }
    end
  end
  local conds, bad = DEP.parse(args[18] or '')
  if bad then
    return { 'INVALID', 'depends-on ' .. bad }
  end

  if est ~= '' then
    if not string.match(est, '^[1-9]%d*$') or #est > 5 or tonumber(est) > 10080 then
      return { 'INVALID' }
    end
  end

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
      return { 'INVALID', 'unknown friend' }
    end
  end

  if front and priority == 0 then
    priority = 1
  end

  local at = now_ms()
  redis.call('HSET', key,
    'kind', kind, 'repo', repo, 'ref', ref, 'pr', pr, 'head', head,
    'title', title, 'effects', effects, 'owner', '', 'priority', tostring(priority),
    'state', 'open', 'attempt', '0', 'token', '0', 'payload_sha', payload_sha,
    'reason', '', 'evidence', '', 'claimed_at', '', 'started_at', '',
    'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '',
    'est', est, 'pushed_at', tostring(at), 'pushed_by', actor or '')
  if needs ~= '' then
    redis.call('HSET', key, 'needs', needs)
  end
  -- #3206 PR A: the queue a task was pushed to, its front flag and the
  -- DEPENDS-ON list stay on the hash, so move, front and the dependency
  -- release find the queue without a scan. Author is the read rule's field.
  local front_text = '0'
  if front then
    front_text = '1'
  end
  redis.call('HSET', key, 'dest', to, 'front', front_text,
    'depends_on', table.concat(conds, ';'), 'author', args[19] or '')
  local unmet = DEP.unmet(S, conds)
  if #unmet > 0 then
    DEP.wait(S, id, key, to, unmet, at)
    receipt(S, 'task push', id, '', 'waiting', 0, '', actor, to, 'depends-on ' .. table.concat(unmet, ';'), '', idem, at)
    return { 'CREATED', 'waiting', tostring(#unmet) }
  end
  DEP.enqueue(S, id, to, front, priority)
  receipt(S, 'task push', id, '', 'open', 0, '', actor, to, '', '', idem, at)
  if #conds > 0 then
    return { 'CREATED', 'on-met' }
  end
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

  -- A task whose needs are not all closed is passed over (#2939): the reply
  -- names the unmet ids and nothing is written.
  local needs = redis.call('HGET', key, 'needs')
  if needs and needs ~= '' then
    local unmet = {}
    for need in string.gmatch(needs, '%S+') do
      if redis.call('SISMEMBER', 's:' .. S .. ':idx:task:closed', need) == 0 then
        unmet[#unmet + 1] = need
      end
    end
    if #unmet > 0 then
      return { 'BLOCKED', table.concat(unmet, ' ') }
    end
  end

  -- Presence and capacity are global, shared by every open sprint. A down
  -- marker refuses the take like an absent beat (#3206 PR A).
  if DEP.down(friend) then
    return { 'DOWN' }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 or
      not NS.beat.live('friend:' .. friend .. ':beat') then
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
  receipt(S, 'task take', id, 'open', 'claimed', attempt, token_sha, actor, friend, '', '', idem, at)
  return { 'CLAIMED', S, id, tostring(attempt), token,
    redis.call('HGET', key, 'kind') or '', redis.call('HGET', key, 'ref') or '',
    redis.call('HGET', key, 'title') or '' }
end

-- task_done: claimed/working -> closed (spec 3.1 row 7, 4.2). The token must
-- match or the call refuses; a review task's evidence must name a verdict, a
-- score and a head equal to the task head. A repeated identical done exits 0;
-- different evidence on a closed task exits 4.
-- nova-tools #3092 rev 7: args 11-18 carry a typed DISPOSITION line parsed by
-- internal/nsprint/disposition (task done --body-file): type, who, url,
-- comment_id, kind, derived kind, scope, reason. A review close with a typed
-- line records it through HD.ingest (hold.lua, ns_ingest_disposition's body)
-- in this same call, before the close: a line whose who is not the task
-- owner, or that ingest refuses, refuses the close with nothing written.
-- Without a typed line (the legacy --verdict/--score/--head flags) the close
-- keeps the base disposition value through HD.write_disp.
local function task_done(keys, args)
  local S, id, token = args[1], args[2], args[3]
  local evidence, verdict, score, head = args[4], args[5], args[6], args[7]
  local actor, idem = args[8], args[9]
  local as = args[10] or ''
  local typ, who, url, comment_id = args[11] or '', args[12] or '', args[13] or '', args[14] or ''
  local kind_explicit, kind_derived, scope, reason = args[15] or '', args[16] or '', args[17] or '', args[18] or ''
  local key = 's:' .. S .. ':task:' .. id

  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  -- #3206 PR A: `done --as <f>` with no token is the friend-queue shape; it
  -- is accepted only when f is the owner (the state rules below still hold,
  -- so only a claimed or working lease closes). Anything else without the
  -- stored token refuses FENCED.
  if token == '' and as ~= '' then
    if redis.call('HGET', key, 'owner') ~= as then
      return { 'FENCED' }
    end
  elseif redis.call('HGET', key, 'token') ~= token then
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
  local friend = redis.call('HGET', key, 'owner')
  local record = nil
  if is_review(kind) then
    if verdict == '' or score == '' or redis.call('HGET', key, 'head') ~= head then
      return { 'INVALID' }
    end
    if typ ~= '' then
      if typ ~= 'DISPOSITION' then
        return { 'REFUSED', 'not-a-disposition' }
      end
      if HD.resolve_who(who) ~= friend then
        return { 'REFUSED', 'who-not-owner' }
      end
      local repo = string.match(redis.call('HGET', key, 'repo') or '', '([^/]*)$')
      record = HD.ingest(nil, { S, repo, redis.call('HGET', key, 'pr') or '', url, comment_id,
        typ, who, head, verdict, score, kind_explicit, kind_derived, scope, reason })
      if record[1] ~= 'RECORD' then
        return record
      end
    end
  elseif typ ~= '' then
    return { 'REFUSED', 'not-a-review' }
  end

  local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
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
  if is_review(kind) and not record then
    HD.write_disp(S, redis.call('HGET', key, 'repo'), redis.call('HGET', key, 'pr'),
      friend .. '@' .. head, verdict .. ' ' .. score .. ' ' .. evidence)
  end
  receipt(S, 'task done', id, state, 'closed', attempt, redis.call('HGET', key, 'token_sha'), actor, '', '', evidence, idem, at)
  local ready = DEP.resolve(S, 'task:' .. id, false, actor, idem, at)
  if record then
    return { 'DONE', unpack(record) }
  end
  return { 'DONE', tostring(ready) }
end

-- task_take_denied: the receipt of a take refused by the CLI because --as is
-- not the initiator (#2929 rev 6). It writes exactly one receipt and touches
-- no other key; the task is unchanged. The library's Take never calls it.
local function task_take_denied(keys, args)
  local S, id, actor, for_friend, idem = args[1], args[2], args[3], args[4], args[5]
  receipt(S, 'task take denied', id, '', '', 0, '', actor, for_friend, 'as-not-initiator', '', idem, now_ms())
  return { 'DENIED' }
end

redis.register_function('ns_task_push', task_push)
redis.register_function('ns_task_take', task_take)
redis.register_function('ns_task_done', task_done)
redis.register_function('ns_task_take_denied', task_take_denied)

-- task_take.lua (a later file, in its own do-block) claims a batch through
-- this one guarded take (#3261); NS is the only way across file blocks.
NS.claim = { take = task_take }
