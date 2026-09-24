-- land.lua: nova_sprint functions for the lander service (Issue #3139 rev 7).
-- Keys 2.2, fences and intent cut 2.3, events 2.2/7.7.
-- Every lua/ file shares one chunk, so locals carry a land_ prefix.

local function land_now_ms()
  local t = redis.call('TIME')
  return string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
end

-- gate_receipt_write (3.7): writes the write-once single or tip gid receipt.
-- ci:<repo>:<head>:<gid> and ci:<repo>:<base>:tip:<tip>:<gid>.
local function gate_receipt_write(repo, head, gid, verdict, kind, base, base_sha, required_set_id, policy_id, runner_id, receipt, bench, pkg, test, at)
  local ckey = 'ci:' .. repo .. ':' .. head .. ':' .. gid
  if redis.call('EXISTS', ckey) == 1 then
    return 'ALREADY'
  end
  at = at or land_now_ms()
  redis.call('HSET', ckey,
    'verdict', verdict,
    'kind', kind,
    'base', base,
    'base_sha', base_sha,
    'required_set_id', required_set_id,
    'policy_id', policy_id,
    'runner_id', runner_id,
    'receipt', receipt,
    'bench', bench or '',
    'pkg', pkg or '',
    'test', test or '',
    'at', tostring(at)
  )
  redis.call('SADD', 'ci:' .. repo .. ':' .. head .. ':gids', gid)
  if kind == 'tip' then
    local tip_key = 'ci:' .. repo .. ':' .. base .. ':tip:' .. base_sha .. ':' .. gid
    redis.call('HSET', tip_key,
      'verdict', verdict,
      'kind', kind,
      'base', base,
      'base_sha', base_sha,
      'required_set_id', required_set_id,
      'policy_id', policy_id,
      'runner_id', runner_id,
      'receipt', receipt,
      'bench', bench or '',
      'pkg', pkg or '',
      'test', test or '',
      'at', tostring(at)
    )
  end
  return 'OK'
end

redis.register_function('ns_gate_receipt_write', function(keys, args)
  local repo, head, gid, verdict = args[1], args[2], args[3], args[4]
  local kind, base, base_sha = args[5], args[6], args[7]
  local required_set_id, policy_id, runner_id = args[8], args[9], args[10]
  local receipt, bench, pkg, test = args[11], args[12], args[13], args[14]
  return gate_receipt_write(repo, head, gid, verdict, kind, base, base_sha, required_set_id, policy_id, runner_id, receipt, bench, pkg, test)
end)

-- ns_unit_head: writes head and metadata for one unit, adds to s:<S>:units index,
-- and increments rec:seq.
redis.register_function('ns_unit_head', function(keys, args)
  local S, unit, repo, base, branch, head, base_sha = args[1], args[2], args[3], args[4], args[5], args[6], args[7]
  local stack_parent, files, paths_hash, security, class = args[8], args[9], args[10], args[11], args[12]
  local pr, author = args[13], args[14]

  local seq = redis.call('INCR', 'rec:seq')
  local ukey = 's:' .. S .. ':u:' .. unit
  local cur_st = redis.call('HGET', ukey, 'state')
  local new_st = cur_st
  if not cur_st or cur_st == '' or cur_st == 'opened' then
    new_st = 'reading'
  end

  redis.call('HSET', ukey,
    'repo', repo,
    'base', base,
    'branch', branch,
    'head', head,
    'base_sha', base_sha,
    'stack_parent', stack_parent or 'none',
    'files', files or '',
    'paths_hash', paths_hash or '',
    'security', security or '',
    'class', class or '',
    'state', new_st,
    'seq', tostring(seq),
    'author', author or ''
  )
  if pr and pr ~= '' and pr ~= '0' then
    redis.call('HSET', ukey, 'pr', pr)
    redis.call('SET', 's:' .. S .. ':prunit:' .. repo .. ':' .. pr, unit)
  end
  redis.call('SADD', 's:' .. S .. ':units', unit)
  return { 'OK', tostring(seq) }
end)

-- ns_unit_eval: transitions unit to landable. Landed units are terminal and never re-enter (L7).
redis.register_function('ns_unit_eval', function(keys, args)
  local S, unit, repo, base, tier = args[1], args[2], args[3], args[4], tonumber(args[5] or 0)
  local ukey = 's:' .. S .. ':u:' .. unit
  local st = redis.call('HGET', ukey, 'state')
  if st == 'landed' then
    return { 'REFUSED', 'landed' }
  end
  redis.call('HSET', ukey, 'state', 'landable')
  local seq = tonumber(redis.call('HGET', ukey, 'seq') or 0)
  local score = tier * 1000000000000 + seq
  redis.call('ZADD', 's:' .. S .. ':landable:' .. repo .. ':' .. base, score, unit)
  return { 'OK', tostring(score) }
end)

-- ns_hold: records an objection or hold. If unit is landing or landed, post_land=1 and
-- follow-up tasks are queued on q:<author> and q:<holder> (2.3 / L23 / L28).
redis.register_function('ns_hold', function(keys, args)
  local S, unit, holder, head, kind, reason = args[1], args[2], args[3], args[4], args[5], args[6]
  local url, files, done_when, origin = args[7], args[8], args[9], args[10]
  local seq = redis.call('INCR', 'rec:seq')
  local ukey = 's:' .. S .. ':u:' .. unit
  local ustate = redis.call('HGET', ukey, 'state') or ''
  local author = redis.call('HGET', ukey, 'author') or ''
  local post_land = '0'
  local now = land_now_ms()

  if ustate == 'landing' or ustate == 'landed' then
    post_land = '1'
    if author ~= '' then
      redis.call('XADD', 'q:' .. author, '*', 'kind', 'follow-up', 'unit', unit, 'holder', holder, 'head', head, 'post_land', '1', 'reason', reason or '')
    end
    redis.call('XADD', 'q:' .. holder, '*', 'kind', 'follow-up', 'unit', unit, 'author', author, 'head', head, 'post_land', '1', 'reason', reason or '')
  else
    redis.call('HINCRBY', ukey, 'holds_open', 1)
  end

  local hkey = 's:' .. S .. ':hold:' .. unit .. ':' .. holder
  redis.call('HSET', hkey,
    'seq', tostring(seq),
    'head', head,
    'kind', kind or '',
    'reason', reason or '',
    'url', url or '',
    'files', files or '',
    'done_when', done_when or '',
    'origin', origin or '',
    'post_land', post_land,
    'at', tostring(now)
  )
  return { 'OK', tostring(seq), post_land }
end)

-- ns_release: releases a hold. Decrements holds_open only when post_land was 0.
redis.register_function('ns_release', function(keys, args)
  local S, unit, holder, released_by, release_kind, release_reason, release_url = args[1], args[2], args[3], args[4], args[5], args[6], args[7]
  local seq = redis.call('INCR', 'rec:seq')
  local hkey = 's:' .. S .. ':hold:' .. unit .. ':' .. holder
  local held = redis.call('HMGET', hkey, 'released_by', 'post_land')
  if not held[1] or held[1] == '' then
    local post_land = held[2] or '0'
    if post_land ~= '1' then
      local h = redis.call('HINCRBY', 's:' .. S .. ':u:' .. unit, 'holds_open', -1)
      if h < 0 then redis.call('HSET', 's:' .. S .. ':u:' .. unit, 'holds_open', 0) end
    end
    local now = land_now_ms()
    redis.call('HSET', hkey,
      'released_by', released_by,
      'release_kind', release_kind or '',
      'release_reason', release_reason or '',
      'release_url', release_url or '',
      'released_at', tostring(now),
      'release_seq', tostring(seq)
    )
  end
  return { 'OK', tostring(seq) }
end)

-- ns_read: records a typed read/approval.
redis.register_function('ns_read', function(keys, args)
  local S, unit, who, head, verdict, score, kind, files, done_when = args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9]
  local seq = redis.call('INCR', 'rec:seq')
  local rkey = 's:' .. S .. ':read:' .. unit .. ':' .. who
  local now = land_now_ms()
  redis.call('HSET', rkey,
    'seq', tostring(seq),
    'head', head,
    'verdict', verdict,
    'score', score or '',
    'kind', kind or '',
    'files', files or '',
    'done_when', done_when or '',
    'at', tostring(now)
  )
  return { 'OK', tostring(seq) }
end)

