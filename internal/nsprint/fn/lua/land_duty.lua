-- The land duty's lease and timer (nova-tools #3898): landing is a
-- reconciler duty. One worker per repo holds lease:land:<repo> while it runs
-- the land sequence (nova-sprint land, #3886) over every stream with a
-- landable member, so two landings of one repo never run at once; the timer
-- is proc:land:<repo> due_at, set at the take to now + cfg:land tick
-- (seconds, default 300), so the duty lands every tick and a pass that runs
-- long holds the lease rather than overlapping the next. Time is Redis TIME.
--
--   lease:land:<repo>  hash  instance, token, host, at (PEXPIRE ttl_ms; the
--                            worker renews it every ttl/3)
--   proc:land:<repo>   hash  due_at, holder, holder_at, pass_at, took_ms,
--                            streams, opened, landed, parked, unread, err, at
do
  local function ld_now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function ld_tick_ms()
    local v = tonumber(redis.call('HGET', 'cfg:land', 'tick') or '')
    if not v or v <= 0 then
      v = 300
    end
    return math.floor(v * 1000)
  end

  local function ld_holds(key, instance, token)
    local held = redis.call('HMGET', key, 'instance', 'token')
    return instance ~= '' and token ~= '' and held[1] == instance and held[2] == token, held[1] or ''
  end

  -- ns_land_duty_take(repo, instance, token, host, ttl_ms): TAKEN at when
  -- the lease is free and the repo's pass is due (due_at then moves to now +
  -- tick); HELD holder while another worker holds it; WAIT due_at when the
  -- last pass was under a tick ago.
  local function land_duty_take(keys, args)
    local repo, instance, token, host, ttl = args[1] or '', args[2] or '', args[3] or '', args[4] or '', tonumber(args[5] or '')
    if repo == '' or instance == '' or token == '' or not ttl or ttl <= 0 then
      return { 'USAGE' }
    end
    local key, proc = 'lease:land:' .. repo, 'proc:land:' .. repo
    local holder = redis.call('HGET', key, 'instance')
    if holder then
      return { 'HELD', holder }
    end
    local now = ld_now_ms()
    local due = tonumber(redis.call('HGET', proc, 'due_at') or '') or 0
    if now < due then
      return { 'WAIT', tostring(due) }
    end
    local at = tostring(now)
    redis.call('HSET', key, 'instance', instance, 'token', token, 'host', host, 'at', at)
    redis.call('PEXPIRE', key, ttl)
    redis.call('HSET', proc, 'due_at', tostring(now + ld_tick_ms()), 'holder', instance, 'holder_at', at)
    return { 'TAKEN', at }
  end

  -- ns_land_duty_renew(repo, instance, token, ttl_ms): OK, or LOST holder.
  local function land_duty_renew(keys, args)
    local repo, instance, token, ttl = args[1] or '', args[2] or '', args[3] or '', tonumber(args[4] or '')
    local key = 'lease:land:' .. repo
    local ok, holder = ld_holds(key, instance, token)
    if not ok or not ttl or ttl <= 0 then
      return { 'LOST', holder }
    end
    redis.call('HSET', key, 'at', tostring(ld_now_ms()))
    redis.call('PEXPIRE', key, ttl)
    return { 'OK' }
  end

  -- ns_land_duty_pass(repo, instance, token, took_ms, streams, opened,
  -- landed, parked, unread, err): the worker's pass line on proc:land:<repo>
  -- and the lease given back, in one call. OK, or LOST holder (nothing
  -- written: another worker holds the repo).
  local function land_duty_pass(keys, args)
    local repo, instance, token = args[1] or '', args[2] or '', args[3] or ''
    local key = 'lease:land:' .. repo
    local ok, holder = ld_holds(key, instance, token)
    if not ok then
      return { 'LOST', holder }
    end
    local at = tostring(ld_now_ms())
    redis.call('HSET', 'proc:land:' .. repo, 'pass_at', at, 'took_ms', args[4] or '0', 'streams', args[5] or '0',
      'opened', args[6] or '0', 'landed', args[7] or '0', 'parked', args[8] or '0', 'unread', args[9] or '0',
      'err', args[10] or '', 'holder', '', 'at', at)
    redis.call('DEL', key)
    return { 'OK', at }
  end

  -- ns_land_duty_release(repo, instance, token): the holder gives the lease
  -- back without a pass line (a reconciler on its way out). OK, or LOST.
  local function land_duty_release(keys, args)
    local repo, instance, token = args[1] or '', args[2] or '', args[3] or ''
    local key = 'lease:land:' .. repo
    local ok, holder = ld_holds(key, instance, token)
    if not ok then
      return { 'LOST', holder }
    end
    redis.call('DEL', key)
    redis.call('HSET', 'proc:land:' .. repo, 'holder', '', 'released_at', tostring(ld_now_ms()))
    return { 'OK' }
  end

  redis.register_function('ns_land_duty_take', land_duty_take)
  redis.register_function('ns_land_duty_renew', land_duty_renew)
  redis.register_function('ns_land_duty_pass', land_duty_pass)
  redis.register_function('ns_land_duty_release', land_duty_release)
end
