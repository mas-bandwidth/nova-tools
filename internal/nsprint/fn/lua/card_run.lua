-- Card launched, beat, and end. One function per transition.
-- Token mismatch returns 3 and writes nothing, before the record is considered,
-- so a fenced caller stops and the record stays for a later ingest.
-- The results directory is this attempt's <sprint>/<label>/<base sha8>/<bench>/<attempt>.
-- A copy of the record in any other directory is not an end.
-- A record whose identity is not this attempt returns NOTHING and writes nothing.
-- Branch names are not read here.

local function now_ms()
  local t = redis.call('TIME')
  local ms = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  return string.format('%.0f', ms)
end

-- now_ms stays in the shared chunk: classify, prtoread, report, review, route
-- and route_lease read it as an upvalue. Everything else in this file is one
-- do-block (like harvest.lua), so its locals never add to the library's
-- 200-local main-function limit (dev crossed it at 6bf01359, #3487).
do
-- Every state write here is NS.card (02_card_move.lua): launched and beat
-- are working -> working, end is working -> done (ok when DONE and the
-- attempt's typed result is not invalid, else fail), and a result stored
-- invalid after a DONE end moves done/ok -> done/fail.
local CARD = NS.card

local function reply(code, status, attempt, receipt)
  if attempt == nil then attempt = '' end
  if receipt == nil then receipt = '' end
  return tostring(code) .. '|' .. tostring(status) .. '|' .. tostring(attempt) .. '|' .. tostring(receipt)
end

local function hget(key, field)
  local v = redis.call('HGET', key, field)
  if not v then return '' end
  return tostring(v)
end

local function reason_ok(outcome, reason)
  if outcome == 'DONE' and reason == 'done' then return true end
  -- refused (#3194): the harness's own program refused the card before it ran
  -- (a NATIVE REFUSED line); the card's why carries that line.
  -- no-commit: a code card whose model said DONE and committed nothing
  -- (w_commit NO-COMMIT); the card's why says so.
  if reason == 'crash' or reason == 'timeout' or reason == 'wall' or reason == 'idle-killed' or reason == 'tests-red' or reason == 'refused'
      or reason == 'no-commit' then
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

-- The end's whole result on the card record (nova-tools#3919; the card model:
-- "at end the whole result"), read from the attempt's result hash that the
-- wrapper wrote from its own facts moments before in ns_card_result, never
-- re-parsed: record field <- the first result field present. A field the
-- result hash lacks is not written.
local RESULT_ON_RECORD = {
  { 'result_line1', 'w_line1', 'line1' },
  { 'result_line2', 'w_line2' },
  { 'check', 'w_check', 'c_check' },
  { 'red', 'c_red' },
  { 'green', 'c_green' },
  { 'result_paths', 'w_paths', 'c_paths' },
  { 'wall_ms', 'w_wall_ms' },
  { 'model', 'w_model' },
  { 'provider', 'w_route' },
}

-- result_fields appends the result's record fields to fields.
local function result_fields(res_key, fields)
  for _, row in ipairs(RESULT_ON_RECORD) do
    for i = 2, #row do
      local v = redis.call('HGET', res_key, row[i])
      if v then
        fields[#fields + 1] = row[1]
        fields[#fields + 1] = v
        break
      end
    end
  end
end

-- An ABSTAIN whose line 2 is `ABSTAIN done-already <sha>` names the commit
-- that already did the card's work: ns_card_end queues its label on
-- s:<S>:done-already (score ended_at) for the reconciler's done-already leg,
-- which checks the sha against the base in its mirror and closes the issue.
local function done_already_sha(line2)
  local sha = string.match(line2 or '', '^ABSTAIN done%-already (%x+)')
  if sha and #sha >= 7 and #sha <= 40 then return string.lower(sha) end
  return nil
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

-- ns_card_claim is the attempt claim (#3328), a Function since #3551: the
-- bench's Redis user may FCALL only, never EVAL/EVALSHA. Fenced on the token
-- and on state dealt; writes claim (<token_sha>:<nonce>, never the token) and
-- claim_at (Redis TIME, ms), fields only this function writes. A claim whose
-- token_sha is not the card's current one belongs to an earlier attempt and is
-- replaced. Codes: 0 claimed (or this nonce's own retry), 2 not dealt,
-- 3 fenced, 4 another wrapper holds the attempt (checked before the state),
-- 5 no card.
redis.register_function('ns_card_claim', function(keys, args)
  local sprint, label, token, nonce = args[1], args[2], args[3] or '', args[4] or ''
  if not card_keys_ok(keys, sprint, label) then return reply(4, 'CONFLICT', '', '') end
  local card_key = keys[1]
  local state = hget(card_key, 'state')
  if state == '' then return reply(5, 'NOTFOUND', '', '') end
  local attempt = hget(card_key, 'attempt')
  if token == '' or token ~= hget(card_key, 'token') then return reply(3, 'FENCED', attempt, '') end
  if nonce == '' then return reply(2, 'STATE', attempt, '') end
  local tsha = hget(card_key, 'token_sha')
  local mine = tsha .. ':' .. nonce
  local held = hget(card_key, 'claim')
  if held == mine then return reply(0, 'OK', attempt, '') end
  -- Another wrapper's claim on this attempt is 4 whatever the state: the
  -- winner may already have moved the card past dealt (ns_card_launched)
  -- before the loser's claim lands, and the loser must still read CONFLICT.
  if held ~= '' and string.sub(held, 1, #tsha + 1) == tsha .. ':' then return reply(4, 'CONFLICT', attempt, '') end
  if state ~= 'dealt' then return reply(2, 'STATE', attempt, '') end
  redis.call('HSET', card_key, 'claim', mine, 'claim_at', now_ms())
  return reply(0, 'OK', attempt, '')
end)

redis.register_function('ns_card_launched', function(keys, args)
  local sprint, label, token, branch, jobdir = args[1], args[2], args[3] or '', args[4] or '', args[5] or ''
  local deadline = args[6] or ''
  -- wall_max_s (#3653): the attempt's wall cap in whole seconds; empty or
  -- absent stores nothing (the pre-#3653 six-argument shape).
  local wall_max_s = args[7] or ''
  if wall_max_s ~= '' and not string.match(wall_max_s, '^[1-9][0-9]*$') then
    return reply(1, 'USAGE', '', '')
  end
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
  if deadline ~= '' then
    local deadline_ms = tonumber(deadline)
    if not deadline_ms or deadline_ms < 1 then return reply(2, 'STATE', attempt, '') end
    if tonumber(at) >= deadline_ms then return reply(2, 'TIMEOUT', attempt, '') end
  end
  local fields = { 'branch', branch, 'jobdir', jobdir, 'launched_at', at }
  if wall_max_s ~= '' then
    fields[#fields + 1] = 'wall_max_s'
    fields[#fields + 1] = wall_max_s
  end
  if CARD.move(card_key, 'working', { state = 'launched', fields = fields, by = 'card-launched', why = 'launched' }) then
    return reply(2, 'STATE', attempt, '')
  end
  local receipt = xadd(log_key, label, 'dealt', 'launched', attempt, hget(card_key, 'token_sha'), 'card-launched', 'launched', branch, idem, at)
  redis.call('HSET', card_key, 'launched_receipt', receipt)
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
    if CARD.move(card_key, 'working', { state = 'running', fields = { 'beat_at', at }, by = 'card-beat', why = 'beat' }) then
      return reply(2, 'STATE', attempt, '')
    end
    redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
  else
    redis.call('HSET', card_key, 'beat_at', at)
  end
  redis.call('ZADD', 'bench:' .. bench .. ':living', at, member)
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
  -- why (#3194) is the wrapper's evidence for the end, one line: for FAILED
  -- refused it is the refusal line the harness's program printed. It is
  -- written to the card's why only when given, so an end without evidence
  -- never blanks a why another writer left.
  local why = string.sub((string.gsub(args[14] or '', '[\r\n]', ' ')), 1, 1024)
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
  if not id_sprint or id_sprint ~= sprint or id_label ~= label or id_base ~= string.sub(hget(card_key, 'base_sha'), 1, 8) or id_bench ~= bench or id_attempt ~= attempt then
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
  local actor = 'card-resolve'
  if mode == 'token' then actor = 'card-end' end
  -- DONE with a typed result that is not invalid is ok; ABSTAIN is abstain
  -- (#3919: the model said the card is not its to do, not that it failed);
  -- any other end is fail.
  local res_key = card_key .. ':result:a' .. attempt
  local ok = 'fail'
  if record_outcome == 'DONE' and hget(res_key, 'valid') ~= '0' then ok = 'ok' end
  if record_outcome == 'ABSTAIN' then ok = 'abstain' end
  local end_fields = { 'outcome', record_outcome, 'reason', record_reason, 'exit', record_exit,
    'pushed_sha', record_pushed, 'results', results, 'ended_at', at, 'commit_sha', record_pushed }
  result_fields(res_key, end_fields)
  -- The end's evidence (#3194) rides the one move, so a refused card's why is
  -- written with its done/fail and never apart from it.
  if why ~= '' then
    end_fields[#end_fields + 1] = 'why'
    end_fields[#end_fields + 1] = why
    end_fields[#end_fields + 1] = 'why_at'
    end_fields[#end_fields + 1] = at
  end
  if CARD.move(card_key, 'done', { state = 'ended', ok = ok, by = actor, why = record_reason,
      fields = end_fields }) then
    return reply(2, 'STATE', attempt, '')
  end
  redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
  redis.call('ZREM', 'bench:' .. bench .. ':living', member)
  redis.call('SADD', 's:' .. sprint .. ':bench:' .. bench .. ':ended', label)
  if record_outcome == 'ABSTAIN' and done_already_sha(hget(res_key, 'w_line2')) then
    redis.call('ZADD', 's:' .. sprint .. ':done-already', at, label)
  end
  local receipt = xadd(log_key, label, state, 'ended', attempt, stored_sha, actor, record_reason, results, idem, at)
  redis.call('HSET', card_key, 'end_receipt', receipt)
  redis.call('HSET', idem_key, idem, receipt)
  return reply(0, 'OK', attempt, receipt)
end)

-- The six typed-record card kinds (typedrec.Kinds). A card hash kind outside
-- this set (model, script) is a runner kind and sets no RESULT expectation.
-- card push refuses any other KIND before the card is stored, naming this set
-- (internal/nsprint/card/kinds.go, nova-tools#3651); its KindMap maps a
-- classification kind (go-verb, spec, ...) to one of these at cut time.
local RESULT_KINDS = { ['fix'] = true, ['recut'] = true, ['port'] = true,
  ['docs-guard'] = true, ['report'] = true, ['read'] = true }

redis.register_function('ns_card_result', function(keys, args)
  local sprint = args[1]
  local label = args[2]
  local attempt = args[3]
  local token = args[4] or ''
  local schema = args[5] or ''
  local kind = args[6] or ''
  local valid = args[7] or '0'
  local field = args[8] or ''
  local defect = args[9] or ''
  local line = args[10] or '0'
  local raw_sha256 = args[11] or ''
  local raw_bytes = args[12] or ''
  local results = args[13] or ''

  local card_key = 's:' .. sprint .. ':card:' .. label
  local state = hget(card_key, 'state')
  if state == '' then return reply(5, 'NOTFOUND', attempt, '') end
  if token == '' or token ~= hget(card_key, 'token') then
    return reply(3, 'FENCED', attempt, '')
  end

  local res_key = 's:' .. sprint .. ':card:' .. label .. ':result:a' .. attempt
  if redis.call('EXISTS', res_key) == 1 then
    local prev_sha = redis.call('HGET', res_key, 'raw_sha256')
    if prev_sha == raw_sha256 then
      return reply(0, 'OK', attempt, '')
    end
    return reply(4, 'CONFLICT', attempt, '')
  end

  local v_repo = hget(card_key, 'repo')
  local v_kind = hget(card_key, 'kind')
  local v_contract = hget(card_key, 'contract')
  local v_base_sha = hget(card_key, 'base_sha')
  local v_bench = hget(card_key, 'bench')
  local v_attempt = hget(card_key, 'attempt')
  local v_branch = hget(card_key, 'branch')
  local v_pr_head = hget(card_key, 'pushed_sha')
  if v_pr_head == '' then
    v_pr_head = hget(card_key, 'head')
  end

  local at = now_ms()
  local hset_args = {
    res_key,
    'schema', schema,
    'kind', kind,
    'valid', valid,
    'field', field,
    'defect', defect,
    'line', line,
    'raw_sha256', raw_sha256,
    'raw_bytes', raw_bytes,
    'results', results,
    'by', 'card-wrapper',
    'at', at,
    'v_repo', v_repo,
    'v_base_sha', v_base_sha,
    'v_bench', v_bench,
    'v_attempt', v_attempt,
    'v_branch', v_branch,
    'v_pr_head', v_pr_head,
  }

  -- w_synth=1 (#3689): the card wrapper wrote KIND, REPO, BRANCH and ATTEMPT
  -- from this card hash and its own run, so a disagreement with the card is a
  -- wrapper bug, never the model's: it is logged (wrapper_bug on the result
  -- hash and a card-result-bug log entry) and the record keeps its validity,
  -- so the card stays DONE. line 1 and HEAD are still the worker's and still
  -- refuse.
  local synth = false
  for i = 14, #args, 2 do
    if args[i] == 'w_synth' and args[i+1] == '1' then synth = true end
  end
  local bugs = {}
  local function contradict(f)
    if synth and f ~= 'line 1' and f ~= 'HEAD' then
      table.insert(bugs, f)
      return
    end
    valid = '0'
    field = f
    defect = 'contradictory'
  end

  -- KIND is checked against the declared set and the card's own KIND here,
  -- whatever the caller's parse said: a valid=1 claim with a kind outside the
  -- six, or a kind other than a typed card's, is persisted invalid.
  if valid == '1' then
    if not RESULT_KINDS[kind] then
      valid = '0'
      field = 'KIND'
      defect = 'malformed'
    elseif RESULT_KINDS[v_kind] and kind ~= v_kind then
      contradict('KIND')
    end
  end

  for i = 14, #args, 2 do
    local k = args[i]
    local v = args[i+1] or ''
    if k and k ~= '' then
      table.insert(hset_args, k)
      table.insert(hset_args, v)
      if valid == '1' then
        if k == 'line1' and v_contract ~= '' and v ~= v_contract then
          contradict('line 1')
        elseif k == 'c_repo' and v ~= '' and v_repo ~= '' and v ~= v_repo then
          contradict('REPO')
        elseif k == 'c_branch' and v ~= '' and v_branch ~= '' and v ~= v_branch then
          contradict('BRANCH')
        elseif k == 'c_attempt' and v ~= '' and v_attempt ~= '' and v ~= v_attempt then
          contradict('ATTEMPT')
        elseif k == 'c_head' and v ~= '' and v_pr_head ~= '' and v ~= v_pr_head then
          contradict('HEAD')
        end
      end
    end
  end
  if #bugs > 0 then
    local bug = table.concat(bugs, ',') .. ' contradictory'
    table.insert(hset_args, 'wrapper_bug')
    table.insert(hset_args, bug)
    if keys[2] == 's:' .. sprint .. ':log' then
      redis.call('XADD', keys[2], '*', 'kind', 'card', 'id', label, 'attempt', tostring(attempt),
        'actor', 'card-result', 'reason', 'wrapper-bug', 'evidence', bug, 'at', at)
    end
  end

  hset_args[7] = valid
  hset_args[9] = field
  hset_args[11] = defect

  -- An invalid result of the attempt that ended DONE moves the card done/ok
  -- -> done/fail BEFORE the result is stored. A refused move (drift) stores
  -- nothing, logs the refusal to sprint:<S>:moves and returns 2|MOVE with
  -- that receipt: the card stays where its sets say, and the wrapper sees a
  -- non-zero code.
  if valid ~= '1' and attempt == v_attempt and hget(card_key, 'where') == 'done' and hget(card_key, 'where_ok') == 'ok' then
    local refused = CARD.move(card_key, 'done', { ok = 'fail', by = 'card-result', why = 'result ' .. field .. ' ' .. defect })
    if refused then
      local r = redis.call('XADD', 'sprint:' .. sprint .. ':moves', 'MAXLEN', '~', '100000', '*',
        'id', card_key, 'stream', hget(card_key, 'stream'), 'from', 'done/ok', 'to', 'REFUSED',
        'by', 'card-result', 'why', refused, 'at', at)
      return reply(2, 'MOVE', attempt, r)
    end
  end
  redis.call('HSET', unpack(hset_args))
  return reply(0, 'OK', attempt, '')
end)

-- ns_card_show S label (read only, #3689): the card hash and its current
-- attempt's result hash in one call, so nobody sshes to a bench to learn what
-- a card did. The token is never returned. Reply: the card's field/value
-- pairs, then '--', then the result hash's pairs (none when there is none).
redis.register_function{
  function_name = 'ns_card_show',
  flags = { 'no-writes' },
  callback = function(keys, args)
    local sprint, label = args[1] or '', args[2] or ''
    if sprint == '' or label == '' then return { 'USAGE' } end
    local card_key = 's:' .. sprint .. ':card:' .. label
    local out = {}
    local attempt = ''
    local h = redis.call('HGETALL', card_key)
    for i = 1, #h, 2 do
      if h[i] == 'attempt' then attempt = h[i + 1] end
      if h[i] ~= 'token' then
        table.insert(out, h[i])
        table.insert(out, h[i + 1])
      end
    end
    table.insert(out, '--')
    if attempt ~= '' then
      local r = redis.call('HGETALL', card_key .. ':result:a' .. attempt)
      for i = 1, #r do table.insert(out, r[i]) end
    end
    return out
  end,
}
end
