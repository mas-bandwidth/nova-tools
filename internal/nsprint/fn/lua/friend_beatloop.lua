-- The friend beat loop's lease and the beat's models (seat-keeps-beat,
-- internal/nsprint/life/beatloop.go and friendbeat.go). Each is its own
-- function so the friend seat (ns-friend, store/acl.go) runs it with the
-- commands its row already has: HGET, HMGET, HSET, PEXPIRE, DEL, ZSCORE,
-- HDEL. No SET: the lease is a hash, not a string.
--
--   friend:<f>:beatloop   token host pid, PEXPIRE lease_ms: the one loop
--                         that beats for friend <f>; its holder renews it
--                         each tick and deletes it when it exits.

-- ns_friend_loop_claim(KEYS[1] lease; ARGV token, lease_ms, host, pid, dead)
-- -> TOOK | HELD <token> <host> <pid>. dead is the token of a holder the
-- caller found dead on its own host (its pid gone); a lease still holding
-- that token is taken over, any other holder is left alone.
redis.register_function('ns_friend_loop_claim', function(keys, args)
  local h = redis.call('HMGET', keys[1], 'token', 'host', 'pid')
  if h[1] and h[1] ~= '' and h[1] ~= args[5] then
    return { 'HELD', h[1], h[2] or '', h[3] or '' }
  end
  redis.call('DEL', keys[1])
  redis.call('HSET', keys[1], 'token', args[1], 'host', args[3], 'pid', args[4])
  redis.call('PEXPIRE', keys[1], args[2])
  return { 'TOOK' }
end)

-- ns_friend_loop_renew(KEYS[1] lease; ARGV token, lease_ms, host, pid) ->
-- 1 extended, 2 taken (it had lapsed), 0 another loop holds it. The holder's
-- host and pid are written each time, so a claim can judge it.
redis.register_function('ns_friend_loop_renew', function(keys, args)
  local t = redis.call('HGET', keys[1], 'token')
  if t and t ~= args[1] then return 0 end
  redis.call('HSET', keys[1], 'token', args[1], 'host', args[3], 'pid', args[4])
  redis.call('PEXPIRE', keys[1], args[2])
  if t then return 1 end
  return 2
end)

-- ns_friend_loop_release(KEYS[1] lease; ARGV token) -> 1 deleted, 0 not
-- this token's.
redis.register_function('ns_friend_loop_release', function(keys, args)
  if redis.call('HGET', keys[1], 'token') == args[1] then return redis.call('DEL', keys[1]) end
  return 0
end)

-- ns_friend_models(KEYS[1] friend:<f>:beat, KEYS[2] friend:<f>:cards:working,
-- KEYS[3..] task:<copy>) -> the models field written: each distinct model of
-- the named copies still in the working set, once, in the order named,
-- comma joined; the field is removed ('' returned) when none names one.
redis.register_function('ns_friend_models', function(keys, args)
  local seen, out = {}, {}
  for i = 3, #keys do
    local id = string.match(keys[i], '^task:(.+)$')
    if id and redis.call('ZSCORE', keys[2], id) then
      local m = redis.call('HGET', keys[i], 'model')
      if m and m ~= '' and not seen[m] then
        seen[m] = true
        out[#out + 1] = m
      end
    end
  end
  if #out == 0 then
    redis.call('HDEL', keys[1], 'models')
    return ''
  end
  local models = table.concat(out, ',')
  redis.call('HSET', keys[1], 'models', models)
  return models
end)
