-- The reconciler's ws-index calls (nova-tools #3873, #4059): the deal that
-- moves a ready task to working, and the note a waiting task with no
-- consumer carries. Called only by the reconciler's duties
-- (internal/nsprint/reconcile/deal_friend.go and waiting_resolve.go), which
-- read the consumers once per tick and hand each consumer its batch.
--
--   ns_deal_friend(token, friend, by, why, id...)
--     -> DEALT took open refused, then (id, why) per refused id | FENCED
--   ns_deal_swarm(token, by, why, id...)
--     -> DEALT took refused, then (id, why) per refused id | FENCED
--   ns_ws_note(token, by, why, id...)
--     -> NOTED noted same refused, then (id, why) per refused id | FENCED
--
-- Glenn 2026-09-25 1:40 PM ET: waiting -> ready is ONE WAY, and a ready card
-- is distributed at once to a friend queue or the swarm and moves to
-- working; ready is never a resting state. So there is no return here: the
-- waiting-resolve duty moves a card to ready only when it has a consumer,
-- and a card whose dependency is met but has none stays in waiting with the
-- note, written once.
--
-- Every move is the one move, NS.task.move (lua/02_card_move.lua, #3778):
-- the double link is checked before any write, the ws set, the holder's
-- friend:<f>:cards:working view, the record's pointer and owner and the
-- ws:log receipt change in that one call, and the score stays the card's
-- created_at. Nothing here writes a task set or a pointer field itself.
-- Every call is fenced on lease:reconciler.
local WD = {
  LOG_MAX = '200000',
}

function WD.fenced(token)
  return token == nil or token == '' or redis.call('HGET', 'lease:reconciler', 'token') ~= token
end

-- WD.slots is the friend's slots: friend:<f>:slots, else friend:<f>:desired
-- slots; nil when neither is a number.
function WD.slots(f)
  local v = tonumber(redis.call('GET', 'friend:' .. f .. ':slots') or '')
  if not v then
    v = tonumber(redis.call('HGET', 'friend:' .. f .. ':desired', 'slots') or '')
  end
  return v
end

function WD.reply(head, refused)
  for _, v in ipairs(refused) do
    head[#head + 1] = v
  end
  return head
end

-- WD.now is the server clock in ms.
function WD.now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- WD.where is the record's where (a record from before the where field:
-- its state read through NS.task.where_of), and the record; nil and the
-- refusal when there is no task with a stream.
function WD.where(id)
  local p = NS.task.read(id)
  if not p or p.stream == '' then
    return nil, 'no task ' .. id .. ' with a stream'
  end
  local where = p.where
  if not p.placed then where = NS.task.where_of[p.state] or p.state end
  return where, p
end

-- WD.take moves one ready id to working for holder through the one move;
-- nil when moved, else the refusal.
function WD.take(id, holder, by, why)
  local where, p = WD.where(id)
  if not where then
    return p
  end
  if where ~= 'ready' then
    return 'task ' .. id .. ' is ' .. tostring(where) .. ', not ready'
  end
  if p.friend ~= '' and p.friend ~= holder then
    return 'task ' .. id .. ' is owned by ' .. p.friend
  end
  return NS.task.move(id, 'working', { by = by, why = why, friend = holder, as = holder })
end

-- ns_deal_friend: up to the friend's open slots (slots minus ZCARD
-- friend:<f>:cards:working, re-read here so two passes racing never fill a
-- friend past its slots), in the order given, ready -> working with owner the
-- friend. An id past the open slots is refused `full`.
local function deal_friend(keys, args)
  local token, f, by, why = args[1], args[2], args[3], args[4]
  if WD.fenced(token) then
    return { 'FENCED' }
  end
  if not f or f == '' or f == 'swarm' then
    return { 'REFUSED', 'no friend' }
  end
  local slots = WD.slots(f)
  if not slots then
    return { 'REFUSED', 'friend ' .. f .. ' has no slots' }
  end
  local open = slots - redis.call('ZCARD', 'friend:' .. f .. ':cards:working')
  if open < 0 then
    open = 0
  end
  local took, refused = 0, {}
  for i = 5, #args do
    local id = args[i]
    local err = 'full'
    if took < open then
      err = WD.take(id, f, by, why)
    end
    if err then
      refused[#refused + 1] = id
      refused[#refused + 1] = err
    else
      took = took + 1
    end
  end
  return WD.reply({ 'DEALT', took, open, #refused / 2 }, refused)
end

-- ns_deal_swarm: ready -> working with owner swarm, for a task whose route
-- names the swarm and that no friend seat took this tick (friend queue up to
-- width, else swarm by route). The swarm has no slots here: its benches'
-- width is the dealer's (internal/nsprint/deal).
local function deal_swarm(keys, args)
  local token, by, why = args[1], args[2], args[3]
  if WD.fenced(token) then
    return { 'FENCED' }
  end
  local took, refused = 0, {}
  for i = 4, #args do
    local err = WD.take(args[i], 'swarm', by, why)
    if err then
      refused[#refused + 1] = args[i]
      refused[#refused + 1] = err
    else
      took = took + 1
    end
  end
  return WD.reply({ 'DEALT', took, #refused / 2 }, refused)
end

-- ns_ws_note: why on a waiting task that stays waiting (no-consumer), with
-- one ws:log entry (from waiting, to waiting) when the why CHANGES; a task
-- already carrying it is `same` and nothing is written, so a card the
-- resolve finds consumer-less every tick is noted once. The task must be
-- in ws:<stream>:waiting, the set its record names; anything else is refused
-- by name and nothing is written for it. This is a note, not a move: the
-- task's set and state do not change.
local function ws_note(keys, args)
  local token, by, why = args[1], args[2], args[3]
  if WD.fenced(token) then
    return { 'FENCED' }
  end
  if not why or why == '' then
    return { 'REFUSED', 'no why' }
  end
  local now, noted, same, refused = WD.now(), 0, 0, {}
  for i = 4, #args do
    local id = args[i]
    local where, p = WD.where(id)
    local err
    if not where then
      err = p
    elseif where ~= 'waiting' then
      err = 'task ' .. id .. ' is ' .. tostring(where) .. ', not waiting'
    elseif not redis.call('ZSCORE', 'ws:' .. p.stream .. ':waiting', id) then
      err = 'task ' .. id .. ' says waiting but is not in ws:' .. p.stream .. ':waiting'
    end
    if err then
      refused[#refused + 1] = id
      refused[#refused + 1] = err
    elseif redis.call('HGET', 'task:' .. id, 'why') == why then
      same = same + 1
    else
      redis.call('HSET', 'task:' .. id, 'why', why, 'why_at', tostring(now))
      redis.call('XADD', 'ws:log', 'MAXLEN', '~', WD.LOG_MAX, '*', 'id', id, 'stream', p.stream,
        'from', 'waiting', 'to', 'waiting', 'by', by or '', 'why', why, 'at', tostring(now))
      noted = noted + 1
    end
  end
  return WD.reply({ 'NOTED', noted, same, #refused / 2 }, refused)
end

redis.register_function('ns_deal_friend', deal_friend)
redis.register_function('ns_deal_swarm', deal_swarm)
redis.register_function('ns_ws_note', ws_note)
NS.ws_deal = WD