-- ns_policy_set: writes base policy record.
redis.register_function('ns_policy_set', function(keys, args)
  local repo, base, policy_id, required_set_id, runner_id = args[1], args[2], args[3], args[4], args[5]
  local pkey = 'land:' .. repo .. ':' .. base .. ':policy'
  local now = land_now_ms()
  redis.call('HSET', pkey,
    'policy_id', policy_id,
    'required_set_id', required_set_id,
    'runner_id', runner_id,
    'at', tostring(now)
  )
  return 'OK'
end)

-- ns_writer: bumps writer gen and sets owner (old-loop or nova-sprint).
redis.register_function('ns_writer', function(keys, args)
  local repo, base, to_owner, by = args[1], args[2], args[3], args[4]
  local wkey = 'land:' .. repo .. ':' .. base .. ':writer'
  local gen = redis.call('INCR', 'land:' .. repo .. ':' .. base .. ':writer:seq')
  local now = land_now_ms()
  redis.call('HSET', wkey,
    'gen', tostring(gen),
    'owner', to_owner,
    'since', tostring(now),
    'by', by or ''
  )
  return { 'OK', tostring(gen) }
end)

-- ns_batch_plan: checks fences, membership, landed status, chain limit;
-- writes batch, sets members batched, enqueues gate, writes PLAN event.
redis.register_function('ns_batch_plan', function(keys, args)
  local S, repo, base, batch_id, lease_val, members_csv, paths_csv, class, from_tip, input_id =
    args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10]

  -- 1. Writer and lease checks
  local wkey = 'land:' .. repo .. ':' .. base .. ':writer'
  local writer = redis.call('HMGET', wkey, 'gen', 'owner')
  if writer[2] ~= 'nova-sprint' then
    return { 'REFUSED', 'writer owner not nova-sprint' }
  end
  local lkey = 'land:' .. repo .. ':' .. base .. ':lease'
  local lease = redis.call('GET', lkey)
  if lease ~= lease_val then
    return { 'REFUSED', 'lease mismatch' }
  end
  local gen = string.match(lease_val or '', '^([^:]+):')
  if gen ~= writer[1] then
    return { 'REFUSED', 'lease gen mismatch' }
  end

  -- 2. Chain limit check (chain_max starts at 4)
  local chain_key = 'land:' .. repo .. ':' .. base .. ':chain'
  local chain_len = redis.call('ZCARD', chain_key)
  if chain_len >= 4 then
    return { 'REFUSED', 'chain_max' }
  end

  -- 3. Parse and check members
  if not members_csv or members_csv == '' then
    return { 'REFUSED', 'no members' }
  end

  local members = {}
  for m in string.gmatch(members_csv, '[^,]+') do
    local unit, head = string.match(m, '^([^@]+)@(.+)$')
    if not unit or unit == '' or not head or head == '' then
      return { 'REFUSED', 'member=' .. m .. ' no unit' }
    end
    -- Refuse if already landed (L7)
    if redis.call('EXISTS', 'landed:' .. repo .. ':' .. unit .. ':' .. head) == 1 then
      return { 'REFUSED', 'member=' .. m .. ' already landed' }
    end
    local ukey = 's:' .. S .. ':u:' .. unit
    local udata = redis.call('HMGET', ukey, 'state', 'batch', 'landed_head')
    if udata[1] == 'landed' or udata[3] == head then
      return { 'REFUSED', 'member=' .. m .. ' already landed' }
    end
    if udata[2] and udata[2] ~= '' and udata[1] == 'batched' then
      return { 'REFUSED', 'member=' .. unit .. ' already batched in ' .. udata[2] }
    end
    if udata[1] ~= 'landable' then
      return { 'REFUSED', 'member=' .. unit .. ' not landable' }
    end
    table.insert(members, { unit = unit, head = head })
  end

  -- 4. Receipt reuse by input identity (spec 5.5, control L31)
  if input_id and input_id ~= '' then
    local prev_rkey = redis.call('GET', 'land:' .. repo .. ':receipt_by_input:' .. input_id)
    if prev_rkey and redis.call('EXISTS', prev_rkey) == 1 then
      local rdata = redis.call('HMGET', prev_rkey, 'verdict', 'train_head', 'train_tree')
      if rdata[1] == 'GREEN' then
        local seq = redis.call('INCR', 'land:' .. repo .. ':' .. base .. ':batch:seq')
        local now = land_now_ms()
        local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
        redis.call('HSET', bkey,
          'seq', tostring(seq),
          'parent', '',
          'from_tip', from_tip or '',
          'members', members_csv,
          'paths', paths_csv or '',
          'class', class or 'go',
          'state', 'green',
          'attempt', '1',
          'token', '0',
          'input_id', input_id,
          'receipt', prev_rkey,
          'train_head', rdata[2] or '',
          'train_tree', rdata[3] or '',
          'created_at', tostring(now)
        )
        for _, m in ipairs(members) do
          local ukey = 's:' .. S .. ':u:' .. m.unit
          redis.call('HSET', ukey, 'state', 'batched', 'batch', batch_id)
          redis.call('ZREM', 's:' .. S .. ':landable:' .. repo .. ':' .. base, m.unit)
        end
        redis.call('ZADD', chain_key, seq, batch_id)
        redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
          'event', 'REUSE',
          'repo', repo,
          'base', base,
          'batch', batch_id,
          'receipt', prev_rkey,
          'at', tostring(now)
        )
        return { 'REUSE', prev_rkey }
      end
    end
  end

  -- 5. Mint token and seq
  local seq = redis.call('INCR', 'land:' .. repo .. ':' .. base .. ':batch:seq')
  local token = redis.call('INCR', 'land:' .. repo .. ':tok')
  local now = land_now_ms()
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id

  redis.call('HSET', bkey,
    'seq', tostring(seq),
    'parent', '',
    'from_tip', from_tip or '',
    'members', members_csv,
    'paths', paths_csv or '',
    'class', class or 'go',
    'state', 'queued',
    'attempt', '1',
    'token', tostring(token),
    'input_id', input_id or '',
    'created_at', tostring(now)
  )

  for _, m in ipairs(members) do
    local ukey = 's:' .. S .. ':u:' .. m.unit
    redis.call('HSET', ukey, 'state', 'batched', 'batch', batch_id)
    redis.call('ZREM', 's:' .. S .. ':landable:' .. repo .. ':' .. base, m.unit)
  end

  redis.call('ZADD', chain_key, seq, batch_id)
  local entry_id = redis.call('XADD', 'land:' .. repo .. ':gates', '*',
    'base', base,
    'batch', batch_id,
    'attempt', '1',
    'token', tostring(token)
  )
  redis.call('HSET', bkey, 'entry_id', entry_id)

  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'PLAN',
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'seq', tostring(seq),
    'token', tostring(token),
    'at', tostring(now)
  )

  return { 'OK', tostring(token), entry_id }
