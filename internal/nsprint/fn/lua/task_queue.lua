-- The one task store (#3206 rev 5 PR A): what bash friend-queue had and the
-- nova-sprint task verb lacked, as Redis Functions on s:<S>:* keys. No
-- shebang: loader.go prepends the library header. Each transition is one
-- Function call that checks its guard, moves the id between index sets and
-- appends its receipt to s:<S>:log, atomically.
--
-- Vocabulary (ruling nova-tools#3516, SPEC-NOTE on #3206 2026-09-24): there
-- is no blocked state and no block/unblock verb. A task carries DEPENDS-ON
-- conditions; while any is unmet it is `waiting` and is in no ready queue,
-- count or fill. Ready is computed from the dependencies: when the last one
-- is met the task goes to the queue it was pushed to (`dest`) at its own
-- priority and front flag.
--
-- Keys added here (spec (5)):
--   s:<S>:waits:<cond>   SET of ids waiting on cond (the release index)
--   friend:<f>:down      HASH {reason, actor, at}; the one writer is
--                        ns_friend_down (a legacy string marker is read too)
-- A dependency-waiting task is in s:<S>:idx:task:waiting (shared with the
-- #3090 lease wait, whose tasks carry wait_on and never depends waits_on) and
-- in friend:<dest>:waiting, so the width line counts it as waiting.
--
-- Conditions (the one DEPENDS-ON form, nova-tools#3409):
--   task:<id>            met when s:<S>:task:<id> is closed (done or closed)
--   key:<k>=<v>          met when GET k equals v
--   anything else        <owner>/<repo>#<n>, stream/<slug>, card:<id>, pr:,
--                        spec:, node: -- met only by ns_task_resolve with
--                        asserted=1 (the resolver duty reads the fact)

-- DEP is task_claim.lua's table (it sorts first); the functions below fill it.
local DEP = NS.DEP

local function tq_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function tq_receipt(S, kind, id, from_state, to_state, attempt, actor, for_friend, reason, evidence, idem, at)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
    'attempt', tostring(attempt or 0), 'token_sha', '',
    'actor', actor or '', 'for', for_friend or '', 'reason', reason or '', 'evidence', evidence or '',
    'idem', idem or '', 'at', tostring(at))
end

-- DEP.down returns the down marker's reason (true when it has none) or nil.
function DEP.down(friend)
  local key = 'friend:' .. friend .. ':down'
  local kind = redis.call('TYPE', key)
  if type(kind) == 'table' then
    kind = kind['ok']
  end
  if kind == 'string' then
    return redis.call('GET', key)
  end
  if kind == 'hash' then
    local reason = redis.call('HGET', key, 'reason')
    if not reason or reason == '' then
      return 'down'
    end
    return reason
  end
  return nil
end

-- DEP.parse splits a DEPENDS-ON list ("none", or conditions joined by ';' or
-- ','). It returns the conditions and, for a malformed one, that condition.
function DEP.parse(text)
  local out = {}
  if text == nil or text == '' or text == 'none' then
    return out, nil
  end
  local seen = {}
  for raw in string.gmatch(text, '[^;,]+') do
    local c = string.match(raw, '^%s*(.-)%s*$')
    if c ~= '' then
      if string.find(c, '%s') or not (string.find(c, ':') or string.find(c, '/') or string.find(c, '#')) then
        return out, c
      end
      if string.sub(c, 1, 4) == 'key:' and not string.match(c, '^key:[^=]+=.*$') then
        return out, c
      end
      if not seen[c] then
        seen[c] = true
        out[#out + 1] = c
      end
    end
  end
  return out, nil
end

-- DEP.holds reports whether a condition can be proven met from Redis alone.
function DEP.holds(S, c)
  if string.sub(c, 1, 5) == 'task:' then
    return redis.call('HGET', 's:' .. S .. ':task:' .. string.sub(c, 6), 'state') == 'closed'
  end
  if string.sub(c, 1, 4) == 'key:' then
    local k, v = string.match(c, '^key:([^=]+)=(.*)$')
    local kind = redis.call('TYPE', k)
    if type(kind) == 'table' then
      kind = kind['ok']
    end
    return kind == 'string' and redis.call('GET', k) == v
  end
  return false
end

function DEP.unmet(S, conds)
  local out = {}
  for _, c in ipairs(conds) do
    if not DEP.holds(S, c) then
      out[#out + 1] = c
    end
  end
  return out
end

-- DEP.score is a task's queue score: priority, negative at the front.
function DEP.score(front, priority)
  if front then
    return -priority
  end
  return priority
end

-- DEP.enqueue puts an open task on its queue: open:<dest>, or the ready pool.
function DEP.enqueue(S, id, dest, front, priority)
  if dest ~= '' then
    redis.call('ZADD', 's:' .. S .. ':open:' .. dest, DEP.score(front, priority), id)
  else
    redis.call('ZADD', 's:' .. S .. ':ready', DEP.score(front, priority), id)
  end
  redis.call('SADD', 's:' .. S .. ':idx:task:open', id)
end

-- DEP.open_location finds an open task's queue and score. Tasks pushed before
-- the one-store cutover have no dest field, so fall back to the registered
-- friend's queue that already contains the id. The friends set is bounded and
-- avoids the KEYS/SCAN commands unavailable to the function ACL.
function DEP.open_location(S, id, dest)
  local q = 's:' .. S .. ':ready'
  if dest ~= '' then
    q = 's:' .. S .. ':open:' .. dest
  end
  local score = redis.call('ZSCORE', q, id)
  if score or dest ~= '' then
    return dest, q, score
  end
  local friends = redis.call('SMEMBERS', 'friends')
  table.sort(friends)
  for _, friend in ipairs(friends) do
    q = 's:' .. S .. ':open:' .. friend
    score = redis.call('ZSCORE', q, id)
    if score then
      return friend, q, score
    end
  end
  return '', 's:' .. S .. ':ready', nil
end

-- DEP.dequeue removes an open task from every queue it can be in.
function DEP.dequeue(S, id, dest)
  if dest ~= '' then
    redis.call('ZREM', 's:' .. S .. ':open:' .. dest, id)
  end
  redis.call('ZREM', 's:' .. S .. ':ready', id)
  redis.call('SREM', 's:' .. S .. ':idx:task:open', id)
end

-- DEP.wait makes a task waiting on its unmet conditions.
function DEP.wait(S, id, key, dest, unmet, at)
  redis.call('HSET', key, 'state', 'waiting', 'waits_on', table.concat(unmet, ';'),
    'wait_on', '', 'wait_since', tostring(at))
  redis.call('SADD', 's:' .. S .. ':idx:task:waiting', id)
  for _, c in ipairs(unmet) do
    redis.call('SADD', 's:' .. S .. ':waits:' .. c, id)
  end
  if dest ~= '' then
    redis.call('ZADD', 'friend:' .. dest .. ':waiting', at, S .. '/' .. id)
  end
end

-- DEP.unwait drops a dependency-waiting task from every waiting index.
function DEP.unwait(S, id, key)
  local dest = redis.call('HGET', key, 'dest') or ''
  for c in string.gmatch(redis.call('HGET', key, 'waits_on') or '', '[^;]+') do
    redis.call('SREM', 's:' .. S .. ':waits:' .. c, id)
  end
  redis.call('SREM', 's:' .. S .. ':idx:task:waiting', id)
  if dest ~= '' then
    redis.call('ZREM', 'friend:' .. dest .. ':waiting', S .. '/' .. id)
  end
end

-- DEP.release: waiting -> open on the queue it was pushed to.
function DEP.release(S, id, key, actor, idem, at)
  DEP.unwait(S, id, key)
  local dest = redis.call('HGET', key, 'dest') or ''
  local front = redis.call('HGET', key, 'front') == '1'
  local priority = tonumber(redis.call('HGET', key, 'priority') or '0') or 0
  redis.call('HSET', key, 'state', 'open', 'waits_on', '', 'ready_at', tostring(at))
  DEP.enqueue(S, id, dest, front, priority)
  tq_receipt(S, 'task ready', id, 'waiting', 'open', redis.call('HGET', key, 'attempt'),
    actor, dest, 'depends-on met', redis.call('HGET', key, 'depends_on') or '', idem, at)
end

-- DEP.resolve settles one condition for every task waiting on it. A task:
-- or key: condition is re-checked here; any other kind is met only when the
-- caller asserts it. A task whose last unmet condition goes is released.
-- Returns the number of tasks made ready.
function DEP.resolve(S, cond, asserted, actor, idem, at)
  local met = DEP.holds(S, cond) or (asserted and string.sub(cond, 1, 5) ~= 'task:' and string.sub(cond, 1, 4) ~= 'key:')
  if not met then
    return 0
  end
  local ready = 0
  local ids = redis.call('SMEMBERS', 's:' .. S .. ':waits:' .. cond)
  table.sort(ids)
  for _, id in ipairs(ids) do
    local key = 's:' .. S .. ':task:' .. id
    redis.call('SREM', 's:' .. S .. ':waits:' .. cond, id)
    if redis.call('HGET', key, 'state') == 'waiting' then
      local left = {}
      for c in string.gmatch(redis.call('HGET', key, 'waits_on') or '', '[^;]+') do
        if c ~= cond then
          left[#left + 1] = c
        end
      end
      if #left == 0 then
        DEP.release(S, id, key, actor, idem, at)
        ready = ready + 1
      else
        redis.call('HSET', key, 'waits_on', table.concat(left, ';'))
      end
    end
  end
  return ready
end

-- DEP.free_lease ends a claimed or working lease without closing the task:
-- the token is fenced (the old done and beat refuse FENCED), the identity
-- leaves the owner's starting/living and the slot is freed on cap:log.
function DEP.free_lease(S, id, key, state, at)
  local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
  local owner = redis.call('HGET', key, 'owner') or ''
  local identity = S .. '/' .. id .. '/' .. attempt
  redis.call('HSET', key, 'token', 'fenced')
  redis.call('ZREM', 'friend:' .. owner .. ':starting', identity)
  redis.call('ZREM', 'friend:' .. owner .. ':living', identity)
  redis.call('SREM', 's:' .. S .. ':idx:task:' .. state, id)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', 'slot-freed', 'consumer', 'friend:' .. owner,
    'sprint', S, 'id', id, 'attempt', tostring(attempt), 'at', tostring(at))
end

-- ns_task_move: re-own a task (friend-queue push --move, rowan-tools #268).
-- args = S, id, to, actor, idem. An open task moves between queues at its
-- score; a dependency-waiting task changes dest. A claimed or working task
-- moves only when its owner is down: its lease is fenced (the old owner's
-- done gets FENCED) and it reopens on open:<to>. A down or unknown target is
-- refused with nothing written.
local function task_move(keys, args)
  local S, id, to, actor, idem = args[1], args[2], args[3], args[4], args[5]
  local key = 's:' .. S .. ':task:' .. id
  if to == nil or to == '' then
    return { 'INVALID', 'to is required' }
  end
  local down = DEP.down(to)
  if down then
    return { 'DOWN', down }
  end
  if redis.call('SISMEMBER', 'friends', to) == 0 then
    return { 'INVALID', 'unknown friend' }
  end
  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  local state = redis.call('HGET', key, 'state')
  local dest = redis.call('HGET', key, 'dest') or ''
  local priority = tonumber(redis.call('HGET', key, 'priority') or '0') or 0
  local at = tq_now_ms()

  if state == 'open' then
    local q, score
    dest, q, score = DEP.open_location(S, id, dest)
    if not score then
      return { 'INVALID', 'not on its queue' }
    end
    if dest == to then
      return { 'SAME' }
    end
    redis.call('ZREM', q, id)
    redis.call('ZADD', 's:' .. S .. ':open:' .. to, score, id)
    redis.call('HSET', key, 'dest', to, 'moved_from', dest, 'moved_at', tostring(at))
    tq_receipt(S, 'task move', id, 'open', 'open', redis.call('HGET', key, 'attempt'), actor, to, 'from ' .. dest, '', idem, at)
    return { 'MOVED', dest }
  end

  if state == 'waiting' and (redis.call('HGET', key, 'wait_on') or '') == '' then
    if dest == to then
      return { 'SAME' }
    end
    if dest ~= '' then
      redis.call('ZREM', 'friend:' .. dest .. ':waiting', S .. '/' .. id)
    end
    redis.call('ZADD', 'friend:' .. to .. ':waiting', at, S .. '/' .. id)
    redis.call('HSET', key, 'dest', to, 'moved_from', dest, 'moved_at', tostring(at))
    tq_receipt(S, 'task move', id, 'waiting', 'waiting', redis.call('HGET', key, 'attempt'), actor, to, 'from ' .. dest, '', idem, at)
    return { 'MOVED', dest }
  end

  if state == 'claimed' or state == 'working' then
    local owner = redis.call('HGET', key, 'owner') or ''
    if not DEP.down(owner) then
      return { 'LEASED', owner }
    end
    DEP.free_lease(S, id, key, state, at)
    redis.call('HSET', key, 'state', 'open', 'owner', '', 'dest', to,
      'moved_from', owner, 'moved_at', tostring(at))
    DEP.enqueue(S, id, to, redis.call('HGET', key, 'front') == '1', priority)
    tq_receipt(S, 'task move', id, state, 'open', redis.call('HGET', key, 'attempt'), actor, to, 'owner down: ' .. owner, '', idem, at)
    return { 'MOVED', owner }
  end
  return { 'INVALID', 'state ' .. (state or '') }
end

-- ns_task_close: an open or dependency-waiting task goes to closed with
-- cancelled=1 (friend-queue cancel). args = S, id, evidence, actor, idem.
-- A lease is given back with `task cancel --token`, never closed here. Tasks
-- waiting on task:<id> are released in the same call when their last
-- condition is met.
local function task_close(keys, args)
  local S, id, evidence, actor, idem = args[1], args[2], args[3], args[4], args[5]
  local key = 's:' .. S .. ':task:' .. id
  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  if evidence == nil or evidence == '' then
    return { 'NOEVIDENCE' }
  end
  local state = redis.call('HGET', key, 'state')
  if state == 'closed' or state == 'cancelled' then
    return { 'CLOSED' }
  end
  local dest = redis.call('HGET', key, 'dest') or ''
  if state == 'open' then
    dest = DEP.open_location(S, id, dest)
    DEP.dequeue(S, id, dest)
  elseif state == 'waiting' then
    local on = redis.call('HGET', key, 'wait_on') or ''
    if on ~= '' then
      redis.call('SREM', 's:' .. S .. ':waiton:' .. on, id)
      redis.call('ZREM', 'friend:' .. (redis.call('HGET', key, 'owner') or '') .. ':waiting', S .. '/' .. id)
    end
    DEP.unwait(S, id, key)
  else
    return { 'LEASED', state or '' }
  end
  local at = tq_now_ms()
  redis.call('HSET', key, 'state', 'closed', 'cancelled', '1', 'evidence', evidence,
    'waits_on', '', 'closed_at', tostring(at))
  redis.call('SADD', 's:' .. S .. ':idx:task:closed', id)
  local who = redis.call('HGET', key, 'owner') or ''
  if who == '' then
    who = dest
  end
  if who ~= '' then
    redis.call('SADD', 's:' .. S .. ':done:' .. who, id)
  end
  tq_receipt(S, 'task close', id, state, 'closed', redis.call('HGET', key, 'attempt'), actor, who, 'cancelled', evidence, idem, at)
  local ready = DEP.resolve(S, 'task:' .. id, false, actor, idem, at)
  return { 'CLOSED-NOW', tostring(ready) }
end

-- ns_task_front: put a task at the front of its friend's queue.
-- args = S, id, as, actor, idem.
local function task_front(keys, args)
  local S, id, as, actor, idem = args[1], args[2], args[3], args[4], args[5]
  local key = 's:' .. S .. ':task:' .. id
  local q = 's:' .. S .. ':open:' .. as
  local score = redis.call('ZSCORE', q, id)
  if not score then
    return { 'NONE' }
  end
  local first = redis.call('ZRANGE', q, 0, 0, 'WITHSCORES')
  if first[1] == id and redis.call('HGET', key, 'front') == '1' then
    return { 'SAME' }
  end
  local at = tq_now_ms()
  local top = tonumber(first[2])
  local new = top
  if first[1] ~= id then
    new = top - 1
  end
  redis.call('ZADD', q, new, id)
  redis.call('HSET', key, 'front', '1')
  tq_receipt(S, 'task front', id, 'open', 'open', redis.call('HGET', key, 'attempt'), actor, as, '', '', idem, at)
  return { 'FRONT' }
end

-- ns_task_depends: a task gains DEPENDS-ON conditions after its push. args =
-- S, id, conds, as, token, actor, idem. An open task leaves its queue; a
-- claimed or working lease (its token, or --as its owner) is ended, fenced and
-- its slot freed. Either way the task is waiting until every condition is
-- met, then it is ready on its queue (the owner's, for a lease). When every
-- condition already holds nothing is written (MET).
local function task_depends(keys, args)
  local S, id, text, as, token, actor, idem = args[1], args[2], args[3], args[4], args[5], args[6], args[7]
  local key = 's:' .. S .. ':task:' .. id
  local conds, bad = DEP.parse(text)
  if bad then
    return { 'INVALID', 'depends-on ' .. bad }
  end
  if #conds == 0 then
    return { 'INVALID', 'depends-on is empty' }
  end
  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  local state = redis.call('HGET', key, 'state')
  if state ~= 'open' and state ~= 'claimed' and state ~= 'working' then
    return { 'INVALID', 'state ' .. (state or '') }
  end
  if state ~= 'open' then
    if token ~= '' then
      if redis.call('HGET', key, 'token') ~= token then
        return { 'FENCED' }
      end
    elseif as == '' or redis.call('HGET', key, 'owner') ~= as then
      return { 'FENCED' }
    end
  end
  local unmet = DEP.unmet(S, conds)
  if #unmet == 0 then
    return { 'MET' }
  end
  local at = tq_now_ms()
  local dest = redis.call('HGET', key, 'dest') or ''
  if state == 'open' then
    DEP.dequeue(S, id, dest)
  else
    DEP.free_lease(S, id, key, state, at)
    dest = redis.call('HGET', key, 'owner') or ''
    redis.call('HSET', key, 'owner', '', 'dest', dest)
  end
  local all = {}
  local seen = {}
  for c in string.gmatch(redis.call('HGET', key, 'depends_on') or '', '[^;]+') do
    if not seen[c] then
      seen[c] = true
      all[#all + 1] = c
    end
  end
  for _, c in ipairs(conds) do
    if not seen[c] then
      seen[c] = true
      all[#all + 1] = c
    end
  end
  redis.call('HSET', key, 'depends_on', table.concat(all, ';'))
  DEP.wait(S, id, key, dest, unmet, at)
  tq_receipt(S, 'task depends', id, state, 'waiting', redis.call('HGET', key, 'attempt'), actor, dest,
    'depends-on ' .. table.concat(unmet, ';'), '', idem, at)
  return { 'WAITING', tostring(#unmet) }
end

-- ns_task_resolve: the event entry for one condition (land-lane after a merge,
-- the resolver duty after a fact read). args = S, cond, asserted (1 when the
-- caller has read the external fact), actor, idem. Returns READY <n>.
local function task_resolve(keys, args)
  local S, cond, asserted, actor, idem = args[1], args[2], args[3] == '1', args[4], args[5]
  local conds, bad = DEP.parse(cond)
  if bad or #conds ~= 1 then
    return { 'INVALID', 'want one condition' }
  end
  local n = DEP.resolve(S, conds[1], asserted, actor, idem, tq_now_ms())
  return { 'READY', tostring(n) }
end

-- ns_friend_down: set or clear friend:<f>:down (HASH {reason, actor, at}).
-- args = friend, on (1|0), reason, actor, idem. One cap:log receipt per
-- write; a repeat writes nothing (SAME).
local function friend_down(keys, args)
  local f, on, reason, actor, idem = args[1], args[2] == '1', args[3] or '', args[4] or '', args[5] or ''
  if f == nil or f == '' then
    return { 'INVALID' }
  end
  local key = 'friend:' .. f .. ':down'
  local current = DEP.down(f)
  if on and current and current == (reason ~= '' and reason or 'down') then
    return { 'SAME' }
  end
  if not on and not current then
    return { 'SAME' }
  end
  local at = tq_now_ms()
  redis.call('DEL', key)
  if on then
    redis.call('HSET', key, 'reason', reason, 'actor', actor, 'at', tostring(at))
  end
  local kind = 'friend-up'
  if on then
    kind = 'friend-down'
  end
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', kind, 'consumer', 'friend:' .. f, 'reason', reason,
    'actor', actor, 'idem', idem, 'at', tostring(at))
  if on then
    return { 'DOWN' }
  end
  return { 'UP' }
end

redis.register_function('ns_task_move', task_move)
redis.register_function('ns_task_close', task_close)
redis.register_function('ns_task_front', task_front)
redis.register_function('ns_task_depends', task_depends)
redis.register_function('ns_task_resolve', task_resolve)
redis.register_function('ns_friend_down', friend_down)
