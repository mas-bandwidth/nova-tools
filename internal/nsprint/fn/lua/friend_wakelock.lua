-- The one repairer's lock on a friend's wake path (nova-tools #3048 rev 3):
-- friend:<f>:wakerepair, PX. The wake path itself (friend:<f>:wakepath) is
-- written by ns_friend_wakepath from nova-config apply; ns_friend_declare,
-- the fleet converge's `friend declare`, is gone with that verb.
-- No shebang: loader.go prepends the single library header.

-- ns_friend_wakelock(friend, session, px): take or renew the one repairer's
-- lock on friend:<f>:wakerepair. HELD and the holder when another session has it.
local function friend_wakelock(keys, args)
  local f, session, px = args[1], args[2], tonumber(args[3] or '')
  if not f or f == '' or not session or session == '' or not px then
    return redis.error_reply('ns_friend_wakelock needs friend, session and px')
  end
  local key = 'friend:' .. f .. ':wakerepair'
  local holder = redis.call('GET', key)
  if holder and holder ~= session then
    return { 'HELD', holder }
  end
  redis.call('SET', key, session, 'PX', px)
  return { 'OK', session }
end

-- ns_friend_wakeunlock(friend, session): release the lock only if session holds it.
local function friend_wakeunlock(keys, args)
  local key = 'friend:' .. (args[1] or '') .. ':wakerepair'
  if redis.call('GET', key) == args[2] then
    redis.call('DEL', key)
    return 1
  end
  return 0
end

redis.register_function('ns_friend_wakelock', friend_wakelock)
redis.register_function('ns_friend_wakeunlock', friend_wakeunlock)