end)

-- ns_gate_take: atomic budget debit, delivery, and claim (spec 5.2, control L2/L2c).
-- args: repo, bench, slot, class[, req_cpu, req_mem]
redis.register_function('ns_gate_take', function(keys, args)
  local repo = args[1]
  local bench = args[2]
  local slot = args[3]
  local class = args[4]
  local req_cpu_arg = args[5]
  local req_mem_arg = args[6]

  local machine = redis.call('HGET', 'bench:' .. bench .. ':desired', 'machine')
  if not machine or machine == '' then
    machine = redis.call('HGET', 'bench:' .. bench .. ':land', 'machine')
  end
  if not machine or machine == '' then
    machine = bench
  end

  local consumer = 'land:' .. bench .. ':' .. slot
  local req_cpu = tonumber(req_cpu_arg or '0') or 0
  local req_mem = tonumber(req_mem_arg or '0') or 0
  if req_cpu <= 0 then
    local c = redis.call('HGET', 'bench:' .. bench .. ':land', 'cores_' .. class)
    if not c or c == '' then c = redis.call('HGET', 'bench:' .. bench .. ':land', 'cores_p95') end
    req_cpu = tonumber(c or '0') or 2000
  end
  if req_mem <= 0 then
    local m = redis.call('HGET', 'bench:' .. bench .. ':land', 'mem_' .. class)
    if not m or m == '' then m = redis.call('HGET', 'bench:' .. bench .. ':land', 'mem_p95') end
    req_mem = tonumber(m or '0') or 4096
  end

  -- 1. Budget debit take (atomic, refuses with NOBUDGET before reading stream, spec 5.2, L2c)
  local b_res = cap_budget_take(nil, { machine, consumer, tostring(req_cpu), tostring(req_mem), '30000', '', 'land' })
  if b_res[1] == 'NOBUDGET' then
    return b_res
  end

  -- 2. Read one entry from gates stream without blocking
  local gkey = 'land:' .. repo .. ':gates'
  pcall(redis.call, 'XGROUP', 'CREATE', gkey, 'workers', '0', 'MKSTREAM')
  local entries = redis.call('XREADGROUP', 'GROUP', 'workers', bench .. '/' .. slot, 'COUNT', 1, 'STREAMS', gkey, '>')

  local entry_found = false
  local entry_id, base, batch_id, attempt, token
  if entries and #entries > 0 and entries[1][2] and #entries[1][2] > 0 then
    local item = entries[1][2][1]
    entry_id = item[1]
    local fields = item[2]
    local fmap = {}
    for i = 1, #fields, 2 do
      fmap[fields[i]] = fields[i+1]
    end
    base = fmap['base']
    batch_id = fmap['batch']
    attempt = fmap['attempt']
    token = fmap['token']
    entry_found = true
  end

  if not entry_found then
    -- No work waiting: return debit immediately, leaving zero leaked debits (L2c)
    cap_budget_give(nil, { machine, consumer, '', 'confirmed' })
    return { 'NODATA' }
  end

  -- 3. Check batch state
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
  local b = redis.call('HMGET', bkey, 'attempt', 'token', 'state')
  if not b[1] or b[3] == 'void' or b[3] ~= 'queued' or b[1] ~= tostring(attempt) or b[2] ~= tostring(token) then
    -- VOID or STALE: acks the entry and gives debit back in the same call (spec 5.2, control L2)
    redis.call('XACK', gkey, 'workers', entry_id)
    cap_budget_give(nil, { machine, consumer, '', 'confirmed' })
    local reason = 'STALE'
    if b[3] == 'void' then reason = 'VOID' end
    return { reason, batch_id, tostring(attempt), tostring(token) }
  end

  -- 4. Claim the batch
  local now = land_now_ms()
  redis.call('HSET', bkey,
    'state', 'gating',
    'bench', bench,
    'slot', slot,
    'claimed_at', tostring(now),
    'entry_id', entry_id
  )
  redis.call('SET', 'worker:' .. bench .. ':' .. slot, batch_id .. ':' .. attempt .. ':' .. token, 'PX', 15000)
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'CLAIM',
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'attempt', tostring(attempt),
    'token', tostring(token),
    'bench', bench,
    'slot', slot,
    'at', tostring(now)
  )
  return { 'OK', base, batch_id, tostring(attempt), tostring(token), entry_id }
