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
-- The two legs over the pr:<name>:<n> record (#3579, #3580; the record
-- `pr record|lines` writes, cmd/nova-sprint/pr.go), keyed by the record's
-- own head and typed lines, never by GitHub or a branch name:
--   ns_route_pr_read  an open PR whose head no counting friend line (SCORE,
--                     DISPOSITION, HOLD by a who that is not jev*, cold* or
--                     the author) covers -> one read task read-<n>-<sha8> on
--                     the least-loaded live reader, never the author; the
--                     read task of the old head is cancelled `superseded by
--                     <head>` unless the record's diff_sha256 equals the one
--                     the task was pushed with, then the task is carried to
--                     the new head instead. Fields read_task, read_task_head,
--                     read_task_to on the record are the receipt.
--   ns_route_pr_fix   a HOLD at head by a counting who with no open fix task
--                     at that head -> ONE fix-<n>-<sha8> task to the author
--                     (the builder task's owner, from the record's task
--                     field or task:pr) naming the HOLD line and the head; a
--                     swarm-built PR (no friend owner) gets recut-<n>-<sha8>
--                     on the coordinator's queue; the third distinct held
--                     head is close-over-recut: close-<n>-<sha8> to the
--                     coordinator with the finding kept. Fields fix_task,
--                     fix_task_head, fix_task_to, fix_task_kind are the receipt.
--   ns_route_sweep    every leg above over every open sprint in one call
--                     (#3831): the reconciler's route duty is one round trip.
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

  -- push_task writes one task in the friend queue's shape onto `to`. The
  -- queue entry id is kept as `xid` so a cancel can delete the entry; extra
  -- is an optional flat list of further fields.
  local function push_task(S, id, kind, to, title, repo, pr, head, stream, actor, why, at, extra)
    local state = 'open'
    if ws_present(stream) then
      state = 'ready'
    end
    local xid = redis.call('XADD', 'q:' .. to, '*', 'id', id)
    redis.call('HSET', 'task:' .. id,
      'kind', kind, 'ref', repo .. '#' .. pr, 'repo', repo, 'pr', pr, 'head', head,
      'owner', to, 'title', title, 'state', state, 'stream', stream, 'sprint', S,
      'route', 'reconcile', 'created_at', tostring(at), 'state_at', tostring(at),
      'queue', 'q:' .. to, 'xid', xid)
    if extra and #extra > 0 then
      redis.call('HSET', 'task:' .. id, unpack(extra))
    end
    redis.call('SADD', 'sprint:' .. S .. ':idx:' .. to .. ':open', id)
    if state == 'ready' then
      redis.call('ZADD', 'ws:' .. stream .. ':ready', at, id)
    end
    ws_log(id, stream, '', state, actor, why, at)
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

  -- The pr:<name>:<n> record legs (#3579, #3580).

  -- repo_name is the repo without its owner; the task id carries it as a
  -- suffix for every repo but nova-tools (fix-311-abcd1234-rowan-tools), the
  -- ids the queues already use.
  local function pr_task_id(kind, repo, pr, head)
    local name = string.match(repo, '([^/]+)$') or repo
    local id = kind .. '-' .. pr .. '-' .. sha8(head)
    if name ~= 'nova-tools' then
      id = id .. '-' .. name
    end
    return id
  end

  -- pr_author is the builder of record: the owner of the record's task
  -- (or of task:pr's task for the PR), when that owner is a friend. A
  -- swarm-built PR has no friend owner and returns ''. The branch name is
  -- never read (#3580: codex/*, fix/*, build-* named nobody).
  local function pr_author(repo, pr, task)
    if not task or task == '' then
      task = redis.call('HGET', 'task:pr', repo .. '#' .. pr) or ''
    end
    if task == '' then
      return ''
    end
    local owner = redis.call('HGET', 'task:' .. task, 'owner') or ''
    if owner == '' or redis.call('SISMEMBER', 'friends', owner) == 0 then
      return ''
    end
    return owner
  end

  -- counts_who: a line by jev, a cold reader or the author never counts.
  local function counts_who(who, author)
    return who ~= '' and who ~= author and string.sub(who, 1, 3) ~= 'jev' and string.sub(who, 1, 4) ~= 'cold'
  end

  -- pr_lines reads the record's typed lines (newline-joined `reads`) and
  -- returns, for the head: read (a counting SCORE/DISPOSITION/HOLD line at
  -- head), hold (the last counting HOLD line at head, per who the last line
  -- wins, so a later SCORE by the same who releases), and heads, the number
  -- of distinct heads a counting who has held (this head included when held).
  local function pr_lines(reads, head, author)
    local lhead = string.lower(head)
    local read, hold, holder = false, nil, nil
    local at_head, held_heads = {}, {}
    for line in string.gmatch(reads or '', '[^\n]+') do
      local words = {}
      for w in string.gmatch(line, '%S+') do words[#words + 1] = w end
      local kind = words[1] or ''
      local kv = {}
      for i = 2, #words do
        local k, v = string.match(words[i], '^([^=]+)=(.*)$')
        if k then kv[k] = string.gsub(v, '[:,;]+$', '') end
      end
      local who = string.lower(kv['who'] or '')
      local h = string.lower(kv['head'] or '')
      local typed = kind == 'SCORE' or kind == 'DISPOSITION' or kind == 'HOLD'
      if typed and #h >= 7 and counts_who(who, author) then
        local held = kind == 'HOLD' or (kind == 'DISPOSITION' and string.upper(kv['verdict'] or '') == 'HOLD')
        if held then
          held_heads[string.sub(h, 1, 7)] = true
        end
        if string.sub(lhead, 1, #h) == h then
          read = true
          at_head[who] = held and line or false
        end
      end
    end
    local whos = {}
    for who in pairs(at_head) do whos[#whos + 1] = who end
    table.sort(whos)
    for _, who in ipairs(whos) do
      if at_head[who] and not hold then
        hold, holder = at_head[who], who
      end
    end
    local n = 0
    for _ in pairs(held_heads) do n = n + 1 end
    return read, hold, holder, n
  end

  -- pr_record reads the record's fields the two legs need; nil when it is
  -- not an open member PR at a head. The key is the one PR record key,
  -- pr:<name>:<n> with the bare repository name (internal/nsprint/prkey).
  local function pr_record(repo, pr)
    local key = 'pr:' .. (string.match(repo, '([^/]+)$') or repo) .. ':' .. pr
    local r = redis.call('HMGET', key, 'head', 'state', 'reads', 'task', 'kind',
      'read_task', 'read_task_head', 'fix_task', 'fix_task_head', 'diff_sha256', 'stream')
    if not r[1] or r[1] == '' then
      return nil, 'no-pr-record'
    end
    if r[2] and r[2] ~= '' and r[2] ~= 'open' then
      return nil, 'state-' .. r[2]
    end
    if r[5] == 'stream' then
      return nil, 'stream-pr'
    end
    return { key = key, head = r[1], reads = r[3] or '', task = r[4] or '', read_task = r[6] or '',
      read_task_head = r[7] or '', fix_task = r[8] or '', fix_task_head = r[9] or '',
      diff = r[10] or '', stream = r[11] or '' }
  end

  -- task_open is true when the task hash exists in a state a reader can
  -- still take or is on: ready, open, waiting, working, claimed.
  local function task_open(id)
    local st = redis.call('HGET', 'task:' .. id, 'state')
    return st == 'ready' or st == 'open' or st == 'waiting' or st == 'working' or st == 'claimed', st or ''
  end

  -- cancel_task closes an unstarted task: state closed, cancelled 1, the
  -- evidence, out of its ws set, its owner's open index and its queue entry.
  local function cancel_task(S, id, stream, actor, why, at)
    local t = redis.call('HMGET', 'task:' .. id, 'state', 'owner', 'stream', 'queue', 'xid')
    local st, owner, tstream = t[1] or '', t[2] or '', t[3] or ''
    if tstream == '' then tstream = stream end
    if tstream ~= '' and (st == 'ready' or st == 'waiting' or st == 'parked') then
      redis.call('ZREM', 'ws:' .. tstream .. ':' .. st, id)
    end
    if owner ~= '' then
      redis.call('SREM', 'sprint:' .. S .. ':idx:' .. owner .. ':open', id)
      redis.call('SADD', 'sprint:' .. S .. ':idx:' .. owner .. ':closed', id)
    end
    if t[4] and t[4] ~= '' and t[5] and t[5] ~= '' then
      redis.pcall('XDEL', t[4], t[5])
    end
    redis.call('HSET', 'task:' .. id, 'state', 'closed', 'cancelled', '1', 'evidence', why,
      'closed_at', tostring(at), 'state_at', tostring(at))
    ws_log(id, tstream, st, 'closed', actor, why, at)
    receipt(S, 'task cancel', id, st, 'closed', actor, why, '', '', at)
  end

  -- ns_route_pr_read token S repo n stream actor reader...
  local function route_pr_read(keys, args)
    local token, S, repo, pr, stream, actor = args[1], args[2], args[3], args[4], args[5], args[6]
    if fenced(token) then return { 'FENCED' } end
    local rec, why = pr_record(repo, pr)
    if not rec then
      return { 'SKIP', why }
    end
    local head = rec.head
    if rec.stream ~= '' then stream = rec.stream end
    local author = pr_author(repo, pr, rec.task)
    local read = pr_lines(rec.reads, head, author)
    if read then
      return { 'SKIP', 'read-at-head' }
    end
    local id = pr_task_id('read', repo, pr, head)
    if rec.read_task_head == head and rec.read_task ~= '' and task_open(rec.read_task) then
      return { 'DUP', rec.read_task }
    end
    local at = now_ms()
    -- The old head's read task: carried when the diff is the same, else
    -- cancelled; a read already under way is noted and left to finish.
    local old = rec.read_task
    if old ~= '' and old ~= id and rec.read_task_head ~= head then
      local open, st = task_open(old)
      if open then
        local odiff = redis.call('HGET', 'task:' .. old, 'diff_sha256') or ''
        if rec.diff ~= '' and odiff == rec.diff and st ~= 'working' and st ~= 'claimed' then
          local title = 'STREAM: ' .. stream .. ' | read ' .. repo .. '#' .. pr .. ' at ' .. sha8(head) ..
            ' (carried from ' .. sha8(rec.read_task_head) .. ': identical diff)'
          redis.call('HSET', 'task:' .. old, 'head', head, 'title', title,
            'carried_from', rec.read_task_head, 'carried_at', tostring(at))
          redis.call('HSET', rec.key, 'read_task_head', head, 'updated_at', tostring(at))
          local note = 'read carry ' .. sha8(rec.read_task_head) .. ' -> ' .. sha8(head) .. ' (identical diff)'
          ws_log(old, stream, st, st, actor, note, at)
          receipt(S, 'task carry', old, st, st, actor, 'route pr read',
            'pr=' .. repo .. '#' .. pr .. ' head=' .. head .. ' from=' .. rec.read_task_head, 'read:' .. repo .. ':' .. pr .. ':' .. head, at)
          return { 'CARRIED', old, redis.call('HGET', 'task:' .. old, 'owner') or '' }
        end
        if st == 'working' or st == 'claimed' then
          redis.call('HSET', 'task:' .. old, 'superseded_by', head)
          ws_log(old, stream, st, st, actor, 'superseded by ' .. head .. ' (read under way, left to finish)', at)
        else
          cancel_task(S, old, stream, actor, 'superseded by ' .. head, at)
        end
      end
    end
    if redis.call('EXISTS', 'task:' .. id) == 1 then
      redis.call('HSET', rec.key, 'read_task', id, 'read_task_head', head, 'updated_at', tostring(at))
      return { 'EXISTS', id }
    end
    local best, best_load = nil, nil
    for i = 7, #args do
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
    local title = 'STREAM: ' .. stream .. ' | read ' .. repo .. '#' .. pr .. ' at ' .. sha8(head)
    local extra = { 'author', author }
    if rec.diff ~= '' then
      extra[#extra + 1] = 'diff_sha256'
      extra[#extra + 1] = rec.diff
    end
    local state = push_task(S, id, 'read', best, title, repo, pr, head, stream, actor, 'route pr read', at, extra)
    redis.call('HSET', rec.key, 'read_task', id, 'read_task_head', head, 'read_task_to', best, 'updated_at', tostring(at))
    receipt(S, 'task push', id, '', state, actor, 'route pr read',
      'pr=' .. repo .. '#' .. pr .. ' head=' .. head .. ' to=' .. best .. ' load=' .. tostring(best_load) .. ' author=' .. author,
      'read:' .. repo .. ':' .. pr .. ':' .. head, at)
    return { 'CREATED', id, best }
  end

  -- ns_route_pr_fix token S repo n stream actor coordinator
  local function route_pr_fix(keys, args)
    local token, S, repo, pr, stream, actor, coordinator = args[1], args[2], args[3], args[4], args[5], args[6], args[7] or ''
    if fenced(token) then return { 'FENCED' } end
    local rec, why = pr_record(repo, pr)
    if not rec then
      return { 'SKIP', why }
    end
    local head = rec.head
    if rec.stream ~= '' then stream = rec.stream end
    local author = pr_author(repo, pr, rec.task)
    local _, hold, holder, heads = pr_lines(rec.reads, head, author)
    if not hold then
      return { 'SKIP', 'no-hold' }
    end
    if rec.fix_task_head == head and rec.fix_task ~= '' and task_open(rec.fix_task) then
      return { 'DUP', rec.fix_task }
    end
    local kind, to, id, title
    local hold_line = string.sub(hold, 1, 400)
    if heads >= 3 then
      kind, to = 'close', coordinator
      id = pr_task_id('close', repo, pr, head)
      title = 'STREAM: ' .. stream .. ' | close over recut ' .. repo .. '#' .. pr .. ' at ' .. sha8(head) ..
        ': held at ' .. tostring(heads) .. ' heads; close with the finding kept: ' .. hold_line
    elseif author ~= '' then
      kind, to = 'fix', author
      id = pr_task_id('fix', repo, pr, head)
      title = 'STREAM: ' .. stream .. ' | fix ' .. repo .. '#' .. pr .. ' at ' .. sha8(head) ..
        ': answer the HOLD by ' .. holder .. ' item by item; REPAIR who=' .. author .. '; line: ' .. hold_line
    else
      kind, to = 'recut', coordinator
      id = pr_task_id('recut', repo, pr, head)
      title = 'STREAM: ' .. stream .. ' | recut ' .. repo .. '#' .. pr .. ' at ' .. sha8(head) ..
        ' (swarm-built, no friend author): HOLD by ' .. holder .. ': ' .. hold_line
    end
    if to == '' then
      return { 'SKIP', 'no-coordinator' }
    end
    local at = now_ms()
    if redis.call('EXISTS', 'task:' .. id) == 1 then
      redis.call('HSET', rec.key, 'fix_task', id, 'fix_task_head', head, 'fix_task_kind', kind, 'updated_at', tostring(at))
      return { 'EXISTS', id }
    end
    local state = push_task(S, id, kind, to, title, repo, pr, head, stream, actor, 'route pr ' .. kind, at,
      { 'author', author, 'holder', holder, 'hold_line', hold_line, 'held_heads', tostring(heads) })
    redis.call('HSET', rec.key, 'fix_task', id, 'fix_task_head', head, 'fix_task_to', to,
      'fix_task_kind', kind, 'updated_at', tostring(at))
    receipt(S, 'task push', id, '', state, actor, 'route pr ' .. kind,
      'pr=' .. repo .. '#' .. pr .. ' head=' .. head .. ' holder=' .. holder .. ' to=' .. to .. ' heads=' .. tostring(heads),
      'fix:' .. repo .. ':' .. pr .. ':' .. head, at)
    return { string.upper(kind), id, to }
  end

  -- The route sweep (nova-tools #3831): every leg over every open sprint in
  -- ONE call, so the reconciler's route duty is one round trip a pass. The
  -- duty made 420 round trips a pass at the fleet's key counts (one
  -- ns_route_read per harvested card per sprint, SKIPs every pass) against a
  -- one second pass on an 83 ms store. Each move is still the leg's own
  -- function above, called here in the order the duty called them.

  local function sorted(xs)
    table.sort(xs)
    return xs
  end

  -- live_readers: the given names, else the `readers` SET, else every
  -- friend; kept when a friend with a beat (friend:<f>:beat), never jev.
  local function live_readers(given)
    local names = given
    if #names == 0 then
      names = redis.call('SMEMBERS', 'readers')
      if #names == 0 then
        names = redis.call('SMEMBERS', 'friends')
      end
    end
    local live = {}
    for _, f in ipairs(names) do
      if f ~= 'jev' and redis.call('SISMEMBER', 'friends', f) == 1
        and redis.call('EXISTS', 'friend:' .. f .. ':beat') == 1 then
        live[#live + 1] = f
      end
    end
    return sorted(live)
  end

  -- coordinator: the first friend by name whose roles hold coordinator;
  -- else rowan when registered; else ''.
  local function coordinator()
    local fallback = ''
    for _, f in ipairs(sorted(redis.call('SMEMBERS', 'friends'))) do
      if f == 'rowan' then fallback = f end
      local roles = redis.call('HGET', 'friend:' .. f .. ':roles', 'roles') or ''
      for r in string.gmatch(roles, '[^,]+') do
        if string.match(r, '^%s*(.-)%s*$') == 'coordinator' then
          return f
        end
      end
    end
    return fallback
  end

  -- pr_ref reads a task's pr (123, #123, <repo>#123 or a pulls URL), repo
  -- and ref (<owner/repo>#<n>) into the PR record's repo and number, as the
  -- Go prRef did: nil when there is no positive number or no repo.
  local function pr_ref(pr, repo, ref)
    local trim = function(x) return string.match(x or '', '^%s*(.-)%s*$') end
    pr, repo, ref = trim(pr), trim(repo), trim(ref)
    local num = ''
    local i = nil
    local from = 1
    while true do
      local j = string.find(pr, '/pull/', from, true)
      if not j then break end
      i, from = j, j + 1
    end
    if i then
      local parts = {}
      for w in string.gmatch(string.sub(pr, 1, i - 1), '[^/]+') do parts[#parts + 1] = w end
      if #parts >= 2 then repo = parts[#parts - 1] .. '/' .. parts[#parts] end
      num = string.match(string.sub(pr, i + 6), '^/*(.-)/*$')
    elseif string.find(pr, '#', 1, true) then
      local h = string.find(pr, '#', 1, true)
      if h > 1 then repo = string.sub(pr, 1, h - 1) end
      num = string.sub(pr, h + 1)
    else
      num = pr
    end
    local h = string.find(ref, '#', 1, true)
    if h then
      local left, right = string.sub(ref, 1, h - 1), string.sub(ref, h + 1)
      if repo == '' or not string.find(repo, '/', 1, true) then
        if repo == '' or string.sub(left, -(#repo + 1)) == '/' .. repo then repo = left end
      end
      if num == '' then num = right end
    end
    if not string.match(num, '^[+-]?%d+$') then return nil end
    local n = tonumber(num)
    if not n or n <= 0 or repo == '' then return nil end
    return repo, n
  end

  -- stream_entries is an XREADGROUP or XAUTOCLAIM entry list as id, fields.
  local function each_entry(list, fn)
    for _, e in ipairs(list or {}) do
      if type(e) == 'table' and e[1] then
        local kv = {}
        local fl = e[2] or {}
        for k = 1, #fl, 2 do kv[fl[k]] = fl[k + 1] end
        fn(e[1], kv)
      end
    end
  end

  -- ns_route_sweep token actor bar consumer sweep_idle_ms reader...
  -- Reply: OK, then pairs: R <id> read pushed, C <id> read carried, F <id>
  -- fix/recut/close pushed, M <id> to merging, S <why> skipped; or FENCED.
  -- sweep_idle_ms above 0 also deletes every route consumer other than
  -- `consumer` idle that long with nothing pending (#3808), after the claim.
  local function route_sweep(keys, args)
    local token, actor, bar, consumer = args[1], args[2], args[3], args[4]
    local idle = tonumber(args[5] or '0') or 0
    if fenced(token) then return { 'FENCED' } end
    local given = {}
    for i = 6, #args do given[#given + 1] = args[i] end
    local readers = live_readers(given)
    local coord = coordinator()
    local out = { 'OK' }
    local function note(res)
      local w = res[1]
      if w == 'CREATED' then
        out[#out + 1], out[#out + 2] = res.leg, res[2]
      elseif w == 'CARRIED' then
        out[#out + 1], out[#out + 2] = 'C', res[2]
      elseif w == 'MERGING' then
        out[#out + 1], out[#out + 2] = 'M', res[2]
      elseif w == 'FIX' or w == 'RECUT' or w == 'CLOSE' then
        out[#out + 1], out[#out + 2] = 'F', res[2]
      elseif w == 'SKIP' and res[2] ~= 'no-hold' then
        out[#out + 1], out[#out + 2] = 'S', res[2]
      end
    end
    local function call(leg, fn, a)
      local res = fn({}, a)
      res.leg = leg
      note(res)
    end
    local function with_readers(a)
      for _, r in ipairs(readers) do a[#a + 1] = r end
      return a
    end
    for _, S in ipairs(sorted(redis.call('SMEMBERS', 'sprints'))) do
      local meta = redis.call('HMGET', 's:' .. S, 'status', 'stream')
      if meta[1] == 'open' then
        local stream = meta[2]
        if not stream or stream == '' then stream = S end
        -- reads: every harvested card.
        local labels = sorted(redis.call('SMEMBERS', 's:' .. S .. ':idx:card:harvested'))
        if #readers == 0 then
          for _ = 1, #labels do out[#out + 1], out[#out + 2] = 'S', 'no-reader' end
        else
          for _, label in ipairs(labels) do
            call('R', route_read, with_readers({ token, S, label, stream, actor }))
          end
        end
        -- fixes: the hold events under the route group, a dead instance's
        -- pending entries claimed first; a note or repair is acked here.
        local ev = 's:' .. S .. ':hold:events'
        redis.pcall('XGROUP', 'CREATE', ev, 'route', '0', 'MKSTREAM')
        redis.call('XGROUP', 'CREATECONSUMER', ev, 'route', consumer)
        local ack = {}
        local function fix(id, kv)
          if kv['type'] ~= 'hold' then
            ack[#ack + 1] = id
            return
          end
          call('F', route_fix, { token, S, id, kv['unit'] or '', kv['repo'] or '', kv['pr'] or '',
            kv['who'] or '', kv['head'] or '', stream, actor })
        end
        local claimed = redis.call('XAUTOCLAIM', ev, 'route', consumer, 0, '0-0', 'COUNT', 1000)
        each_entry(claimed[2], fix)
        local read = redis.call('XREADGROUP', 'GROUP', 'route', consumer, 'COUNT', 1000, 'STREAMS', ev, '>')
        if type(read) == 'table' then
          for _, s in ipairs(read) do each_entry(s[2], fix) end
        end
        if #ack > 0 then
          redis.call('XACK', ev, 'route', unpack(ack))
        end
        if idle > 0 then
          for _, c in ipairs(redis.call('XINFO', 'CONSUMERS', ev, 'route')) do
            local kv = {}
            for k = 1, #c, 2 do kv[c[k]] = c[k + 1] end
            if kv['name'] ~= consumer and tonumber(kv['pending']) == 0 and tonumber(kv['idle']) >= idle then
              redis.call('XGROUP', 'DELCONSUMER', ev, 'route', kv['name'])
            end
          end
        end
        -- merging: a unit that may be ready (open, a PR, no open hold, CI
        -- receipts at head); the function re-checks every guard.
        for _, u in ipairs(sorted(redis.call('SMEMBERS', 's:' .. S .. ':units'))) do
          local v = redis.call('HMGET', 's:' .. S .. ':u:' .. u, 'head', 'state', 'repo', 'pr', 'holds_open')
          local head, st, repo, pr = v[1] or '', v[2] or '', v[3] or '', v[4] or ''
          if head ~= '' and pr ~= '' and st ~= 'landed' and st ~= 'landing' and st ~= 'dropped' then
            if (tonumber(v[5] or '0') or 0) > 0 then
              out[#out + 1], out[#out + 2] = 'S', 'holds-open'
            elseif redis.call('SCARD', 'ci:' .. repo .. ':' .. head .. ':gids') == 0 then
              out[#out + 1], out[#out + 2] = 'S', 'no-ci'
            else
              call('M', route_merging, { token, S, u, bar, stream, actor })
            end
          end
        end
        -- the pr:<name>:<n> legs over the stream's working and merging tasks.
        local seen, cands = {}, {}
        for _, set in ipairs({ 'working', 'merging' }) do
          for _, id in ipairs(redis.call('ZRANGE', 'ws:' .. stream .. ':' .. set, 0, -1)) do
            local t = redis.call('HMGET', 'task:' .. id, 'pr', 'repo', 'ref')
            local repo, n = pr_ref(t[1], t[2], t[3])
            if repo and not seen[repo .. '#' .. n] then
              seen[repo .. '#' .. n] = true
              cands[#cands + 1] = { repo = repo, n = n }
            end
          end
        end
        table.sort(cands, function(a, b)
          if a.repo ~= b.repo then return a.repo < b.repo end
          return a.n < b.n
        end)
        for _, c in ipairs(cands) do
          local n = tostring(c.n)
          local r = redis.call('HMGET', 'pr:' .. (string.match(c.repo, '([^/]+)$') or c.repo) .. ':' .. n, 'head', 'state')
          local head, st = r[1] or '', r[2] or ''
          if head ~= '' and (st == '' or st == 'open') then
            if #readers == 0 then
              out[#out + 1], out[#out + 2] = 'S', 'no-reader'
            else
              call('R', route_pr_read, with_readers({ token, S, c.repo, n, stream, actor }))
            end
            call('F', route_pr_fix, { token, S, c.repo, n, stream, actor, coord })
          end
        end
      end
    end
    return out
  end

  redis.register_function('ns_route_pr_read', route_pr_read)
  redis.register_function('ns_route_sweep', route_sweep)
  redis.register_function('ns_route_pr_fix', route_pr_fix)
  redis.register_function('ns_route_read', route_read)
  redis.register_function('ns_route_fix', route_fix)
  redis.register_function('ns_route_merging', route_merging)
end
