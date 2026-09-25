-- Friend seat serve (nova-tools #2938): the zero-token loop unit that sits on
-- one friend's seat, beats, takes ready work and dispatches it to the friend's
-- own harness. Each function below is one atomic Redis Function call. No
-- shebang: the loader prepends the single library header and wraps this file
-- in its own do-block, so its locals are private to it.
--
-- Keys:
--   friend:<f>:serve   string, the serving session, PX FS_LOCK_MS. One serve
--                      per seat: a second serve on the seat reads BUSY and
--                      refuses; a stalled serve frees the seat within the TTL.
--   friend:<f>:beat    the #2756 v5 presence hash (harness, host, session,
--                      at, stale_ms), no TTL (#3878): what ns_task_take reads
--                      as up while TIME - at < stale_ms (NS.beat).
--   friend:<f>         the friend row (#3447, one writer, untouched by serve),
--                      or on a non-row store the #2673 presence hash (at,
--                      width, cap, host, serve, stale_ms), no TTL (#3878): a
--                      reader judges its at against stale_ms, never the key's
--                      existence.
--   friend:<f>:last    the untimed memory of the last beat (#2673).
--   cap:log            friend-up / friend-down receipts, as presence.lua,
--                      never trimmed (#3878).
-- friend:<f>:log, the serve's own receipt stream, is written by the Go loop
-- (one XADD per event) and never by these functions.

local FS_LOCK_MS = 5000
local FS_BEAT_MS = 5000

local function fs_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function fs_caplog(kind, subject, reason, actor, at)
  redis.call('XADD', 'cap:log', '*',
    'kind', kind, 'subject', subject, 'reason', reason or '',
    'actor', actor or '', 'idem', '', 'at', tostring(at))
end

-- fs_is_row returns true if row is the friend row (a hash carrying 'up', #3447).
-- The friend row has one writer (friend-row) and must not be touched or given a TTL (#3813).
local function fs_is_row(row)
  local kind = redis.call('TYPE', row)['ok']
  return kind == 'hash' and redis.call('HEXISTS', row, 'up') == 1
end

-- fs_row_hash makes friend:<f> writable as a hash: a string left by a beat
-- older than #2673 is dropped first (HSET on it would be WRONGTYPE).
local function fs_row_hash(row)
  local kind = redis.call('TYPE', row)['ok']
  if kind ~= 'hash' and kind ~= 'none' then
    redis.call('DEL', row)
  end
end

-- serve_beat: args friend, session, stamp (RFC 3339 UTC from the caller's
-- clock; Redis Lua has no date formatter), width (children in use), cap (the
-- seat's width), harness, host, actor. It takes or renews the seat lock (a
-- lease, PX FS_LOCK_MS) and writes both presence hashes, each stamped with
-- its stale_ms and neither with a TTL (#3878), and the untimed :last.
-- BUSY names the session that holds the seat; UNREGISTERED is a name not in
-- the friends SET (nova-sprint capacity friend registers one).
local function serve_beat(keys, args)
  local friend, session, stamp = args[1], args[2], args[3]
  local width, cap, harness, host, actor = args[4], args[5], args[6], args[7], args[8]
  if not friend or friend == '' or not session or session == '' then
    return { 'INVALID' }
  end
  local lock = 'friend:' .. friend .. ':serve'
  local holder = redis.call('GET', lock)
  if holder and holder ~= session then
    return { 'BUSY', holder }
  end
  if redis.call('SISMEMBER', 'friends', friend) == 0 then
    return { 'UNREGISTERED' }
  end
  local beat_key = 'friend:' .. friend .. ':beat'
  local at = fs_now_ms()
  local was_up = NS.beat.up(beat_key, at)
  -- A stopped beat keeps its hash (#3878); its session holds the seat only
  -- while the beat is live.
  local beat_session = redis.call('HGET', beat_key, 'session')
  if beat_session and beat_session ~= session and was_up == 1 then
    return { 'BUSY', beat_session }
  end
  redis.call('SET', lock, session, 'PX', FS_LOCK_MS)
  redis.call('HSET', beat_key,
    'harness', harness or '', 'host', host or '', 'session', session,
    'at', tostring(at), NS.beat.STALE, tostring(FS_BEAT_MS))
  redis.call('PERSIST', beat_key)
  local row = 'friend:' .. friend
  if not fs_is_row(row) then
    fs_row_hash(row)
    redis.call('HSET', row, 'at', stamp or '', 'width', width or '0',
      'cap', cap or '0', 'host', host or '', 'serve', session,
      NS.beat.STALE, tostring(FS_BEAT_MS))
    redis.call('PERSIST', row)
    redis.call('SET', row .. ':last', stamp or '')
  end
  if was_up == 0 then
    fs_caplog('friend-up', friend, 'serve', actor, at)
  end
  return { 'OK', tostring(was_up) }
end

-- serve_release: the seat's bye. Only the serving session releases; another
-- session's live lock or beat is BUSY. It clears the lock and presence
-- hash(es) (the friend row is preserved) and logs friend-down when the
-- friend was up. Registration and queued work stay for the next serve.
local function serve_release(keys, args)
  local friend, session, actor = args[1], args[2], args[3]
  if not friend or friend == '' or not session or session == '' then
    return { 'INVALID' }
  end
  local lock = 'friend:' .. friend .. ':serve'
  local holder = redis.call('GET', lock)
  if holder and holder ~= session then
    return { 'BUSY', holder }
  end
  local beat_key = 'friend:' .. friend .. ':beat'
  local at = fs_now_ms()
  local was_up = NS.beat.up(beat_key, at)
  local beat_session = redis.call('HGET', beat_key, 'session')
  if beat_session and beat_session ~= session and was_up == 1 then
    return { 'BUSY', beat_session }
  end
  local row = 'friend:' .. friend
  if fs_is_row(row) then
    redis.call('DEL', lock, beat_key)
  else
    redis.call('DEL', lock, beat_key, row)
  end
  if was_up == 1 then
    fs_caplog('friend-down', friend, 'serve', actor, at)
  end
  return { 'DOWN', tostring(was_up) }
end

redis.register_function('ns_friend_serve_beat', serve_beat)
redis.register_function('ns_friend_serve_release', serve_release)