end)

-- ns_gate_claim: claims a queued gate for one worker slot.
redis.register_function('ns_gate_claim', function(keys, args)
  local repo, base, batch_id, attempt, token, bench, slot =
    args[1], args[2], args[3], args[4], args[5], args[6], args[7]
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
  local b = redis.call('HMGET', bkey, 'attempt', 'token', 'state')
  if not b[1] then return 'NOTFOUND' end
  if b[1] ~= tostring(attempt) or b[2] ~= tostring(token) then return 'STALE' end
  if b[3] ~= 'queued' then return 'STALE' end

  local now = land_now_ms()
  redis.call('HSET', bkey, 'state', 'gating', 'bench', bench, 'slot', slot, 'claimed_at', tostring(now))
  redis.call('SET', 'worker:' .. bench .. ':' .. slot, batch_id .. ':' .. attempt .. ':' .. token, 'PX', 15000)
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'CLAIM',
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'attempt', tostring(attempt),
    'token', tostring(token),
    'bench', bench,
    'slot', slot,
    'at', tostring(now)
  )
  return 'OK'
end)

-- ns_gate_receipt: writes write-once receipt, moves batch state, and XACKs gate entry.
redis.register_function('ns_gate_receipt', function(keys, args)
  local repo, base, batch_id, attempt, token, verdict = args[1], args[2], args[3], args[4], args[5], args[6]
  local bench, worker, train_head, train_tree = args[7], args[8], args[9], args[10]
  local input_id, selection, steps = args[11], args[12], args[13]
  local failing, flaky_rerun, core_s = args[14], args[15], args[16]

  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
  local b = redis.call('HMGET', bkey, 'attempt', 'token', 'state', 'entry_id', 'from_tip', 'slot')
  if not b[1] then return 'NOTFOUND' end
  if b[1] ~= tostring(attempt) or b[2] ~= tostring(token) then return 'STALE' end

  local rkey = 'land:' .. repo .. ':receipt:' .. batch_id .. ':' .. attempt
  if redis.call('EXISTS', rkey) == 1 then
    if b[4] and b[4] ~= '' then
      redis.call('XACK', 'land:' .. repo .. ':gates', 'workers', b[4])
    end
    return 'ALREADY'
  end

  local now = land_now_ms()
  redis.call('HSET', rkey,
    'bench', bench or '',
    'worker', worker or '',
    'token', tostring(token),
    'from_tip', b[5] or '',
    'train_head', train_head or '',
    'train_tree', train_tree or '',
    'input_id', input_id or '',
    'selection', selection or '',
    'steps', steps or '',
    'verdict', verdict,
    'failing', failing or '',
    'flaky_rerun', flaky_rerun or '',
    'core_s', core_s or '0',
    'at', tostring(now)
  )

  local new_st = string.lower(verdict)
  redis.call('HSET', bkey, 'state', new_st, 'receipt', rkey, 'train_head', train_head or '', 'train_tree', train_tree or '')

  if b[4] and b[4] ~= '' then
    redis.call('XACK', 'land:' .. repo .. ':gates', 'workers', b[4])
  end

  -- Return budget debit for this slot
  local slot = b[6]
  if bench and bench ~= '' and slot and slot ~= '' then
    local m = redis.call('HGET', 'bench:' .. bench .. ':desired', 'machine')
    if not m or m == '' then m = redis.call('HGET', 'bench:' .. bench .. ':land', 'machine') end
    if not m or m == '' then m = bench end
    cap_budget_give(nil, { m, 'land:' .. bench .. ':' .. slot, '', 'confirmed' })
  end

  -- Index input_id for GREEN receipt reuse (spec 5.5, L31)
  if verdict == 'GREEN' and input_id and input_id ~= '' then
    redis.call('SET', 'land:' .. repo .. ':receipt_by_input:' .. input_id, rkey)
  end

  -- Write gid receipt through gate_receipt_write if gid is provided (spec 3.7, 5.5)
  local gid, kind, head, base_sha, req_set_id, policy_id, runner_id, pkg, test =
    args[17], args[18], args[19], args[20], args[21], args[22], args[23], args[24], args[25]
  if gid and gid ~= '' then
    gate_receipt_write(repo, head or '', gid, verdict, kind or 'single', base, base_sha or b[5] or '', req_set_id or '', policy_id or '', runner_id or '', rkey, bench, pkg or '', test or '', now)
  end

  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'GATE',
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'attempt', tostring(attempt),
    'verdict', verdict,
    'at', tostring(now)
  )
  return 'OK'
