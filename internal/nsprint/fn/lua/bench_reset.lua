-- Bench reset: a persistent, fenced hold around stopping and requeueing one
-- bench's in-flight cards (#3311). No reset key has a TTL.
-- Wrapped in do/end so its helpers stay out of the library main chunk's
-- 200-local limit (the same shape as classify.lua and route.lua).
do
  -- The requeue's state write is NS.card (02_card_move.lua): working -> ready.
  local CARD = NS.card

  local function reset_now_ms()
    local t = redis.call('TIME')
    return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  end

  local function reset_record(bench)
    return 'bench:' .. bench .. ':reset'
  end

  local function reset_fence(bench, id)
    local key = reset_record(bench)
    local current = redis.call('HGET', key, 'id')
    if not current or current ~= id then
      return { 'FENCED', current or '' }
    end
    return nil
  end

  local function reset_receipt(kind, bench, id, actor, why, stopped, requeued, kept, alive, at)
    redis.call('XADD', 'cap:log', 'MAXLEN', '~', '100000', '*',
      'kind', kind, 'bench', bench, 'id', id or '', 'actor', actor or '',
      'why', why or '', 'stopped', tostring(stopped or 0),
      'requeued', tostring(requeued or 0), 'kept', tostring(kept or 0),
      'alive', tostring(alive or 0), 'at', tostring(at))
  end

  -- ns_bench_reset_begin(bench, id, actor)
  local function bench_reset_begin(keys, args)
    local bench, id, actor, idem = args[1], args[2], args[3], args[4]
    if not bench or bench == '' or not id or id == '' or not actor or actor == '' then
      return redis.error_reply('ns_bench_reset_begin: bench, id and actor are required')
    end
    if redis.call('SISMEMBER', 'benches', bench) == 0 then
      return { 'UNREGISTERED', bench }
    end
    local key = reset_record(bench)
    local old = redis.call('HMGET', key, 'id', 'actor', 'phase', 'at')
    local now = reset_now_ms()
    if old[1] then
      local age = now - (tonumber(old[4]) or 0)
      if old[3] == 'running' and age < 120000 then
        return { 'BUSY', old[2] or '', tostring(math.floor(age / 1000)) }
      end
      reset_receipt('bench-reset-takeover', bench, id, actor, 'from=' .. (old[1] or ''), 0, 0, 0, 0, now)
    end
    redis.call('HSET', key, 'id', id, 'actor', actor, 'phase', 'running',
      'why', '', 'started_at', tostring(now), 'at', tostring(now), 'idem', idem or '')
    redis.call('PERSIST', key)
    return { 'STARTED', id, tostring(now), old[1] or '' }
  end

  -- ns_bench_reset_beat(bench, id)
  local function bench_reset_beat(keys, args)
    local bench, id = args[1], args[2]
    local fenced = reset_fence(bench, id)
    if fenced then return fenced end
    local key = reset_record(bench)
    if redis.call('HGET', key, 'phase') ~= 'running' then
      return { 'FENCED', redis.call('HGET', key, 'id') or '' }
    end
    local now = reset_now_ms()
    redis.call('HSET', key, 'at', tostring(now))
    return { 'BEAT', tostring(now) }
  end

  local function reset_card_receipt(S, label, from_state, attempt, token_sha, actor, at)
    redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'card reset', 'id', label, 'from', from_state, 'to', 'queued',
      'attempt', tostring(attempt), 'token_sha', token_sha or '',
      'actor', actor or '', 'reason', 'bench-reset', 'evidence', '',
      'idem', '', 'at', tostring(at))
  end

  -- ns_bench_reset_requeue(bench, id, actor, then S,label,attempt,status)
  -- status is STOPPED, GONE, ALIVE or KEPT. The call returns one row for every
  -- input card: S,label,attempt,from,to, followed by the four counts.
  local function bench_reset_requeue(keys, args)
    local bench, id, actor = args[1], args[2], args[3]
    local fenced = reset_fence(bench, id)
    if fenced then return fenced end
    if (#args - 3) % 4 ~= 0 then
      return redis.error_reply('ns_bench_reset_requeue: cards are S,label,attempt,status')
    end
    -- Validate the entire caller-controlled shape before the first write: a
    -- Redis Function error does not roll earlier writes back.
    for i = 4, #args, 4 do
      local status = args[i + 3]
      if status ~= 'STOPPED' and status ~= 'GONE' and status ~= 'ALIVE' and status ~= 'KEPT' then
        return redis.error_reply('ns_bench_reset_requeue: status must be STOPPED, GONE, ALIVE or KEPT')
      end
    end
    local at = reset_now_ms()
    local rows = {}
    local stopped, requeued, kept, alive = 0, 0, 0, 0
    for i = 4, #args, 4 do
      local S, label, attempt, status = args[i], args[i + 1], args[i + 2], args[i + 3]
      local ck = 's:' .. S .. ':card:' .. label
      local c = redis.call('HMGET', ck, 'state', 'bench', 'attempt', 'token_sha', 'pin', 'priority')
      local from, to = c[1] or '', 'kept'
      if c[2] == bench and c[3] == attempt and (from == 'dealt' or from == 'launched' or from == 'running') then
        local qk = 's:' .. S .. ':bench:' .. bench .. ':queue'
        if (status == 'STOPPED' or status == 'GONE') and
            not CARD.move(ck, 'ready', { state = 'queued', bench = c[5] or '', by = actor, why = 'bench-reset',
              priority = tonumber(redis.call('ZSCORE', qk, label) or c[6]) or 0,
              fields = { 'token', '', 'reason', 'bench-reset' } }) then
          local pin = c[5] or ''
          if pin ~= bench then
            redis.call('ZREM', qk, label)
          end
          reset_card_receipt(S, label, from, attempt, c[4], actor, at)
          stopped = stopped + 1
          requeued = requeued + 1
          to = 'queued'
        elseif status == 'ALIVE' then
          alive = alive + 1
          to = 'alive'
        else
          kept = kept + 1
        end
      else
        kept = kept + 1
      end
      rows[#rows + 1] = S
      rows[#rows + 1] = label
      rows[#rows + 1] = attempt
      rows[#rows + 1] = from
      rows[#rows + 1] = to
    end
    local out = { 'RESET', tostring(stopped), tostring(requeued), tostring(kept), tostring(alive) }
    for _, v in ipairs(rows) do out[#out + 1] = v end
    return out
  end

  -- ns_bench_reset_end(bench,id,actor,stopped,requeued,kept,alive)
  local function bench_reset_end(keys, args)
    local bench, id, actor = args[1], args[2], args[3]
    local fenced = reset_fence(bench, id)
    if fenced then return fenced end
    local at = reset_now_ms()
    -- Receipt first: if cap:log is malformed, the persistent reset guard stays.
    reset_receipt('bench-reset', bench, id, actor, '', args[4], args[5], args[6], args[7], at)
    redis.call('DEL', reset_record(bench))
    return { 'ENDED', tostring(at) }
  end

  -- ns_bench_reset_hold(bench,id,actor,why,stopped,requeued,kept,alive)
  local function bench_reset_hold(keys, args)
    local bench, id, actor, why = args[1], args[2], args[3], args[4]
    local fenced = reset_fence(bench, id)
    if fenced then return fenced end
    if not why or why == '' then return redis.error_reply('ns_bench_reset_hold: why is required') end
    local at = reset_now_ms()
    redis.call('HSET', reset_record(bench), 'phase', 'held', 'why', why, 'at', tostring(at))
    redis.call('PERSIST', reset_record(bench))
    reset_receipt('bench-reset-held', bench, id, actor, why, args[5], args[6], args[7], args[8], at)
    return { 'HELD', tostring(at) }
  end

  -- ns_bench_reset_clear(bench,actor,why): held, or stale running only.
  local function bench_reset_clear(keys, args)
    local bench, actor, why = args[1], args[2], args[3]
    if not why or why == '' then return redis.error_reply('ns_bench_reset_clear: why is required') end
    local key = reset_record(bench)
    local h = redis.call('HMGET', key, 'id', 'phase', 'at')
    if not h[1] then return { 'NOT-HELD' } end
    local now = reset_now_ms()
    local age = now - (tonumber(h[3]) or 0)
    if h[2] == 'running' and age < 120000 then return { 'BUSY', tostring(math.floor(age / 1000)) } end
    if h[2] ~= 'held' and h[2] ~= 'running' then return { 'NOT-HELD' } end
    -- Receipt first: clear never unblocks a bench without its operator record.
    reset_receipt('bench-reset-cleared', bench, h[1], actor, why, 0, 0, 0, 0, now)
    redis.call('DEL', key)
    return { 'CLEARED', h[1], tostring(now) }
  end

  redis.register_function('ns_bench_reset_begin', bench_reset_begin)
  redis.register_function('ns_bench_reset_beat', bench_reset_beat)
  redis.register_function('ns_bench_reset_requeue', bench_reset_requeue)
  redis.register_function('ns_bench_reset_end', bench_reset_end)
  redis.register_function('ns_bench_reset_hold', bench_reset_hold)
  redis.register_function('ns_bench_reset_clear', bench_reset_clear)
end
