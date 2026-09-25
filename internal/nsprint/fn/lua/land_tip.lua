-- land_tip.lua: base red is a freeze, from local receipts (Issue #3139 rev 7
-- 8.4, build B11, control L21). Three functions:
--
--   ns_tip_tick   land serve's tip tick, under the publisher fence: gates the
--                 tip (full class, every tip_full_every and at once while
--                 frozen), freezes on a red tip receipt, thaws on a green one,
--                 gates every landed tip since the last green one in parallel,
--                 and when the first red one is known plans the revert train
--                 of exactly its batch at the front of the chain, with a fix
--                 task to each author.
--   ns_freeze     land freeze by hand; ns_thaw land thaw (either source).
--
-- A tip gate is batch tip-<sha> (kind tip, class full, no members, from_tip
-- the tip), off the chain, indexed in land:<repo>:<base>:tipgates for the
-- reclaim sweep; ns_gate_receipt writes its tip gid receipt. A revert train is
-- batch revert-<batch> (kind revert, class full, no members, from_tip the tip
-- it is planned on, revert_head and revert_parent the red batch's train_head
-- and from_tip); gate workers and the publisher build the same revert commit
-- from those three (land.BuildRevert). ns_land_intent refuses every batch but
-- a revert train while land:<repo>:<base>:freeze exists.

local lease_refusal, batch_void = NS.land.lease_refusal, NS.land.batch_void

local function tip_now()
  local t = redis.call('TIME')
  return string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
end

local function tip_event(repo, base, now, ...)
  redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
    'repo', repo, 'base', base, 'at', now, ...)
end

-- tip_gate_queue: a new attempt of tip-<sha>'s full gate on the gates stream.
local function tip_gate_queue(repo, base, sha, now)
  local pre = 'land:' .. repo .. ':' .. base
  local id = 'tip-' .. sha
  local bkey = pre .. ':batch:' .. id
  local old = redis.call('HGET', bkey, 'entry_id')
  if old and old ~= '' then
    redis.call('XACK', 'land:' .. repo .. ':gates', 'workers', old)
  end
  local attempt = redis.call('HINCRBY', bkey, 'attempt', 1)
  local token = redis.call('INCR', 'land:' .. repo .. ':tok')
  local eid = redis.call('XADD', 'land:' .. repo .. ':gates', '*',
    'base', base, 'batch', id, 'attempt', tostring(attempt), 'token', tostring(token), 'kind', 'tip')
  redis.call('HSET', bkey, 'kind', 'tip', 'class', 'full', 'members', '', 'paths', '', 'from_tip', sha,
    'state', 'queued', 'token', tostring(token), 'entry_id', eid, 'queued_at', now, 'seen', '')
  redis.call('HSETNX', bkey, 'created_at', now)
  redis.call('ZADD', pre .. ':tipgates', tonumber(now), id)
  redis.call('ZREMRANGEBYRANK', pre .. ':tipgates', 0, -65)
  tip_event(repo, base, now, 'event', 'TIP GATE', 'batch', id, 'sha', sha, 'attempt', tostring(attempt))
  return id
end

