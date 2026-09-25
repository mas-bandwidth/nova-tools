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
--
-- Keys (rowan-new specs/ws-index.md, "Tasks are cards"):
--   task:<id>                    HASH where stream state created_at owner why ...
--   ws:<stream>:<where>          ZSET id -> created_at ms (waiting, ready,
--                                working, merging, landed, done, parked)
--   friend:<f>:cards:working     ZSET id -> created_at ms, the friend's working
--   friend:<f>:slots             STRING the friend's slots (else the slots
--                                field of friend:<f>:desired)
--   ws:log                       STREAM one entry per move
--
-- ONE PLACE: before any write each id must be in ws:<stream>:ready, the set
-- its record names, and in none of the stream's other sets; a mismatch is a
-- refusal by name and nothing is written for that id. Every move is the one
-- move, NS.task.move (lua/02_card_move.lua, #3778): it changes the ws set,
-- the friend set, the record's pointer, state and owner and the ws:log
-- receipt in the same call, the score stays the card's created_at, and it
-- refuses by name what it would not move. The slots are re-read inside the
-- call, so two passes racing never fill a friend past its slots. Every call
-- is fenced on lease:reconciler.
local DF = {
  WHERE = { 'waiting', 'ready', 'working', 'merging', 'landed', 'done', 'parked' },
}

function DF.fenced(token)
  return token == nil or token == '' or redis.call('HGET', 'lease:reconciler', 'token') ~= token
end

function DF.key(stream, state)
  return 'ws:' .. stream .. ':' .. state
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

-- DF.ready checks the double link of one id that must be ready: its where
-- (for a record that predates the where field, the where its state names).
-- It returns the stream and the score (the card's age), or nil and the
-- refusal.
function DF.ready(id)
  local p = NS.task.read(id)
  local stream = p and p.stream or ''
  if stream == '' then
    return nil, 'no task ' .. id .. ' with a stream'
  end
  local where = p.where
  if not p.placed then
    where = NS.task.where_of[p.state] or p.state
  end
  if where ~= 'ready' then
    return nil, 'task ' .. id .. ' is ' .. tostring(where) .. ', not ready'
  end
  local age = redis.call('ZSCORE', DF.key(stream, 'ready'), id)
  if not age then
    return nil, 'task ' .. id .. ' says ready but is not in ' .. DF.key(stream, 'ready')
  end
  for _, st in ipairs(DF.WHERE) do
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
  local open = slots - redis.call('ZCARD', fk)
  if open < 0 then
    open = 0
  end
  local took, refused = 0, {}
  for i = 5, #args do
    local id = args[i]
    local stream, err = DF.ready(id)
    local owner = redis.call('HGET', 'task:' .. id, 'owner') or ''
    if took >= open then
      stream, err = nil, 'full'
    elseif stream and owner ~= '' and owner ~= f then
      stream, err = nil, 'task ' .. id .. ' is owned by ' .. owner
    end
    if stream then
      -- ready -> working with the friend as holder: the one move.
      err = NS.task.move(id, 'working', { by = by, why = why, as = f, friend = f })
      if not err then took = took + 1 end
    end
    if err then
      refused[#refused + 1] = id
      refused[#refused + 1] = err
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
    local stream, err = DF.ready(id)
    if stream then
      -- ready -> waiting with the why on the record: the one move.
      err = NS.task.move(id, 'waiting', { by = by, why = why })
      if not err then moved = moved + 1 end
    end
    if err then
      refused[#refused + 1] = id
      refused[#refused + 1] = err
    end
  end
  return DF.reply({ 'RETURNED', moved, #refused / 2 }, refused)
end

redis.register_function('ns_deal_friend', deal_friend)
redis.register_function('ns_deal_return', deal_return)
NS.deal_friend = DF
