-- Exclusive writer generations (issue #3139 rev 7 section 10.2).
-- land:<repo>:<base>:writer holds gen, owner (old-loop or nova-sprint), since, by.
-- ns_writer is the sole writer of land:<repo>:<base>:writer.
--
-- Cutover: bumps gen, sets owner=nova-sprint (refuses while old loop inflight count is nonzero).
-- Rollback: bumps gen, sets owner=old-loop.

local function now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- ns_writer(keys, args):
-- keys[1]: land:<repo>:<base>:writer
-- keys[2..]: optional land:<repo>:events, land:<repo>:<base>:inflight
-- args[1]: to ('nova-sprint' or 'old-loop' or 'get')
-- args[2]: by (actor name)
-- args[3]: optional inflight count override
local function writer_fn(keys, args)
  local writer_key = keys[1]
  if not writer_key or writer_key == '' then
    return redis.error_reply('writer key is required')
  end

  local current = redis.call('HMGET', writer_key, 'gen', 'owner', 'since', 'by')
  local gen = tonumber(current[1]) or 0
  local owner = current[2] or 'old-loop'

  local to = args[1]
  if not to or to == '' or to == 'get' then
    return { 'OK', tostring(gen), owner, current[3] or '', current[4] or '' }
  end

  if to ~= 'old-loop' and to ~= 'nova-sprint' then
    return { 'INVALID', 'owner must be old-loop or nova-sprint' }
  end

  local by = args[2] or ''
  local at = tostring(now_ms())

  if to == 'nova-sprint' then
    -- Check inflight count if provided in args[3]
    if args[3] and args[3] ~= '' then
      local inf = tonumber(args[3]) or 0
      if inf > 0 then
        return { 'REFUSED', 'inflight', tostring(inf) }
      end
    end
    -- Also check if any key in keys[2..#keys] is an inflight key
    for i = 2, #keys do
      if string.match(keys[i], ':inflight$') then
        local inf = 0
        if redis.call('EXISTS', keys[i]) == 1 then
          local ktype = redis.call('TYPE', keys[i])['ok']
          if ktype == 'string' then
            inf = tonumber(redis.call('GET', keys[i])) or 0
          elseif ktype == 'set' then
            inf = redis.call('SCARD', keys[i])
          elseif ktype == 'zset' then
            inf = redis.call('ZCARD', keys[i])
          end
        end
        if inf > 0 then
          return { 'REFUSED', 'inflight', tostring(inf) }
        end
      end
    end
  end

  gen = gen + 1
  redis.call('HSET', writer_key,
    'gen', tostring(gen),
    'owner', to,
    'since', at,
    'by', by)

  -- Record event in events stream if provided in keys
  for i = 2, #keys do
    if string.match(keys[i], ':events$') then
      redis.call('XADD', keys[i], 'MAXLEN', '~', 100000, '*',
        'event', 'WRITER',
        'gen', tostring(gen),
        'owner', to,
        'by', by,
        'at', at)
    end
  end

  return { 'OK', tostring(gen), to, at, by }
end

redis.register_function('ns_writer', writer_fn)
