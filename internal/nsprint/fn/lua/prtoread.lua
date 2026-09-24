-- pr-to-read rule functions for nova-sprint (#2756 10.7, 10.8.3, control 33; nova-tools #3040).
-- No shebang: loader.go prepends the library header.
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function receipt(S, kind, id, from_state, to_state, attempt, actor, reason, evidence, idem, at)
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
      'attempt', tostring(attempt or 0), 'token_sha', '',
      'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
      'idem', idem or '', 'at', tostring(at))
  end

  local function create_task(S, id, kind, title, effects, repo, pr, head, ref, to, front, priority, payload_sha, actor, idem, at)
    local key = 's:' .. S .. ':task:' .. id
    local existing = redis.call('HGET', key, 'payload_sha')
    if existing then
      if existing ~= payload_sha then
        return 'CONFLICT'
      end
      local state = redis.call('HGET', key, 'state')
      if state == 'closed' or state == 'cancelled' then
        return 'CLOSED'
      end
      return 'EXISTS'
    end
    redis.call('HSET', key,
      'kind', kind, 'repo', repo, 'ref', ref, 'pr', pr, 'head', head,
      'title', title, 'effects', effects, 'owner', '', 'priority', tostring(priority),
      'state', 'open', 'attempt', '0', 'token', '0', 'payload_sha', payload_sha,
      'reason', '', 'evidence', '', 'claimed_at', '', 'started_at', '',
      'beat_at', '', 'closed_at', '', 'verdict', '', 'score', '')
    local score = tonumber(priority)
    if front then
      score = -score
    end
    redis.call('ZADD', 's:' .. S .. ':open:' .. to, score, id)
    redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
    receipt(S, 'task push', id, '', 'open', 0, actor, 'pr-to-read', '', idem, at)
    return 'CREATED'
  end

  local function status_rank(st)
    if st == 'rerequested' then
      return -1
    elseif st == 'queued' or st == 'created' then
      return 0
    elseif st == 'in_progress' then
      return 1
    elseif st == 'completed' then
      return 2
    end
    return 0
  end

  -- ns_prtoread_runner(key, row, attempt_json)
  -- Stores the highest attempt key (gen, check_run_id, status_rank, at) into
  -- ci:<repo>:<sha> field runner:<row>.
  local function prtoread_runner(keys, args)
    local key, row, attempt_json = args[1], args[2], args[3]
    if not key or not row or not attempt_json then
      return { 'USAGE' }
    end
    local entry = cjson.decode(attempt_json)
    local action = entry.action or ''
    local entry_id = tonumber(entry.check_run_id) or 0
    local status = entry.status or ''
    if action == 'rerequested' then
      status = 'rerequested'
    elseif status == '' or status == 'created' then
      if action == 'created' or action == 'queued' then
        status = 'queued'
      end
    end
    local conclusion = entry.conclusion or ''
    local at = entry.at or ''

    local raw = redis.call('HGET', key, 'runner:' .. row)
    local stored = nil
    if raw and raw ~= '' then
      stored = cjson.decode(raw)
      stored.gen = tonumber(stored.gen) or 0
      stored.check_run_id = tonumber(stored.check_run_id) or 0
      stored.rereq_id = tonumber(stored.rereq_id) or 0
      stored.rereq_at = stored.rereq_at or ''
    end

    if action == 'rerequested' then
      if stored then
        if entry_id < stored.check_run_id then
          return { 'KEPT', tostring(stored.gen), tostring(entry_id), status, conclusion }
        end
        if entry_id == stored.rereq_id and at == stored.rereq_at then
          return { 'KEPT', tostring(stored.gen), tostring(entry_id), status, conclusion }
        end
      end
      local new_gen = 1
      if stored then
        new_gen = stored.gen + 1
      end
      local new_stored = {
        gen = new_gen,
        check_run_id = entry_id,
        rereq_id = entry_id,
        rereq_at = at,
        status = 'rerequested',
        conclusion = '',
        source = 'runner:' .. row,
        at = at,
      }
      redis.call('HSET', key, 'runner:' .. row, cjson.encode(new_stored))
      return { 'RERUN', tostring(new_gen), tostring(entry_id), 'rerequested', '' }
    end

    -- Every other entry is placed in a generation before the compare
    local entry_gen = 0
    if stored then
      if stored.rereq_id and stored.rereq_id > 0 then
        if entry_id > stored.rereq_id or (entry_id == stored.rereq_id and at > stored.rereq_at) then
          entry_gen = stored.gen
        else
          entry_gen = stored.gen - 1
        end
      else
        entry_gen = stored.gen
      end
    end

    if stored then
      if entry_gen < stored.gen then
        return { 'KEPT', tostring(entry_gen), tostring(entry_id), status, conclusion }
      end

      -- entry_gen == stored.gen: compare (gen, check_run_id, status_rank, at)
      if entry_id < stored.check_run_id then
        return { 'KEPT', tostring(entry_gen), tostring(entry_id), status, conclusion }
      elseif entry_id == stored.check_run_id then
        local e_rank = status_rank(status)
        local s_rank = status_rank(stored.status)
        if e_rank < s_rank then
          return { 'KEPT', tostring(entry_gen), tostring(entry_id), status, conclusion }
        elseif e_rank == s_rank then
          if at <= (stored.at or '') then
            return { 'KEPT', tostring(entry_gen), tostring(entry_id), status, conclusion }
          end
        end
      end
    end

    local to_store = {
      gen = entry_gen,
      check_run_id = entry_id,
      status = status,
      conclusion = conclusion,
      source = 'runner:' .. row,
      at = at,
    }
    if stored and stored.rereq_id and stored.rereq_id > 0 then
      to_store.rereq_id = stored.rereq_id
      to_store.rereq_at = stored.rereq_at
    end
    redis.call('HSET', key, 'runner:' .. row, cjson.encode(to_store))
    return { 'REPLACED', tostring(entry_gen), tostring(entry_id), status, conclusion }
  end

  -- ns_prtoread_adopt S repo pr head readers_str cut_at actor n [id friend title priority payload_sha]...
  local function prtoread_adopt(keys, args)
    local S, repo, pr, head, readers_str, cut_at, actor = args[1], args[2], args[3], args[4], args[5], args[6], args[7]
    local n = tonumber(args[8]) or 0
    if not S or not repo or not pr or not head then
      return { 'USAGE' }
    end
    local adopt_key = 's:' .. S .. ':adopt:' .. repo .. ':' .. pr
    local existing_head = redis.call('HGET', adopt_key, 'head')
    if existing_head == head then
      return { 'NOOP' }
    end

    local at = now_ms()
    local at_str = tostring(at)
    local idem = 'adopt:' .. repo .. ':' .. pr .. ':' .. head
    local base = 9
    local ids = {}
    for i = 0, n - 1 do
      local id = args[base + i * 5]
      local friend = args[base + i * 5 + 1]
      local title = args[base + i * 5 + 2]
      local priority = args[base + i * 5 + 3]
      local payload_sha = args[base + i * 5 + 4]
      local status = create_task(S, id, 'review', title, 'none', repo, pr, head, '',
        friend, true, priority, payload_sha, actor, idem, at_str)
      ids[#ids + 1] = status .. ' ' .. id
    end
    redis.call('HSET', adopt_key, 'head', head, 'readers', readers_str, 'cut_at', cut_at, 'at', at_str)
    return { 'OK', table.concat(ids, ',') }
  end

  redis.register_function('ns_prtoread_runner', prtoread_runner)
  redis.register_function('ns_prtoread_adopt', prtoread_adopt)
end
