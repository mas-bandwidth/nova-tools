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
-- refusal by name and nothing is written for that id. The ws set, the
-- friend set and the record's state and owner change in the same call, the
-- score stays the card's created_at. The slots are re-read inside the call,
-- so two passes racing never fill a friend past its slots. Every call is
-- fenced on lease:reconciler.
local DF = {
  STATES = { 'waiting', 'ready', 'working', 'merging', 'landed', 'parked' },
  LOG_MAX = '200000',
}

function DF.now()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

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

-- DF.ready checks the double link of one id that must be ready. It returns
-- the stream and the score (the card's age), or nil and the refusal.
function DF.ready(id)
  local f = redis.call('HMGET', 'task:' .. id, 'stream', 'state')
  local stream, state = f[1], f[2]
  if not stream or stream == '' then
    return nil, 'no task ' .. id .. ' with a stream'
  end
  if state ~= 'ready' then
    return nil, 'task ' .. id .. ' is ' .. tostring(state) .. ', not ready'
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

function DF.log(id, stream, from, to, by, why, at)
  redis.call('XADD', 'ws:log', 'MAXLEN', '~', DF.LOG_MAX, '*', 'id', id, 'stream', stream,
    'from', from, 'to', to, 'by', by or '', 'why', why or '', 'at', tostring(at))
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
  local now, took, refused = DF.now(), 0, {}
  for i = 5, #args do
    local id = args[i]
    local stream, age = DF.ready(id)
    local owner = redis.call('HGET', 'task:' .. id, 'owner') or ''
    if took >= open then
      stream, age = nil, 'full'
    elseif stream and owner ~= '' and owner ~= f then
      stream, age = nil, 'task ' .. id .. ' is owned by ' .. owner
    end
    if not stream then
      refused[#refused + 1] = id
      refused[#refused + 1] = age
    else
      redis.call('ZREM', DF.key(stream, 'ready'), id)
      redis.call('ZADD', DF.key(stream, 'working'), age, id)
      redis.call('ZADD', fk, age, id)
      redis.call('HSET', 'task:' .. id, 'state', 'working', 'state_at', tostring(now), 'owner', f)
      DF.log(id, stream, 'ready', 'working', by, why, now)
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
  local now, moved, refused = DF.now(), 0, {}
  for i = 4, #args do
    local id = args[i]
    local stream, age = DF.ready(id)
    if not stream then
      refused[#refused + 1] = id
      refused[#refused + 1] = age
    else
      redis.call('ZREM', DF.key(stream, 'ready'), id)
      redis.call('ZADD', DF.key(stream, 'waiting'), age, id)
      redis.call('HSET', 'task:' .. id, 'state', 'waiting', 'state_at', tostring(now), 'why', why or '')
      DF.log(id, stream, 'ready', 'waiting', by, why, now)
      moved = moved + 1
    end
  end
  return DF.reply({ 'RETURNED', moved, #refused / 2 }, refused)
end

redis.register_function('ns_deal_friend', deal_friend)
redis.register_function('ns_deal_return', deal_return)
NS.deal_friend = DF
