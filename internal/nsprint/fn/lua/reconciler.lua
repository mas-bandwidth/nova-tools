-- The reconciler lease and pass record for nova-tools #2726 (#2756 section
-- 5.1). No shebang: the loader (internal/nsprint/fn/loader.go) prepends the
-- library header and embeds every lua/ file into one nova_sprint library.
--
-- lease:reconciler is a hash {instance, token, host, pid, at, acquired_at}
-- with a Redis TTL. Liveness is the key existing: a holder that stops
-- renewing loses it when the TTL lapses, whatever its pid is doing. The pid is
-- recorded for a human reading the hash and is never read by any function
-- (the reused-pid dealer, #2726). Every write the reconciler makes presents
-- its token; a token that is not the stored one refuses FENCED and the caller
-- must stop (spec 2.1 rule 8).
--
-- A function in another file that must be fenced by the reconciler (the deal
-- pass, #2743) takes the token as an argument and checks it itself before any
-- write, in the same call:
--   if redis.call('HGET', 'lease:reconciler', 'token') ~= token then
--     return { 'FENCED' }
--   end
-- (a Redis Function cannot call another function, and this file sorts after
-- card_*.lua, so there is no shared helper to call.)

local RECONCILER_LEASE = 'lease:reconciler'
local RECONCILER_PROC = 'proc:reconciler'

local function reconciler_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function reconciler_ttl(raw)
  local ttl = tonumber(raw)
  if ttl == nil or ttl < 1 then
    return nil
  end
  return math.floor(ttl)
end

-- A missing key, a lapsed key and another instance's token are one case:
-- this caller does not hold the lease.
local function reconciler_holds(token)
  return token ~= nil and token ~= '' and redis.call('HGET', RECONCILER_LEASE, 'token') == token
end

local function reconciler_fenced()
  local h = redis.call('HMGET', RECONCILER_LEASE, 'instance', 'host')
  return { 'FENCED', h[1] or '', h[2] or '' }
end

-- ns_reconciler_acquire(instance, nonce, host, pid, ttl_ms)
-- Takes the lease only when no instance holds it. A second instance refuses
-- with HELD, the holder, its host and the age of its last renewal. The token
-- is <acquired_at_ms>.<nonce>; the caller supplies 128 random bits as nonce.
local function reconciler_acquire(keys, args)
  local instance, nonce, host, pid = args[1], args[2], args[3], args[4]
  local ttl = reconciler_ttl(args[5])
  if instance == nil or instance == '' or nonce == nil or nonce == '' or ttl == nil then
    return redis.error_reply('ns_reconciler_acquire: instance, nonce and ttl_ms are required')
  end
  local at = reconciler_now_ms()
  if redis.call('EXISTS', RECONCILER_LEASE) == 1 then
    local h = redis.call('HMGET', RECONCILER_LEASE, 'instance', 'host', 'at')
    local age = at - (tonumber(h[3]) or at)
    return { 'HELD', h[1] or '', h[2] or '', tostring(age), tostring(redis.call('PTTL', RECONCILER_LEASE)) }
  end
  local token = tostring(at) .. '.' .. nonce
  redis.call('HSET', RECONCILER_LEASE,
    'instance', instance, 'token', token, 'host', host or '', 'pid', pid or '',
    'at', tostring(at), 'acquired_at', tostring(at))
  redis.call('PEXPIRE', RECONCILER_LEASE, ttl)
  return { 'ACQUIRED', token, tostring(at) }
end

-- ns_reconciler_renew(token, ttl_ms): the holder extends its lease.
local function reconciler_renew(keys, args)
  local token = args[1]
  local ttl = reconciler_ttl(args[2])
  if ttl == nil then
    return redis.error_reply('ns_reconciler_renew: ttl_ms is required')
  end
  if not reconciler_holds(token) then
    return reconciler_fenced()
  end
  local at = reconciler_now_ms()
  redis.call('HSET', RECONCILER_LEASE, 'at', tostring(at))
  redis.call('PEXPIRE', RECONCILER_LEASE, ttl)
  return { 'OK', tostring(at) }
end

-- ns_reconciler_pass(token, ttl_ms, took_ms, dealt, routed, expired, err,
-- retried, ambiguous)
-- Records one pass in proc:reconciler (one writer: the lease holder) and
-- renews the lease in the same call. pass_at and at are Redis TIME. retried
-- and ambiguous (#2930 rev 5, the expire duty) read 0 when absent; n counts
-- every transition once: dealt + routed + expired + retried + ambiguous.
local function reconciler_pass(keys, args)
  local token = args[1]
  local ttl = reconciler_ttl(args[2])
  if ttl == nil then
    return redis.error_reply('ns_reconciler_pass: ttl_ms is required')
  end
  if not reconciler_holds(token) then
    return reconciler_fenced()
  end
  local took = tonumber(args[3]) or 0
  local dealt = tonumber(args[4]) or 0
  local routed = tonumber(args[5]) or 0
  local expired = tonumber(args[6]) or 0
  local err = args[7] or ''
  local retried = tonumber(args[8]) or 0
  local ambiguous = tonumber(args[9]) or 0
  local at = reconciler_now_ms()
  local h = redis.call('HMGET', RECONCILER_LEASE, 'instance', 'host')
  redis.call('HSET', RECONCILER_PROC,
    'pass_at', tostring(at), 'took_ms', tostring(took),
    'dealt', tostring(dealt), 'routed', tostring(routed), 'expired', tostring(expired),
    'retried', tostring(retried), 'ambiguous', tostring(ambiguous),
    'n', tostring(dealt + routed + expired + retried + ambiguous), 'err', err,
    'instance', h[1] or '', 'host', h[2] or '', 'at', tostring(at))
  redis.call('HSET', RECONCILER_LEASE, 'at', tostring(at))
  redis.call('PEXPIRE', RECONCILER_LEASE, ttl)
  return { 'OK', tostring(at) }
end

-- ns_reconciler_release(token): the holder gives the lease up on shutdown so
-- the next start does not wait out the TTL. Any other token refuses FENCED and
-- deletes nothing.
local function reconciler_release(keys, args)
  if not reconciler_holds(args[1]) then
    return reconciler_fenced()
  end
  redis.call('DEL', RECONCILER_LEASE)
  return { 'RELEASED' }
end

redis.register_function('ns_reconciler_acquire', reconciler_acquire)
redis.register_function('ns_reconciler_renew', reconciler_renew)
redis.register_function('ns_reconciler_pass', reconciler_pass)
redis.register_function('ns_reconciler_release', reconciler_release)
