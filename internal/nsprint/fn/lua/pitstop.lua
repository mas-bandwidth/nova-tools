-- The sprint pit stop (nova-tools #3371). No shebang: loader.go prepends the
-- single library header. One key per sprint, s:<S>:pitstop, a hash {by, why,
-- at} (at from Redis TIME in ms), is the whole stop: the deal pass reads it
-- (deal.lua: ns_card_deal deals nothing from a sprint while it exists; the Go
-- plan skips the sprint first), and every other reader (the feed, the table)
-- reads the same key through internal/nsprint/pitstop. It is never a bus
-- note: a friend reads a key, never judges a note's authority.
--
-- Each call is the check and the write in one step, with its receipt on
-- s:<S>:log in the shape deal.lua writes (kind, id, from, to, attempt,
-- token_sha, actor, reason, evidence, idem, at).

local function pitstop_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function pitstop_receipt(S, kind, from_state, to_state, by, why, evidence, idem, at)
  redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', S, 'from', from_state, 'to', to_state,
    'attempt', '0', 'token_sha', '', 'actor', by, 'reason', why or '',
    'evidence', evidence or '', 'idem', idem or '', 'at', tostring(at))
end

-- ns_pitstop_set(S, by, why, force, idem)
-- Sets the stop. A sprint with no status is UNKNOWN; an existing stop is
-- REFUSED (with its by, why, at) unless force is '1', which replaces it whole
-- and names the replaced stop in the receipt's evidence. Returns UNKNOWN,
-- REFUSED <by> <why> <at>, or SET <at> <replaced by> <replaced why>
-- (both empty when nothing was replaced).
local function pitstop_set(keys, args)
  local S, by, why, force, idem = args[1], args[2], args[3] or '', args[4], args[5]
  if not S or S == '' then
    return redis.error_reply('ns_pitstop_set: sprint is required')
  end
  if not by or by == '' then
    return redis.error_reply('ns_pitstop_set: by is required')
  end
  if not redis.call('HGET', 's:' .. S, 'status') then
    return { 'UNKNOWN', S }
  end
  local key = 's:' .. S .. ':pitstop'
  local cur = redis.call('HMGET', key, 'by', 'why', 'at')
  local had = redis.call('EXISTS', key) == 1
  if had and force ~= '1' then
    return { 'REFUSED', cur[1] or '', cur[2] or '', cur[3] or '' }
  end
  local at = pitstop_now_ms()
  local evidence, from_state = '', 'none'
  if had then
    from_state = 'set'
    evidence = 'replaced by=' .. (cur[1] or '') .. ' at=' .. (cur[3] or '') .. ' why=' .. (cur[2] or '')
  end
  redis.call('DEL', key)
  redis.call('HSET', key, 'by', by, 'why', why, 'at', tostring(at))
  pitstop_receipt(S, 'pitstop set', from_state, 'set', by, why, evidence, idem, at)
  if had then
    return { 'SET', tostring(at), cur[1] or '', cur[2] or '' }
  end
  return { 'SET', tostring(at), '', '' }
end

-- ns_pitstop_clear(S, by, idem)
-- Lifts the stop. With none set it is NONE and writes nothing. Returns NONE
-- or CLEARED <at> <was by> <was why> <was at>.
local function pitstop_clear(keys, args)
  local S, by, idem = args[1], args[2], args[3]
  if not S or S == '' then
    return redis.error_reply('ns_pitstop_clear: sprint is required')
  end
  if not by or by == '' then
    return redis.error_reply('ns_pitstop_clear: by is required')
  end
  local key = 's:' .. S .. ':pitstop'
  if redis.call('EXISTS', key) == 0 then
    return { 'NONE' }
  end
  local cur = redis.call('HMGET', key, 'by', 'why', 'at')
  local at = pitstop_now_ms()
  redis.call('DEL', key)
  pitstop_receipt(S, 'pitstop clear', 'set', 'none', by, cur[2] or '',
    'was by=' .. (cur[1] or '') .. ' at=' .. (cur[3] or ''), idem, at)
  return { 'CLEARED', tostring(at), cur[1] or '', cur[2] or '', cur[3] or '' }
end

redis.register_function('ns_pitstop_set', pitstop_set)
redis.register_function('ns_pitstop_clear', pitstop_clear)
