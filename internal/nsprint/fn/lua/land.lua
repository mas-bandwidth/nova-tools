-- land.lua is one do-block: the whole nova_sprint library is one Lua main
-- function, which holds at most 200 locals (luaY MAXVARS). The six helpers
-- below are used only here, so their scope ends with this file and they do
-- not count against the verbs loaded after it (dev reached 206 at #3487).
do

-- land.lua: nova_sprint functions for the lander service (Issue #3139 rev 7).
-- Keys 2.2, fences and intent cut 2.3, events 2.2/7.7.
-- Every lua/ file shares one chunk, so locals carry a land_ prefix.

-- From ci.lua through NS (loader.go: each file is its own do-block).
local ci_sha256_hex, gate_receipt_write = NS.ci.sha256_hex, NS.ci.gate_receipt_write
-- cap_budget_take and cap_budget_give are capacity.lua's (it sorts before
-- this file), handed over through NS.capacity.
local cap_budget_take, cap_budget_give = NS.capacity.cap_budget_take, NS.capacity.cap_budget_give

local function land_now_ms()
  local t = redis.call('TIME')
  return string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
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
  if branch and branch ~= '' then
    -- #3139 B2: the branch index the evaluator's mirror reconcile and webhook
    -- heads resolve a ref through (refs/heads/<branch> -> unit), no scan.
    redis.call('SET', 's:' .. S .. ':branchunit:' .. repo .. ':' .. branch, unit)
  end
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
  if st == 'dropped' then
    local d = redis.call('HMGET', ukey, 'head', 'drop_head')
    if d[1] and d[1] == d[2] then
      return { 'REFUSED', 'dropped at head' }
    end
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
  -- Who may release (#3139 3.4, 3.5; arg 8 is the may-hold roster, csv):
  --   the holder itself, any kind (superseded is only ever the holder's own);
  --   repair-scoped by anyone, only for a hold that names a done_when test
  --     (the evaluator checks the five 3.5 conditions before it calls this);
  --   a login:<x> inbound hold by a friend's --releases record (3.4);
  --   otherwise only a may-hold reader, and only while friend:<holder>:down.
  if released_by ~= holder then
    local hk = 's:' .. S .. ':hold:' .. unit .. ':' .. holder
    if release_kind == 'superseded' then
      return { 'REFUSED', 'superseded only by the holder' }
    elseif release_kind == 'repair-scoped' then
      local dw = redis.call('HGET', hk, 'done_when')
      if not dw or dw == '' then
        return { 'REFUSED', 'repair-scoped needs done_when' }
      end
    elseif not string.find(holder, '^login:') then
      if redis.call('EXISTS', 'friend:' .. holder .. ':down') ~= 1 then
        return { 'REFUSED', 'holder not down' }
      end
      local may = false
      for r in string.gmatch(args[8] or '', '[^,%s]+') do
        if r == released_by then may = true end
      end
      if not may then
        return { 'REFUSED', 'releaser not may-hold' }
      end
    end
  end

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
  -- #3139 B2: the unit's readers index, so the evaluator counts reads and
  -- supersedes holds without a KEYS scan (2.2).
  redis.call('SADD', 's:' .. S .. ':readers:' .. unit, who)
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

-- ns_ci_single (#3139 B2, 3.3, L31b): queues the one ci single for a unit's
-- expected identity, once. args: repo, base, unit, head, gid. A receipt that
-- already exists returns HAVE; an identity already queued returns ALREADY and
-- writes nothing; otherwise the marker land:<repo>:ciq:<head>:<gid>, a token
-- from the gate fence counter and one land:<repo>:gates entry (attempt 1).
redis.register_function('ns_ci_single', function(keys, args)
  local repo, base, unit, head, gid = args[1], args[2], args[3], args[4], args[5]
  if redis.call('EXISTS', 'ci:' .. repo .. ':' .. head .. ':' .. gid) == 1 then
    return { 'HAVE', '' }
  end
  local mk = 'land:' .. repo .. ':ciq:' .. head .. ':' .. gid
  if not redis.call('SET', mk, unit, 'NX') then
    return { 'ALREADY', redis.call('GET', mk) or '' }
  end
  local token = redis.call('INCR', 'land:' .. repo .. ':tok')
  local now = land_now_ms()
  local id = redis.call('XADD', 'land:' .. repo .. ':gates', '*',
    'base', base, 'batch', 'ci:' .. gid, 'attempt', '1', 'token', tostring(token),
    'unit', unit, 'head', head, 'gid', gid, 'kind', 'single', 'priority', 'ci')
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'CI QUEUED', 'repo', repo, 'base', base, 'unit', unit, 'head', head, 'gid', gid, 'at', now)
  return { 'QUEUED', id, tostring(token) }
end)

-- ns_ref_seen (#3139 B2, 3.2, L32): the head of one ref as the evaluator saw it.
-- args: repo, ref, sha, source (delivery: a webhook named it and the mirror
-- was fetched; git: the reconcile read the mirror). A git read that finds the
-- ref moved since the last record, with no delivery naming the new sha,
-- writes INBOUND MISSED (event and land:<repo>:inbound missed count).
redis.register_function('ns_ref_seen', function(keys, args)
  local repo, ref, sha, source = args[1], args[2], args[3], args[4]
  local k = 'land:' .. repo .. ':ref:' .. ref
  local prev = redis.call('HGET', k, 'sha')
  local now = land_now_ms()
  redis.call('HSET', k, 'sha', sha, 'source', source, 'at', now)
  if not prev then
    return { 'NEW', '' }
  end
  if prev ~= sha and source == 'git' then
    redis.call('HINCRBY', 'land:' .. repo .. ':inbound', 'missed', 1)
    redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
      'event', 'INBOUND MISSED', 'repo', repo, 'ref', ref, 'sha', sha, 'prev', prev, 'at', now)
    return { 'MISSED', prev }
  end
  if prev ~= sha then
    return { 'MOVED', prev }
  end
  return { 'SEEN', prev }
end)

-- ns_inbound_beat (#3139 B2, 3.2): the land consumer's beat on
-- ev:github:consumer:land after each drain. args: last delivery id, pending.
redis.register_function('ns_inbound_beat', function(keys, args)
  local now = land_now_ms()
  redis.call('HSET', 'ev:github:consumer:land', 'last_id', args[1] or '', 'pending', args[2] or '0', 'at', now)
  return { 'OK', now }
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

-- ns_writer: the sole writer of land:<repo>:<base>:writer (gen, owner, since, by),
-- #3139 rev 7 section 10.2 (B0). args: repo, base, to, by, [inflight].
-- to '' or 'get' reads (gen 0, owner old-loop when unset); to old-loop|nova-sprint
-- bumps gen from land:<repo>:<base>:writer:seq and appends one WRITER event to
-- land:<repo>:events. Cutover to nova-sprint is REFUSED while the old loop's
-- inflight count (the inflight arg, or land:<repo>:<base>:inflight as a string,
-- set or zset) is nonzero. Rollback to old-loop is REFUSED (REFUSED pub <batch>) while
-- land:<repo>:<base>:pub:active names an intent not yet resolved (state other than dead),
-- per 10.2 "bumps gen after resolving any pub:* by 7.2": ns_pub_state dead or ns_land
-- clears it first.
redis.register_function('ns_writer', function(keys, args)
  local repo, base, to_owner, by, inflight_arg = args[1], args[2], args[3], args[4], args[5]
  if not repo or repo == '' or not base or base == '' then
    return redis.error_reply('ns_writer: repo and base are required')
  end
  local wkey = 'land:' .. repo .. ':' .. base .. ':writer'
  if not to_owner or to_owner == '' or to_owner == 'get' then
    local cur = redis.call('HMGET', wkey, 'gen', 'owner', 'since', 'by')
    return { 'OK', cur[1] or '0', cur[2] or 'old-loop', cur[3] or '', cur[4] or '' }
  end
  if to_owner ~= 'old-loop' and to_owner ~= 'nova-sprint' then
    return { 'INVALID', 'owner must be old-loop or nova-sprint' }
  end
  if to_owner == 'nova-sprint' then
    if inflight_arg and inflight_arg ~= '' then
      local inf = tonumber(inflight_arg) or 0
      if inf > 0 then
        return { 'REFUSED', 'inflight', tostring(inf) }
      end
    end
    local ikey = 'land:' .. repo .. ':' .. base .. ':inflight'
    local ktype = redis.call('TYPE', ikey)['ok']
    local inf = 0
    if ktype == 'string' then
      inf = tonumber(redis.call('GET', ikey)) or 0
    elseif ktype == 'set' then
      inf = redis.call('SCARD', ikey)
    elseif ktype == 'zset' then
      inf = redis.call('ZCARD', ikey)
    end
    if inf > 0 then
      return { 'REFUSED', 'inflight', tostring(inf) }
    end
  end
  if to_owner == 'old-loop' then
    local active_pub = redis.call('GET', 'land:' .. repo .. ':' .. base .. ':pub:active')
    if active_pub and active_pub ~= '' then
      local ap_st = redis.call('HGET', 'land:' .. repo .. ':' .. base .. ':pub:' .. active_pub, 'state')
      if ap_st ~= 'dead' then
        return { 'REFUSED', 'pub', active_pub }
      end
    end
  end
  local gen = redis.call('INCR', 'land:' .. repo .. ':' .. base .. ':writer:seq')
  local now = land_now_ms()
  redis.call('HSET', wkey,
    'gen', tostring(gen),
    'owner', to_owner,
    'since', tostring(now),
    'by', by or ''
  )
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'WRITER',
    'gen', tostring(gen),
    'owner', to_owner,
    'by', by or '',
    'at', tostring(now))
  return { 'OK', tostring(gen), to_owner, tostring(now), by or '' }
end)

-- land_storage_split: the five storage-split paths under docs/roadmaps/ (4.1, amendment 5802461060).
local function land_storage_split(p)
  if p == 'docs/roadmaps/nova-work.sexp' or p == 'docs/roadmaps/ingest-map.sexp' then return true end
  if string.match(p, '^docs/roadmaps/work/[^/]+%.sexp$') then return true end
  if string.sub(p, 1, 20) == 'docs/roadmaps/blobs/' then return true end
  return false
end

-- land_lease_refusal (2.3): the writer and lease check every batcher and publisher write runs
-- first. Returns nil when writer owner is nova-sprint, the lease key holds lease_val, and
-- lease_val's gen is the writer gen; otherwise the refusal reason, and the caller writes nothing.
local function land_lease_refusal(repo, base, lease_val)
  local writer = redis.call('HMGET', 'land:' .. repo .. ':' .. base .. ':writer', 'gen', 'owner')
  if writer[2] ~= 'nova-sprint' then
    return 'writer owner not nova-sprint'
  end
  local lease = redis.call('GET', 'land:' .. repo .. ':' .. base .. ':lease')
  if not lease_val or lease_val == '' or lease ~= lease_val then
    return 'lease mismatch'
  end
  local gen = string.match(lease_val, '^([^:]+):')
  if gen ~= writer[1] then
    return 'lease gen mismatch'
  end
  return nil
end

-- ns_batch_plan (4.4): in one call checks writer gen and lease, chain below chain_max, a unit id on
-- every member (L27), every member landable with an empty batch (L1, L7), PATHS disjoint (L3), one
-- class and the roadmap rule (L3b), alone units alone, stack parents earlier (L24). A failed check
-- writes nothing. Then writes the batch (parent, from_tip, input_id), sets members batched, appends
-- to the chain, XADDs the gate and a PLAN event. args: S repo base batch_id lease members_csv
-- paths_csv class from_tip input_id [parent] [chain_max]; an empty batch_id is minted as b<seq>.
redis.register_function('ns_batch_plan', function(keys, args)
  local S, repo, base, batch_id, lease_val, members_csv, paths_csv, class, from_tip, input_id =
    args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10]
  local parent = args[11] or ''
  local chain_max = tonumber(args[12] or '') or 4
  class = (class and class ~= '') and class or 'go'

  -- 1. Writer and lease checks
  local refusal = land_lease_refusal(repo, base, lease_val)
  if refusal then
    return { 'REFUSED', refusal }
  end

  -- 2. Chain limit check (chain_max starts at 4, 4.3)
  local chain_key = 'land:' .. repo .. ':' .. base .. ':chain'
  local chain_len = redis.call('ZCARD', chain_key)
  if chain_len >= chain_max then
    return { 'REFUSED', 'chain_max' }
  end
  if parent ~= '' and not redis.call('ZSCORE', chain_key, parent) then
    return { 'REFUSED', 'parent=' .. parent .. ' not in chain' }
  end

  -- 3. Parse and check members
  if not members_csv or members_csv == '' then
    return { 'REFUSED', 'no members' }
  end

  local units_key = 's:' .. S .. ':units'
  local members = {}
  local pos = {}
  for m in string.gmatch(members_csv, '[^,]+') do
    local unit, head = string.match(m, '^([^@]+)@(.+)$')
    if not unit or unit == '' or not head or head == '' or redis.call('SISMEMBER', units_key, unit) == 0 then
      return { 'REFUSED', 'member=' .. m .. ' no unit' }
    end
    -- Refuse if already landed (L7)
    if redis.call('EXISTS', 'landed:' .. repo .. ':' .. unit .. ':' .. head) == 1 then
      return { 'REFUSED', 'member=' .. m .. ' already landed' }
    end
    local ukey = 's:' .. S .. ':u:' .. unit
    local udata = redis.call('HMGET', ukey, 'state', 'batch', 'landed_head', 'files', 'class', 'alone', 'stack_parent')
    if udata[1] == 'landed' or udata[3] == head then
      return { 'REFUSED', 'member=' .. m .. ' already landed' }
    end
    if udata[2] and udata[2] ~= '' and udata[1] == 'batched' then
      return { 'REFUSED', 'member=' .. unit .. ' already batched in ' .. udata[2] }
    end
    if udata[1] ~= 'landable' then
      return { 'REFUSED', 'member=' .. unit .. ' not landable' }
    end
    if pos[unit] then
      return { 'REFUSED', 'member=' .. unit .. ' twice' }
    end
    table.insert(members, { unit = unit, head = head, files = udata[4] or '', class = udata[5] or '', alone = udata[6] or '', parent = udata[7] or '' })
    pos[unit] = #members
  end

  -- 4. Shape: one class, disjoint PATHS, the roadmap rule, alone units alone, stack parents earlier.
  local seen = {}
  local paths = {}
  for i, m in ipairs(members) do
    if m.class ~= '' and m.class ~= class then
      return { 'REFUSED', 'class=' .. m.unit .. ' ' .. m.class .. '!=' .. class }
    end
    if m.alone == '1' and #members > 1 then
      return { 'REFUSED', 'alone=' .. m.unit }
    end
    for p in string.gmatch(m.files, '[^,]+') do
      if seen[p] then
        return { 'REFUSED', 'overlap=' .. p }
      end
      seen[p] = m.unit
      table.insert(paths, p)
      if class == 'roadmap' then
        if string.sub(p, 1, 14) ~= 'docs/roadmaps/' then
          return { 'REFUSED', 'roadmap-class=' .. p }
        end
      elseif land_storage_split(p) then
        return { 'REFUSED', 'roadmap-path=' .. p }
      end
    end
    local sp = m.parent
    if sp ~= '' and sp ~= 'none' then
      local ok = false
      if pos[sp] and pos[sp] < i then
        ok = true
      elseif not pos[sp] then
        local pdata = redis.call('HMGET', 's:' .. S .. ':u:' .. sp, 'state', 'batch')
        if pdata[1] == 'landed' then
          ok = true
        elseif pdata[2] and pdata[2] ~= '' and redis.call('ZSCORE', chain_key, pdata[2]) then
          ok = true
        end
      end
      if not ok then
        return { 'REFUSED', 'stack-parent=' .. sp .. ' member=' .. m.unit }
      end
    end
  end
  if (not paths_csv or paths_csv == '') and #paths > 0 then
    paths_csv = table.concat(paths, ',')
  end

  -- 4b. Receipt reuse by input identity (spec 5.5, control L31), after the shape check
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
  if not batch_id or batch_id == '' then
    batch_id = 'b' .. tostring(seq)
  end
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id

  redis.call('HSET', bkey,
    'seq', tostring(seq),
    'parent', parent,
    'from_tip', from_tip or '',
    'members', members_csv,
    'paths', paths_csv or '',
    'class', class,
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
    'parent', parent,
    'token', tostring(token),
    'at', tostring(now)
  )

  return { 'OK', tostring(token), entry_id, batch_id }
end)

-- ns_batch_bind (4.2): sets from_tip (and input_id) on a chain batch planned before its parent had a
-- train head. Only an empty from_tip is bound, only to the parent's train_head, and only while the
-- batch is queued; anything else is STALE and writes nothing. Checks writer gen and lease first
-- (REFUSED <reason>). args: repo base batch_id lease from_tip input_id.
redis.register_function('ns_batch_bind', function(keys, args)
  local repo, base, batch_id, lease_val, from_tip, input_id = args[1], args[2], args[3], args[4], args[5], args[6]
  local refusal = land_lease_refusal(repo, base, lease_val)
  if refusal then
    return 'REFUSED ' .. refusal
  end
  local bkey = 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id
  local b = redis.call('HMGET', bkey, 'state', 'from_tip', 'parent')
  if b[1] ~= 'queued' or (b[2] and b[2] ~= '') or not b[3] or b[3] == '' then
    return 'STALE'
  end
  local pb = redis.call('HMGET', 'land:' .. repo .. ':' .. base .. ':batch:' .. b[3], 'state', 'train_head')
  if (pb[1] ~= 'green' and pb[1] ~= 'landed') or not pb[2] or pb[2] == '' or pb[2] ~= from_tip then
    return 'STALE'
  end
  redis.call('HSET', bkey, 'from_tip', from_tip, 'input_id', input_id or '')
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'BIND', 'repo', repo, 'base', base, 'batch', batch_id, 'from_tip', from_tip, 'at', land_now_ms())
  return 'OK'
end)

-- ns_unit_drop (4.1, L26): drops a unit at one head with a reason; an optional task kind (rebase)
-- is queued once on q:<author>. A second drop at the same head is ALREADY and queues nothing; a
-- drop naming a head the unit no longer has is STALE. ns_unit_eval re-offers it only on a new head.
-- Checks writer gen and lease first (REFUSED <reason>). args: S unit repo base lease head reason task.
redis.register_function('ns_unit_drop', function(keys, args)
  local S, unit, repo, base, lease_val, head, reason, task = args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8]
  local refusal = land_lease_refusal(repo, base, lease_val)
  if refusal then
    return 'REFUSED ' .. refusal
  end
  local ukey = 's:' .. S .. ':u:' .. unit
  local u = redis.call('HMGET', ukey, 'state', 'head', 'drop_head', 'author', 'conflict_drops')
  if not u[1] then return 'NOTFOUND' end
  if u[1] == 'dropped' and u[3] == head then return 'ALREADY' end
  if u[2] ~= head or u[1] == 'landed' or u[1] == 'landing' then return 'STALE' end
  local seq = redis.call('INCR', 'rec:seq')
  local now = land_now_ms()
  local h8 = string.sub(head, 1, 8)
  redis.call('HSET', ukey, 'state', 'dropped', 'drop_head', head, 'drop_key', h8 .. ':' .. tostring(seq),
    'drop_reason', reason or '', 'batch', '')
  if reason == 'conflict' then
    local prev = u[5] or ''
    local last = string.match(prev, '([^,]+)$') or ''
    local cd = (last ~= '') and (last .. ',' .. now) or now
    redis.call('HSET', ukey, 'conflict_drops', cd)
  end
  redis.call('ZREM', 's:' .. S .. ':landable:' .. repo .. ':' .. base, unit)
  if task and task ~= '' and u[4] and u[4] ~= '' then
    redis.call('XADD', 'q:' .. u[4], '*', 'kind', task, 'unit', unit, 'head', head, 'reason', reason or '',
      'drop_key', h8 .. ':' .. tostring(seq))
  end
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'event', 'DROP', 'repo', repo, 'base', base, 'unit', unit, 'head', head, 'reason', reason or '', 'at', now)
  return 'OK'
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
  local b = redis.call('HMGET', bkey, 'attempt', 'token', 'state', 'entry_id', 'from_tip', 'class', 'members', 'slot', 'kind')
  if not b[1] then return 'NOTFOUND' end
  if b[1] ~= tostring(attempt) or b[2] ~= tostring(token) then return 'STALE' end

  local rkey = 'land:' .. repo .. ':receipt:' .. batch_id .. ':' .. attempt
  if (not flaky_rerun or flaky_rerun == '') then
    -- A rerun gate (ns_batch_split rerun, 6.1 step 1) receipts the tests it reran.
    flaky_rerun = redis.call('HGET', bkey, 'rerun') or ''
  end
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

  -- The gid receipt's kind is the batch's shape (#3139 3.3, 5.4, 8.4): the
  -- selector's class for a ci single and a tip gate is full, so a full batch
  -- of exactly one member is a single (its head, on from_tip) and a full batch
  -- with no members is a tip gate (from_tip itself). Any other batch is a
  -- train and writes no gid receipt. No field is filled with a default: a
  -- gate with no from_tip writes none either.
  local class, members, from_tip = b[6] or '', b[7] or '', b[5] or ''
  local kind, head = nil, ''
  -- A revert train (8.4, B11) is also a full batch with no members: it is a
  -- train, never a tip gate, so its kind field keeps it out.
  if class == 'full' and from_tip ~= '' and b[9] ~= 'revert' then
    if members == '' then
      kind, head = 'tip', from_tip
    elseif not string.find(members, ',') then
      kind, head = 'single', string.match(members, '^[^@]+@([^@,]+)$') or ''
    end
  end

  if kind and head ~= '' then
    local base_sha = from_tip
    local pol_key = 'land:' .. repo .. ':' .. base .. ':policy'
    local pol = redis.call('HMGET', pol_key, 'policy_id', 'required_set_id', 'runner_id')
    local policy_id, required_set_id, runner_id = pol[1], pol[2], pol[3]
    if policy_id and policy_id ~= '' and required_set_id and required_set_id ~= '' and runner_id and runner_id ~= '' and head ~= '' then
      local raw = 'kind=' .. kind .. ',' .. base .. ',' .. base_sha .. ',' .. required_set_id .. ',' .. policy_id .. ',' .. runner_id
      local gid = string.sub(ci_sha256_hex(raw), 1, 16)
      local gverdict = 'FAIL'
      if string.upper(verdict or '') == 'GREEN' or string.upper(verdict or '') == 'OK' then
        gverdict = 'OK'
      end
      local pkg, test = '', ''
      if failing and failing ~= '' then
        local p, t = string.match(failing, '^([^%s]+)%s+(.+)$')
        if p and t then
          pkg, test = p, t
        else
          pkg = failing
        end
      end
      gate_receipt_write(repo, head, gid, gverdict, kind, base, base_sha, required_set_id, policy_id, runner_id, rkey, bench or '', pkg, test, now)
    end
  end

  local new_st = string.lower(verdict)
  redis.call('HSET', bkey, 'state', new_st, 'receipt', rkey, 'train_head', train_head or '', 'train_tree', train_tree or '')

  if b[4] and b[4] ~= '' then
    redis.call('XACK', 'land:' .. repo .. ':gates', 'workers', b[4])
  end

  -- Return budget debit for this slot
  local slot = b[8]
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

-- land_batch_void: marks one batch void, removes it from the chain, returns its members to landable.
local function land_batch_void(S, repo, base, batch_id, reason)
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
end

-- ns_batch_void: marks batch void, removes from chain, returns members to landable. Checks writer
-- gen and lease first ({REFUSED, reason}, nothing written), like every batcher write (2.3).
-- args: S repo base batch_id lease reason.
redis.register_function('ns_batch_void', function(keys, args)
  local S, repo, base, batch_id, lease_val, reason = args[1], args[2], args[3], args[4], args[5], args[6]
  local refusal = land_lease_refusal(repo, base, lease_val)
  if refusal then
    return { 'REFUSED', refusal }
  end
  land_batch_void(S, repo, base, batch_id, reason)
  return { 'OK' }
end)

-- ns_chain_void (4.3, L16): a red batch leaves the chain (its members stay batched for red-batch
-- attribution, 6.1) and every batch behind it is voided in the same call, members back to landable
-- for a re-plan on the new base. Returns the voided ids in chain order. Checks writer gen and lease
-- first ({REFUSED, reason}). args: S repo base batch_id lease reason.
redis.register_function('ns_chain_void', function(keys, args)
  local S, repo, base, batch_id, lease_val, reason = args[1], args[2], args[3], args[4], args[5], args[6]
  local refusal = land_lease_refusal(repo, base, lease_val)
  if refusal then
    return { 'REFUSED', refusal }
  end
  local chain_key = 'land:' .. repo .. ':' .. base .. ':chain'
  local seq = redis.call('ZSCORE', chain_key, batch_id)
  if not seq then return { 'NOTFOUND' } end
  local behind = redis.call('ZRANGEBYSCORE', chain_key, '(' .. seq, '+inf')
  redis.call('ZREM', chain_key, batch_id)
  local out = { 'OK' }
  for _, id in ipairs(behind) do
    land_batch_void(S, repo, base, id, (reason and reason ~= '') and reason or ('behind-red:' .. batch_id))
    table.insert(out, id)
  end
  return out
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

  -- 1b. A frozen base publishes only its revert train (8.4, L21): ns_freeze by
  -- hand or a red tip receipt through ns_tip_tick. Gating continues.
  local frozen = redis.call('HGET', 'land:' .. repo .. ':' .. base .. ':freeze', 'source')
  if frozen and redis.call('HGET', 'land:' .. repo .. ':' .. base .. ':batch:' .. batch_id, 'kind') ~= 'revert' then
    return { 'REFUSED', 'frozen ' .. frozen }
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

  redis.call('HSET', 'land:' .. repo .. ':' .. base .. ':tip', 'sha', train_head, 'at', tostring(now), 'by', 'publisher', 'batch', batch_id)
  -- The landed tips in order (8.4): what ns_tip_tick gates back to the last green tip.
  redis.call('ZADD', 'land:' .. repo .. ':' .. base .. ':landed', tonumber(now), batch_id)
  redis.call('ZREMRANGEBYRANK', 'land:' .. repo .. ':' .. base .. ':landed', 0, -257)
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

-- NS.land hands the later files (their own do-blocks) the fence, the clock and the one void:
-- land_red.lua (red batches, 6.1) so a red split writes a batch and its members exactly as
-- ns_chain_void does, and land_tip.lua when it plans a revert train (8.4, B11).
NS.land = { now_ms = land_now_ms, lease_refusal = land_lease_refusal, batch_void = land_batch_void }

end
