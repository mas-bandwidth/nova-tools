-- The friend deal (nova-tools #3873): ready -> working to a friend, and a
-- card no live consumer may take back to waiting. Called only by the
-- reconciler's deal duty (internal/nsprint/reconcile/deal_friend.go), which
-- reads every ws:<stream>:ready in ws:order rank order, honours WHO and kind,
-- and hands each friend its batch in one call per tick.
--
--   ns_deal_friend(token, friend, by, why, id...)
--     -> DEALT took open refused, then (id, why) per refused id | FENCED
--   ns_deal_return(token, by, why, id...)
--     -> RETURNED moved refused, then (id, why) per refused id | FENCED
--   ns_friend_rebalance(token, friend, by, stale, target...)   (#4145)
--     -> REBALANCED moved refused, then (id, dest) per move, then (id, why)
--        per refused id | REFUSED reason, then (id, why) per refused id |
--        UP | FENCED
--
-- Keys (rowan-new specs/ws-index.md, "Tasks are cards"):
--   task:<id>                    HASH stream state created_at owner why ...
--   ws:<stream>:<state>          ZSET id -> created_at ms (waiting, ready,
--                                working, merging, landed, parked)
--   friend:<f>:cards:working     ZSET id -> created_at ms, the friend's working
--   friend:<f>:slots             STRING the friend's slots (else the slots
--                                field of friend:<f>:desired)
--   ws:log                       STREAM one entry per move
--
-- ONE PLACE: before any write each id must be in ws:<stream>:ready, the set
-- its record names, and in none of the stream's other sets; a mismatch is a
-- refusal by name and nothing is written for that id. Every write is the
-- one move, NS.task.move (lua/02_card_move.lua, #3778): the ws set, the
-- friend set, the record's pointer and owner and the ws:log receipt change
-- in that one call, the score stays the card's created_at. The slots are
-- re-read inside the call, so two passes racing never fill a friend past its
-- slots. Every call is fenced on lease:reconciler.
local DF = {
  STATES = { 'waiting', 'ready', 'working', 'merging', 'landed', 'parked' },
}

function DF.fenced(token)
  return token == nil or token == '' or redis.call('HGET', 'lease:reconciler', 'token') ~= token
end

-- DF.key: the stream's set under the current epoch (nova-tools#4238).
function DF.key(stream, state)
  return NS.card.wskey(NS.card.epoch(), stream, state)
end


-- DF.slots is the friend's slots: friend:<f>:slots, else friend:<f>:desired
-- slots; nil when neither is a number.
function DF.slots(f)
  local v = tonumber(redis.call('GET', 'friend:' .. f .. ':slots') or '')
  if not v then
    v = tonumber(redis.call('HGET', 'friend:' .. f .. ':desired', 'slots') or '')
  end
  return v
end

-- DF.ci is the CI legs running on the machine friend f's session runs on,
-- as its beat counts them (friend:<f>:beat ci, nova-tools#4293: the Studio
-- hosts friends and CI both); 0 when the beat carries none. Each holds a
-- slot while it runs.
function DF.ci(f)
  return tonumber(redis.call('HGET', 'friend:' .. f .. ':beat', 'ci')) or 0
end

-- DF.ready checks the double link of one id that must be ready. It returns
-- the stream and the score (the card's age), or nil and the refusal. Ready
-- is the record's where (a record from before the where field: its state
-- read through NS.task.where_of).
function DF.ready(id)
  local p = NS.task.read(id)
  local stream = p and p.stream or ''
  if stream == '' then
    return nil, 'no task ' .. id .. ' with a stream'
  end
  local where = p.where
  if not p.placed then where = NS.task.where_of[p.state] or p.state end
  if where ~= 'ready' then
    return nil, 'task ' .. id .. ' is ' .. tostring(where) .. ', not ready'
  end
  local age = redis.call('ZSCORE', DF.key(stream, 'ready'), id)
  if not age then
    return nil, 'task ' .. id .. ' says ready but is not in ' .. DF.key(stream, 'ready')
  end
  for _, st in ipairs(DF.STATES) do
    if st ~= 'ready' and redis.call('ZSCORE', DF.key(stream, st), id) then
      return nil, 'task ' .. id .. ' says ready but is also in ' .. DF.key(stream, st)
    end
  end
  return stream, age
end

function DF.reply(head, refused)
  for _, v in ipairs(refused) do
    head[#head + 1] = v
  end
  return head
end

-- ns_deal_friend: up to the friend's open slots (slots minus ZCARD
-- friend:<f>:cards:working), in the order given, ready -> working with owner
-- the friend. An id past the open slots is refused `full`; one another
-- friend owns is refused by name.
local function deal_friend(keys, args)
  local token, f, by, why = args[1], args[2], args[3], args[4]
  if DF.fenced(token) then
    return { 'FENCED' }
  end
  if not f or f == '' then
    return { 'REFUSED', 'no friend' }
  end
  local slots = DF.slots(f)
  if not slots then
    return { 'REFUSED', 'friend ' .. f .. ' has no slots' }
  end
  local fk = 'friend:' .. f .. ':cards:working'
  local open = slots - DF.ci(f) - redis.call('ZCARD', fk)
  if open < 0 then
    open = 0
  end
  local took, refused = 0, {}
  for i = 5, #args do
    local id = args[i]
    local stream, age = DF.ready(id)
    local p = NS.task.read(id)
    local owner = p and p.friend or ''
    if took >= open then
      stream, age = nil, 'full'
    elseif stream and owner ~= '' and owner ~= f then
      stream, age = nil, 'task ' .. id .. ' is owned by ' .. owner
    end
    if stream then
      local err = NS.task.move(id, 'working', { by = by, why = why, friend = f, as = f })
      if err then stream, age = nil, err end
    end
    if not stream then
      refused[#refused + 1] = id
      refused[#refused + 1] = age
    else
      took = took + 1
    end
  end
  return DF.reply({ 'DEALT', took, open, #refused / 2 }, refused)
end

-- ns_deal_return: ready -> waiting with why on the record (Ready must be
-- READY: a card no live consumer may take never stays in ready).
local function deal_return(keys, args)
  local token, by, why = args[1], args[2], args[3]
  if DF.fenced(token) then
    return { 'FENCED' }
  end
  local moved, refused = 0, {}
  for i = 4, #args do
    local id = args[i]
    local stream, age = DF.ready(id)
    if stream then
      local err = NS.task.move(id, 'waiting', { by = by, why = why })
      if err then stream, age = nil, err end
    end
    if not stream then
      refused[#refused + 1] = id
      refused[#refused + 1] = age
    else
      moved = moved + 1
    end
  end
  return DF.reply({ 'RETURNED', moved, #refused / 2 }, refused)
end

-- DF.admits is the deal duty's WhoAdmits (internal/nsprint/reconcile): WHO
-- is any | only a,b | except a,b (the #3409 form), empty is any, names
-- compare case insensitively, an unreadable WHO admits no one.
function DF.admits(who, g)
  local w = string.lower(string.match(who or '', '^%s*(.-)%s*$'))
  if w == '' or w == 'any' then return true end
  local mode, list = string.match(w, '^(%S+)%s+(.*)$')
  if not mode then mode, list = w, '' end
  local c = string.lower(g)
  local found = false
  for n in string.gmatch(list, '[^,]+') do
    if string.match(n, '^%s*(.-)%s*$') == c then found = true end
  end
  if mode == 'only' then return found end
  if mode == 'except' then return not found end
  return false
end

-- DF.open is g's open slots for a moved card: its slots minus the CI legs
-- on its machine (DF.ci) minus its working and ready sets (a queue is
-- never filled past its slots); nil when g has no slots or is down.
function DF.open(g)
  if redis.call('EXISTS', 'friend:' .. g .. ':down') == 1 then return nil end
  local slots = DF.slots(g)
  if not slots then return nil end
  return slots - DF.ci(g) - redis.call('ZCARD', 'friend:' .. g .. ':cards:working') -
    redis.call('ZCARD', 'friend:' .. g .. ':cards:ready')
end

-- DF.rebalance moves a down friend f's cards (nova-tools #4145): every id in
-- friend:<f>:cards:ready, oldest first, and every id in
-- friend:<f>:cards:working whose lease_until has passed. Each goes to ready
-- on the queue of the target (a name in targets, the friends the caller
-- judged up) with the most open slots that the deal's rules admit (WHO; a
-- read never to its author and only to a member of `readers` when that set
-- is not empty), names breaking ties in the order given; with no such target
-- it goes back to its stream's ready set, unowned, why=no-consumer, where
-- the deal duty deals it or returns it to waiting. Every move is the one
-- move, NS.task.move. It returns the moves (id, dest; dest `ready` for the
-- stream's set), the refusals (id, why) and the size of f's ready set.
function DF.rebalance(f, targets, by)
  local now = redis.call('TIME')
  now = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
  local readers = {}
  local nreaders = 0
  for _, r in ipairs(redis.call('SMEMBERS', 'readers')) do
    readers[r] = true
    nreaders = nreaders + 1
  end
  local open = {}
  for _, g in ipairs(targets) do
    if g ~= f and open[g] == nil then open[g] = DF.open(g) or false end
  end
  local moves, refused = {}, {}
  local function one(id, why)
    local p = NS.task.read(id)
    if not p then
      refused[#refused + 1] = id
      refused[#refused + 1] = 'no task ' .. id
      return
    end
    local rec = redis.call('HMGET', 'task:' .. id, 'who', 'author')
    local who, author = rec[1] or '', rec[2] or ''
    local best, best_open = nil, 0
    for _, g in ipairs(targets) do
      local n = open[g]
      if n and n > best_open and DF.admits(who, g) and
          (p.kind ~= 'read' or (g ~= author and (nreaders == 0 or readers[g]))) then
        best, best_open = g, n
      end
    end
    local err
    if best then
      err = NS.task.move(id, 'ready', { friend = best, by = by, why = why })
    elseif p.stream == '' then
      err = 'no stream and no friend with open slots: nowhere but ' .. f .. "'s queue"
    else
      err = NS.task.move(id, 'ready', { friend = '', by = by, why = 'no-consumer' })
    end
    if err then
      refused[#refused + 1] = id
      refused[#refused + 1] = err
      return
    end
    if best then open[best] = open[best] - 1 end
    moves[#moves + 1] = id
    moves[#moves + 1] = best or 'ready'
  end
  local ready = redis.call('ZRANGE', 'friend:' .. f .. ':cards:ready', 0, -1)
  for _, id in ipairs(ready) do
    one(id, 'rebalance: ' .. f .. ' down')
  end
  for _, id in ipairs(redis.call('ZRANGE', 'friend:' .. f .. ':cards:working', 0, -1)) do
    local lease = tonumber(redis.call('HGET', 'task:' .. id, 'lease_until') or '')
    if lease and lease < now then
      one(id, 'lease lapsed: ' .. f .. ' down')
    end
  end
  return moves, refused, #ready
end

-- DF.rebalance_reply is ns_friend_rebalance's reply: REFUSED with the reason
-- when f's ready set was not empty and nothing moved (moved=0 is never
-- silent), else REBALANCED.
function DF.rebalance_reply(f, moves, refused, nready)
  if nready > 0 and #moves == 0 then
    local reason = f .. ' is down with ' .. nready .. ' ready and none moved'
    if #refused > 0 then reason = reason .. ': ' .. refused[2] end
    return DF.reply({ 'REFUSED', reason }, refused)
  end
  local out = { 'REBALANCED', #moves / 2, #refused / 2 }
  for _, v in ipairs(moves) do out[#out + 1] = v end
  return DF.reply(out, refused)
end

-- ns_friend_rebalance: the reconciler's deal duty calls it in the pass that
-- sees friend f change status, and `nova-sprint friend down|up` right after
-- it writes friend:<f>:down. f is down when friend:<f>:down exists or stale
-- is '1' (the caller saw f's beat older than the down window); an up friend
-- keeps its queue (UP) and the next deal fills it. token is the reconciler
-- lease's (fenced); the verb, which holds no lease, passes ''.
local function friend_rebalance(keys, args)
  local token, f, by, stale = args[1], args[2], args[3], args[4]
  if token ~= '' and DF.fenced(token) then
    return { 'FENCED' }
  end
  if not f or f == '' then
    return { 'REFUSED', 'no friend' }
  end
  if stale ~= '1' and redis.call('EXISTS', 'friend:' .. f .. ':down') == 0 then
    return { 'UP' }
  end
  local targets = {}
  for i = 5, #args do targets[#targets + 1] = args[i] end
  local moves, refused, nready = DF.rebalance(f, targets, by)
  return DF.rebalance_reply(f, moves, refused, nready)
end

redis.register_function('ns_deal_friend', deal_friend)
redis.register_function('ns_deal_return', deal_return)
redis.register_function('ns_friend_rebalance', friend_rebalance)
NS.deal_friend = DF
