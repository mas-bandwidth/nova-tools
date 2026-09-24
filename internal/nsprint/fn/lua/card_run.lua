-- Card launched, beat, and end. One function per transition.
-- Token mismatch returns 3 and writes nothing, before the record is considered,
-- so a fenced caller stops and the record stays for a later ingest.
-- The results directory is this attempt's <sprint>/<label>/<base sha8>/<bench>/<attempt>.
-- A copy of the record in any other directory is not an end.
-- A record whose identity is not this attempt returns NOTHING and writes nothing.
-- Branch names are not read here.

local function reply(code, status, attempt, receipt)
  if attempt == nil then attempt = '' end
  if receipt == nil then receipt = '' end
  return tostring(code) .. '|' .. tostring(status) .. '|' .. tostring(attempt) .. '|' .. tostring(receipt)
end

local function now_ms()
  local t = redis.call('TIME')
  local ms = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  return string.format('%.0f', ms)
end

local function hget(key, field)
  local v = redis.call('HGET', key, field)
  if not v then return '' end
  return tostring(v)
end

local function reason_ok(outcome, reason)
  if outcome == 'DONE' and reason == 'done' then return true end
  if reason == 'crash' or reason == 'timeout' or reason == 'idle-killed' or reason == 'tests-red' then
    return outcome == 'FAILED'
  end
  if reason == 'env' or reason == 'base-moved' or reason == 'deps' or reason == 'spec' or reason == 'access' then
    return outcome == 'BLOCKED'
  end
  if reason == 'scope' then return outcome == 'ABSTAIN' end
  if reason == 'other' then
    return outcome == 'ABSTAIN' or outcome == 'BLOCKED' or outcome == 'FAILED'
  end
  return false
end

local function identity_parts(identity)
  local sprint, label, base, bench, attempt = string.match(identity, '^([^/]+)/([^/]+)/([^/]+)/([^/]+)/([^/]+)$')
  if not sprint then return nil end
  if not string.match(base, '^[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]$') then
    return nil
  end
  if not string.match(attempt, '^[1-9][0-9]*$') then return nil end
  return sprint, label, base, bench, attempt
end

local function card_keys_ok(keys, sprint, label)
  return keys[1] == 's:' .. sprint .. ':card:' .. label
    and keys[2] == 's:' .. sprint .. ':log'
    and keys[3] == 's:' .. sprint .. ':idem'
end

