-- The sprint pit stop (nova-tools #3371). No shebang: loader.go prepends the
-- single library header. One key per sprint, s:<S>:pitstop, a hash {by, why,
-- at, scope} (at from Redis TIME in ms), is the whole stop: the deal pass reads it
-- (deal.lua: ns_card_deal deals nothing from a sprint while it exists; the Go
-- plan skips the sprint first), and every other reader (the feed, the table)
-- reads the same key through internal/nsprint/pitstop. It is never a bus
-- note: a friend reads a key, never judges a note's authority.
--
-- Scope: scope=all stops every stream except those named by a field
-- lifted:<stream>; scope=streams stops only the streams named by a field
-- stream:<stream>. A hash with no scope field (written before scope) is all.
-- A stream name may hold spaces and colons ("redis: store + bus"), so each
-- stream is its own field and a reader asks with one HEXISTS. NS.pitstop
-- .in_scope(S, stream) is that question for a file sorted after this one (the
-- take path); the Go reader is pitstop.Stop.InScope.
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

-- NS.pitstop.in_scope(S, stream): true while s:<S>:pitstop stops the stream
-- (one EXISTS and at most two field reads; false when no stop is set).
NS.pitstop = {}
function NS.pitstop.in_scope(S, stream)
  local key = 's:' .. S .. ':pitstop'
  if redis.call('EXISTS', key) == 0 then
    return false
  end
  if redis.call('HGET', key, 'scope') == 'streams' then
    return redis.call('HEXISTS', key, 'stream:' .. stream) == 1
  end
  return redis.call('HEXISTS', key, 'lifted:' .. stream) == 0
end

-- ns_pitstop_set(S, by, why, force, idem[, stream...])
-- Sets the stop: with no stream it is scope=all, with streams scope=streams
-- over exactly those. A sprint with no status is UNKNOWN; an existing stop is
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
  local fields, scope = { 'by', by, 'why', why, 'at', tostring(at), 'scope', 'all' }, 'all'
  if #args > 5 then
    fields[8], scope = 'streams', ''
    for i = 6, #args do
      if args[i] == '' then
        return redis.error_reply('ns_pitstop_set: a stream is empty')
      end
      fields[#fields + 1], fields[#fields + 2] = 'stream:' .. args[i], tostring(at)
      scope = scope .. (i > 6 and '; ' or '') .. args[i]
    end
  end
  if evidence ~= '' then
    evidence = evidence .. ' '
  end
  evidence = evidence .. 'scope=' .. scope
  redis.call('DEL', key)
  redis.call('HSET', key, unpack(fields))
  pitstop_receipt(S, 'pitstop set', from_state, 'set', by, why, evidence, idem, at)
  if had then
    return { 'SET', tostring(at), cur[1] or '', cur[2] or '' }
  end
  return { 'SET', tostring(at), '', '' }
end

-- ns_pitstop_clear(S, by, idem[, stream...])
-- Lifts the stop. With none set it is NONE and writes nothing. With no stream
-- it deletes the stop whole: CLEARED <at> <was by> <was why> <was at>. With
-- streams it narrows the stop by exactly those: under scope=all each becomes
-- lifted:<stream>; under scope=streams each stream:<stream> goes, and the
-- last one going deletes the stop (CLEARED). A named stream already out of
-- scope refuses the whole call with nothing written: NOTIN <stream>. A narrow
-- that leaves a stop is NARROWED <at> <was by> <was why> <was at>.
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
  local was = { cur[1] or '', cur[2] or '', cur[3] or '' }
  local at = pitstop_now_ms()
  if #args <= 3 then
    redis.call('DEL', key)
    pitstop_receipt(S, 'pitstop clear', 'set', 'none', by, was[2],
      'was by=' .. was[1] .. ' at=' .. was[3], idem, at)
    return { 'CLEARED', tostring(at), was[1], was[2], was[3] }
  end
  local streams = redis.call('HGET', key, 'scope') == 'streams'
  local lifted = ''
  for i = 4, #args do
    if args[i] == '' then
      return redis.error_reply('ns_pitstop_clear: a stream is empty')
    end
    if not NS.pitstop.in_scope(S, args[i]) then
      return { 'NOTIN', args[i] }
    end
    lifted = lifted .. (i > 4 and '; ' or '') .. args[i]
  end
  for i = 4, #args do
    if streams then
      redis.call('HDEL', key, 'stream:' .. args[i])
    else
      redis.call('HSET', key, 'lifted:' .. args[i], tostring(at))
    end
  end
  local left = true
  if streams then
    left = false
    for _, f in ipairs(redis.call('HKEYS', key)) do
      if string.sub(f, 1, 7) == 'stream:' then
        left = true
        break
      end
    end
  end
  local evidence = 'was by=' .. was[1] .. ' at=' .. was[3] .. ' lifted=' .. lifted
  if not left then
    redis.call('DEL', key)
    pitstop_receipt(S, 'pitstop clear', 'set', 'none', by, was[2], evidence, idem, at)
    return { 'CLEARED', tostring(at), was[1], was[2], was[3] }
  end
  pitstop_receipt(S, 'pitstop clear', 'set', 'set', by, was[2], evidence, idem, at)
  return { 'NARROWED', tostring(at), was[1], was[2], was[3] }
end

redis.register_function('ns_pitstop_set', pitstop_set)
redis.register_function('ns_pitstop_clear', pitstop_clear)
