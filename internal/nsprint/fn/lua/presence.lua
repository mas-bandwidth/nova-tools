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
--                                    launcher, live, why, build, at), TTL from
--                                    the caller (3 x the beat interval, #3372)
--   bench:<b>:live              set of card identities, same TTL
--   bench:<b>:owner             fenced owner session, same TTL (single
--                                    instance; a different session is BUSY)
--   bench:<b>                   the host table row (host, load1, ncpu, at),
--                                    no TTL: an old at prints stale (#3440)
--   friend:<f>                  the friend table row (at, up, ready, queue,
--                                    working, waiting, width, done, slots),
--                                    no TTL, and friend:<f>:last (#3440)
--   machine:<m>:ceiling         hash with slots, shared with capacity friend
--   cap:log                     presence-change stream
--   friend:<f>:events           lifecycle events (#3153): beat, deliver,
--                                    turn-start [cause], turn-end, turn-error,
--                                    usage-limit; written only by
--                                    ns_friend_event, MAXLEN ~ 100000

-- The whole file is one block: none of its names is shared, and the files
-- are concatenated into one chunk whose main function allows 200 locals.
do
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

-- friend_hello refreshes presence for a friend already registered by
-- ns_capacity_desired. It never registers a friend or writes desired capacity.
-- The registry is durable across bye: assigned work can still target a down friend.
local function friend_hello(keys, args)
  local friend = args[1]
  local slots = tonumber(args[2]) -- compatibility argument; deliberately ignored
  local harness, host, session = args[3], args[4], args[5]
  local machine_hint, actor, idem = args[6], args[7], args[8]
  if not friend or friend == '' or
      not host or host == '' or not session or session == '' then
    return { 'INVALID' }
  end
  -- NAME-IS-LOGIN (#3092 rev 6) comes before UNREGISTERED: a mapped login
  -- is refused by name on every hello, never reported as merely unknown.
  if redis.call('HEXISTS', 'friends:login', friend) == 1 then
    return { 'NAME-IS-LOGIN', friend }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return { 'UNREGISTERED' }
  end
  local beat_key = 'friend:' .. friend .. ':beat'
  local current_session = redis.call('HGET', beat_key, 'session')
  if current_session and current_session ~= session then
    return { 'BUSY' }
  end
  local desired_key = 'friend:' .. friend .. ':desired'
  local machine = redis.call('HGET', desired_key, 'machine')
  local current_slots = tonumber(redis.call('HGET', desired_key, 'slots') or '')
  if not machine or machine == '' or not current_slots then
    return { 'UNREGISTERED' }
  end
  if machine_hint ~= '' and machine ~= machine_hint then
    return { 'MACHINE' }
  end
  -- Login aliases (#3092 rev 6): args[9..] are `--login` values. This is the
  -- only writer of friends:login; every alias is checked before any write.
  -- NAME-IS-LOGIN holds on every hello, with or without aliases: a name
  -- that is a mapped login never registers as a friend. Aliases repeated in
  -- one request are written (and receipted) once.
  local logins = {}
  local seen = {}
  for i = 9, #args do
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
  local at = pl_now_ms()
  local was_up = redis.call('EXISTS', beat_key)
  for _, alias in ipairs(logins) do
    redis.call('HSET', 'friends:login', alias, friend)
    pl_caplog('friend-login', friend, alias, actor, idem, at)
  end
  redis.call('HSET', beat_key,
    'harness', harness or '', 'host', host, 'session', session,
    'at', tostring(at))
  redis.call('PEXPIRE', beat_key, PL_BEAT_MS)
  if was_up == 0 then
    pl_caplog('friend-up', friend, '', actor, idem, at)
  end
  return { 'UP', tostring(current_slots), tostring(was_up) }
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

-- pl_lease_beat is a card's lease beat carried by its bench's beat
-- (nova-tools#3925): one bench beat names every live card on the bench
-- (<S>/<label>/<attempt>, or the attempt identity
-- <S>/<label>/<base8>/<bench>/<attempt>), and each one whose record is
-- launched or running on this bench under this attempt gets beat_at, the
-- field the lease reaper (ns_lease_reap) and the expire sweep read. A name
-- that is not such a card writes nothing.
local function pl_lease_beat(bench, identity, at)
  local S, label, attempt = string.match(identity, '^([-a-z0-9]+)/([A-Za-z0-9][A-Za-z0-9._-]*)/([1-9][0-9]*)$')
  if not S then
    local b
    S, label, b, attempt = string.match(identity,
      '^([-a-z0-9]+)/([A-Za-z0-9][A-Za-z0-9._-]*)/[0-9a-f]+/([^/]+)/([1-9][0-9]*)$')
    if b ~= bench then return end
  end
  if not S then return end
  local id = 's:' .. S .. ':card:' .. label
  local c = redis.call('HMGET', id, 'state', 'attempt', 'bench')
  if (c[1] == 'launched' or c[1] == 'running') and c[2] == attempt and c[3] == bench then
    redis.call('HSET', id, 'beat_at', tostring(at))
  end
end

-- bench_beat is the bench-owned one-second loop. The fenced owner key gives
-- cross-process single-instance ownership: while a different session owns the
-- bench this call returns BUSY. When the owner key and beat have expired the
-- bench is free, so a stalled process never wedges it (stale expiry recovery).
-- Absence-to-presence logs bench-up. The live set is rewritten each beat.
-- args[17] is the TTL in ms the caller's loop promises (3 x its interval,
-- #3372); a missing or non-positive value keeps PL_BEAT_MS. args[18] is the
-- host row's at (RFC 3339 UTC) and args[19] the bench's ncpu; an empty
-- args[18] writes no row (#3440). args[20] is the CPU busy percent over the
-- beat interval, on the beat as cpu (empty when unmeasured). args[21] is the
-- count of CI legs running on the bench (one Runner.Worker per running job,
-- nova-tools#4293), on the beat as ci every beat: the deal passes and
-- ns_cm_work's fill take it off the bench's slots, so a copy is never put
-- beside a leg it would slow; empty when unmeasured (nothing is taken off).
local function bench_beat(keys, args)
  local bench = args[1]
  if not bench or bench == '' then
    return { 'INVALID' }
  end
  local host, user, load1 = args[2], args[3], args[4]
  local ssh, probe, launcher = args[5], args[6], args[7]
  local live, why = args[8], args[9]
  local session, actor, idem = args[10], args[11], args[12]
  local build = args[13]
  -- The bench's own facts, measured on the bench each beat (#3646):
  -- harness versions present, git mirrors present, free GiB under its root.
  -- A caller that predates them passes nothing and they read as missing.
  local harness, mirrors, disk_gib = args[14], args[15], args[16]
  local ttl = tonumber(args[17])
  if not ttl or ttl <= 0 then
    ttl = PL_BEAT_MS
  end
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
    'ssh', ssh or '', 'launcher', launcher or '',
    'live', '0', 'why', why or '', 'build', build or '',
    'harness', harness or '', 'mirrors', mirrors or '', 'disk_gib', disk_gib or '',
    'ci', args[21] or '', 'at', tostring(at))
  -- probe is the bench's last probe result, written by fleet build after an
  -- install (nova-tools#4237: a probe's result lives here, never in the
  -- consumer sets); a beat that names none leaves it as it is.
  if probe and probe ~= '' then
    redis.call('HSET', 'bench:' .. bench .. ':beat', 'probe', probe)
  end
  redis.call('PEXPIRE', 'bench:' .. bench .. ':beat', ttl)
  redis.call('SET', owner_key, session, 'PX', ttl)

  local live_key = 'bench:' .. bench .. ':live'
  redis.call('DEL', live_key)
  local live_count = 0
  if live and live ~= '' then
    for identity in string.gmatch(live, '[^' .. PL_LIVE_SEP .. ']+') do
      redis.call('SADD', live_key, identity)
      live_count = live_count + 1
      pl_lease_beat(bench, identity, at)
    end
    redis.call('PEXPIRE', live_key, ttl)
  end
  redis.call('HSET', 'bench:' .. bench .. ':beat', 'live', tostring(live_count))
  -- The host table row (#3440), in the same call as the beat: host is the
  -- bench name (the table shows a row only when host equals its key), at is
  -- the caller's RFC 3339 stamp. Counts are not here: the table reads them
  -- from the card views bench:<b>:cards:<w> (#3692). Fields other writers
  -- keep on this hash (the dealer's) are left as they are.
  local row_at, ncpu = args[18], args[19]
  if row_at and row_at ~= '' then
    redis.call('HSET', 'bench:' .. bench,
      'host', bench, 'load1', load1 or '', 'ncpu', ncpu or '', 'at', row_at)
    -- ncpu on the beat too: the table prints load1 / ncpu as a percent of
    -- every core (Glenn 2026-09-26 8:33 AM ET); the beat is the row the
    -- table reads, never the host row
    redis.call('HSET', 'bench:' .. bench .. ':beat', 'ncpu', ncpu or '', 'cpu', args[20] or '')
  end
  if first == 0 then
    pl_caplog('bench-up', bench, '', actor, idem, at)
  end
  return { 'OK', session }
end

-- pl_count is SCARD of a friend-queue index set; a missing key or a key of
-- another type counts 0, as bin/friend-row's cnt did.
local function pl_count(key)
  local n = redis.pcall('SCARD', key)
  if type(n) ~= 'number' then
    return 0
  end
  return n
end

-- friend_row writes one friend's sprint-table row (#3440, the atomic-row rule
-- #3281): one read of the friend-queue index sets sprint:<S>:idx:<f>:open,
-- working, waiting and closed, the declared slots and the seat's own beat,
-- then ONE HSET of friend:<f> with one at, and friend:<f>:last. up is 1 while
-- friend:<f>:beat exists (written only by the seat's own hello/beat). A
-- friend:<f> left over as another type is replaced. The row never expires:
-- an old at is what the table prints stale. args = friend, sprint, at.
local function friend_row(keys, args)
  local friend, sprint, at = args[1], args[2], args[3]
  if not friend or not string.match(friend, '^[a-z0-9][a-z0-9-]*$') or
      not sprint or sprint == '' or not at or at == '' then
    return { 'INVALID' }
  end
  local ix = 'sprint:' .. sprint .. ':idx:' .. friend .. ':'
  local ready = pl_count(ix .. 'open')
  local working = pl_count(ix .. 'working')
  local waiting = pl_count(ix .. 'waiting')
  local done = pl_count(ix .. 'closed')
  local slots = redis.pcall('HGET', 'friend:' .. friend .. ':desired', 'slots')
  if type(slots) ~= 'string' or not string.match(slots, '^[0-9]+$') then
    slots = ''
  end
  local up = redis.call('EXISTS', 'friend:' .. friend .. ':beat')
  local row = 'friend:' .. friend
  local kind = redis.call('TYPE', row)
  if type(kind) == 'table' then
    kind = kind['ok']
  end
  if kind ~= 'hash' and kind ~= 'none' then
    redis.call('DEL', row)
  end
  redis.call('HSET', row, 'at', at, 'up', tostring(up),
    'ready', tostring(ready), 'queue', tostring(ready),
    'working', tostring(working), 'waiting', tostring(waiting),
    'width', tostring(working), 'done', tostring(done), 'slots', slots)
  redis.call('SET', row .. ':last', at)
  return { 'OK', tostring(up), tostring(ready), tostring(working),
    tostring(waiting), tostring(done), slots }
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

-- friend_event appends one lifecycle event to friend:<f>:events (#3153), the
-- one writer of that stream. args = friend, kind, cause ('' when none),
-- at_ms (the producer's UTC ms), actor. kind is one of the six below; cause
-- is only for turn-start. actor ~= friend is refused: shape validation for
-- defence in depth; the seat check (NOVA_FRIEND == --as) is the CLI's.
-- Returns OK <stream id> or INVALID <reason>.
local PL_EVENT_KINDS = { ['beat'] = true, ['deliver'] = true, ['turn-start'] = true,
  ['turn-end'] = true, ['turn-error'] = true, ['usage-limit'] = true }

local function friend_event(keys, args)
  local friend, kind, cause, at_ms, actor = args[1], args[2], args[3] or '', args[4], args[5]
  if not friend or friend == '' then
    return { 'INVALID', 'friend is required' }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return { 'INVALID', 'unknown friend ' .. friend }
  end
  if actor ~= friend then
    return { 'INVALID', 'actor ' .. tostring(actor) .. ' is not friend ' .. friend }
  end
  if not kind or not PL_EVENT_KINDS[kind] then
    return { 'INVALID', 'unknown kind ' .. tostring(kind) }
  end
  if cause ~= '' and kind ~= 'turn-start' then
    return { 'INVALID', 'cause is only for turn-start' }
  end
  local at = tonumber(at_ms or '')
  if not at or at < 0 then
    return { 'INVALID', 'at_ms must be a number' }
  end
  local id
  if cause ~= '' then
    id = redis.call('XADD', 'friend:' .. friend .. ':events', 'MAXLEN', '~', 100000, '*',
      'kind', kind, 'at', tostring(math.floor(at)), 'cause', cause)
  else
    id = redis.call('XADD', 'friend:' .. friend .. ':events', 'MAXLEN', '~', 100000, '*',
      'kind', kind, 'at', tostring(math.floor(at)))
  end
  return { 'OK', id }
end

redis.register_function('ns_friend_hello', friend_hello)
redis.register_function('ns_friend_beat', friend_beat)
redis.register_function('ns_friend_bye', friend_bye)
redis.register_function('ns_friend_wake', friend_wake)
redis.register_function('ns_friend_poll_wake', friend_poll_wake)
redis.register_function('ns_friend_row', friend_row)
redis.register_function('ns_bench_beat', bench_beat)
redis.register_function('ns_bench_release', bench_release)
redis.register_function('ns_friend_event', friend_event)
end