end)

-- ns_requeue: re-queues an unacknowledged/dead gate attempt.
redis.register_function('ns_requeue', function(keys, args)
  local repo, base, batch_id = args[1], args[2], args[3]
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
  local attempt = redis.call('HINCRBY', bkey, 'attempt', 1)
  local token = redis.call('INCR', 'land:' .. repo .. ':tok')
  local old_entry_id = redis.call('HGET', bkey, 'entry_id')
  if old_entry_id and old_entry_id ~= '' then
    redis.call('XACK', 'land:' .. repo .. ':gates', 'workers', old_entry_id)
  end
  local new_entry_id = redis.call('XADD', 'land:' .. repo .. ':gates', '*',
    'base', base,
    'batch', batch_id,
    'attempt', tostring(attempt),
    'token', tostring(token)
  )
  local now = land_now_ms()
  redis.call('HSET', bkey, 'state', 'queued', 'token', tostring(token), 'entry_id', new_entry_id)
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'REQUEUE',
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'attempt', tostring(attempt),
    'token', tostring(token),
    'at', tostring(now)
  )
  return { 'OK', tostring(token), new_entry_id }
end)

-- ns_batch_void: marks batch void, removes from chain, returns members to landable.
redis.register_function('ns_batch_void', function(keys, args)
  local S, repo, base, batch_id, reason = args[1], args[2], args[3], args[4], args[5]
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
  local now = land_now_ms()
  redis.call('HSET', bkey, 'state', 'void', 'reason', reason or '')
  redis.call('ZREM', 'land:' .. repo .. ':' .. base .. ':chain', batch_id)

  local members_csv = redis.call('HGET', bkey, 'members') or ''
  for m in string.gmatch(members_csv, '[^,]+') do
    local unit = string.match(m, '^([^@]+)@')
    if unit then
      local ukey = 's:' .. S .. ':u:' .. unit
      redis.call('HSET', ukey, 'state', 'landable', 'batch', '')
      local seq = tonumber(redis.call('HGET', ukey, 'seq') or 0)
      redis.call('ZADD', 's:' .. S .. ':landable:' .. repo .. ':' .. base, seq, unit)
    end
  end

  local active_pub = redis.call('GET', 'land:' .. repo .. ':' .. base .. ':pub:active')
  if active_pub == batch_id then
    redis.call('DEL', 'land:' .. repo .. ':' .. base .. ':pub:' .. batch_id)
    redis.call('DEL', 'land:' .. repo .. ':' .. base .. ':pub:active')
  end

  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'VOID',
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'reason', reason or '',
    'at', tostring(now)
  )
  return 'OK'
