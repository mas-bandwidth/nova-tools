-- Holds and releases as records (nova-tools #3092 rev 7), keyed by the #3139
-- unit contract: a PR resolves to its unit through s:<S>:prunit:<repo>:<n>,
-- and head and author are read from s:<S>:u:<unit>. The retired PR record
-- s:<S>:pr:* is never read or written here (#3491). s:<S>:disp:<repo>:<n> is
-- written, never read: ns_ingest_disposition is its one writer (#3092 rev 7
-- key table, moved from task_claim.lua), in the land/doc.go shape
-- <friend>@<head> -> "<verdict> <score> <url> <comment_id>", for the readers
-- still on it (task.lua read waits, redistribute.lua, consume/prread.go,
-- fold) until #3491 moves them to s:<S>:read:<unit>:<friend>.
--   s:<S>:read:<unit>:<friend>   the typed read (ns_read's field set + url, comment_id)
--   s:<S>:hold:<unit>:<holder>   the hold (ns_hold's field set + score, scope, comment_id)
--   s:<S>:note:<unit>            hash n<k> -> JSON {who, head, kind, reason, url, score, at}
--   s:<S>:repair:<unit>          hash <head> -> "<who> <url> <seq> <at>"
--   s:<S>:holdowner:<unit>       hash h:<holder>:<head> | n<k> | r<head>:<holder> -> "<task id> <at> <to>"
--   s:<S>:holdpark               hash <repo>:<n>:<owner field> -> JSON
--   s:<S>:hold:events            stream, group hold-route
-- The locals are scoped to this block (the route.lua pattern) so the
-- library's one chunk stays under Lua's local limit. HD is the block's one
-- chunk local: task_claim.lua's ns_task_done calls HD.ingest in its own
-- atomic call (task done --body-file), and HD.write_disp for the legacy
-- flag close.
local HD = {}
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function receipt(S, kind, id, from_state, to_state, actor, reason, evidence, idem, at)
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
      'attempt', '0', 'token_sha', '',
      'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
      'idem', idem or '', 'at', tostring(at))
  end

  -- is_down: friend:<f>:down set, or friend:<f>:state state down,
  -- out-of-credits or away. Up is the negation.
  local function is_down(f)
    if redis.call('EXISTS', 'friend:' .. f .. ':down') == 1 then
      return true
    end
    local st = redis.call('HGET', 'friend:' .. f .. ':state', 'state')
    return st == 'down' or st == 'out-of-credits' or st == 'away'
  end

  local function resolve_who(who)
    if redis.call('SISMEMBER', 'friends', who) == 1 then
      return who
    end
    local f = redis.call('HGET', 'friends:login', who)
    if f and f ~= '' then
      return f
    end
    return nil
  end

  local function hold_open(hkey)
    if redis.call('EXISTS', hkey) == 0 then
      return false
    end
    local rb = redis.call('HGET', hkey, 'released_by')
    return not rb or rb == ''
  end

  local function events_key(S)
    return 's:' .. S .. ':hold:events'
  end

  -- Typed ingest is a read producer, so it keeps the #3091 reap fields in
  -- step with ns_read. Missing sentinels stay missing: the reap contract is
  -- fail-closed and must not be reconstructed by a later writer.
  local function update_reap_read_fields(ukey, who, head, verdict, kind, seq, at)
    local v = string.upper(verdict or '')
    local counted = (v == 'APPROVE' or v == 'HOLD') and
      string.lower(who or '') ~= 'jev' and (kind or '') ~= 'ci'
    if not counted then return end
    local u = redis.call('HMGET', ukey, 'last_read_at', 'approve_head', 'head')
    local now_s = math.floor(tonumber(at) / 1000)
    if u[1] then
      local stored = tonumber(u[1])
      if u[1] == '' or (stored and now_s > stored) then
        redis.call('HSET', ukey, 'last_read_at', string.format('%d', now_s))
      end
    end
    if v == 'APPROVE' and u[2] then
      if head == u[3] or u[2] == '' or u[2] ~= u[3] then
        redis.call('HSET', ukey, 'approve_head', head, 'approve_seq', tostring(seq))
      end
    end
  end

  -- write_disp: the one s:<S>:disp writer (see the header).
  local function write_disp(S, repo, pr, field, value)
    redis.call('HSET', 's:' .. S .. ':disp:' .. repo .. ':' .. pr, field, value)
  end

  -- ns_ingest_disposition: one typed line, already parsed by
  -- internal/nsprint/disposition (Go), becomes at most one record. Who and
  -- self are resolved here, against the registry and the unit's author.
  local function ingest(keys, args)
    local S, repo, pr, url, comment_id = args[1], args[2], args[3], args[4], args[5]
    local typ, who, head, verdict, score = args[6], args[7], args[8], args[9], args[10]
    local kind_explicit, kind_derived, scope, reason = args[11], args[12], args[13], args[14]
    local f = resolve_who(who)
    if not f then
      return { 'REFUSED', 'unknown-who' }
    end
    local unit = redis.call('GET', 's:' .. S .. ':prunit:' .. repo .. ':' .. pr)
    if not unit then
      return { 'REFUSED', 'no-unit' }
    end
    local ukey = 's:' .. S .. ':u:' .. unit
    local u = redis.call('HMGET', ukey, 'head', 'author', 'state')
    local uhead, author, ustate = u[1] or '', u[2] or '', u[3] or ''
    if uhead ~= head then
      return { 'REFUSED', 'stale-head' }
    end
    local at = now_ms()

    if typ == 'REPAIR' then
      local fix_to = redis.call('HGET', 's:' .. S .. ':policy', 'fix_to') or ''
      if f ~= fix_to and f ~= author then
        return { 'REFUSED', 'not-fix-to-or-author' }
      end
      for _, h in ipairs(redis.call('SMEMBERS', 'friends')) do
        local hkey = 's:' .. S .. ':hold:' .. unit .. ':' .. h
        if hold_open(hkey) and redis.call('HGET', hkey, 'head') == head then
          return { 'REFUSED', 'same-head' }
        end
      end
      local seq = redis.call('INCR', 'rec:seq')
      redis.call('HSET', 's:' .. S .. ':repair:' .. unit, head,
        f .. ' ' .. url .. ' ' .. seq .. ' ' .. at)
      redis.call('XADD', events_key(S), 'MAXLEN', '~', 10000, '*',
        'type', 'repair', 'unit', unit, 'repo', repo, 'pr', pr, 'who', f,
        'head', head, 'seq', tostring(seq), 'at', tostring(at))
      return { 'RECORD', 'repair', unit, f, head }
    end

    local kind = kind_explicit
    if f == author then
      kind = 'self'
    elseif kind == '' and verdict == 'HOLD' then
      kind = kind_derived
    end
    local seq = redis.call('INCR', 'rec:seq')
    redis.call('HSET', 's:' .. S .. ':read:' .. unit .. ':' .. f,
      'seq', tostring(seq), 'head', head, 'verdict', verdict, 'score', score,
      'kind', kind, 'files', '', 'done_when', '', 'at', tostring(at),
      'url', url, 'comment_id', comment_id)
    update_reap_read_fields(ukey, f, head, verdict, kind, seq, at)
    if kind ~= 'self' then
      write_disp(S, repo, pr, f .. '@' .. head,
        verdict .. ' ' .. score .. ' ' .. url .. ' ' .. (comment_id ~= '' and comment_id or '-'))
    end
    local hkey = 's:' .. S .. ':hold:' .. unit .. ':' .. f

    if verdict == 'APPROVE' then
      -- The holder's typed line at a later head releases the holder's hold.
      if hold_open(hkey) and redis.call('HGET', hkey, 'head') ~= head then
        if redis.call('HGET', hkey, 'post_land') ~= '1' then
          local n = redis.call('HINCRBY', ukey, 'holds_open', -1)
          if n < 0 then redis.call('HSET', ukey, 'holds_open', 0) end
        end
        redis.call('HSET', hkey, 'released_by', f, 'release_kind', 'typed',
          'release_reason', 'APPROVE at ' .. head, 'release_url', url,
          'released_at', tostring(at), 'release_seq', tostring(seq))
        return { 'RECORD', 'read', unit, f, head, 'released' }
      end
      return { 'RECORD', 'read', unit, f, head }
    end

    if kind == 'substance' or kind == 'scope' or kind == 'control' then
      local post_land = '0'
      if ustate == 'landing' or ustate == 'landed' then
        post_land = '1'
      elseif not hold_open(hkey) then
        redis.call('HINCRBY', ukey, 'holds_open', 1)
      end
      redis.call('HDEL', hkey, 'released_by', 'release_kind', 'release_reason',
        'release_url', 'released_at', 'release_seq', 'release_for')
      redis.call('HSET', hkey,
        'seq', tostring(seq), 'head', head, 'kind', kind, 'reason', reason,
        'url', url, 'files', '', 'done_when', '', 'origin', 'typed',
        'post_land', post_land, 'at', tostring(at),
        'score', score, 'scope', scope, 'comment_id', comment_id)
      redis.call('XADD', events_key(S), 'MAXLEN', '~', 10000, '*',
        'type', 'hold', 'unit', unit, 'repo', repo, 'pr', pr, 'who', f,
        'head', head, 'kind', kind, 'seq', tostring(seq), 'at', tostring(at))
      return { 'RECORD', 'hold', unit, f, head, kind }
    end

    local nkey = 's:' .. S .. ':note:' .. unit
    local field = 'n' .. tostring(redis.call('HLEN', nkey) + 1)
    redis.call('HSET', nkey, field, cjson.encode({
      who = f, head = head, kind = kind, reason = reason, url = url,
      score = score, at = at }))
    redis.call('XADD', events_key(S), 'MAXLEN', '~', 10000, '*',
      'type', 'note', 'unit', unit, 'repo', repo, 'pr', pr, 'who', f,
      'head', head, 'kind', kind, 'note', field, 'seq', tostring(seq), 'at', tostring(at))
    return { 'RECORD', 'note', unit, f, head, kind, field }
  end

  -- create_task is ns_task_push's create-only write with its field set; the
  -- payload sha comes from the Go caller (task.PayloadSHA).
  local function create_task(S, c, repo, pr, head, actor, idem, at)
    local key = 's:' .. S .. ':task:' .. c.id
    if redis.call('EXISTS', key) == 1 then
      return 'EXISTS'
    end
    redis.call('HSET', key,
      'kind', c.kind, 'repo', repo, 'ref', repo .. '#' .. pr, 'pr', pr, 'head', head,
      'title', c.title, 'effects', 'none', 'owner', '', 'priority', '1',
      'state', 'open', 'attempt', '0', 'token', '0', 'payload_sha', c.sha,
      'reason', '', 'evidence', '', 'claimed_at', '', 'started_at', '',
      'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '',
      'est', '', 'pushed_at', tostring(at))
    redis.call('ZADD', 's:' .. S .. ':open:' .. c.to, -1, c.id)
    redis.call('SADD', 's:' .. S .. ':idx:task:open', c.id)
    receipt(S, 'task push', c.id, '', 'open', actor, 'hold-route', '', idem, at)
    return 'CREATED'
  end

  local function close_task(S, id, to, evidence, actor, at)
    local key = 's:' .. S .. ':task:' .. id
    local state = redis.call('HGET', key, 'state')
    if not state or state == 'closed' or state == 'cancelled' then
      return false
    end
    redis.call('HSET', key, 'state', 'closed', 'evidence', evidence, 'closed_at', tostring(at))
    redis.call('SREM', 's:' .. S .. ':idx:task:open', id)
    redis.call('SREM', 's:' .. S .. ':idx:task:claimed', id)
    redis.call('SREM', 's:' .. S .. ':idx:task:working', id)
    redis.call('SADD', 's:' .. S .. ':idx:task:closed', id)
    if to and to ~= '' then
      redis.call('ZREM', 's:' .. S .. ':open:' .. to, id)
    end
    receipt(S, 'task done', id, state, 'closed', actor, 'hold-route repair', evidence, '', at)
    return true
  end

  -- dead: a hold action whose hold was released or superseded, or whose unit
  -- is landed or dropped, is never pushed.
  local function dead(S, unit, action, holder, hold_head)
    local ust = redis.call('HGET', 's:' .. S .. ':u:' .. unit, 'state') or ''
    if ust == 'landed' or ust == 'dropped' or ust == 'settled' then
      return 'unit-' .. ust
    end
    if action == 'fix' then
      local hkey = 's:' .. S .. ':hold:' .. unit .. ':' .. holder
      if not hold_open(hkey) then
        return 'released'
      end
      if redis.call('HGET', hkey, 'head') ~= hold_head then
        return 'superseded'
      end
      local hseq = tonumber(redis.call('HGET', hkey, 'seq') or '0')
      local reps = redis.call('HGETALL', 's:' .. S .. ':repair:' .. unit)
      for i = 1, #reps, 2 do
        local rseq = tonumber(string.match(reps[i + 1], '^%S+ %S+ (%d+)') or '0')
        if reps[i] ~= hold_head and rseq > hseq then
          return 'superseded'
        end
      end
    end
    return nil
  end

  -- open_fix: a fix-<n>-* task in any nonterminal state (open, claimed,
  -- working, waiting, waiting-ci, parked, reconcile-required; the TASK_Y set
  -- of sprint.lua less closed) holds the at-most-one guard. A take moves a
  -- fix out of idx:task:open, and the next holder must still see it.
  local LIVE_FIX_IDX = { 'open', 'claimed', 'working', 'waiting', 'waiting-ci',
    'parked', 'reconcile-required' }
  local function open_fix(S, pr)
    local prefix = 'fix-' .. pr .. '-'
    for _, idx in ipairs(LIVE_FIX_IDX) do
      for _, id in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':idx:task:' .. idx)) do
        if string.sub(id, 1, #prefix) == prefix then
          local st = redis.call('HGET', 's:' .. S .. ':task:' .. id, 'state')
          if st and st ~= 'closed' and st ~= 'cancelled' then
            return id
          end
        end
      end
    end
    return nil
  end

  -- ns_hold_route: one action of the hold router. Modes: `event` (one action
  -- of one hold:events entry; the last action of an entry XACKs it) and
  -- `unpark` (one s:<S>:holdpark field). Candidates come from Go in order;
  -- the first that is a registered friend, up and not the unit's author is
  -- the target, else the action parks. Every guard is read in this call.
  local function route(keys, args)
    local S, mode, ref, suffix, last = args[1], args[2], args[3], args[4], args[5] == '1'
    local unit, repo, pr, head, owner_field = args[6], args[7], args[8], args[9], args[10]
    local action, holder, hold_head = args[11], args[12], args[13]
    local seen_fix_to, seen_reader, role, actor = args[14], args[15], args[16], args[17]
    local n = tonumber(args[18] or '0')
    local cands = {}
    for i = 0, n - 1 do
      local b = 19 + i * 6
      cands[#cands + 1] = { to = args[b], id = args[b + 1], kind = args[b + 2],
        title = args[b + 3], release_for = args[b + 4], sha = args[b + 5] }
    end
    local ev = events_key(S)
    local idem_key = 's:' .. S .. ':idem'
    local idem = 'hold-route:' .. ref .. suffix
    local at = now_ms()

    local function finish(result)
      if mode == 'event' then
        redis.call('HSET', idem_key, idem, result)
        if last then
          redis.call('XACK', ev, 'hold-route', ref)
        end
      end
      return { result }
    end

    if mode == 'event' then
      local prev = redis.call('HGET', idem_key, idem)
      if prev then
        if last then
          redis.call('XACK', ev, 'hold-route', ref)
        end
        return { 'DUP', prev }
      end
    end
    local pol = redis.call('HMGET', 's:' .. S .. ':policy', 'fix_to', 'release_reader')
    if (pol[1] or '') ~= seen_fix_to or (pol[2] or '') ~= seen_reader then
      return { 'RETRY' }
    end
    if action == 'none' then
      return finish('NONE')
    end
    local author = redis.call('HGET', 's:' .. S .. ':u:' .. unit, 'author') or ''
    local owners = 's:' .. S .. ':holdowner:' .. unit
    local park_field = repo .. ':' .. pr .. ':' .. owner_field
    local parks = 's:' .. S .. ':holdpark'

    -- A REPAIR closes the hold's open fix task first, parked or not.
    if action == 'reread' and mode == 'event' then
      local hkey = 's:' .. S .. ':hold:' .. unit .. ':' .. holder
      local fixown = redis.call('HGET', owners, 'h:' .. holder .. ':' .. (redis.call('HGET', hkey, 'head') or ''))
      if fixown then
        local id, _, to = string.match(fixown, '^(%S+) (%S+) ?(%S*)$')
        if id then
          close_task(S, id, to, 'repair at ' .. string.sub(head, 1, 12), actor, at)
        end
      end
      redis.call('HDEL', parks, repo .. ':' .. pr .. ':h:' .. holder .. ':' .. (redis.call('HGET', hkey, 'head') or ''))
    end

    local why = dead(S, unit, action, holder, hold_head)
    if why then
      if mode == 'unpark' then
        redis.call('HDEL', parks, park_field)
        receipt(S, 'unpark-drop', owner_field, 'parked', 'dropped', actor, why, '', '', at)
        return { 'DROPPED', why }
      end
      return finish('SUPERSEDED ' .. why)
    end

    local recorded = redis.call('HGET', owners, owner_field)
    local function eligible(to)
      return to ~= '' and to ~= author and redis.call('SISMEMBER', 'friends', to) == 1 and not is_down(to)
    end

    if mode == 'unpark' then
      if not redis.call('HGET', parks, park_field) then
        return { 'GONE' }
      end
      for _, c in ipairs(cands) do
        if recorded or redis.call('EXISTS', 's:' .. S .. ':task:' .. c.id) == 1 then
          redis.call('HDEL', parks, park_field)
          return { 'OWNED' }
        end
      end
    else
      if recorded then
        local rid = string.match(recorded, '^(%S+)')
        if redis.call('EXISTS', 's:' .. S .. ':task:' .. rid) == 1 then
          return finish('OWNED')
        end
        -- Recovery (a): owner field present, no task hash -> push the recorded id.
        for _, c in ipairs(cands) do
          if c.id == rid and eligible(c.to) then
            create_task(S, c, repo, pr, head, actor, idem, at)
            redis.call('HSET', owners, owner_field, rid .. ' ' .. at .. ' ' .. c.to)
            return finish('CREATED ' .. c.id .. ' ' .. c.to .. ' recovered')
          end
        end
      end
      for _, c in ipairs(cands) do
        if redis.call('EXISTS', 's:' .. S .. ':task:' .. c.id) == 1 then
          -- Recovery (b): task present, no owner field -> write the owner.
          redis.call('HSET', owners, owner_field, c.id .. ' ' .. at .. ' ' .. c.to)
          return finish('OWNER ' .. c.id)
        end
      end
      if action == 'fix' then
        local open = open_fix(S, pr)
        if open then
          return finish('OPEN-FIX ' .. open)
        end
      end
    end

    for _, c in ipairs(cands) do
      if eligible(c.to) then
        create_task(S, c, repo, pr, head, actor, idem, at)
        redis.call('HSET', owners, owner_field, c.id .. ' ' .. at .. ' ' .. c.to)
        if mode == 'unpark' then
          redis.call('HDEL', parks, park_field)
          receipt(S, 'unpark', c.id, 'parked', 'open', actor, role, '', '', at)
          return { 'UNPARKED', c.id, c.to }
        end
        return finish('CREATED ' .. c.id .. ' ' .. c.to)
      end
    end

    -- No eligible target: park (or stay parked with the current reason).
    local first = cands[1] and cands[1].to or ''
    local reason = 'down'
    if first == '' or first == author then
      reason = 'author'
    end
    local prev = redis.call('HGET', parks, park_field)
    local parked_at = at
    if prev then
      parked_at = cjson.decode(prev).parked_at or at
    end
    local c1 = cands[1] or { id = '', kind = '' }
    redis.call('HSET', parks, park_field, cjson.encode({
      id = c1.id, kind = c1.kind, role = role, holder = holder,
      release_for = (role == 'holder') and holder or '', reason = reason,
      parked_at = parked_at, unit = unit, repo = repo, pr = pr, head = head,
      owner = owner_field, action = action, hold_head = hold_head }))
    if mode == 'unpark' then
      return { 'PARKED', reason }
    end
    return finish('PARKED ' .. reason)
  end

  -- ns_hold_release: `hold release`. The holder at the unit's current head
  -- releases its own hold; release_reader releases a down holder's hold with
  -- release_for=<holder>. A moved or stale head, or an open security read on
  -- the reader path, is refused.
  local function release(keys, args)
    local S, repo, pr, holder, as, head, evidence = args[1], args[2], args[3], args[4], args[5], args[6], args[7]
    local unit = redis.call('GET', 's:' .. S .. ':prunit:' .. repo .. ':' .. pr)
    if not unit then
      return { 'REFUSED', 'no-unit' }
    end
    local ukey = 's:' .. S .. ':u:' .. unit
    local u = redis.call('HMGET', ukey, 'head', 'security')
    if (u[1] or '') ~= head then
      return { 'REFUSED', 'stale-head' }
    end
    local hkey = 's:' .. S .. ':hold:' .. unit .. ':' .. holder
    if not hold_open(hkey) then
      return { 'REFUSED', 'no-open-hold' }
    end
    local release_for = ''
    if as ~= holder then
      local reader = redis.call('HGET', 's:' .. S .. ':policy', 'release_reader') or ''
      if as ~= reader then
        return { 'REFUSED', 'not-holder' }
      end
      if not is_down(holder) then
        return { 'REFUSED', 'holder-up' }
      end
      local sec = u[2] or ''
      if sec == '1' or sec == 'true' or sec == 'yes' then
        return { 'REFUSED', 'security' }
      end
      release_for = holder
    end
    local seq = redis.call('INCR', 'rec:seq')
    local at = now_ms()
    if redis.call('HGET', hkey, 'post_land') ~= '1' then
      local n = redis.call('HINCRBY', ukey, 'holds_open', -1)
      if n < 0 then redis.call('HSET', ukey, 'holds_open', 0) end
    end
    redis.call('HSET', hkey, 'released_by', as, 'release_kind', 'release',
      'release_reason', 'hold release at ' .. head, 'release_url', evidence,
      'released_at', tostring(at), 'release_seq', tostring(seq),
      'release_for', release_for)
    return { 'RELEASED', unit, tostring(seq) }
  end

  HD.ingest, HD.resolve_who, HD.write_disp = ingest, resolve_who, write_disp
  redis.register_function('ns_ingest_disposition', ingest)
  redis.register_function('ns_hold_route', route)
  redis.register_function('ns_hold_release', release)
end
