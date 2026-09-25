-- The friend wake registry (nova-tools #3048 rev 3): every friend's declared
-- wake path, written by one owner, the fleet converge's `friend declare`, and
-- stamped with the fleet source revision and digest it was read from.
-- No shebang: loader.go prepends the single library header. No TTL except the
-- repair lock.
--
-- Keys: friends:decl (hash rev, digest, path, at), friend:<f>:wakepath (hash:
-- the entry's fields plus kind (= mode, the ladder in friend.lua reads it),
-- decl, rev, at), friends:declared (set), friend:<f>:wakerepair (the one
-- repairer's lock, PX), cap:log (friend-declared / friend-undeclared receipts).

local function fd_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function fd_receipt(kind, subject, reason, at)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', kind, 'subject', subject, 'reason', reason,
    'actor', 'friend-declare', 'idem', '', 'at', tostring(at))
end

-- ns_friend_declare(prev_rev, rev, digest, path, n, then n entries of
-- name, npairs, k1, v1, ... k_npairs, v_npairs)
-- The compare-and-set: when friends:decl rev is not prev_rev ('' for an empty
-- registry) it returns CONFLICT and the stored rev, and writes nothing, so two
-- declarers that read the same prev_rev never both write. Otherwise, in this
-- one call: every wakepath (replaced whole), the friends:declared adds and
-- removes (a removed friend's wakepath goes with it), friends:decl, and one
-- receipt per add or remove carrying rev.
local function friend_declare(keys, args)
  local prev, rev, digest, path = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
  local n = tonumber(args[5] or '')
  if rev == '' or digest == '' or not n then
    return redis.error_reply('ns_friend_declare needs prev_rev, rev, digest, path and n')
  end
  local cur = redis.call('HGET', 'friends:decl', 'rev') or ''
  if cur ~= prev then
    return { 'CONFLICT', cur }
  end
  local at = fd_now_ms()
  local want = {}
  local i = 6
  for _ = 1, n do
    local name, np = args[i], tonumber(args[i + 1] or '')
    if not name or name == '' or not np then
      return redis.error_reply('ns_friend_declare: malformed entry at argument ' .. tostring(i))
    end
    local wp = 'friend:' .. name .. ':wakepath'
    local fields = { 'rev', rev, 'at', tostring(at) }
    for j = 0, np - 1 do
      fields[#fields + 1] = args[i + 2 + 2 * j]
      fields[#fields + 1] = args[i + 3 + 2 * j]
    end
    redis.call('DEL', wp)
    redis.call('HSET', wp, unpack(fields))
    want[name] = true
    i = i + 2 + 2 * np
  end
  local added, removed = 0, 0
  for _, old in ipairs(redis.call('SMEMBERS', 'friends:declared')) do
    if not want[old] then
      redis.call('SREM', 'friends:declared', old)
      redis.call('DEL', 'friend:' .. old .. ':wakepath')
      fd_receipt('friend-undeclared', old, 'rev=' .. rev, at)
      removed = removed + 1
    end
  end
  for name in pairs(want) do
    if redis.call('SADD', 'friends:declared', name) == 1 then
      fd_receipt('friend-declared', name, 'rev=' .. rev, at)
      added = added + 1
    end
  end
  redis.call('HSET', 'friends:decl', 'rev', rev, 'digest', digest, 'path', path, 'at', tostring(at))
  return { 'OK', tostring(n), tostring(added), tostring(removed) }
end

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

redis.register_function('ns_friend_declare', friend_declare)
redis.register_function('ns_friend_wakelock', friend_wakelock)
redis.register_function('ns_friend_wakeunlock', friend_wakeunlock)