end)

-- ns_land_intent: the linearization point (2.3).
-- Checks writer & lease, no unresolved pub:*, green receipt on concrete tip,
-- all members green at gated head with holds_open=0, current policy, inbound freshness.
-- Stamped with rec_seq_cut = rec:seq. Moves members to landing.
redis.register_function('ns_land_intent', function(keys, args)
  local S, repo, base, batch_id, lease_val = args[1], args[2], args[3], args[4], args[5]

  -- 1. Writer and lease checks
  local wkey = 'land:' .. repo .. ':' .. base .. ':writer'
  local writer = redis.call('HMGET', wkey, 'gen', 'owner')
  if writer[2] ~= 'nova-sprint' then
    return { 'REFUSED', 'writer owner not nova-sprint' }
  end
  local lkey = 'land:' .. repo .. ':' .. base .. ':lease'
  local lease = redis.call('GET', lkey)
  if lease ~= lease_val then
    return { 'REFUSED', 'lease mismatch' }
  end
  local gen = string.match(lease_val or '', '^([^:]+):')
  if gen ~= writer[1] then
    return { 'REFUSED', 'lease gen mismatch' }
  end

  -- 2. No unresolved pub:* on this base
  local active_pub = redis.call('GET', 'land:' .. repo .. ':' .. base .. ':pub:active')
  if active_pub and active_pub ~= '' and active_pub ~= batch_id then
    local ap_st = redis.call('HGET', 'land:' .. repo .. ':' .. base .. ':pub:' .. active_pub, 'state')
    if ap_st and ap_st ~= 'dead' then
      return { 'REFUSED', 'unresolved pub ' .. active_pub }
    end
  end

  -- 3. Batch receipt is GREEN and from_tip equals tip record
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
  local bstate = redis.call('HGET', bkey, 'state')
  if bstate ~= 'green' then
    return { 'REFUSED', 'batch not green' }
  end
  local bfrom_tip = redis.call('HGET', bkey, 'from_tip')
  local tip_sha = redis.call('HGET', 'land:' .. repo .. ':' .. base .. ':tip', 'sha')
  if bfrom_tip ~= tip_sha then
    return { 'REFUSED', 'from_tip mismatch' }
  end

  -- 4. Every member is green at the gated head with holds_open = 0
  local members_csv = redis.call('HGET', bkey, 'members') or ''
  local members = {}
  for m in string.gmatch(members_csv, '[^,]+') do
    local unit, head = string.match(m, '^([^@]+)@(.+)$')
    if not unit or not head then
      return { 'REFUSED', 'invalid member ' .. m }
    end
    local ukey = 's:' .. S .. ':u:' .. unit
    local udata = redis.call('HMGET', ukey, 'head', 'holds_open')
    if udata[1] ~= head then
      return { 'REFUSED', 'head mismatch on ' .. unit }
    end
    local holds_open = tonumber(udata[2] or '0')
    if holds_open > 0 then
      return { 'REFUSED', 'hold on ' .. unit }
    end
    table.insert(members, { unit = unit, head = head })
  end

  -- 5. Receipt's policy_id equals current policy
  local cur_pol = redis.call('HGET', 'land:' .. repo .. ':' .. base .. ':policy', 'policy_id')
  local bpol = redis.call('HGET', bkey, 'policy_id') or cur_pol
  if cur_pol and bpol and cur_pol ~= bpol then
    return { 'REFUSED', 'policy mismatch' }
  end

  -- 6. Inbound consumer freshness
  local cons = redis.call('HMGET', 'ev:github:consumer:land', 'pending', 'at')
  if cons[1] and cons[1] ~= '' then
    local pending = tonumber(cons[1] or '0')
    local at = tonumber(cons[2] or '0')
    local now_num = tonumber(land_now_ms())
    local age_s = (now_num / 1000) - (at > 100000000000 and (at / 1000) or at)
    if pending > 0 or age_s > 10 then
      return { 'REFUSED', 'inbound-stale' }
    end
  end

  -- All checks pass: the cut
  local seq_cut = redis.call('INCR', 'rec:seq')
  local now = land_now_ms()
  local train_head = redis.call('HGET', bkey, 'train_head') or ''
  local train_tree = redis.call('HGET', bkey, 'train_tree') or ''
  local pub_key = 'land:' .. repo .. ':' .. base .. ':pub:' .. batch_id

  redis.call('HSET', pub_key,
    'state', 'intent',
    'from_tip', bfrom_tip,
    'train_head', train_head,
    'train_tree', train_tree,
    'gen', gen,
    'policy_id', cur_pol or '',
    'rec_seq_cut', tostring(seq_cut),
    'at_intent', tostring(now)
  )
  redis.call('SET', 'land:' .. repo .. ':' .. base .. ':pub:active', batch_id)

  for _, m in ipairs(members) do
    redis.call('HSET', 's:' .. S .. ':u:' .. m.unit, 'state', 'landing')
  end

  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'INTENT',
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'rec_seq_cut', tostring(seq_cut),
    'at', tostring(now)
  )

  return { 'OK', tostring(seq_cut) }
