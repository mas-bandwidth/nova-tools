-- pr-to-read rule of `nova-sprint route` (#2756 10.7, 10.8.3, control 33;
-- nova-tools #3040 rev 4). No shebang: loader.go prepends the single library
-- header. The file is one do-block so its locals never add to the shared
-- chunk's local count.
--
-- ns_prtoread_runner keeps, in ci:<repo>:<head>:<gid>:runners field
-- runner:<row>, the
-- attempt with the highest key (gen, check_run_id, status_rank, at), whatever
-- its conclusion; status_rank is rerequested=-1 < queued=0 < in_progress=1 <
-- completed=2. A rerequested entry opens a new generation (the sentinel
-- status rerequested, rank -1) unless it is a redelivery or a rerequest of an
-- attempt a newer id already superseded. The compare and the write are one
-- call, so a redelivered entry is a no-op (KEPT).
--
-- ns_prtoread_adopt gives a sprint PR no card produced its review tasks at
-- its head, create-only, and records s:<S>:adopt:<repo>:<n>; a head already
-- adopted is a NOOP.
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local RANK = { queued = 0, in_progress = 1, completed = 2 }

  local function status_rank(status)
    if status == 'rerequested' then
      return -1
    end
    return RANK[status] or 0
  end

  local function cmp_str(a, b)
    if a < b then return -1 elseif a > b then return 1 end
    return 0
  end

  -- cmp_id orders check_run ids numerically; a non-numeric id falls back to
  -- length then bytes, which is the numeric order for decimal strings.
  local function cmp_id(a, b)
    a, b = tostring(a or ''), tostring(b or '')
    local x, y = tonumber(a), tonumber(b)
    if x and y then
      if x < y then return -1 elseif x > y then return 1 end
      return 0
    end
    if #a ~= #b then
      return #a < #b and -1 or 1
    end
    return cmp_str(a, b)
  end

  local function cmp_key(g1, id1, r1, at1, g2, id2, r2, at2)
    if g1 ~= g2 then
      return g1 < g2 and -1 or 1
    end
    local c = cmp_id(id1, id2)
    if c ~= 0 then
      return c
    end
    if r1 ~= r2 then
      return r1 < r2 and -1 or 1
    end
    return cmp_str(at1, at2)
  end

  local function str(v)
    if v == nil or v == cjson.null then
      return ''
    end
    return tostring(v)
  end

  -- ns_prtoread_runner KEYS[1]=ci:<repo>:<head>:<gid>:runners ARGV row
  -- attempt_json; attempt_json: {check_run_id, action, status, conclusion, at}.
  -- The caller resolves the gid (civerdict.ExpectedFrom over the PR's base,
  -- the base tip and the base policy) and refuses when any is missing; this
  -- function writes only KEYS[1]. The rows are mutable, so they never share
  -- the write-once receipt ci:<repo>:<head>:<gid> (gate_receipt_write).
  -- Reply: { verdict, gen, check_run_id, status, conclusion } where verdict is
  -- REPLACED, RERUN or KEPT; for a KEPT entry the gen is the one it was
  -- placed in and the rest are the entry's own.
  local function prtoread_runner(keys, args)
    local key, row, raw = keys[1] or '', args[1] or '', args[2] or ''
    local ok, e = pcall(cjson.decode, raw)
    if key == '' or row == '' or not ok or type(e) ~= 'table' then
      return redis.error_reply('ERR ns_prtoread_runner: usage key row attempt_json')
    end
    local id, action, status = str(e.check_run_id), str(e.action), str(e.status)
    local conclusion, at = str(e.conclusion), str(e.at)
    if id == '' then
      return redis.error_reply('ERR ns_prtoread_runner: attempt has no check_run_id')
    end

    if not string.match(key, '^ci:[^:]+:[^:]+:[0-9a-f]+:runners$') then
      return redis.error_reply('ERR ns_prtoread_runner: key is not ci:<repo>:<head>:<gid>:runners')
    end

    local field = 'runner:' .. row
    local s = nil
    local cur = redis.call('HGET', key, field)
    if cur then
      local ok2, v = pcall(cjson.decode, cur)
      if ok2 and type(v) == 'table' then
        s = v
      end
    end
    local sgen = s and (tonumber(s.gen) or 0) or 0
    local sid = s and str(s.check_run_id) or ''
    local srid, srat = s and str(s.rereq_id) or '', s and str(s.rereq_at) or ''

    local function write(gen, rid, rat, st, concl)
      redis.call('HSET', key, field, cjson.encode({
        gen = gen, check_run_id = id, status = st, conclusion = concl,
        rereq_id = rid, rereq_at = rat, source = 'runner:' .. row, at = at,
      }))
    end

    if action == 'rerequested' then
      -- A redelivery of the rerequest that opened this generation, or a
      -- rerequest of an attempt a newer id superseded: nothing moves.
      if s and ((srid == id and srat == at) or cmp_id(id, sid) < 0) then
        return { 'KEPT', tostring(sgen), id, 'rerequested', '' }
      end
      local gen = sgen + 1
      write(gen, id, at, 'rerequested', '')
      return { 'RERUN', tostring(gen), id, 'rerequested', '' }
    end

    if not s then
      write(0, '', '', status, conclusion)
      return { 'REPLACED', '0', id, status, conclusion }
    end
    -- Place the entry in a generation: the current one when it is newer than
    -- the rerequest that opened it, the one before otherwise.
    local egen = sgen
    if srid ~= '' then
      local c = cmp_id(id, srid)
      if not (c > 0 or (c == 0 and at > srat)) then
        egen = sgen - 1
      end
    end
    if cmp_key(egen, id, status_rank(status), at, sgen, sid, status_rank(str(s.status)), str(s.at)) > 0 then
      write(sgen, srid, srat, status, conclusion)
      return { 'REPLACED', tostring(sgen), id, status, conclusion }
    end
    return { 'KEPT', tostring(egen), id, status, conclusion }
  end

  local function receipt(S, id, actor, idem, at)
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'task push', 'id', id, 'from', '', 'to', 'open',
      'attempt', '0', 'token_sha', '', 'actor', actor or '', 'reason', 'pr-to-read',
      'evidence', '', 'idem', idem or '', 'at', tostring(at))
  end

  -- ns_prtoread_adopt S repo pr head actor cut n (id friend title priority
  -- payload_sha ref)*n
  -- The review tasks of one no-card PR at its head, with route.lua's
  -- create_task semantics (create-only; same payload EXISTS or CLOSED), at
  -- the front of each reader's queue, and s:<S>:adopt:<repo>:<pr>. Any id taken
  -- by another payload, or cancelled, refuses the whole call with BLOCKED and
  -- writes only one unresolved item naming it. An unregistered reader
  -- refuses with RETRY and writes nothing.
  local function prtoread_adopt(keys, args)
    local S, repo, pr, head, actor, cut = args[1], args[2], args[3], args[4], args[5], args[6]
    local n = tonumber(args[7])
    if not S or S == '' or not repo or repo == '' or not pr or pr == '' or not head or head == '' then
      return redis.error_reply('ERR ns_prtoread_adopt: usage S repo pr head actor cut n ...')
    end
    local akey = 's:' .. S .. ':adopt:' .. repo .. ':' .. pr
    if redis.call('HGET', akey, 'head') == head then
      return { 'NOOP', head }
    end
    -- The caller read the PR record, card and holds before this call; recheck
    -- them here, atomically with the writes, so a head that moved or a card,
    -- hold, draft or close that landed in between gets no review tasks (a
    -- review at a dead head; task_push's live-head guard is not on this path).
    local live = redis.call('HMGET', 's:' .. S .. ':pr:' .. repo .. ':' .. pr, 'head', 'state', 'draft')
    if live[1] ~= head then
      return { 'WAIT', 'head moved' }
    end
    if live[2] == 'landed' or live[2] == 'dropped' or live[2] == 'closed' then
      return { 'WAIT', 'state ' .. live[2] }
    end
    if live[3] == 'true' or live[3] == '1' then
      return { 'WAIT', 'draft' }
    end
    local card = redis.call('HGET', 's:' .. S .. ':prcard', repo .. '#' .. pr)
    if card and card ~= '' then
      return { 'WAIT', 'card ' .. card }
    end
    local holds = redis.call('HVALS', 's:' .. S .. ':hold:' .. repo .. ':' .. pr)
    for _, raw in ipairs(holds) do
      local ok, h = pcall(cjson.decode, raw)
      if not ok or type(h) ~= 'table' or type(h.released_by) ~= 'string' or h.released_by == '' then
        return { 'WAIT', 'hold' }
      end
    end
    if n == nil or n < 1 then
      return { 'RETRY', 'no readers' }
    end
    local base, width = 8, 6
    for i = 0, n - 1 do
      local friend = args[base + i * width + 1]
      if redis.call('SISMEMBER', 'friends', friend) == 0 then
        return { 'RETRY', 'unregistered ' .. tostring(friend) }
      end
    end
    for i = 0, n - 1 do
      local id, payload_sha = args[base + i * width], args[base + i * width + 4]
      local tkey = 'task:' .. id
      local existing = redis.call('HGET', tkey, 'payload_sha')
      local why = nil
      if existing and existing ~= payload_sha then
        why = 'conflict'
      elseif existing and redis.call('HGET', tkey, 'state') == 'cancelled' then
        why = 'cancelled'
      end
      if why then
        redis.call('HSETNX', 's:' .. S .. ':unresolved', repo .. '#' .. pr .. ':review-' .. why .. ':' .. id, head)
        return { 'BLOCKED', why .. ' ' .. id }
      end
    end
    local at = now_ms()
    local idem = 'pr-to-read:adopt:' .. repo .. ':' .. pr .. ':' .. head
    local ids, readers = {}, {}
    for i = 0, n - 1 do
      local o = base + i * width
      local id, friend, title, priority = args[o], args[o + 1], args[o + 2], args[o + 3]
      local payload_sha, ref = args[o + 4], args[o + 5]
      readers[#readers + 1] = friend
      local tkey = 'task:' .. id
      local status = 'EXISTS'
      if redis.call('EXISTS', tkey) == 0 then
        -- The front of open:<friend> is negative (classify.lua: a requeued card's priority).
        NS.task.create(id, {
          'kind', 'review', 'repo', repo, 'ref', ref, 'pr', pr, 'head', head,
          'title', title, 'effects', 'none', 'priority', tostring(priority),
          'attempt', '0', 'token', '0', 'payload_sha', payload_sha,
          'reason', '', 'evidence', '', 'claimed_at', '', 'started_at', '',
          'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '', 'dest', friend },
          { where = 'ready', state = 'open', friend = friend, sprint = S, created = at, by = actor, why = 'pr-to-read',
            qscore = -math.abs(tonumber(priority) or 0) - 1 })
        receipt(S, id, actor, idem, at)
        status = 'CREATED'
      elseif redis.call('HGET', tkey, 'state') == 'closed' then
        status = 'CLOSED'
      end
      ids[#ids + 1] = status .. ' ' .. id
    end
    local cut_at = ''
    if cut == '1' then
      cut_at = tostring(at)
    end
    redis.call('HSET', akey, 'head', head, 'readers', table.concat(readers, ','),
      'cut_at', cut_at, 'at', tostring(at))
    return { 'OK', table.concat(ids, ',') }
  end

  redis.register_function('ns_prtoread_runner', prtoread_runner)
  redis.register_function('ns_prtoread_adopt', prtoread_adopt)
end
