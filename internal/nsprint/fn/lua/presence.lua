-- Friend and bench presence (#2756 v5). No shebang: loader.go prepends the
-- single library header. Each verb below is one Redis Function call: it reads
-- server TIME, changes the global presence keys and appends exactly one
-- cap:log receipt, all atomically. cap:log is the stream for presence
-- changes; sprint receipts stay on s:<sprint>:log (task_claim.lua).
--
-- Global keys (#2756 v5):
--   friends                     set of registered friend names
--   friend:<f>:desired          hash (slots, machine, paused, at)
--   friend:<f>:beat             hash (harness, host, session, at), TTL 5 s
--   friend:<f>:wake             list max 1, no TTL; stale values are discarded
--   bench:<b>:beat              hash (host, user, load1, ssh, probe,
--                                    launcher, live, why, at), TTL 5 s
--   bench:<b>:live              set of card identities, TTL 5 s
--   bench:<b>:owner             fenced owner session, TTL 5 s (single
--                                    instance; a different session is BUSY)
--   machine:<m>:ceiling         hash with slots, shared with capacity friend
--   cap:log                     presence-change stream

local PL_BEAT_MS = 5000
local PL_LIVE_SEP = '\31'

local function pl_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function pl_caplog(kind, subject, reason, actor, idem, at)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', kind, 'subject', subject, 'reason', reason or '',
    'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
end

-- friend_hello registers on the first call and refreshes the beat. A slot
-- change obeys the same machine-wide ceiling as capacity friend. The friend
-- registry is durable across bye: assigned work can still target a down friend.
local function friend_hello(keys, args)
  local friend, slots = args[1], tonumber(args[2])
  local harness, host, session = args[3], args[4], args[5]
  local machine_hint, actor, idem = args[6], args[7], args[8]
  if not friend or friend == '' or not slots or slots < -1 or
      not host or host == '' or not session or session == '' then
    return { 'INVALID' }
  end
  local beat_key = 'friend:' .. friend .. ':beat'
  local current_session = redis.call('HGET', beat_key, 'session')
  if current_session and current_session ~= session then
    return { 'BUSY' }
  end
  local desired_key = 'friend:' .. friend .. ':desired'
  local machine = redis.call('HGET', desired_key, 'machine')
  if not machine or machine == '' then
    machine = machine_hint ~= '' and machine_hint or host
  elseif machine_hint ~= '' and machine ~= machine_hint then
    return { 'MACHINE' }
  end
  local current_slots = tonumber(redis.call('HGET', desired_key, 'slots') or '0')
  if slots == -1 then slots = current_slots end
  local ceiling = tonumber(redis.call('HGET', 'machine:' .. machine .. ':ceiling', 'slots') or '')
  if not ceiling then
    return { 'NOCEILING' }
  end
  local sum = slots
  for _, other in ipairs(redis.call('SMEMBERS', 'friends')) do
    if other ~= friend and redis.call('HGET', 'friend:' .. other .. ':desired', 'machine') == machine then
      sum = sum + tonumber(redis.call('HGET', 'friend:' .. other .. ':desired', 'slots') or '0')
    end
  end
  for _, bench in ipairs(redis.call('SMEMBERS', 'benches')) do
    if redis.call('HGET', 'bench:' .. bench .. ':desired', 'machine') == machine then
      sum = sum + tonumber(redis.call('HGET', 'bench:' .. bench .. ':desired', 'slots') or '0')
    end
  end
  if sum > ceiling then
    return { 'CEILING', machine, tostring(sum), tostring(ceiling) }
  end
  local at = pl_now_ms()
  local was_up = redis.call('EXISTS', beat_key)
  redis.call('SADD', 'friends', friend)
  local paused = redis.call('HGET', desired_key, 'paused') or '0'
  redis.call('HSET', desired_key,
    'slots', tostring(slots), 'machine', machine, 'paused', paused,
    'at', tostring(at))
  redis.call('HSET', beat_key,
    'harness', harness or '', 'host', host, 'session', session,
    'at', tostring(at))
  redis.call('PEXPIRE', beat_key, PL_BEAT_MS)
  if was_up == 0 then
    pl_caplog('friend-up', friend, '', actor, idem, at)
  end
  return { 'UP', tostring(slots), tostring(was_up) }
end

-- friend_beat refreshes one live friend's beat. A friend that is not
-- registered returns DOWN, never a silent re-registration.
local function friend_beat(keys, args)
  local friend, harness, host, session = args[1], args[2], args[3], args[4]
  if not friend or friend == '' then
    return { 'INVALID' }
  end
  local beat_key = 'friend:' .. friend .. ':beat'
  if redis.call('SISMEMBER', 'friends', friend) == 0 or redis.call('EXISTS', beat_key) == 0 then
    return { 'DOWN' }
  end
  if redis.call('HGET', beat_key, 'session') ~= session then
    return { 'FENCED' }
  end
  local at = pl_now_ms()
  redis.call('HSET', beat_key,
    'harness', harness or '', 'host', host or '', 'session', session,
    'at', tostring(at))
  redis.call('PEXPIRE', beat_key, PL_BEAT_MS)
  return { 'OK' }
end

-- friend_bye stops presence: it clears the beat, not registration. Assigned
-- work is deliberately left in s:<sprint>:open:<friend> so a later hello can
-- take it with no manual deal.
local function friend_bye(keys, args)
  local friend, session, actor, idem = args[1], args[2], args[3], args[4]
  if not friend or friend == '' then
    return { 'INVALID' }
  end
  local beat_key = 'friend:' .. friend .. ':beat'
  local active_session = redis.call('HGET', beat_key, 'session')
  if active_session and session ~= '' and active_session ~= session then
    return { 'FENCED' }
  end
  local was_up = redis.call('EXISTS', beat_key)
  local at = pl_now_ms()
  redis.call('DEL', beat_key)
  if was_up == 1 then
    pl_caplog('friend-down', friend, '', actor, idem, at)
  end
  return { 'DOWN', tostring(was_up) }
end

-- friend_wake routes one wake for a friend: the list is capped at one entry
-- (max 1) and has no TTL. The reader discards entries older than 600 s.
-- tokens; this verb never calls the model.
local function friend_wake(keys, args)
  local friend, reason, actor, idem = args[1], args[2], args[3], args[4]
  if not friend or friend == '' then
    return { 'INVALID' }
  end
  if not reason or reason == '' then
    reason = 'reconciler'
  end
  local at = pl_now_ms()
  local wake = 'friend:' .. friend .. ':wake'
  redis.call('LPUSH', wake, tostring(at) .. ':' .. reason)
  redis.call('LTRIM', wake, 0, 0)
  pl_caplog('friend-wake', friend, reason, actor, idem, at)
  return { 'WAKE' }
end

-- The harness consumes a wake without invoking a model. Consumption is itself
-- a function transition, so a wake cannot be removed without a cap:log record.
local function friend_poll_wake(keys, args)
  local friend = args[1]
  if not friend or friend == '' then return { 'INVALID' } end
  local wake = redis.call('RPOP', 'friend:' .. friend .. ':wake')
  if not wake then return { 'NONE' } end
  local at = pl_now_ms()
  local wake_at = tonumber(string.match(wake, '^(%d+):'))
  if not wake_at or at - wake_at > 600000 then
    redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
      'kind', 'friend-wake-consumed', 'subject', friend, 'reason', wake,
      'actor', '', 'idem', '', 'at', tostring(at), 'stale', '1')
    return { 'NONE', 'STALE' }
  end
  pl_caplog('friend-wake-consumed', friend, wake, '', '', at)
  return { 'WAKE', wake }
end

-- bench_beat is the bench-owned one-second loop. The fenced owner key gives
-- cross-process single-instance ownership: while a different session owns the
-- bench this call returns BUSY. When the owner key and beat have expired the
-- bench is free, so a stalled process never wedges it (stale expiry recovery).
-- Absence-to-presence logs bench-up. The live set is rewritten each beat.
local function bench_beat(keys, args)
  local bench = args[1]
  if not bench or bench == '' then
    return { 'INVALID' }
  end
  local host, user, load1 = args[2], args[3], args[4]
  local ssh, probe, launcher = args[5], args[6], args[7]
  local live, why = args[8], args[9]
  local session, actor, idem = args[10], args[11], args[12]
  if not session or session == '' then
    session = actor or ''
  end
  if session == '' then
    return { 'INVALID' }
  end

  local owner_key = 'bench:' .. bench .. ':owner'
  local owner = redis.call('GET', owner_key)
  if owner and owner ~= session then
    return { 'BUSY', owner }
  end

  local at = pl_now_ms()
  local first = redis.call('EXISTS', 'bench:' .. bench .. ':beat')
  redis.call('HSET', 'bench:' .. bench .. ':beat',
    'host', host or '', 'user', user or '', 'load1', load1 or '',
    'ssh', ssh or '', 'probe', probe or '', 'launcher', launcher or '',
    'live', '0', 'why', why or '', 'at', tostring(at))
  redis.call('PEXPIRE', 'bench:' .. bench .. ':beat', PL_BEAT_MS)
  redis.call('SET', owner_key, session, 'PX', PL_BEAT_MS)

  local live_key = 'bench:' .. bench .. ':live'
  redis.call('DEL', live_key)
  local live_count = 0
  if live and live ~= '' then
    for identity in string.gmatch(live, '[^' .. PL_LIVE_SEP .. ']+') do
      redis.call('SADD', live_key, identity)
      live_count = live_count + 1
    end
    redis.call('PEXPIRE', live_key, PL_BEAT_MS)
  end
  redis.call('HSET', 'bench:' .. bench .. ':beat', 'live', tostring(live_count))
  if first == 0 then
    pl_caplog('bench-up', bench, '', actor, idem, at)
  end
  return { 'OK', session }
end

-- bench_release stops a bench's owned loop. Only the owning session may
-- release; a different session is BUSY. This is what makes the loop
-- stoppable from the owner.
local function bench_release(keys, args)
  local bench, session, actor, idem = args[1], args[2], args[3], args[4]
  if not bench or bench == '' then
    return { 'INVALID' }
  end
  local owner = redis.call('GET', 'bench:' .. bench .. ':owner')
  if owner and session and session ~= '' and owner ~= session then
    return { 'BUSY', owner }
  end
  local at = pl_now_ms()
  redis.call('DEL', 'bench:' .. bench .. ':beat', 'bench:' .. bench .. ':owner',
    'bench:' .. bench .. ':live')
  pl_caplog('bench-down', bench, '', actor, idem, at)
  return { 'DOWN' }
end

redis.register_function('ns_friend_hello', friend_hello)
redis.register_function('ns_friend_beat', friend_beat)
redis.register_function('ns_friend_bye', friend_bye)
redis.register_function('ns_friend_wake', friend_wake)
redis.register_function('ns_friend_poll_wake', friend_poll_wake)
redis.register_function('ns_bench_beat', bench_beat)
redis.register_function('ns_bench_release', bench_release)