-- tip_revert: the revert train of culprit on tip, at the front of an emptied
-- chain (every batch there was gated on a red base), and one fix task per
-- author. Returns the revert batch id.
local function tip_revert(S, repo, base, culprit, tip, now)
  local pre = 'land:' .. repo .. ':' .. base
  local cb = redis.call('HMGET', pre .. ':batch:' .. culprit, 'train_head', 'from_tip', 'members')
  local rid = 'revert-' .. culprit
  for _, id in ipairs(redis.call('ZRANGE', pre .. ':chain', 0, -1)) do
    batch_void(S, repo, base, id, 'frozen-revert:' .. culprit)
  end
  local seq = redis.call('INCR', pre .. ':batch:seq')
  local token = redis.call('INCR', 'land:' .. repo .. ':tok')
  local rkey = pre .. ':batch:' .. rid
  redis.call('HSET', rkey, 'seq', tostring(seq), 'parent', '', 'from_tip', tip, 'members', '', 'paths', '',
    'class', 'full', 'kind', 'revert', 'revert_of', culprit, 'revert_head', cb[1] or '',
    'revert_parent', cb[2] or '', 'state', 'queued', 'attempt', '1', 'token', tostring(token),
    'input_id', '', 'created_at', now)
  redis.call('ZADD', pre .. ':chain', seq, rid)
  local eid = redis.call('XADD', 'land:' .. repo .. ':gates', '*',
    'base', base, 'batch', rid, 'attempt', '1', 'token', tostring(token))
  redis.call('HSET', rkey, 'entry_id', eid)
  for m in string.gmatch(cb[3] or '', '[^,]+') do
    local unit, head = string.match(m, '^([^@]+)@(.+)$')
    local author = unit and redis.call('HGET', 's:' .. S .. ':u:' .. unit, 'author')
    if author and author ~= '' then
      redis.call('XADD', 'q:' .. author, '*', 'kind', 'fix', 'unit', unit, 'head', head,
        'reason', 'tip-red revert ' .. rid, 'id', 'fix-' .. unit .. '-' .. string.sub(head, 1, 8))
    end
  end
  tip_event(repo, base, now, 'event', 'REVERT', 'batch', rid, 'revert_of', culprit,
    'red', cb[1] or '', 'from_tip', tip)
  return rid
end

