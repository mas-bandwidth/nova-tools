-- task_live: the path-lint snapshot for task push (nova-tools #3067). One
-- read-only call returns whether the pushed id already exists (the push
-- function then answers EXISTS, CLOSED or CONFLICT, never the lint), then
-- every live (open, claimed, working or waiting) task of every sprint in sprint:order
-- plus the pushing sprint, as flat rows of sprint, id, kind, repo, title. The
-- state indexes are only candidates: a row is returned only when its hash
-- says the task is live. Nothing is written.
local function task_live(keys, args)
  local push_sprint, push_id = args[1], args[2]
  local sprints = redis.call('ZRANGE', 'sprint:order', 0, -1)
  local seen = {}
  local order = {}
  for _, s in ipairs(sprints) do
    if not seen[s] then
      seen[s] = true
      order[#order + 1] = s
    end
  end
  if push_sprint ~= '' and not seen[push_sprint] then
    order[#order + 1] = push_sprint
  end
  local out = { tostring(redis.call('EXISTS', 's:' .. push_sprint .. ':task:' .. push_id)) }
  local live = { open = true, claimed = true, working = true, waiting = true }
  for _, s in ipairs(order) do
    local done = {}
    for _, idx in ipairs({ 'open', 'claimed', 'working', 'waiting' }) do
      for _, id in ipairs(redis.call('SMEMBERS', 's:' .. s .. ':idx:task:' .. idx)) do
        if not done[id] then
          done[id] = true
          local row = redis.call('HMGET', 's:' .. s .. ':task:' .. id, 'state', 'kind', 'repo', 'title')
          if row[1] and live[row[1]] then
            out[#out + 1] = s
            out[#out + 1] = id
            out[#out + 1] = row[2] or ''
            out[#out + 1] = row[3] or ''
            out[#out + 1] = row[4] or ''
          end
        end
      end
    end
  end
  return out
end

redis.register_function{ function_name = 'ns_task_live', callback = task_live,
  flags = { 'no-writes' } }

-- Durable WAITING (nova-tools #3090, #2756 5.7 (7), controls 62 and 63).
-- Stella (bus stella-9d205486dcb9): "separate productive execution from
-- management and durable waiting state, so a waiting build does not occupy a
-- child merely to retain ownership". A WORKING task that is only waiting on
-- CI, a read, a dependency or a human moves to `waiting` with a typed
-- wait_on and wait_since: its child lease is closed (the identity leaves
-- friend:<f>:living and the stored token is fenced, so the old child's beat
-- and done refuse FENCED), its slot is freed on cap:log, and the owner is
-- kept. The event that satisfies wait_on puts it back open at the FRONT of the
-- SAME owner's queue; a key that can no longer resolve goes to
-- reconcile-required with an unresolved item. waiting is neither working
-- (not in living) nor ready_open (not in open:<f>), so #3071's desired never
-- counts it. Every lua/ file shares one chunk: this block declares exactly
-- one chunk local, TW.
--
-- Keys: s:<S>:idx:task:waiting (ids), s:<S>:waiton:<key> (ids waiting on
-- key, the wake index), friend:<f>:waiting (zset S/id -> wait_since, global
-- to the friend like living; the width tick prints its ZCARD beside working).
--
-- wait_on keys:
--   ci:<repo>:<head>   CI at head; resolved by the CI end event (ns_task_wake)
--   read:<pr>@<head>   a typed read of the task's repo PR at head; the sweep
--                      resolves it from s:<S>:disp:<repo>:<pr> (a field
--                      <friend>@<head>), dead when the PR head moved
--   dep:<task>         a task in the same sprint; the sweep resolves it when
--                      the task is closed and its PR (if any) merged=1, dead
--                      when it is cancelled, reconcile-required or missing
--   human:<channel>    a person; resolved only by ns_task_wake

local TW = {}

function TW.now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

function TW.receipt(S, kind, id, from_state, to_state, attempt, token_sha, actor, reason, evidence, idem, at)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
    'attempt', tostring(attempt or 0), 'token_sha', token_sha or '',
    'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
    'idem', idem or '', 'at', tostring(at))
end

-- TW.kind parses a wait_on key and returns its kind, or nil when malformed.
function TW.kind(on)
  if string.match(on, '^ci:[%w._-]+:[0-9a-f]+$') then return 'ci' end
  if string.match(on, '^read:%d+@[0-9a-f]+$') then return 'read' end
  if string.match(on, '^dep:[^%s]+$') then return 'dep' end
  if string.match(on, '^human:[^%s]+$') then return 'human' end
  return nil
end

-- TW.holds: the lease:reconciler fence (reconciler.lua's rule). An empty
-- fence is a direct caller (the CI end event, a verb), not the reconciler.
function TW.holds(fence)
  return fence == '' or redis.call('HGET', 'lease:reconciler', 'token') == fence
end

function TW.leave(S, id, f, on)
  redis.call('SREM', 's:' .. S .. ':idx:task:waiting', id)
  redis.call('SREM', 's:' .. S .. ':waiton:' .. on, id)
  redis.call('ZREM', 'friend:' .. f .. ':waiting', S .. '/' .. id)
end

-- TW.resume: waiting -> open at the front of the same owner's queue.
function TW.resume(S, id, key, actor, idem, at)
  local f = redis.call('HGET', key, 'owner') or ''
  local on = redis.call('HGET', key, 'wait_on') or ''
  local since = tonumber(redis.call('HGET', key, 'wait_since') or '0') or 0
  local priority = tonumber(redis.call('HGET', key, 'priority') or '0') or 0
  TW.leave(S, id, f, on)
  local q = 's:' .. S .. ':open:' .. f
  local score = -priority
  local first = redis.call('ZRANGE', q, 0, 0, 'WITHSCORES')
  if first[2] and tonumber(first[2]) - 1 < score then
    score = tonumber(first[2]) - 1
  end
  redis.call('ZADD', q, score, id)
  redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
  redis.call('HSET', key, 'state', 'open', 'wait_on', '', 'waited_ms', tostring(at - since))
  TW.receipt(S, 'task resume', id, 'waiting', 'open', redis.call('HGET', key, 'attempt'),
    '', actor, on, 'owner=' .. f, idem, at)
end

-- TW.dead: waiting -> reconcile-required with an unresolved item; the owner
-- is kept on the hash, nothing is requeued.
function TW.dead(S, id, key, reason, actor, idem, at)
  local f = redis.call('HGET', key, 'owner') or ''
  local on = redis.call('HGET', key, 'wait_on') or ''
  TW.leave(S, id, f, on)
  redis.call('HSET', key, 'state', 'reconcile-required', 'reason', 'wait-dead ' .. reason)
  redis.call('SADD', 's:' .. S .. ':idx:task:reconcile-required', id)
  redis.call('HSET', 's:' .. S .. ':unresolved', id .. ':wait-dead:' .. on,
    'owner=' .. f .. ' wait_on=' .. on .. ' reason=' .. reason ..
    ' wait_since=' .. (redis.call('HGET', key, 'wait_since') or '') .. ' at=' .. tostring(at))
  TW.receipt(S, 'task wait-dead', id, 'waiting', 'reconcile-required', redis.call('HGET', key, 'attempt'),
    '', actor, reason, on, idem, at)
end

-- TW.check: the sweep's verdict on one waiting task from Redis state alone:
-- 'ok', 'dead', reason, or nil when only an event can resolve it.
function TW.check(S, key, on)
  local kind = TW.kind(on)
  if kind == 'dep' then
    local dk = 's:' .. S .. ':task:' .. string.sub(on, 5)
    local state = redis.call('HGET', dk, 'state')
    if not state then return 'dead', 'dep:missing' end
    if state == 'cancelled' or state == 'reconcile-required' then return 'dead', 'dep:' .. state end
    if state ~= 'closed' then return nil end
    local pr = redis.call('HGET', dk, 'pr') or ''
    if pr == '' or pr == '0' then return 'ok', 'dep:closed' end
    local ph = 's:' .. S .. ':pr:' .. (redis.call('HGET', dk, 'repo') or '') .. ':' .. pr
    if redis.call('HGET', ph, 'merged') == '1' then return 'ok', 'dep:merged' end
    if redis.call('HGET', ph, 'state') == 'closed' then return 'dead', 'dep:pr-closed' end
    return nil
  end
  if kind == 'read' then
    local pr, head = string.match(on, '^read:(%d+)@([0-9a-f]+)$')
    local repo = redis.call('HGET', key, 'repo') or ''
    local disp = redis.call('HKEYS', 's:' .. S .. ':disp:' .. repo .. ':' .. pr)
    for _, who in ipairs(disp) do
      if string.sub(who, -(#head + 1)) == '@' .. head then return 'ok', 'read:' .. who end
    end
    local ph = 's:' .. S .. ':pr:' .. repo .. ':' .. pr
    if redis.call('HGET', ph, 'state') == 'closed' then return 'dead', 'read:pr-closed' end
    local now = redis.call('HGET', ph, 'head') or ''
    if now ~= '' and now ~= head then return 'dead', 'read:head-moved' end
    return nil
  end
  return nil
end

-- ns_task_wait: working -> waiting (#3090). args = S, id, token, on, actor,
-- idem. Only the current token of a WORKING task may wait; the call closes
-- the child lease (living, token fenced), frees the slot on cap:log, keeps
-- the owner and indexes the task under its key, atomically, with one receipt.
local function task_wait(keys, args)
  local S, id, token, on, actor, idem = args[1], args[2], args[3], args[4], args[5], args[6]
  local key = 's:' .. S .. ':task:' .. id
  if not TW.kind(on or '') then
    return { 'BADKEY' }
  end
  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  if redis.call('HGET', key, 'token') ~= token then
    return { 'FENCED' }
  end
  if redis.call('HGET', key, 'state') ~= 'working' then
    return { 'INVALID' }
  end
  local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
  local f = redis.call('HGET', key, 'owner') or ''
  local token_sha = redis.call('HGET', key, 'token_sha') or ''
  local at = TW.now_ms()
  redis.call('HSET', key, 'state', 'waiting', 'token', 'fenced', 'wait_on', on, 'wait_since', tostring(at))
  redis.call('SREM', 's:' .. S .. ':idx:task:working', id)
  redis.call('SADD', 's:' .. S .. ':idx:task:waiting', id)
  redis.call('SADD', 's:' .. S .. ':waiton:' .. on, id)
  redis.call('ZREM', 'friend:' .. f .. ':living', S .. '/' .. id .. '/' .. attempt)
  redis.call('ZADD', 'friend:' .. f .. ':waiting', at, S .. '/' .. id)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', 'slot-freed', 'consumer', 'friend:' .. f,
    'sprint', S, 'id', id, 'attempt', tostring(attempt), 'at', tostring(at))
  TW.receipt(S, 'task wait', id, 'working', 'waiting', attempt, token_sha, actor, on, '', idem, at)
  return { 'WAITING', S, id, tostring(attempt) }
end

-- ns_task_wake: the event that settles a key (the CI end at head, a person
-- answering). args = S, on, outcome (ok|dead), reason, actor, idem, fence.
-- ok puts every task waiting on the key back open at the front of its own
-- owner's queue; dead sends each to reconcile-required with an unresolved
-- item. Returns { 'WOKE', resumed, dead }.
local function task_wake(keys, args)
  local S, on, outcome, reason = args[1], args[2], args[3], args[4]
  local actor, idem, fence = args[5], args[6], args[7] or ''
  if not TW.holds(fence) then
    return { 'FENCED' }
  end
  if not TW.kind(on or '') then
    return { 'BADKEY' }
  end
  if outcome ~= 'ok' and outcome ~= 'dead' then
    return { 'INVALID' }
  end
  if reason == nil or reason == '' then
    reason = outcome
  end
  local at = TW.now_ms()
  local resumed, dead = 0, 0
  local ids = redis.call('SMEMBERS', 's:' .. S .. ':waiton:' .. on)
  table.sort(ids)
  for _, id in ipairs(ids) do
    local key = 's:' .. S .. ':task:' .. id
    if redis.call('HGET', key, 'state') == 'waiting' and redis.call('HGET', key, 'wait_on') == on then
      if outcome == 'ok' then
        TW.resume(S, id, key, actor, idem, at)
        resumed = resumed + 1
      else
        TW.dead(S, id, key, reason, actor, idem, at)
        dead = dead + 1
      end
    else
      redis.call('SREM', 's:' .. S .. ':waiton:' .. on, id)
    end
  end
  return { 'WOKE', tostring(resumed), tostring(dead) }
end

-- ns_task_wait_sweep: the reconciler's pass over s:<S>:idx:task:waiting.
-- dep: and read: keys resolve (or die) from Redis state; ci: and human: keys
-- wait for ns_task_wake. args = S, actor, idem, fence (the lease:reconciler
-- token, or empty for a direct caller). Returns { 'SWEPT', resumed, dead }.
local function task_wait_sweep(keys, args)
  local S, actor, idem, fence = args[1], args[2], args[3], args[4] or ''
  if not TW.holds(fence) then
    return { 'FENCED' }
  end
  local at = TW.now_ms()
  local resumed, dead = 0, 0
  local ids = redis.call('SMEMBERS', 's:' .. S .. ':idx:task:waiting')
  table.sort(ids)
  for _, id in ipairs(ids) do
    local key = 's:' .. S .. ':task:' .. id
    if redis.call('HGET', key, 'state') ~= 'waiting' then
      redis.call('SREM', 's:' .. S .. ':idx:task:waiting', id)
    else
      local verdict, why = TW.check(S, key, redis.call('HGET', key, 'wait_on') or '')
      if verdict == 'ok' then
        TW.resume(S, id, key, actor, idem, at)
        resumed = resumed + 1
      elseif verdict == 'dead' then
        TW.dead(S, id, key, why, actor, idem, at)
        dead = dead + 1
      end
    end
  end
  return { 'SWEPT', tostring(resumed), tostring(dead) }
end

redis.register_function('ns_task_wait', task_wait)
redis.register_function('ns_task_wake', task_wake)
redis.register_function('ns_task_wait_sweep', task_wait_sweep)
