-- The one task store (#3206 rev 5 PR A): what bash friend-queue had and the
-- nova-sprint task verb lacked, as Redis Functions. No shebang: loader.go
-- prepends the library header. Each transition is one Function call that
-- checks its guard, moves the task through the one task move (NS.task,
-- 02_card_move.lua: task:<id>, its where, its sets, the sprint's idx sets and
-- ready queues; nova-tools #3778) and appends its receipt to s:<S>:log,
-- atomically. Nothing here writes a task's state or a queue itself.
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
--   task:<id>            met by the one dependency rule (NS.dep, 01_dep.lua):
--                        task:<id> landed, or done/ok (closed); a stream
--                        sentinel only when landed. A bare sentinel id
--                        (<slug>:sentinel) is stored as task:<slug>:sentinel.
--                        The move that meets it releases its waiters
--                        (TK.release_waiters, 02_card_move.lua).
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
      -- a stream's sentinel named bare is the task edge task:<slug>:sentinel
      if NS.dep.is_sentinel(c) then c = 'task:' .. c end
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
    return NS.dep.met(string.sub(c, 6))
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

-- DEP.wait records the unmet conditions of a task the one move has just
-- made waiting: the release index and the width line's friend waiting set.
function DEP.wait(S, id, key, dest, unmet, at)
  redis.call('HSET', key, 'waits_on', table.concat(unmet, ';'), 'wait_on', '', 'wait_since', tostring(at))
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
  if dest ~= '' then
    redis.call('ZREM', 'friend:' .. dest .. ':waiting', S .. '/' .. id)
  end
end

-- DEP.release: waiting -> open on the queue it was pushed to.
function DEP.release(S, id, key, actor, idem, at)
  DEP.unwait(S, id, key)
  local dest = redis.call('HGET', key, 'dest') or ''
  NS.task.set(id, 'open', { sprint = S, by = actor, why = 'depends-on met',
    fields = { 'waits_on', '', 'ready_at', tostring(at) } })
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
    local key = 'task:' .. id
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
-- leaves the owner's working set (NS.moves.drop) and the slot is freed on
-- cap:log; the caller's one move takes the task out of working.
function DEP.free_lease(S, id, key, state, at)
  local attempt = tonumber(redis.call('HGET', key, 'attempt') or '0')
  local owner = redis.call('HGET', key, 'owner') or ''
  local identity = S .. '/' .. id .. '/' .. attempt
  redis.call('HSET', key, 'token', 'fenced')
  NS.moves.drop('friend:' .. owner, identity)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', 'slot-freed', 'consumer', 'friend:' .. owner,
    'sprint', S, 'id', id, 'attempt', tostring(attempt), 'at', tostring(at))
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

redis.register_function('ns_friend_down', friend_down)

-- The move that meets a dependency (landed, or done/ok) releases its waiters
-- through DEP.resolve (TK.release_waiters, 02_card_move.lua).
NS.task.on_met(DEP.resolve)