-- tip_scan: the landed tips since the last green one, oldest first. Gates each
-- that has none (all at once), and returns the first red one's batch once
-- every tip before it is green; nil while any is still gating.
local function tip_scan(repo, base, now, out)
  local pre = 'land:' .. repo .. ':' .. base
  -- With no green tip on record, the 16 newest landed tips (four chains' worth).
  local from = redis.call('HGET', pre .. ':tipgreen', 'score')
  if not from then
    local r = redis.call('ZREVRANGE', pre .. ':landed', 16, 16, 'WITHSCORES')
    from = r[2] or '0'
  end
  local ids = redis.call('ZRANGEBYSCORE', pre .. ':landed', '(' .. from, '+inf', 'LIMIT', 0, 64)
  local waiting = false
  for _, id in ipairs(ids) do
    local head = redis.call('HGET', pre .. ':batch:' .. id, 'train_head')
    if head and head ~= '' then
      local st = redis.call('HGET', pre .. ':batch:tip-' .. head, 'state')
      if not st then
        tip_gate_queue(repo, base, head, now)
        table.insert(out, 'TIP GATE ' .. string.sub(head, 1, 8) .. ' landed=' .. id)
        waiting = true
      elseif st == 'red' then
        if waiting then return nil end
        return id
      elseif st ~= 'green' then
        waiting = true
      end
    end
  end
  return nil
end

-- ns_tip_tick: args S repo base lease every_ms. Returns { status, line... }:
-- status NOTIP (no tip record), or OK; every line is one transition
-- (TIP GATE, FROZEN, THAW, TIP GREEN, REVERT). REFUSED <reason> when the
-- publisher fence fails, and nothing is written.
redis.register_function('ns_tip_tick', function(keys, args)
  local S, repo, base, lease_val, every = args[1], args[2], args[3], args[4], tonumber(args[5] or '') or 600000
  local refusal = lease_refusal(repo, base, lease_val)
  if refusal then
    return { 'REFUSED', refusal }
  end
  local pre = 'land:' .. repo .. ':' .. base
  local tr = redis.call('HMGET', pre .. ':tip', 'sha', 'batch')
  local tip = tr[1]
  if not tip or tip == '' then return { 'NOTIP' } end
  local now = tip_now()
  local out = { 'OK' }
  local fz = redis.call('HMGET', pre .. ':freeze', 'source', 'culprit')
  local bkey = pre .. ':batch:tip-' .. tip
  local g = redis.call('HMGET', bkey, 'state', 'attempt', 'seen', 'queued_at')

  -- 1. The tip's own gate: none yet, an ERROR, or due again while green.
  local due = not g[1] or g[1] == 'error'
    or (not fz[1] and g[1] ~= 'queued' and g[1] ~= 'gating' and every > 0
        and tonumber(now) - tonumber(g[4] or '0') >= every)
  if due then
    tip_gate_queue(repo, base, tip, now)
    table.insert(out, 'TIP GATE ' .. string.sub(tip, 1, 8))
    g = { 'queued', '', '', now }
  end

  -- 2. Its verdict, acted on once per attempt (a hand thaw holds until the next).
  if (g[1] == 'green' or g[1] == 'red') and g[3] ~= g[2] then
    redis.call('HSET', bkey, 'seen', g[2])
    if g[1] == 'green' then
      local score = (tr[2] and tr[2] ~= '') and redis.call('ZSCORE', pre .. ':landed', tr[2]) or nil
      redis.call('HSET', pre .. ':tipgreen', 'sha', tip, 'batch', tr[2] or '', 'score', score or now, 'at', now)
      table.insert(out, 'TIP GREEN ' .. string.sub(tip, 1, 8))
      if fz[1] == 'tip' then
        redis.call('DEL', pre .. ':freeze')
        tip_event(repo, base, now, 'event', 'THAW', 'sha', tip, 'by', 'tip')
        table.insert(out, 'THAW ' .. base .. ' green tip ' .. string.sub(tip, 1, 8))
      end
      return out
    end
    if not fz[1] then
      redis.call('HSET', pre .. ':freeze', 'source', 'tip', 'reason', 'tip-red ' .. string.sub(tip, 1, 8),
        'remedy', 'nova-sprint land status', 'red_tip', tip, 'at', now)
      tip_event(repo, base, now, 'event', 'FROZEN', 'reason', 'tip-red', 'sha', tip)
      table.insert(out, 'FROZEN ' .. base .. ' tip-red ' .. string.sub(tip, 1, 8))
      fz = { 'tip', false }
    end
  end

  -- 3. Frozen by a red tip with no culprit yet: find the first red landed tip.
  -- An intent cut before the freeze resolves first: the chain is not voided
  -- under an unresolved publication (2.3).
  local active = redis.call('GET', pre .. ':pub:active')
  if fz[1] == 'tip' and (not fz[2] or fz[2] == '') and active then
    table.insert(out, 'WAIT pub ' .. active)
  elseif fz[1] == 'tip' and (not fz[2] or fz[2] == '') then
    local culprit = tip_scan(repo, base, now, out)
    if culprit then
      local rid = tip_revert(S, repo, base, culprit, tip, now)
      redis.call('HSET', pre .. ':freeze', 'culprit', culprit, 'revert', rid)
      table.insert(out, 'REVERT ' .. culprit .. ' in ' .. rid .. ' on ' .. string.sub(tip, 1, 8))
    end
  end
  return out
end)

-- ns_freeze: args repo base reason by. Stops publishing on the base (the
-- revert train excepted); gating continues. ALREADY when frozen.
redis.register_function('ns_freeze', function(keys, args)
  local repo, base, reason, by = args[1], args[2], args[3] or '', args[4] or ''
  local pre = 'land:' .. repo .. ':' .. base
  if redis.call('EXISTS', pre .. ':freeze') == 1 then
    return { 'ALREADY', redis.call('HGET', pre .. ':freeze', 'reason') or '' }
  end
  local now = tip_now()
  redis.call('HSET', pre .. ':freeze', 'source', 'hand', 'reason', reason, 'by', by,
    'remedy', 'nova-sprint land thaw ' .. repo .. ' ' .. base, 'at', now)
  tip_event(repo, base, now, 'event', 'FROZEN', 'reason', reason, 'by', by)
  return { 'OK' }
end)

-- ns_thaw: args repo base by. Resumes publishing; NOTFROZEN when not frozen.
redis.register_function('ns_thaw', function(keys, args)
  local repo, base, by = args[1], args[2], args[3] or ''
  local pre = 'land:' .. repo .. ':' .. base
  local src = redis.call('HGET', pre .. ':freeze', 'source')
  if not src then return { 'NOTFROZEN' } end
  local now = tip_now()
  redis.call('DEL', pre .. ':freeze')
  tip_event(repo, base, now, 'event', 'THAW', 'by', by, 'source', src)
  return { 'OK', src }
end)