-- results is a Unix absolute path on the bench (#3329, #3336): a leading '/',
-- not '//' (a network share), no backslash, no CR/LF, no '..' segment. A drive
-- root ('C:\x', 'C:/x') or a scheme ('file:///x') has no leading '/' and is refused.
-- card.AbsResults is the same rule on the Go side. The card hash field is read
-- by harvest with no root to join it to.
local function results_absolute(results)
  if string.sub(results, 1, 1) ~= '/' or string.sub(results, 2, 2) == '/' then return false end
  if string.find(results, '[\\\r\n]') then return false end
  for seg in string.gmatch(results, '[^/]+') do
    if seg == '..' then return false end
  end
  return true
end

-- results is the canonical attempt directory under an absolute root.
local function results_bound(results, identity)
  local sprint, label, base, bench, attempt = identity_parts(identity)
  if not sprint or results == '' then return false end
  local want = sprint .. '/' .. label .. '/' .. base .. '/' .. bench .. '/' .. attempt
  local suffix = '/' .. want
  if #results < #suffix then return false end
  return string.sub(results, -#suffix) == suffix
end

local function xadd(log_key, id, from, to, attempt, token_sha, actor, reason, evidence, idem, at)
  return redis.call('XADD', log_key, '*',
    'kind', 'card',
    'id', id,
    'from', from,
    'to', to,
    'attempt', tostring(attempt),
    'token_sha', token_sha,
    'actor', actor,
    'reason', reason,
    'evidence', evidence,
    'idem', idem,
    'at', at)
end

redis.register_function('ns_card_launched', function(keys, args)
  local sprint, label, token, branch, jobdir = args[1], args[2], args[3] or '', args[4] or '', args[5] or ''
  if not card_keys_ok(keys, sprint, label) then return reply(4, 'CONFLICT', '', '') end
  local card_key, log_key, idem_key = keys[1], keys[2], keys[3]
  local state = hget(card_key, 'state')
  if state == '' then return reply(5, 'NOTFOUND', '', '') end
  local attempt = hget(card_key, 'attempt')
  if token == '' or token ~= hget(card_key, 'token') then
    return reply(3, 'FENCED', attempt, '')
  end
  if branch == '' or jobdir == '' then return reply(2, 'STATE', attempt, '') end
  local identity = hget(card_key, 'identity')
  local idem = 'launched:' .. identity
  local prev = hget(idem_key, idem)
  if state == 'launched' or state == 'running' then
    if hget(card_key, 'branch') == branch and prev ~= '' and (state == 'running' or hget(card_key, 'jobdir') == jobdir) then
      return reply(0, 'OK', attempt, prev)
    end
    return reply(4, 'CONFLICT', attempt, '')
  end
  if state ~= 'dealt' then return reply(2, 'STATE', attempt, '') end
  local at = now_ms()
  local receipt = xadd(log_key, label, 'dealt', 'launched', attempt, hget(card_key, 'token_sha'), 'card-launched', 'launched', branch, idem, at)
  redis.call('HSET', card_key, 'state', 'launched', 'branch', branch, 'jobdir', jobdir, 'launched_at', at, 'launched_receipt', receipt)
  redis.call('SREM', 's:' .. sprint .. ':idx:card:dealt', label)
  redis.call('SADD', 's:' .. sprint .. ':idx:card:launched', label)
  redis.call('HSET', idem_key, idem, receipt)
  return reply(0, 'OK', attempt, receipt)
end)

redis.register_function('ns_card_beat', function(keys, args)
  local sprint, label, token = args[1], args[2], args[3] or ''
  if not card_keys_ok(keys, sprint, label) then return reply(4, 'CONFLICT', '', '') end
  local card_key, log_key = keys[1], keys[2]
  local state = hget(card_key, 'state')
  if state == '' then return reply(5, 'NOTFOUND', '', '') end
  local attempt = hget(card_key, 'attempt')
  if token == '' or token ~= hget(card_key, 'token') then
    return reply(3, 'FENCED', attempt, '')
  end
  if state ~= 'launched' and state ~= 'running' then return reply(2, 'STATE', attempt, '') end
  local bench = hget(card_key, 'bench')
  if bench == '' then return reply(2, 'STATE', attempt, '') end
  local at = now_ms()
  local member = sprint .. '/' .. label .. '/' .. attempt
  local from = state
  if state == 'launched' then
    redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
    redis.call('SREM', 's:' .. sprint .. ':idx:card:launched', label)
    redis.call('SADD', 's:' .. sprint .. ':idx:card:running', label)
  end
  redis.call('ZADD', 'bench:' .. bench .. ':living', at, member)
  redis.call('HSET', card_key, 'state', 'running', 'beat_at', at)
  local idem = 'beat:' .. hget(card_key, 'identity') .. ':' .. at
  local receipt = xadd(log_key, label, from, 'running', attempt, hget(card_key, 'token_sha'), 'card-beat', 'beat', '-', idem, at)
  return reply(0, 'OK', attempt, receipt)
end)

redis.register_function('ns_card_end', function(keys, args)
  local mode = args[1]
  local sprint, label = args[2], args[3]
  local token = args[4] or ''
  local results = args[5] or ''
  local record_identity = args[6] or ''
  local record_outcome = args[7] or ''
  local record_reason = args[8] or ''
  local record_sha = args[9] or ''
  local record_pushed = args[10] or ''
  local record_exit = args[11] or ''
  local claim_outcome = args[12] or ''
  local claim_reason = args[13] or ''
  if mode ~= 'token' and mode ~= 'record' then return reply(2, 'STATE', '', '') end
  if not results_absolute(results) then return reply(1, 'USAGE', '', '') end
  if not card_keys_ok(keys, sprint, label) then return reply(4, 'CONFLICT', '', '') end
  local card_key, log_key, idem_key = keys[1], keys[2], keys[3]
  local state = hget(card_key, 'state')
  if state == '' then return reply(5, 'NOTFOUND', '', '') end
  local attempt = hget(card_key, 'attempt')
  local stored_sha = hget(card_key, 'token_sha')
  local identity = hget(card_key, 'identity')
  local bench = hget(card_key, 'bench')

  if mode == 'token' then
    if token == '' or token ~= hget(card_key, 'token') then
      return reply(3, 'FENCED', attempt, '')
    end
  end

  -- A copy in any other directory is not this attempt's record.
  if not results_bound(results, identity) then
    record_identity = ''
  end

  if record_identity == '' then
    if mode == 'token' then return reply(2, 'NO-RECORD', attempt, '') end
    return reply(0, 'NOTHING', attempt, '')
  end

  -- Another attempt, another cut, or another bench is not this card.
  if record_identity ~= identity then
    if mode == 'token' then return reply(2, 'IDENTITY', attempt, '') end
    return reply(0, 'NOTHING', attempt, '')
  end

  local id_sprint, id_label, id_base, id_bench, id_attempt = identity_parts(identity)
  if not id_sprint or id_sprint ~= sprint or id_label ~= label or id_base ~= hget(card_key, 'base_sha') or id_bench ~= bench or id_attempt ~= attempt then
    return reply(4, 'CONFLICT', attempt, '')
  end

  if record_sha == '' or record_sha ~= stored_sha then
    if mode == 'token' then return reply(2, 'TOKEN-SHA', attempt, '') end
    return reply(0, 'NOTHING', attempt, '')
  end

  if claim_outcome ~= record_outcome or claim_reason ~= record_reason then
    return reply(2, 'RECORD', attempt, '')
  end
  if not reason_ok(record_outcome, record_reason) then
    return reply(2, 'REASON', attempt, '')
  end

  local idem = 'end:' .. identity
  local prev = hget(idem_key, idem)
  if state == 'ended' then
    if hget(card_key, 'outcome') == record_outcome and hget(card_key, 'reason') == record_reason and prev ~= '' then
      return reply(0, 'OK', attempt, prev)
    end
    return reply(4, 'CONFLICT', attempt, '')
  end

  local allowed = (mode == 'token' and state == 'running')
    or (mode == 'record' and (state == 'running' or state == 'reconcile-required' or state == 'orphan-effect'))
  if not allowed then return reply(2, 'STATE', attempt, '') end

  local at = now_ms()
  local member = sprint .. '/' .. label .. '/' .. attempt
  redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
  redis.call('ZREM', 'bench:' .. bench .. ':living', member)
  redis.call('SREM', 's:' .. sprint .. ':idx:card:' .. state, label)
  redis.call('SADD', 's:' .. sprint .. ':idx:card:ended', label)
  redis.call('SADD', 's:' .. sprint .. ':bench:' .. bench .. ':ended', label)
  local actor = 'card-resolve'
  if mode == 'token' then actor = 'card-end' end
  local receipt = xadd(log_key, label, state, 'ended', attempt, stored_sha, actor, record_reason, results, idem, at)
  redis.call('HSET', card_key,
    'state', 'ended',
    'outcome', record_outcome,
    'reason', record_reason,
    'exit', record_exit,
    'pushed_sha', record_pushed,
    'results', results,
    'ended_at', at,
    'end_receipt', receipt)
  redis.call('HSET', idem_key, idem, receipt)
  return reply(0, 'OK', attempt, receipt)
end)
