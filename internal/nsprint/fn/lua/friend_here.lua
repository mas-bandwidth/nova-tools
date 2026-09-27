-- nova-friend here: the one presence process of a friend (the person, not
-- the work). No shebang: loader.go prepends the single library header. Two
-- functions, each one call that reads server TIME, writes friend:<f>:beat
-- and appends at most one cap:log receipt, atomically.
--
-- The beat has NO TTL (PERSIST): keys do not expire, readers judge age
-- (nova-friend list and show, the consumer table's minute). A session that
-- stops beating reads down after that minute; a session that finds another
-- session's beat older than the stale window takes it over, never BUSY.
--
-- Keys: friends (read), friend:<f>:desired (read), friend:<f>:beat (hash:
-- harness, host, session, at, load1, ncpu, cpu), friends:login (alias ->
-- friend, the --login aliases), cap:log (friend-up, friend-takeover and
-- friend-login receipts).

local function fh_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function fh_caplog(kind, subject, reason, actor, idem, at)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', kind, 'subject', subject, 'reason', reason or '',
    'actor', actor or '', 'idem', idem or '', 'at', tostring(at))
end

-- ns_friend_here(friend, host, harness, session, stale_ms, actor, idem,
-- login...) registers a session's presence: UP <slots> <was_up> <took_over>.
-- A name that is a login alias is NAME-IS-LOGIN; a name not in the roster
-- (friends and friend:<f>:desired slots, which nova-config writes) is
-- UNREGISTERED; another session whose beat is at most stale_ms old is BUSY
-- <host> <session> <age_ms>; an older one is taken over (receipt
-- friend-takeover). args[8..] are the friend's login aliases (--login), the
-- runtime fact hello wrote to friends:login: every alias is checked before
-- any write (INVALID <alias>, LOGIN-IS-FRIEND <alias>, LOGIN-TAKEN <alias>
-- <friend>), and a new one is written with a friend-login receipt.
local function friend_here(keys, args)
  local friend, host, harness, session = args[1], args[2], args[3], args[4]
  local stale = tonumber(args[5] or '')
  local actor, idem = args[6], args[7]
  if not friend or friend == '' or not host or host == '' or
      not session or session == '' or not stale or stale < 0 then
    return { 'INVALID' }
  end
  if redis.call('HEXISTS', 'friends:login', friend) == 1 then
    return { 'NAME-IS-LOGIN', friend }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return { 'UNREGISTERED' }
  end
  local slots = tonumber(redis.call('HGET', 'friend:' .. friend .. ':desired', 'slots') or '')
  if not slots then
    return { 'UNREGISTERED' }
  end
  local logins, seen = {}, {}
  for i = 8, #args do
    local alias = args[i]
    if not seen[alias] then
      seen[alias] = true
      if not string.match(alias, '^[A-Za-z0-9][A-Za-z0-9-]*$') then
        return { 'INVALID', alias }
      end
      if alias == friend or redis.call('SISMEMBER', 'friends', alias) == 1 then
        return { 'LOGIN-IS-FRIEND', alias }
      end
      local mapped = redis.call('HGET', 'friends:login', alias)
      if mapped and mapped ~= friend then
        return { 'LOGIN-TAKEN', alias, mapped }
      end
      if not mapped then
        logins[#logins + 1] = alias
      end
    end
  end
  local beat_key = 'friend:' .. friend .. ':beat'
  local at = fh_now_ms()
  local cur = redis.call('HMGET', beat_key, 'session', 'host', 'at')
  local took_over = '0'
  if cur[1] and cur[1] ~= session then
    local age = at - (tonumber(cur[3] or '') or 0)
    if age <= stale then
      return { 'BUSY', cur[2] or '', cur[1], tostring(age) }
    end
    took_over = '1'
    fh_caplog('friend-takeover', friend, 'stale session ' .. cur[1] .. ' on ' .. (cur[2] or '') .. ' age_ms=' .. tostring(age), actor, idem, at)
  end
  local was_up = redis.call('EXISTS', beat_key)
  for _, alias in ipairs(logins) do
    redis.call('HSET', 'friends:login', alias, friend)
    fh_caplog('friend-login', friend, alias, actor, idem, at)
  end
  redis.call('HSET', beat_key, 'harness', harness or '', 'host', host, 'session', session, 'at', tostring(at))
  redis.call('PERSIST', beat_key)
  if was_up == 0 then
    fh_caplog('friend-up', friend, '', actor, idem, at)
  end
  return { 'UP', tostring(slots), tostring(was_up), took_over }
end

-- ns_friend_here_beat(friend, session, host, harness, load1, ncpu, cpu) is
-- one tick of the here loop: OK <at>. A friend gone from the roster is
-- UNREGISTERED; a beat another session holds is FENCED <session> <host>. A
-- beat that is gone (a bye from elsewhere) is written again: this session is
-- the presence. An empty measurement is not written.
local function friend_here_beat(keys, args)
  local friend, session, host, harness = args[1], args[2], args[3], args[4]
  local load1, ncpu, cpu = args[5] or '', args[6] or '', args[7] or ''
  if not friend or friend == '' or not session or session == '' then
    return { 'INVALID' }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return { 'UNREGISTERED' }
  end
  local beat_key = 'friend:' .. friend .. ':beat'
  local cur = redis.call('HMGET', beat_key, 'session', 'host')
  if cur[1] and cur[1] ~= session then
    return { 'FENCED', cur[1], cur[2] or '' }
  end
  local at = fh_now_ms()
  local fields = { 'harness', harness or '', 'host', host or '', 'session', session, 'at', tostring(at) }
  if load1 ~= '' then fields[#fields + 1] = 'load1'; fields[#fields + 1] = load1 end
  if ncpu ~= '' then fields[#fields + 1] = 'ncpu'; fields[#fields + 1] = ncpu end
  if cpu ~= '' then fields[#fields + 1] = 'cpu'; fields[#fields + 1] = cpu end
  redis.call('HSET', beat_key, unpack(fields))
  redis.call('PERSIST', beat_key)
  return { 'OK', tostring(at) }
end

redis.register_function('ns_friend_here', friend_here)
redis.register_function('ns_friend_here_beat', friend_here_beat)
