-- The `nova-sprint route` process lease (#2756 4.5 and 5.1 shape; nova-tools
-- #3036): one router per sprint holds `lease:route:<S>` (hash instance,
-- token, host, at), renewed every 2 s with a 6 s TTL. A second instance is
-- refused while the lease lives; a holder whose instance or token no longer
-- matches has lost it and stops. Time is Redis TIME (spec 2.1 rule 3).
do
  local function now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  -- ns_route_lease_take(S, instance, token, host, ttl_ms): OK at, or HELD
  -- holder at when another instance holds a live lease.
  local function lease_take(keys, args)
    local key, instance, token, host, ttl = 'lease:route:' .. args[1], args[2], args[3], args[4], tonumber(args[5])
    local held = redis.call('HMGET', key, 'instance', 'at')
    if held[1] and held[1] ~= instance then
      return { 'HELD', held[1], held[2] or '' }
    end
    local at = tostring(now_ms())
    redis.call('HSET', key, 'instance', instance, 'token', token, 'host', host, 'at', at)
    redis.call('PEXPIRE', key, ttl)
    return { 'OK', at }
  end

  -- ns_route_lease_renew(S, instance, token, ttl_ms): OK, or LOST holder.
  local function lease_renew(keys, args)
    local key, instance, token, ttl = 'lease:route:' .. args[1], args[2], args[3], tonumber(args[4])
    local held = redis.call('HMGET', key, 'instance', 'token')
    if held[1] ~= instance or held[2] ~= token then
      return { 'LOST', held[1] or '' }
    end
    redis.call('HSET', key, 'at', tostring(now_ms()))
    redis.call('PEXPIRE', key, ttl)
    return { 'OK' }
  end

  -- ns_route_lease_release(S, instance, token): deletes the lease only when
  -- this instance still holds it.
  local function lease_release(keys, args)
    local key, instance, token = 'lease:route:' .. args[1], args[2], args[3]
    local held = redis.call('HMGET', key, 'instance', 'token')
    if held[1] ~= instance or held[2] ~= token then
      return { 'LOST', held[1] or '' }
    end
    redis.call('DEL', key)
    return { 'OK' }
  end

  redis.register_function('ns_route_lease_take', lease_take)
  redis.register_function('ns_route_lease_renew', lease_renew)
  redis.register_function('ns_route_lease_release', lease_release)
end
