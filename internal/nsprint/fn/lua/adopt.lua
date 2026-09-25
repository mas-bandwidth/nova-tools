-- Adoption receipts (nova-tools #3186, the matrix slice). No shebang:
-- loader.go prepends the single library header. A verb's adoption is one
-- Redis hash, adopt:<verb>, written only here, and the matrix is read from
-- those hashes, never from a hand-kept file:
--   adopt:verbs              ZSET verb -> created_at ms (age, strictly
--                            increasing; a later receipt never changes it)
--   adopt:<verb>             HASH verb hand state who at pov gap note
--                            created_at receipts, pov:<pov> -> "<who> <at>
--                            <state> <gap|->", sha:<who>:<pov> -> body sha
--   adopt:<verb>:receipts    LIST, one JSON receipt per accepted write
-- No TTL: keys do not expire.

local ADOPT_STATES = { ['adopted'] = true, ['adopted-gaps'] = true,
  ['in-flight'] = true, ['unexercised'] = true, ['blocked'] = true, ['hack'] = true }
-- A state that names a gap must carry one: the receipt's --gap, or a gap
-- already on the record.
local ADOPT_NEEDS_GAP = { ['adopted-gaps'] = true, ['blocked'] = true, ['hack'] = true }
local ADOPT_POVS = { 'coordinator', 'bench', 'reader', 'friend' }

local function adopt_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

local function adopt_is_pov(p)
  for _, v in ipairs(ADOPT_POVS) do
    if v == p then return true end
  end
  return false
end

-- ns_adopt_receipt(verb, who, pov, state, gap, hand, note)
-- One receipt: the check and the write in one call. Returns
-- REFUSED <reason>, UNCHANGED <at> (the same who, pov and body as the last
-- accepted receipt: nothing written), or RECORDED <at> <receipts>.
local function adopt_receipt(keys, args)
  local verb, who, pov, state = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
  local gap, hand, note = args[5] or '', args[6] or '', args[7] or ''
  if verb == '' then return { 'REFUSED', 'no-verb' } end
  if who == '' then return { 'REFUSED', 'no-who' } end
  if not adopt_is_pov(pov) then return { 'REFUSED', 'bad-pov' } end
  if not ADOPT_STATES[state] then return { 'REFUSED', 'bad-state' } end
  local key = 'adopt:' .. verb
  local cur = redis.call('HMGET', key, 'gap', 'sha:' .. who .. ':' .. pov, 'at')
  if ADOPT_NEEDS_GAP[state] and gap == '' and (cur[1] or '') == '' then
    return { 'REFUSED', 'no-gap' }
  end
  local sha = redis.sha1hex(state .. '\n' .. gap .. '\n' .. hand .. '\n' .. note)
  if cur[2] == sha then
    return { 'UNCHANGED', cur[3] or '' }
  end
  local at = adopt_now_ms()
  if not redis.call('ZSCORE', 'adopt:verbs', verb) then
    -- Age order is insertion order: a verb first seen in the same ms as the
    -- newest one is scored 1 ms after it, so ZRANGE never falls back to
    -- lexical order.
    local created = at
    local last = redis.call('ZRANGE', 'adopt:verbs', -1, -1, 'WITHSCORES')
    if last[2] and tonumber(last[2]) >= created then created = tonumber(last[2]) + 1 end
    redis.call('ZADD', 'adopt:verbs', created, verb)
    redis.call('HSET', key, 'created_at', string.format('%d', created))
  end
  redis.call('HSET', key, 'verb', verb, 'state', state, 'who', who, 'at', tostring(at),
    'pov', pov, 'note', note, 'sha:' .. who .. ':' .. pov, sha,
    'pov:' .. pov, who .. ' ' .. at .. ' ' .. state .. ' ' .. (gap ~= '' and gap or '-'))
  if gap ~= '' then redis.call('HSET', key, 'gap', gap) end
  if hand ~= '' then redis.call('HSET', key, 'hand', hand) end
  local n = redis.call('HINCRBY', key, 'receipts', 1)
  redis.call('RPUSH', key .. ':receipts', cjson.encode({ who = who, at = at, pov = pov,
    state = state, gap = gap, hand = hand, note = note }))
  return { 'RECORDED', tostring(at), tostring(n) }
end

-- ns_adopt_matrix(): every verb in age order, read-only. One row per verb:
-- {verb, hand, state, who, at, pov, gap, receipts, povs}, povs the POVs
-- that hold a receipt, comma-joined in the fixed order.
local function adopt_matrix(keys, args)
  local out = {}
  for _, verb in ipairs(redis.call('ZRANGE', 'adopt:verbs', 0, -1)) do
    local h = redis.call('HMGET', 'adopt:' .. verb, 'hand', 'state', 'who', 'at', 'pov', 'gap',
      'receipts', 'pov:coordinator', 'pov:bench', 'pov:reader', 'pov:friend')
    local povs = {}
    for i, p in ipairs(ADOPT_POVS) do
      if h[7 + i] then povs[#povs + 1] = p end
    end
    out[#out + 1] = { verb, h[1] or '', h[2] or '', h[3] or '', h[4] or '', h[5] or '',
      h[6] or '', h[7] or '0', table.concat(povs, ',') }
  end
  return out
end

redis.register_function('ns_adopt_receipt', adopt_receipt)
redis.register_function({ function_name = 'ns_adopt_matrix', flags = { 'no-writes' },
  callback = adopt_matrix })
