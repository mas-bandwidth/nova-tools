-- The reconciler's route duty (nova-tools #3323, #3199): the three record
-- moves that had no consumer, verb, duty or unit. Each is one Redis Function
-- call, fenced on lease:reconciler, idempotent under s:<S>:routed, with one
-- receipt in s:<S>:log (actor reconciler, so the refill never wakes on it).
-- No GitHub is read here: every guard is a record.
--   ns_route_read     an OK (harvested) card with a PR record (s:<S>:prunit ->
--                     s:<S>:u:<unit>) and a JEV line at the PR head
--                     (s:<S>:read:<unit>:jev) -> one read task
--                     read-<n>-<sha8> on the least-loaded reader's queue
--   ns_route_fix      a HOLD still open at the unit's head -> one fix task
--                     fix-<n>-<sha8> on the author's queue
--   ns_route_merging  a PR with a non-jev APPROVE at or over the bar, CI
--                     green at head and no open hold -> its task to merging
-- The task shape is the friend queue's: task:<id> hash, q:<friend> stream
-- entry `id`, sprint:<S>:idx:<friend>:open. When the stream index is present
-- (ws:names holds the stream) the task is also in ws:<stream>:ready, and a
-- merging move goes to ws:<stream>:merging; otherwise to the legacy
-- s:<S>:idx:task:merging set. The locals are scoped to this block.
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function fenced(token)
    return token == nil or token == '' or redis.call('HGET', 'lease:reconciler', 'token') ~= token
  end

  local function receipt(S, kind, id, from_state, to_state, actor, reason, evidence, idem, at)
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
      'attempt', '0', 'token_sha', '',
      'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
      'idem', idem or '', 'at', tostring(at))
  end

  local function ws_present(stream)
    return stream ~= nil and stream ~= '' and redis.call('SISMEMBER', 'ws:names', stream) == 1
  end

  local function ws_log(id, stream, from_state, to_state, by, why, at)
    redis.call('XADD', 'ws:log', 'MAXLEN', '~', 100000, '*',
      'id', id, 'stream', stream, 'from', from_state, 'to', to_state,
      'by', by, 'why', why, 'at', tostring(at))
  end

  local function sha8(head)
    return string.sub(head or '', 1, 8)
  end

  -- push_task writes one task in the friend queue's shape onto `to`.
  local function push_task(S, id, kind, to, title, repo, pr, head, stream, actor, why, at)
    local state = 'open'
    if ws_present(stream) then
      state = 'ready'
    end
    redis.call('HSET', 'task:' .. id,
      'kind', kind, 'ref', repo .. '#' .. pr, 'repo', repo, 'pr', pr, 'head', head,
      'owner', to, 'title', title, 'state', state, 'stream', stream, 'sprint', S,
      'route', 'reconcile', 'created_at', tostring(at), 'state_at', tostring(at))
    redis.call('XADD', 'q:' .. to, '*', 'id', id)
    redis.call('SADD', 'sprint:' .. S .. ':idx:' .. to .. ':open', id)
    if state == 'ready' then
      redis.call('ZADD', 'ws:' .. stream .. ':ready', at, id)
      ws_log(id, stream, '', 'ready', actor, why, at)
    end
    return state
  end

  -- load is a friend's ready queue depth: both of its streams.
  local function load(f)
    return redis.call('XLEN', 'q:' .. f) + redis.call('XLEN', 'q:' .. f .. ':front')
  end

  -- ns_route_read token S label stream actor reader...
  -- The readers come in name order, already filtered to a live beat by the
  -- caller; the least-loaded one that is not the PR's author or jev wins.
  local function route_read(keys, args)
    local token, S, label, stream, actor = args[1], args[2], args[3], args[4], args[5]
    if fenced(token) then return { 'FENCED' } end
    local ckey = 's:' .. S .. ':card:' .. label
    local c = redis.call('HMGET', ckey, 'state', 'pr', 'repo', 'head')
    if c[1] ~= 'harvested' or not c[2] or c[2] == '' then
      return { 'SKIP', 'not-ok' }
    end
    local pr, repo = c[2], c[3] or ''
    local unit = redis.call('GET', 's:' .. S .. ':prunit:' .. repo .. ':' .. pr)
    if not unit then
      return { 'SKIP', 'no-pr-record' }
    end
    local u = redis.call('HMGET', 's:' .. S .. ':u:' .. unit, 'head', 'author')
    local head, author = u[1] or '', u[2] or ''
    if head == '' then
      return { 'SKIP', 'no-pr-record' }
    end
    local jev = redis.call('HGET', 's:' .. S .. ':read:' .. unit .. ':jev', 'head')
    if jev ~= head then
      return { 'SKIP', 'no-jev-line' }
    end
    local routed = 's:' .. S .. ':routed'
    local idem = 'read:' .. repo .. ':' .. pr .. ':' .. head
    local prev = redis.call('HGET', routed, idem)
    if prev then
      return { 'DUP', prev }
    end
    local id = 'read-' .. pr .. '-' .. sha8(head)
    if redis.call('EXISTS', 'task:' .. id) == 1 then
      redis.call('HSET', routed, idem, id)
      return { 'EXISTS', id }
    end
    local best, best_load = nil, nil
    for i = 6, #args do
      local f = args[i]
      if f ~= author and f ~= 'jev' then
        local n = load(f)
        if not best or n < best_load then
          best, best_load = f, n
        end
      end
    end
    if not best then
      return { 'SKIP', 'no-reader' }
    end
    local at = now_ms()
    local title = 'STREAM: ' .. stream .. ' | read ' .. repo .. '#' .. pr .. ' at ' .. sha8(head) .. ' (card ' .. label .. ')'
    local state = push_task(S, id, 'read', best, title, repo, pr, head, stream, actor, 'route read', at)
    redis.call('HSET', routed, idem, id)
    receipt(S, 'task push', id, '', state, actor, 'route read',
      'pr=' .. repo .. '#' .. pr .. ' head=' .. head .. ' to=' .. best .. ' load=' .. tostring(best_load), idem, at)
    return { 'CREATED', id, best }
  end

  -- ns_route_fix token S event_id unit repo pr holder head stream actor
  -- One s:<S>:hold:events entry of type hold. Every answer acknowledges the
  -- entry for the route group: a hold that is released or superseded needs
  -- nothing, and a new hold writes a new entry.
  local function route_fix(keys, args)
    local token, S, event_id, unit, repo, pr = args[1], args[2], args[3], args[4], args[5], args[6]
    local holder, head, stream, actor = args[7], args[8], args[9], args[10]
    if fenced(token) then return { 'FENCED' } end
    local ev = 's:' .. S .. ':hold:events'
    local function finish(result)
      redis.call('XACK', ev, 'route', event_id)
      return result
    end
    local u = redis.call('HMGET', 's:' .. S .. ':u:' .. unit, 'head', 'author', 'state')
    local uhead, author, ustate = u[1] or '', u[2] or '', u[3] or ''
    if uhead == '' then
      return finish({ 'SKIP', 'no-unit' })
    end
    if uhead ~= head then
      return finish({ 'SKIP', 'superseded' })
    end
    if ustate == 'landed' or ustate == 'landing' or ustate == 'dropped' then
      return finish({ 'SKIP', 'unit-' .. ustate })
    end
    local hkey = 's:' .. S .. ':hold:' .. unit .. ':' .. holder
    local h = redis.call('HMGET', hkey, 'head', 'released_by', 'reason', 'kind')
    if h[1] ~= head or (h[2] and h[2] ~= '') then
      return finish({ 'SKIP', 'released' })
    end
    if author == '' or redis.call('SISMEMBER', 'friends', author) == 0 then
      return finish({ 'SKIP', 'no-author' })
    end
    local routed = 's:' .. S .. ':routed'
    local idem = 'fix:' .. unit .. ':' .. holder .. ':' .. head
    local prev = redis.call('HGET', routed, idem)
    if prev then
      return finish({ 'DUP', prev })
    end
    local id = 'fix-' .. pr .. '-' .. sha8(head)
    if redis.call('EXISTS', 'task:' .. id) == 1 then
      redis.call('HSET', routed, idem, id)
      return finish({ 'EXISTS', id })
    end
    local at = now_ms()
    local title = 'STREAM: ' .. stream .. ' | fix ' .. repo .. '#' .. pr .. ' HOLD ' .. (h[4] or '') ..
      ' by ' .. holder .. ' at ' .. sha8(head) .. ': ' .. (h[3] or '')
    local state = push_task(S, id, 'fix', author, title, repo, pr, head, stream, actor, 'route fix', at)
    redis.call('HSET', routed, idem, id)
    receipt(S, 'task push', id, '', state, actor, 'route fix',
      'pr=' .. repo .. '#' .. pr .. ' head=' .. head .. ' holder=' .. holder .. ' to=' .. author, idem, at)
    return finish({ 'CREATED', id, author })
  end

  -- task_key is the task's hash: the friend queue's task:<id>, else the
  -- sprint store's s:<S>:task:<id>.
  local function task_key(S, id)
    if redis.call('EXISTS', 'task:' .. id) == 1 then
      return 'task:' .. id
    end
    if redis.call('EXISTS', 's:' .. S .. ':task:' .. id) == 1 then
      return 's:' .. S .. ':task:' .. id
    end
    return nil
  end

  -- ns_route_merging token S unit bar stream actor
  local function route_merging(keys, args)
    local token, S, unit, bar, stream, actor = args[1], args[2], args[3], tonumber(args[4] or '8'), args[5], args[6]
    if fenced(token) then return { 'FENCED' } end
    local u = redis.call('HMGET', 's:' .. S .. ':u:' .. unit, 'head', 'state', 'repo', 'pr', 'author', 'task', 'holds_open')
    local head, ustate, repo, pr, author = u[1] or '', u[2] or '', u[3] or '', u[4] or '', u[5] or ''
    if head == '' or pr == '' then
      return { 'SKIP', 'no-pr-record' }
    end
    if ustate == 'landed' or ustate == 'landing' or ustate == 'dropped' then
      return { 'SKIP', 'unit-' .. ustate }
    end
    local routed = 's:' .. S .. ':routed'
    local idem = 'merging:' .. repo .. ':' .. pr .. ':' .. head
    local prev = redis.call('HGET', routed, idem)
    if prev then
      return { 'DUP', prev }
    end
    if tonumber(u[7] or '0') > 0 then
      return { 'SKIP', 'holds-open' }
    end
    local approved = nil
    for _, f in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':readers:' .. unit)) do
      if f ~= 'jev' and f ~= author then
        local r = redis.call('HMGET', 's:' .. S .. ':read:' .. unit .. ':' .. f, 'head', 'verdict', 'score', 'kind')
        if r[1] == head and string.upper(r[2] or '') == 'APPROVE' and (r[4] or '') ~= 'ci'
          and tonumber(r[3] or '0') and tonumber(r[3]) >= bar then
          approved = f .. ' ' .. r[3]
        end
      end
    end
    if not approved then
      return { 'SKIP', 'no-approve-at-bar' }
    end
    local gids = redis.call('SMEMBERS', 'ci:' .. repo .. ':' .. head .. ':gids')
    if #gids == 0 then
      return { 'SKIP', 'no-ci' }
    end
    for _, gid in ipairs(gids) do
      local v = redis.call('HGET', 'ci:' .. repo .. ':' .. head .. ':' .. gid, 'verdict')
      if v ~= 'OK' then
        return { 'SKIP', 'ci-' .. (v or 'missing') }
      end
    end
    local id = u[6]
    if not id or id == '' then
      id = redis.call('HGET', 'task:pr', repo .. '#' .. pr)
    end
    if not id or id == '' then
      id = redis.call('HGET', routed, 'read:' .. repo .. ':' .. pr .. ':' .. head)
    end
    if not id or id == '' then
      return { 'SKIP', 'no-task' }
    end
    local tkey = task_key(S, id)
    if not tkey then
      return { 'SKIP', 'no-task' }
    end
    local t = redis.call('HMGET', tkey, 'stream', 'state', 'owner')
    local tstream, tstate, owner = t[1] or '', t[2] or '', t[3] or ''
    if tstream == '' then
      tstream = stream
    end
    if tstate == 'merging' then
      redis.call('HSET', routed, idem, id)
      return { 'DUP', id }
    end
    local at = now_ms()
    if ws_present(tstream) then
      if tstate ~= '' then
        redis.call('ZREM', 'ws:' .. tstream .. ':' .. tstate, id)
      end
      redis.call('ZADD', 'ws:' .. tstream .. ':merging', at, id)
      ws_log(id, tstream, tstate, 'merging', actor, 'route merging', at)
    else
      for _, st in ipairs({ 'open', 'claimed', 'working' }) do
        redis.call('SREM', 's:' .. S .. ':idx:task:' .. st, id)
        if owner ~= '' then
          redis.call('SREM', 'sprint:' .. S .. ':idx:' .. owner .. ':' .. st, id)
        end
      end
      redis.call('SADD', 's:' .. S .. ':idx:task:merging', id)
    end
    redis.call('HSET', tkey, 'state', 'merging', 'state_at', tostring(at), 'pr', pr, 'head', head, 'stream', tstream)
    redis.call('HSET', routed, idem, id)
    receipt(S, 'task merging', id, tstate, 'merging', actor, 'route merging',
      'pr=' .. repo .. '#' .. pr .. ' head=' .. head .. ' approve=' .. approved .. ' ci=' .. tostring(#gids), idem, at)
    return { 'MERGING', id, tstream }
  end

  redis.register_function('ns_route_read', route_read)
  redis.register_function('ns_route_fix', route_fix)
  redis.register_function('ns_route_merging', route_merging)
end
