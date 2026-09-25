-- land_red.lua: red batches (nova-tools #3139 rev 7 section 6, B8). A RED receipt on a chain batch
-- becomes, in one call each:
--   ns_batch_split     rerun  flaky first (6.1 step 1, 6.2): the same batch gates again on the
--                             same tree (attempt+1, rerun=<tests>), still in the chain; each
--                             failing test records a hit in flaky:<repo>:<pkg>.<Test>, and the
--                             second distinct batch makes it flaky with one task to its owner.
--                      freeze a rerun red again on the same test no member touches: the base is
--                             red (8.4); land:<repo>:<base>:freeze is set, the batch and its
--                             successors void, their members landable.
--                      attr   the batch and its successors leave the chain; one wall of gates on
--                             from_tip, all queued at once: the tip alone (<id>.t), each member
--                             alone (<id>.s<i>) and each prefix m1..mk (<id>.p<k>, k = 2..N-1;
--                             P_1 is S_1 and P_N is the red batch). A batch of one is its own
--                             attribution and drops at once.
--   ns_batch_attribute        once every gate has a receipt: the tip red freezes the base; a
--                             member red alone drops alone=red@<tip8>:<test>; otherwise the first
--                             red prefix P_k drops m_k combo-red-with=<units of P_(k-1)>. Every
--                             drop pushes one fix task (fix-<unit>-<head8>) to the unit's author.
--                             The rest return to landable with red_round+1; at rounds_max they
--                             carry red_alone and the batcher plans each alone.
-- Keys: land:<repo>:<base>:split:<id> (hash: from_tip, members, class, paths, gates, failing,
-- round, state attributing|done, reason, at) and land:<repo>:<base>:splits (zset of ids in
-- attribution, score at), both written only here. Unit fields red_round, red_head, red_alone
-- are written only here; state, batch and the drop fields as ns_unit_drop writes them.
-- Every call checks the writer gen and the publisher lease first and writes nothing on a refusal.

local RB = { L = NS.land, FQ = NS.fq }

function RB.key(repo, base, what) return 'land:' .. repo .. ':' .. base .. ':' .. what end

function RB.event(repo, ...)
  redis.call('XADD', 'land:' .. repo .. ':events', '*', ...)
end

-- RB.test names a receipt's failing field "<pkg> <Test>" as <pkg>.<Test> (one word in a reason).
function RB.test(failing) return (string.gsub(failing or '', '%s+', '.')) end

function RB.members(csv)
  local out = {}
  for m in string.gmatch(csv or '', '[^,]+') do
    local unit, head = string.match(m, '^([^@]+)@(.+)$')
    if unit then table.insert(out, { unit = unit, head = head, m = m }) end
  end
  return out
end

-- RB.gate queues one attribution gate: a batch hash outside the chain, attempt 1, one entry.
function RB.gate(repo, base, id, members_csv, from_tip, class, paths, red_of, now)
  local token = redis.call('INCR', 'land:' .. repo .. ':tok')
  local bkey = RB.key(repo, base, 'batch:' .. id)
  redis.call('HSET', bkey, 'parent', '', 'from_tip', from_tip, 'members', members_csv, 'paths', paths,
    'class', class, 'state', 'queued', 'attempt', '1', 'token', tostring(token), 'input_id', '',
    'kind', 'attr', 'red_of', red_of, 'created_at', now)
  local entry = redis.call('XADD', 'land:' .. repo .. ':gates', '*', 'base', base, 'batch', id,
    'attempt', '1', 'token', tostring(token))
  redis.call('HSET', bkey, 'entry_id', entry)
end

-- RB.requeue gates one batch again (attempt+1, new token and entry), as ns_requeue does.
function RB.requeue(repo, base, id, extra_field, extra_value)
  local bkey = RB.key(repo, base, 'batch:' .. id)
  local attempt = redis.call('HINCRBY', bkey, 'attempt', 1)
  local token = redis.call('INCR', 'land:' .. repo .. ':tok')
  local old = redis.call('HGET', bkey, 'entry_id')
  if old and old ~= '' then redis.call('XACK', 'land:' .. repo .. ':gates', 'workers', old) end
  local entry = redis.call('XADD', 'land:' .. repo .. ':gates', '*', 'base', base, 'batch', id,
    'attempt', tostring(attempt), 'token', tostring(token))
  redis.call('HSET', bkey, 'state', 'queued', 'token', tostring(token), 'entry_id', entry)
  if extra_field then redis.call('HSET', bkey, extra_field, extra_value) end
  return token
end

-- RB.drop drops one member at its head (the ns_unit_drop fields) and pushes its fix task (6.3),
-- deduplicated on unit and head by the task id. Returns the task id or the push refusal.
function RB.drop(S, repo, base, unit, head, reason, title, now)
  local ukey = 's:' .. S .. ':u:' .. unit
  local u = redis.call('HMGET', ukey, 'state', 'head', 'author')
  if not u[1] or u[2] ~= head or u[1] == 'landed' or u[1] == 'landing' then return 'stale' end
  local seq = redis.call('INCR', 'rec:seq')
  local h8 = string.sub(head, 1, 8)
  redis.call('HSET', ukey, 'state', 'dropped', 'drop_head', head, 'drop_key', h8 .. ':' .. tostring(seq),
    'drop_reason', reason, 'batch', '')
  redis.call('ZREM', 's:' .. S .. ':landable:' .. repo .. ':' .. base, unit)
  local to = (u[3] and u[3] ~= '') and u[3] or 'rowan'
  local id = 'fix-' .. unit .. '-' .. h8
  local _, refusal = RB.FQ.push(S, id, to, 'fix', unit, title, head, false, tonumber(now))
  RB.event(repo, 'event', 'DROP', 'repo', repo, 'base', base, 'unit', unit, 'head', head, 'reason', reason,
    'task', refusal and (id .. ' ' .. refusal) or id, 'at', now)
  return refusal and (id .. ' ' .. refusal) or id
end

-- RB.keep returns a surviving member to landable (6.1: re-planned on the new base) and counts
-- the round against its head; at rounds_max it carries red_alone and plans alone. rounds_max 0
-- returns it uncounted (the base was red, not the member).
function RB.keep(S, repo, base, unit, head, rounds_max)
  local ukey = 's:' .. S .. ':u:' .. unit
  local u = redis.call('HMGET', ukey, 'state', 'head', 'red_head', 'red_round', 'seq')
  if u[1] ~= 'batched' or u[2] ~= head then return nil end
  local ukey_seq = tonumber(u[5] or '0') or 0
  if rounds_max == 0 then
    redis.call('HSET', ukey, 'state', 'landable', 'batch', '')
    redis.call('ZADD', 's:' .. S .. ':landable:' .. repo .. ':' .. base, ukey_seq, unit)
    return 0
  end
  local round = 1
  if u[3] == head then round = (tonumber(u[4] or '0') or 0) + 1 end
  redis.call('HSET', ukey, 'state', 'landable', 'batch', '', 'red_head', head, 'red_round', tostring(round),
    'red_alone', round >= rounds_max and '1' or '')
  redis.call('ZADD', 's:' .. S .. ':landable:' .. repo .. ':' .. base, ukey_seq, unit)
  return round
end

-- RB.flaky records one hit of a failing test outside the selection closure (6.2): distinct
-- batch ids in batches (the last 16), hits their count, state seen|flaky; the second distinct
-- batch makes it flaky and pushes one task on the owning unit to the owner. No GitHub issue.
function RB.flaky(S, repo, test, batch_id, owner, unit, now)
  local fkey = 'flaky:' .. repo .. ':' .. test
  redis.call('HSETNX', fkey, 'first_seen', now)
  local f = redis.call('HMGET', fkey, 'batches', 'state')
  local seen = false
  for id in string.gmatch(f[1] or '', '[^,]+') do
    if id == batch_id then seen = true end
  end
  local hits = tonumber(redis.call('HGET', fkey, 'hits') or '0') or 0
  if not seen then
    local list = {}
    for id in string.gmatch(f[1] or '', '[^,]+') do table.insert(list, id) end
    table.insert(list, batch_id)
    while #list > 16 do table.remove(list, 1) end
    hits = redis.call('HINCRBY', fkey, 'hits', 1)
    redis.call('HSET', fkey, 'batches', table.concat(list, ','), 'last_at', now)
  end
  local state = f[2] or ''
  if hits >= 2 and state ~= 'flaky' then
    state = 'flaky'
    local to = (owner and owner ~= '') and owner or 'rowan'
    local id = 'flaky-' .. repo .. '-' .. test
    local title = 'flaky ' .. test .. ' (' .. repo .. '): red in ' .. tostring(hits) ..
      ' batches outside the selection closure (' .. redis.call('HGET', fkey, 'batches') .. '); rerun once, never dropped'
    local _, refusal = RB.FQ.push(S, id, to, 'flaky', unit, title, '', false, tonumber(now))
    redis.call('HSET', fkey, 'state', 'flaky', 'owner', to, 'task', refusal and (id .. ' ' .. refusal) or id)
  elseif state == '' then
    state = 'seen'
    redis.call('HSET', fkey, 'state', 'seen')
  end
  return test .. '=' .. state .. ':' .. tostring(hits)
end

-- RB.void_from takes batch_id out of the chain and voids every batch behind it (ns_chain_void).
function RB.void_from(S, repo, base, batch_id)
  local chain = RB.key(repo, base, 'chain')
  local seq = redis.call('ZSCORE', chain, batch_id)
  if not seq then return {} end
  local behind = redis.call('ZRANGEBYSCORE', chain, '(' .. seq, '+inf')
  redis.call('ZREM', chain, batch_id)
  for _, id in ipairs(behind) do RB.L.batch_void(S, repo, base, id, 'behind-red:' .. batch_id) end
  return behind
end

function RB.freeze(repo, base, reason, now)
  redis.call('HSET', RB.key(repo, base, 'freeze'), 'reason', reason,
    'remedy', 'fix the base, then thaw land:' .. repo .. ':' .. base .. ':freeze', 'at', now)
  RB.event(repo, 'event', 'FROZEN', 'repo', repo, 'base', base, 'reason', reason, 'at', now)
end

-- ns_batch_split: args S repo base batch_id lease mode [tests owners] | [reason].
-- mode rerun (tests, owners: parallel csv of <pkg>.<Test> and owner, every owner named) -> {RERUN, token, hit...};
-- mode freeze (reason) -> {FROZEN, voided...}; mode attr -> {SPLIT, gate...} or
-- {DROPPED, unit, task} for a batch of one. {STALE, why} when the batch is not a red chain batch
-- (or was already rerun); {REFUSED, reason} on the fence.
redis.register_function('ns_batch_split', function(keys, args)
  local S, repo, base, batch_id, lease_val, mode = args[1], args[2], args[3], args[4], args[5], args[6]
  local refusal = RB.L.lease_refusal(repo, base, lease_val)
  if refusal then return { 'REFUSED', refusal } end
  local bkey = RB.key(repo, base, 'batch:' .. batch_id)
  local b = redis.call('HMGET', bkey, 'state', 'members', 'from_tip', 'class', 'receipt', 'rerun', 'paths')
  if b[1] ~= 'red' then return { 'STALE', 'state=' .. tostring(b[1] or '') } end
  if not redis.call('ZSCORE', RB.key(repo, base, 'chain'), batch_id) then return { 'STALE', 'not in chain' } end
  local now = RB.L.now_ms()
  local members = RB.members(b[2])

  if mode == 'rerun' then
    if b[6] and b[6] ~= '' then return { 'STALE', 'rerun done' } end
    local owners = {}
    for o in string.gmatch(args[8] or '', '[^,]+') do table.insert(owners, o) end
    local out, tests, i = { 'RERUN' }, {}, 0
    for t in string.gmatch(args[7] or '', '[^,]+') do
      i = i + 1
      table.insert(tests, t)
      table.insert(out, RB.flaky(S, repo, t, batch_id, owners[i], members[1] and members[1].unit or '', now))
    end
    if #tests == 0 then return { 'STALE', 'no tests' } end
    local token = RB.requeue(repo, base, batch_id, 'rerun', table.concat(tests, ','))
    table.insert(out, 2, tostring(token))
    RB.event(repo, 'event', 'RERUN', 'repo', repo, 'base', base, 'batch', batch_id, 'tests', table.concat(tests, ','), 'at', now)
    return out
  end

  if mode == 'freeze' then
    local behind = RB.void_from(S, repo, base, batch_id)
    RB.L.batch_void(S, repo, base, batch_id, 'base-red')
    RB.freeze(repo, base, args[7] or ('base-red at ' .. string.sub(b[3] or '', 1, 8)), now)
    local out = { 'FROZEN' }
    for _, id in ipairs(behind) do table.insert(out, id) end
    return out
  end

  if mode ~= 'attr' then return redis.error_reply('ns_batch_split: mode is rerun, freeze or attr') end
  local from_tip, class, paths = b[3] or '', b[4] or 'go', b[7] or ''
  local failing = ''
  if b[5] and b[5] ~= '' then failing = RB.test(redis.call('HGET', b[5], 'failing')) end
  RB.void_from(S, repo, base, batch_id)
  redis.call('HSET', bkey, 'state', 'split')
  local skey = RB.key(repo, base, 'split:' .. batch_id)
  local tip8 = string.sub(from_tip, 1, 8)
  if #members == 1 then
    local m = members[1]
    local reason = 'alone=red@' .. tip8 .. ':' .. failing
    local task = RB.drop(S, repo, base, m.unit, m.head, reason,
      'fix ' .. m.unit .. '@' .. string.sub(m.head, 1, 8) .. ': ' .. failing .. ' red alone on ' .. tip8 .. ' (receipt ' .. (b[5] or '') .. ')', now)
    redis.call('HSET', skey, 'from_tip', from_tip, 'members', b[2], 'gates', '', 'failing', failing,
      'state', 'done', 'reason', reason, 'at', now)
    return { 'DROPPED', m.unit, task }
  end
  local gates = { batch_id .. '.t' }
  RB.gate(repo, base, batch_id .. '.t', '', from_tip, class, paths, batch_id, now)
  for i, m in ipairs(members) do
    table.insert(gates, batch_id .. '.s' .. i)
    RB.gate(repo, base, batch_id .. '.s' .. i, m.m, from_tip, class, paths, batch_id, now)
  end
  local prefix = { members[1].m }
  for k = 2, #members - 1 do
    table.insert(prefix, members[k].m)
    table.insert(gates, batch_id .. '.p' .. k)
    RB.gate(repo, base, batch_id .. '.p' .. k, table.concat(prefix, ','), from_tip, class, paths, batch_id, now)
  end
  redis.call('HSET', skey, 'from_tip', from_tip, 'members', b[2], 'class', class, 'paths', paths,
    'gates', table.concat(gates, ','), 'failing', failing, 'state', 'attributing', 'reason', '', 'at', now)
  redis.call('ZADD', RB.key(repo, base, 'splits'), tonumber(now), batch_id)
  RB.event(repo, 'event', 'SPLIT', 'repo', repo, 'base', base, 'batch', batch_id, 'gates', tostring(#gates),
    'failing', failing, 'at', now)
  local out = { 'SPLIT' }
  for _, g in ipairs(gates) do table.insert(out, g) end
  return out
end)

-- ns_batch_attribute: args S repo base batch_id lease rounds_max. {PENDING, n} while a gate has no
-- receipt (an ERROR gate is queued again); {FROZEN, reason} when the tip alone is red; otherwise
-- {OK, 'DROP <unit> <reason> <task>'..., 'KEEP <unit> <round>'...}. {NOTFOUND} | {ALREADY}.
redis.register_function('ns_batch_attribute', function(keys, args)
  local S, repo, base, batch_id, lease_val = args[1], args[2], args[3], args[4], args[5]
  local rounds_max = tonumber(args[6] or '') or 3
  local refusal = RB.L.lease_refusal(repo, base, lease_val)
  if refusal then return { 'REFUSED', refusal } end
  local skey = RB.key(repo, base, 'split:' .. batch_id)
  local sp = redis.call('HMGET', skey, 'state', 'from_tip', 'members', 'gates', 'failing')
  if not sp[1] then return { 'NOTFOUND' } end
  if sp[1] == 'done' then return { 'ALREADY' } end
  local verdict, failing_of, pending = {}, {}, 0
  for g in string.gmatch(sp[4] or '', '[^,]+') do
    local gb = redis.call('HMGET', RB.key(repo, base, 'batch:' .. g), 'state', 'receipt')
    if gb[1] == 'error' then
      RB.requeue(repo, base, g)
      pending = pending + 1
    elseif gb[1] ~= 'green' and gb[1] ~= 'red' and gb[1] ~= 'conflict' then
      pending = pending + 1
    else
      verdict[g] = gb[1]
      failing_of[g] = (gb[2] and gb[2] ~= '') and RB.test(redis.call('HGET', gb[2], 'failing')) or ''
    end
  end
  if pending > 0 then return { 'PENDING', tostring(pending) } end

  local now = RB.L.now_ms()
  local from_tip, members = sp[2] or '', RB.members(sp[3])
  local tip8 = string.sub(from_tip, 1, 8)
  local done = function(reason)
    redis.call('HSET', skey, 'state', 'done', 'reason', reason, 'done_at', now)
    redis.call('ZREM', RB.key(repo, base, 'splits'), batch_id)
    RB.event(repo, 'event', 'ATTRIBUTE', 'repo', repo, 'base', base, 'batch', batch_id, 'reason', reason, 'at', now)
  end

  if verdict[batch_id .. '.t'] ~= 'green' then
    local reason = 'base-red ' .. (failing_of[batch_id .. '.t'] ~= '' and failing_of[batch_id .. '.t'] or (sp[5] or '')) .. ' at ' .. tip8
    RB.freeze(repo, base, reason, now)
    for _, m in ipairs(members) do RB.keep(S, repo, base, m.unit, m.head, 0) end
    done(reason)
    return { 'FROZEN', reason }
  end

  local out, dropped = { 'OK' }, {}
  for i, m in ipairs(members) do
    local g = batch_id .. '.s' .. i
    if verdict[g] ~= 'green' then
      local what = verdict[g] == 'conflict' and 'conflict' or failing_of[g]
      local reason = 'alone=' .. verdict[g] .. '@' .. tip8 .. ':' .. what
      local task = RB.drop(S, repo, base, m.unit, m.head, reason,
        'fix ' .. m.unit .. '@' .. string.sub(m.head, 1, 8) .. ': ' .. what .. ' ' .. verdict[g] .. ' alone on ' .. tip8 .. ' (red batch ' .. batch_id .. ', gate ' .. g .. ')', now)
      dropped[i] = true
      table.insert(out, 'DROP ' .. m.unit .. ' ' .. reason .. ' ' .. task)
    end
  end
  if next(dropped) == nil then
    -- The first red prefix P_k holds the failing set: m_k completes it with m1..m(k-1).
    local k = #members
    for j = 2, #members - 1 do
      if verdict[batch_id .. '.p' .. j] ~= 'green' then k = j break end
    end
    local with, with_heads = {}, {}
    for j = 1, k - 1 do
      table.insert(with, members[j].unit)
      table.insert(with_heads, members[j].m)
    end
    local m = members[k]
    local reason = 'combo-red-with=' .. table.concat(with, ',')
    local what = (k < #members) and failing_of[batch_id .. '.p' .. k] or (sp[5] or '')
    local task = RB.drop(S, repo, base, m.unit, m.head, reason,
      'fix ' .. m.unit .. '@' .. string.sub(m.head, 1, 8) .. ': ' .. what .. ' red with ' .. table.concat(with_heads, ',') .. ' on ' .. tip8 .. ' (red batch ' .. batch_id .. ')', now)
    redis.call('HSET', 's:' .. S .. ':u:' .. m.unit, 'combo_with', table.concat(with_heads, ','))
    dropped[k] = true
    table.insert(out, 'DROP ' .. m.unit .. ' ' .. reason .. ' ' .. task)
  end
  for i, m in ipairs(members) do
    if not dropped[i] then
      local round = RB.keep(S, repo, base, m.unit, m.head, rounds_max)
      if round then table.insert(out, 'KEEP ' .. m.unit .. ' ' .. tostring(round)) end
    end
  end
  done('attributed')
  return out
end)