end)

-- ns_pub_state: updates publisher state for one intent (pushed, verified, dead).
redis.register_function('ns_pub_state', function(keys, args)
  local repo, base, batch_id, state = args[1], args[2], args[3], args[4]
  local pub_key = 'land:' .. repo .. ':' .. base .. ':pub:' .. batch_id
  local now = land_now_ms()
  redis.call('HSET', pub_key, 'state', state, 'at_' .. state, tostring(now))
  if state == 'dead' then
    redis.call('DEL', 'land:' .. repo .. ':' .. base .. ':pub:active')
  end
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', string.upper(state),
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'at', tostring(now)
  )
  return 'OK'
end)

-- ns_land: final landing transition. Checks writer gen and lease, writes landed:<repo>:<unit>:<head>,
-- advances tip, clears pub, and emits exactly one LANDED event (7.1, L6, L28).
redis.register_function('ns_land', function(keys, args)
  local S, repo, base, batch_id, lease_val, train_head, merge_shas_csv, land_cycle =
    args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8]

  -- 1. Lease and writer check
  local wkey = 'land:' .. repo .. ':' .. base .. ':writer'
  local writer = redis.call('HMGET', wkey, 'gen', 'owner')
  if writer[2] ~= 'nova-sprint' then
    return 'STALE'
  end
  local lkey = 'land:' .. repo .. ':' .. base .. ':lease'
  local lease = redis.call('GET', lkey)
  if lease ~= lease_val then
    return 'STALE'
  end
  local gen = string.match(lease_val or '', '^([^:]+):')
  if gen ~= writer[1] then
    return 'STALE'
  end

  -- 2. Double landing check (L6)
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
  local bstate = redis.call('HGET', bkey, 'state')
  if bstate == 'landed' then
    return 'ALREADY'
  end

  local members_csv = redis.call('HGET', bkey, 'members') or ''
  local members = {}
  for m in string.gmatch(members_csv, '[^,]+') do
    local unit, head = string.match(m, '^([^@]+)@(.+)$')
    if unit and head then
      if redis.call('EXISTS', 'landed:' .. repo .. ':' .. unit .. ':' .. head) == 1 then
        return 'ALREADY'
      end
      table.insert(members, { unit = unit, head = head })
    end
  end

  local merge_shas = {}
  if merge_shas_csv and merge_shas_csv ~= '' then
    for s in string.gmatch(merge_shas_csv, '[^,]+') do
      table.insert(merge_shas, s)
    end
  end

  local now = land_now_ms()
  local receipt = redis.call('HGET', bkey, 'receipt') or ''
  redis.call('HSET', bkey, 'state', 'landed', 'landed_at', tostring(now))

  for i, m in ipairs(members) do
    local msha = merge_shas[i] or train_head
    redis.call('SET', 'landed:' .. repo .. ':' .. m.unit .. ':' .. m.head, msha .. ' ' .. batch_id .. ' ' .. receipt)
    redis.call('HSET', 's:' .. S .. ':u:' .. m.unit, 'state', 'landed', 'merge_sha', msha, 'landed_head', m.head)
    redis.call('ZREM', 's:' .. S .. ':landable:' .. repo .. ':' .. base, m.unit)
  end

  redis.call('HSET', 'land:' .. repo .. ':' .. base .. ':tip', 'sha', train_head, 'at', tostring(now), 'by', 'publisher')
  redis.call('ZREM', 'land:' .. repo .. ':' .. base .. ':chain', batch_id)
  redis.call('DEL', 'land:' .. repo .. ':' .. base .. ':pub:' .. batch_id)
  redis.call('DEL', 'land:' .. repo .. ':' .. base .. ':pub:active')

  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'LANDED',
    'repo', repo,
    'base', base,
    'batch', batch_id,
    'train_head', train_head,
    'L', land_cycle or '0',
    'at', tostring(now)
  )
  return 'OK'
end)
