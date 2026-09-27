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
-- A task is a task card (02_card_move.lua, NS.task, nova-tools #3778): push
-- is NS.task.create onto the friend's ready set, merging and cancel are
-- NS.task.move, which keeps every set (ws:<stream>:<where>,
-- friend:<f>:cards:<where>) and the friend-queue shape (q:<friend> entry,
-- sprint:<S>:idx:<friend>:*) and the sprint's fine-state index
-- (s:<S>:idx:task:<state>); this file writes none of them itself. The locals
-- are scoped to this block.
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

  local function ws_log(id, stream, from_state, to_state, by, why, at)
    redis.call('XADD', 'ws:log', 'MAXLEN', '~', 100000, '*',
      'id', id, 'stream', stream, 'from', from_state, 'to', to_state,
      'by', by, 'why', why, 'at', tostring(at))
  end

  local function sha8(head)
    return string.sub(head or '', 1, 8)
  end

  -- push_task creates one task card on `to`'s ready set (NS.task.create);
  -- extra is an optional flat list of further fields. It returns the where
  -- (ready), or the refusal.
  local function push_task(S, id, kind, to, title, repo, pr, head, stream, actor, why, at, extra)
    local fields = { 'kind', kind, 'ref', repo .. '#' .. pr, 'origin', 'https://github.com/' .. repo .. '/pull/' .. pr,
      'repo', repo, 'pr', pr, 'head', head, 'title', title, 'route', 'reconcile' }
    for _, v in ipairs(extra or {}) do fields[#fields + 1] = v end
    local err, info = NS.task.create(id, fields, { where = 'ready', friend = to, stream = stream, sprint = S,
      created = at, by = actor, why = why })
    if err then
      return 'refused ' .. err
    end
    return info.to
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

  -- task_key is the task's record, task:<id> (one store, nova-tools #3778),
  -- or nil when there is none.
  local function task_key(S, id)
    if redis.call('EXISTS', 'task:' .. id) == 1 then
      return 'task:' .. id
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
    local at = now_ms()
    -- Through the one task move. A task nobody took is taken first
    -- (ready -> working -> merging, two receipted moves); a closed one moves
    -- on to merging (done/ok -> merging).
    local p = NS.task.read(id)
    local tstream, tstate = p.stream, p.where
    if tstream == '' then tstream = stream end
    if p.placed and tstate == 'merging' then
      redis.call('HSET', routed, idem, id)
      return { 'DUP', id }
    end
    local o = { by = actor, why = 'route merging', stream = tstream, sprint = S, fields = { 'pr', pr, 'head', head } }
    local err
    if not p.placed or tstate == 'ready' then
      err = NS.task.move(id, 'working', o)
    end
    if not err then err = NS.task.move(id, 'merging', o) end
    if err then
      return { 'SKIP', 'task-' .. err }
    end
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

  -- task_open is true when the task card is where a reader can still take
  -- it or is on it: ready, waiting, working. The second value is its where
  -- (a record that predates the where field: its friend-queue state).
  local function task_open(id)
    local p = NS.task.read(id)
    if not p then return false, '' end
    local w = p.where
    if not p.placed then
      w = ({ open = 'ready', ready = 'ready', waiting = 'waiting', working = 'working', claimed = 'working' })[p.state]
        or p.state
    end
    return w == 'ready' or w == 'waiting' or w == 'working', w
  end

  -- cancel_task closes an unstarted task: done/fail through the one task
  -- move (its sets, its friend's open index and its queue entry follow),
  -- with the evidence.
  local function cancel_task(S, id, stream, actor, why, at)
    local p = NS.task.read(id)
    local st = p and p.where or ''
    NS.task.move(id, 'done', { ok = 'fail', by = actor, why = why, sprint = S,
      fields = { 'evidence', why, 'closed_at', tostring(at) } })
    receipt(S, 'task cancel', id, st, 'done', actor, why, '', '', at)
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

  -- The whole route pass in one call (Glenn 2026-09-27: "You always need
  -- to batch redis"; the store is 128 ms from the Studio and the Go pass
  -- made 17 round trips, one per read it chained). The open sprints, the
  -- live readers and the coordinator are read here; then, per sprint, the
  -- four legs call the local functions above exactly as the Go pass called
  -- them over the wire. Every function checks the fence itself, so a pass
  -- that lost its lease writes nothing past the first refusal.

  -- trim(s, chars): s without leading and trailing chars (Go strings.Trim).
  local function trim(s, chars)
    chars = chars or '%s'
    return (string.gsub(string.gsub(s, '^[' .. chars .. ']+', ''), '[' .. chars .. ']+$', ''))
  end

  -- pr_ref reads a task's PR fields (pr as 123, #123, <repo>#123 or a pulls
  -- URL; repo; ref as <owner/repo>#<n>) into the record's repo and number,
  -- the reconcile package's prRef; nil when they name no PR.
  local function pr_ref(pr, repo, ref)
    pr, repo, ref = trim(pr), trim(repo), trim(ref)
    local num = ''
    local last = nil
    local from = 1
    while true do
      local i = string.find(pr, '/pull/', from, true)
      if not i then break end
      last, from = i, i + 1
    end
    if last then
      local parts = {}
      for w in string.gmatch(trim(string.sub(pr, 1, last - 1), '/'), '[^/]+') do parts[#parts + 1] = w end
      if #parts >= 2 then repo = parts[#parts - 1] .. '/' .. parts[#parts] end
      num = trim(string.sub(pr, last + 6), '/')
    else
      local l, r = string.match(pr, '^([^#]*)#(.*)$')
      if l then
        if l ~= '' then repo = l end
        num = r
      else
        num = pr
      end
    end
    local rl, rr = string.match(ref, '^([^#]*)#(.*)$')
    if rl then
      if repo == '' or not string.find(repo, '/', 1, true) then
        if repo == '' or string.sub(rl, -(#repo + 1)) == '/' .. repo then repo = rl end
      end
      if num == '' then num = rr end
    end
    local n = tonumber(num)
    if not n or n <= 0 or n ~= math.floor(n) or repo == '' or not string.match(num, '^%d+$') then return nil end
    return repo, n
  end

  -- held_at: some HOLD line, or DISPOSITION verdict=HOLD line, names this
  -- head (the Go prefilter of the fix leg; route_pr_fix re-reads the lines
  -- with the author and the last-line-wins rule, this only spares a call).
  local function held_at(reads, head)
    head = string.lower(head)
    for line in string.gmatch(reads or '', '[^\n]+') do
      local words = {}
      for w in string.gmatch(line, '%S+') do words[#words + 1] = w end
      local kind = words[1] or ''
      local h, hold = '', false
      if kind == 'HOLD' or kind == 'DISPOSITION' then
        local kv = {}
        for i = 2, #words do
          local k, v = string.match(words[i], '^([^=]+)=(.*)$')
          if k then kv[k] = string.gsub(v, '[:,;]+$', '') end
        end
        h = string.lower(kv['head'] or '')
        hold = kind == 'HOLD' or string.upper(kv['verdict'] or '') == 'HOLD'
      end
      if hold and #h >= 7 and string.sub(head, 1, #h) == h then return true end
    end
    return false
  end

  -- ns_route_pass token actor bar instance sweep max_idle_ms now_ms reader...
  -- reader... is the verb's --readers (empty: the readers set, else every
  -- friend); a reader counts when it is a registered friend with a beat
  -- within a minute (beat.Window) and is not jev. sweep=1 deletes the route
  -- group's consumers idle past max_idle_ms with nothing pending (once a
  -- minute per instance, #3808). Reply: pairs, a kind then a value, in the
  -- order the moves were made: read <id>, fix <id>, merging <id>, carried
  -- <id>, skip <why>, err <text>; FENCED alone when the token is not the
  -- lease's.
  local function route_pass(keys, args)
    local token, actor = args[1], args[2]
    local bar = tonumber(args[3] or '') or 8
    local instance = args[4] or ''
    local sweep, max_idle = args[5] == '1', tonumber(args[6] or '') or 3600000
    local now = tonumber(args[7] or '') or now_ms()
    if fenced(token) then return { 'FENCED' } end
    local out = {}
    local function emit(k, v)
      out[#out + 1] = k
      out[#out + 1] = v
    end
    local function moved(reply, made)
      local w = reply[1]
      if w == 'FENCED' then return true end
      if made[w] then emit(made[w], reply[2]) elseif w == 'SKIP' then emit('skip', reply[2]) end
      return false
    end
    -- the open sprints, in name order
    local all = redis.call('SMEMBERS', 'sprints')
    table.sort(all)
    local sprints = {}
    for _, S in ipairs(all) do
      if redis.call('HGET', 's:' .. S, 'status') == 'open' then sprints[#sprints + 1] = S end
    end
    -- the live readers
    local names = {}
    for i = 8, #args do names[#names + 1] = args[i] end
    if #names == 0 then names = redis.call('SMEMBERS', 'readers') end
    if #names == 0 then names = redis.call('SMEMBERS', 'friends') end
    local readers = {}
    for _, f in ipairs(names) do
      if f ~= 'jev' and redis.call('SISMEMBER', 'friends', f) == 1 then
        local at = NS.task.ms(redis.call('HGET', 'friend:' .. f .. ':beat', 'at'))
        if at and math.abs(now - at) <= 60000 then readers[#readers + 1] = f end
      end
    end
    table.sort(readers)
    -- the coordinator: the first friend whose roles hold coordinator, else rowan
    local friends = redis.call('SMEMBERS', 'friends')
    table.sort(friends)
    local coordinator = ''
    for _, f in ipairs(friends) do
      if coordinator == '' then
        for r in string.gmatch(redis.call('HGET', 'friend:' .. f .. ':roles', 'roles') or '', '[^,]+') do
          if trim(r) == 'coordinator' then coordinator = f end
        end
      end
    end
    if coordinator == '' then
      for _, f in ipairs(friends) do
        if f == 'rowan' then coordinator = f end
      end
    end
    local epoch = NS.task.epoch()
    for _, S in ipairs(sprints) do
      local stream = redis.call('HGET', 's:' .. S, 'stream')
      if not stream or stream == '' then stream = S end
      -- reads: every harvested card with the live readers
      local labels = redis.call('SMEMBERS', 's:' .. S .. ':idx:card:harvested')
      table.sort(labels)
      if #readers == 0 and #labels > 0 then
        for _ = 1, #labels do emit('skip', 'no-reader') end
      else
        for _, label in ipairs(labels) do
          local a = { token, S, label, stream, actor }
          for _, r in ipairs(readers) do a[#a + 1] = r end
          if moved(route_read({}, a), { CREATED = 'read' }) then return { 'FENCED' } end
        end
      end
      -- fixes: the hold events under the route group; a pending entry a dead
      -- instance left is claimed first; a note or repair is acknowledged
      -- here, a hold by the function; a hold router's sprint (fix_to and
      -- release_reader in its policy) has its events acknowledged unrouted
      local ev = 's:' .. S .. ':hold:events'
      local consumer = 'reconciler-' .. instance
      local pol = redis.call('HMGET', 's:' .. S .. ':policy', 'fix_to', 'release_reader')
      local hold_router = (pol[1] ~= false and pol[1] ~= '') and (pol[2] ~= false and pol[2] ~= '')
      local made = redis.pcall('XGROUP', 'CREATE', ev, 'route', '0', 'MKSTREAM')
      if type(made) == 'table' and made.err and not string.find(made.err, 'BUSYGROUP', 1, true) then
        emit('err', S .. ': group: ' .. made.err)
      else
        redis.call('XGROUP', 'CREATECONSUMER', ev, 'route', consumer)
        local msgs = {}
        local claimed = redis.call('XAUTOCLAIM', ev, 'route', consumer, '0', '0-0', 'COUNT', '1000')
        for _, m in ipairs(claimed[2] or {}) do msgs[#msgs + 1] = m end
        local read = redis.call('XREADGROUP', 'GROUP', 'route', consumer, 'COUNT', '1000', 'STREAMS', ev, '>')
        if read then
          for _, st in ipairs(read) do
            for _, m in ipairs(st[2]) do msgs[#msgs + 1] = m end
          end
        end
        local ack = {}
        for _, m in ipairs(msgs) do
          local id, fl = m[1], m[2] or {}
          local v = {}
          for i = 1, #fl, 2 do v[fl[i]] = fl[i + 1] end
          if hold_router or v['type'] ~= 'hold' then
            if hold_router then emit('skip', 'hold-router') end
            ack[#ack + 1] = id
          else
            local reply = route_fix({}, { token, S, id, v['unit'] or '', v['repo'] or '', v['pr'] or '',
              v['who'] or '', v['head'] or '', stream, actor })
            if moved(reply, { CREATED = 'fix' }) then return { 'FENCED' } end
          end
        end
        if #ack > 0 then redis.call('XACK', ev, 'route', unpack(ack)) end
        if sweep then
          for _, c in ipairs(redis.call('XINFO', 'CONSUMERS', ev, 'route')) do
            local cv = {}
            for i = 1, #c, 2 do cv[c[i]] = c[i + 1] end
            if cv['name'] ~= consumer and (tonumber(cv['pending']) or 0) == 0 and (tonumber(cv['idle']) or 0) >= max_idle then
              redis.call('XGROUP', 'DELCONSUMER', ev, 'route', cv['name'])
            end
          end
        end
      end
      -- merging: a unit that may be ready (open, a PR, no open hold, CI
      -- receipts at head) is offered to route_merging, which re-checks
      local units = redis.call('SMEMBERS', 's:' .. S .. ':units')
      table.sort(units)
      for _, u in ipairs(units) do
        local r = redis.call('HMGET', 's:' .. S .. ':u:' .. u, 'head', 'state', 'repo', 'pr', 'holds_open')
        local head, state, repo, pr = r[1] or '', r[2] or '', r[3] or '', r[4] or ''
        if head ~= '' and pr ~= '' and state ~= 'landed' and state ~= 'landing' and state ~= 'dropped' then
          if (tonumber(r[5] or '0') or 0) > 0 then
            emit('skip', 'holds-open')
          elseif redis.call('SCARD', 'ci:' .. repo .. ':' .. head .. ':gids') == 0 then
            emit('skip', 'no-ci')
          elseif moved(route_merging({}, { token, S, u, tostring(bar), stream, actor }), { MERGING = 'merging' }) then
            return { 'FENCED' }
          end
        end
      end
      -- the PR record legs over the stream's working and merging tasks
      local ids = {}
      for _, w in ipairs({ 'working', 'merging' }) do
        for _, id in ipairs(redis.call('ZRANGE', NS.task.wskey(epoch, stream, w), 0, -1)) do ids[#ids + 1] = id end
      end
      local cands, seen = {}, {}
      for _, id in ipairs(ids) do
        local t = redis.call('HMGET', 'task:' .. id, 'pr', 'repo', 'ref')
        local repo, n = pr_ref(t[1] or '', t[2] or '', t[3] or '')
        if repo and not seen[repo .. '#' .. n] then
          seen[repo .. '#' .. n] = true
          cands[#cands + 1] = { repo = repo, n = n }
        end
      end
      table.sort(cands, function(a, b)
        if a.repo ~= b.repo then return a.repo < b.repo end
        return a.n < b.n
      end)
      for _, c in ipairs(cands) do
        local n = string.format('%d', c.n)
        local key = 'pr:' .. (string.match(c.repo, '([^/]+)$') or c.repo) .. ':' .. n
        local rec = redis.call('HMGET', key, 'head', 'state', 'reads')
        local head, state, reads = rec[1] or '', rec[2] or '', rec[3] or ''
        if head ~= '' and (state == '' or state == 'open') then
          if #readers == 0 then
            emit('skip', 'no-reader')
          else
            local a = { token, S, c.repo, n, stream, actor }
            for _, r in ipairs(readers) do a[#a + 1] = r end
            if moved(route_pr_read({}, a), { CREATED = 'read', CARRIED = 'carried' }) then return { 'FENCED' } end
          end
          if held_at(reads, head) then
            local reply = route_pr_fix({}, { token, S, c.repo, n, stream, actor, coordinator })
            if moved(reply, { FIX = 'fix', RECUT = 'fix', CLOSE = 'fix' }) then return { 'FENCED' } end
          end
        end
      end
    end
    return out
  end

  redis.register_function('ns_route_pr_read', route_pr_read)
  redis.register_function('ns_route_pr_fix', route_pr_fix)
  redis.register_function('ns_route_read', route_read)
  redis.register_function('ns_route_fix', route_fix)
  redis.register_function('ns_route_merging', route_merging)
  redis.register_function('ns_route_pass', route_pass)
end
