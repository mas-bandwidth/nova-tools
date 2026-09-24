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

-- land_write_once: HSETNX one write-once reap field (nova-tools#3091). An empty or
-- absent value writes nothing. A stored value that differs is kept, and the offer is
-- HSETNXed into s:<S>:unresolved as <unit>:<field>-changed:<seq>.
local function land_write_once(S, ukey, unit, seq, field, value, changed)
  if not value or value == '' then
    return
  end
  if redis.call('HSETNX', ukey, field, value) == 1 then
    return
  end
  if redis.call('HGET', ukey, field) ~= value then
    redis.call('HSETNX', 's:' .. S .. ':unresolved', unit .. ':' .. field .. '-changed:' .. tostring(seq), value)
    table.insert(changed, field .. '-changed')
  end
end

-- ns_unit_head: writes head and metadata for one unit, adds to s:<S>:units index,
-- and increments rec:seq.
-- nova-tools#3091: arg 15 card is the label of the card whose TASK: line names the
-- unit; arg 16 is that card's PATHS canonicalized by the Go caller (pr.CanonJSON),
-- empty when refused. On the create path (no unit hash before this call) it writes
-- the sentinels last_read_at, approve_head and merged_at present-empty, so a stored
-- value is never reset and a deleted field is never re-created. paths, card_type
-- and cut_at are write-once from an existing card hash; no card leaves them absent.
-- Reply { 'OK', seq } or { 'OK', seq, 'UNRESOLVED', '<field>-changed', ... }.
redis.register_function('ns_unit_head', function(keys, args)
  local S, unit, repo, base, branch, head, base_sha = args[1], args[2], args[3], args[4], args[5], args[6], args[7]
  local stack_parent, files, paths_hash, security, class = args[8], args[9], args[10], args[11], args[12]
  local pr, author = args[13], args[14]
  local card, paths_json = args[15], args[16]

  local seq = redis.call('INCR', 'rec:seq')
  local ukey = 's:' .. S .. ':u:' .. unit
  local created = redis.call('EXISTS', ukey) == 0
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
  if created then
    redis.call('HSET', ukey, 'last_read_at', '', 'approve_head', '', 'merged_at', '')
  end
  -- A mergeable word is the forge's answer at one head (ns_unit_mergeable):
  -- a head move leaves it UNKNOWN until the writer answers for the new head.
  local mh = redis.call('HGET', ukey, 'mergeable_head')
  if mh and mh ~= head then
    redis.call('HSET', ukey, 'mergeable', 'UNKNOWN', 'mergeable_head', head)
  end
  if pr and pr ~= '' and pr ~= '0' then
    redis.call('HSET', ukey, 'pr', pr)
    redis.call('SET', 's:' .. S .. ':prunit:' .. repo .. ':' .. pr, unit)
  end
  local changed = {}
  if card and card ~= '' then
    local ckey = 's:' .. S .. ':card:' .. card
    if redis.call('EXISTS', ckey) == 1 then
      local cv = redis.call('HMGET', ckey, 'card_type', 'cut_at')
      land_write_once(S, ukey, unit, seq, 'paths', paths_json, changed)
      land_write_once(S, ukey, unit, seq, 'card_type', cv[1], changed)
      land_write_once(S, ukey, unit, seq, 'cut_at', cv[2], changed)
    end
  end
  redis.call('SADD', 's:' .. S .. ':units', unit)
  if #changed > 0 then
    return { 'OK', tostring(seq), 'UNRESOLVED', unpack(changed) }
  end
  return { 'OK', tostring(seq) }
end)

-- ns_unit_mergeable: the one writer of a unit's mergeable word (nova-tools
-- #3092 rev 7: the hold router sends a ci/self note to update-<n>-<sha8> on
-- CONFLICTING). args = S, unit, head, word. The word is the forge's
-- (land.Forge): MERGEABLE, CONFLICTING or UNKNOWN; anything else is INVALID.
-- The write is fenced on the unit head: a word observed at another head is
-- STALE and writes nothing, and ns_unit_head resets the word to UNKNOWN on a
-- head move. Reply { 'OK', seq } | { 'SAME' } | { 'NOUNIT' } |
-- { 'STALE', <unit head> } | { 'INVALID', <word> }.
redis.register_function('ns_unit_mergeable', function(keys, args)
  local S, unit, head, word = args[1], args[2], args[3], args[4]
  if word ~= 'MERGEABLE' and word ~= 'CONFLICTING' and word ~= 'UNKNOWN' then
    return { 'INVALID', word or '' }
  end
  local ukey = 's:' .. S .. ':u:' .. unit
  if redis.call('EXISTS', ukey) == 0 then
    return { 'NOUNIT' }
  end
  local u = redis.call('HMGET', ukey, 'head', 'mergeable', 'mergeable_head')
  if (u[1] or '') ~= head or head == '' then
    return { 'STALE', u[1] or '' }
  end
  if u[2] == word and u[3] == head then
    return { 'SAME' }
  end
  local seq = redis.call('INCR', 'rec:seq')
  redis.call('HSET', ukey, 'mergeable', word, 'mergeable_head', head,
    'mergeable_at', tostring(land_now_ms()), 'mergeable_seq', tostring(seq))
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
-- nova-tools#3091: a counted read (verdict APPROVE or HOLD, who not jev, kind not ci)
-- stamps the unit's last_read_at (Redis TIME seconds; replaces empty, then max) and a
-- counted APPROVE its approve_head/approve_seq: an APPROVE at the unit's current head
-- always wins; one at another head replaces only an empty value or one that is not the
-- current head. A field that is absent stays absent. No unit hash: the read record is
-- written, no unit field is, and the reply is { 'OK', seq, 'NOUNIT' }.
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
  local ukey = 's:' .. S .. ':u:' .. unit
  if redis.call('EXISTS', ukey) == 0 then
    return { 'OK', tostring(seq), 'NOUNIT' }
  end
  local v = string.upper(verdict or '')
  local counted = (v == 'APPROVE' or v == 'HOLD') and string.lower(who or '') ~= 'jev' and (kind or '') ~= 'ci'
  if counted then
    local u = redis.call('HMGET', ukey, 'last_read_at', 'approve_head', 'head')
    local now_s = math.floor(tonumber(now) / 1000)
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

  -- 4. Mint token and seq
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
  local b = redis.call('HMGET', bkey, 'attempt', 'token', 'state', 'entry_id', 'from_tip')
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
    local ukey = 's:' .. S .. ':u:' .. m.unit
    if redis.call('HGET', ukey, 'merged_at') == '' then
      -- nova-tools#3091: merged_at (Redis TIME seconds) only while still empty.
      redis.call('HSET', ukey, 'state', 'landed', 'merge_sha', msha, 'landed_head', m.head,
        'merged_at', string.format('%d', math.floor(tonumber(now) / 1000)))
    else
      redis.call('HSET', ukey, 'state', 'landed', 'merge_sha', msha, 'landed_head', m.head)
    end
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
