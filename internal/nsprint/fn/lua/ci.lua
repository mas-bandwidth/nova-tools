-- CI passes are cards (#2756 section 10, nova-tools #2936). One function per
-- transition; each checks its guard, writes the ci card hash, the global
-- verdict record ci:<repo>:<head>:<gid> and one receipt in the sprint log in one
-- atomic call (spec 2.1 rule 2). No shebang: loader.go prepends the header.
--
-- The verdict record is a write-once receipt (#3139 rev 7 3.7): ns_ci_cut
-- and ns_ci_rerun write only the card (PENDING and the attempt live on
-- s:<S>:card:<label>), and ns_ci_end writes a final verdict through
-- gate_receipt_write in the same call that ends the card. ns_ci_dispose is the
-- typed disposition on a FLAKY head (10.5 item 3).
--
-- Every lua/ file shares one chunk and Lua allows 200 locals in it, so only
-- the two names land.lua shares (ci_sha256_hex, gate_receipt_write) are
-- file-level locals, exported as NS.ci at the end; the rest of this file is
-- one do-block.
--
-- Execution state and verdict are distinct (10.5 item 5): a script that ran
-- to its end ends DONE with verdict OK or FAIL; a wrapper failure ends FAILED
-- or BLOCKED and writes no verdict, so the head reads MISSING.
--
-- Tasks wait on a head (nova-tools #3382, the waiting-ci step of the closed
-- #3128 rebuilt here per ci card): a task parked on cicard:<repo>:<sha> is state
-- waiting-ci in its sprint's idx:task:waiting-ci, on no open queue, with its
-- queue in `to` ('' is the ready pool), and <sprint>/<id> is a member of the
-- head's set cicard:<repo>:<sha>:waiting. Its queue score is kept: `wait_score`
-- is the ZSCORE it had when it left its queue (a front push is -priority);
-- absent, the score is its stored `priority`, as for a plain push. ns_ci_end settles that set in the call
-- that writes the verdict: OK opens every member to its queue (enqueued_at is
-- this TIME, so the read clock starts at the OK); FAIL parks them (state
-- parked, reason ci-fail); FLAKY and MISSING leave them waiting.
--
-- The ci-fail item <pr>:<head>:ci-fail:<pkg> in s:<S>:unresolved has one
-- writer, ns_ci_end, when the failure stands with the rerun budget spent.
-- Parking cuts no item of its own, and a HOLD on a FLAKY head writes its fix
-- item under <pr>:<head>:flaky-hold:<pkg>.

local ci_sha256_hex, gate_receipt_write
do
local CI_FRONT = -1000000000

local ci_sha256_k = {
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
}

function ci_sha256_hex(msg)
  local band, bor, bxor, bnot = bit.band, bit.bor, bit.bxor, bit.bnot
  local ror, rshift, lshift = bit.ror, bit.rshift, bit.lshift

  local h0, h1, h2, h3, h4, h5, h6, h7 =
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
    0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19

  local len = #msg
  local words = {}
  local word = 0
  local byte_idx = 0
  for i = 1, len do
    word = bor(word, lshift(string.byte(msg, i), (3 - (byte_idx % 4)) * 8))
    byte_idx = byte_idx + 1
    if byte_idx % 4 == 0 then
      table.insert(words, word)
      word = 0
    end
  end

  word = bor(word, lshift(0x80, (3 - (byte_idx % 4)) * 8))
  byte_idx = byte_idx + 1
  if byte_idx % 4 == 0 then
    table.insert(words, word)
    word = 0
  end

  while (byte_idx % 64) ~= 56 do
    byte_idx = byte_idx + 1
    if byte_idx % 4 == 0 then
      table.insert(words, word)
      word = 0
    end
  end
  if byte_idx % 4 ~= 0 then
    table.insert(words, word)
    word = 0
  end

  local bit_len = len * 8
  table.insert(words, math.floor(bit_len / 4294967296))
  table.insert(words, bit.tobit(bit_len % 4294967296))

  local W = {}
  for b = 0, (#words / 16) - 1 do
    for t = 0, 15 do
      W[t + 1] = words[b * 16 + t + 1]
    end
    for t = 16, 63 do
      local s0 = bxor(ror(W[t - 15 + 1], 7), ror(W[t - 15 + 1], 18), rshift(W[t - 15 + 1], 3))
      local s1 = bxor(ror(W[t - 2 + 1], 17), ror(W[t - 2 + 1], 19), rshift(W[t - 2 + 1], 10))
      W[t + 1] = bit.tobit(W[t - 16 + 1] + s0 + W[t - 7 + 1] + s1)
    end

    local a, b_val, c, d, e, f, g, h = h0, h1, h2, h3, h4, h5, h6, h7
    for t = 0, 63 do
      local S1 = bxor(ror(e, 6), ror(e, 11), ror(e, 25))
      local ch = bxor(band(e, f), band(bnot(e), g))
      local temp1 = bit.tobit(h + S1 + ch + ci_sha256_k[t + 1] + W[t + 1])
      local S0 = bxor(ror(a, 2), ror(a, 13), ror(a, 22))
      local maj = bxor(band(a, b_val), band(a, c), band(b_val, c))
      local temp2 = bit.tobit(S0 + maj)

      h = g
      g = f
      f = e
      e = bit.tobit(d + temp1)
      d = c
      c = b_val
      b_val = a
      a = bit.tobit(temp1 + temp2)
    end

    h0 = bit.tobit(h0 + a)
    h1 = bit.tobit(h1 + b_val)
    h2 = bit.tobit(h2 + c)
    h3 = bit.tobit(h3 + d)
    h4 = bit.tobit(h4 + e)
    h5 = bit.tobit(h5 + f)
    h6 = bit.tobit(h6 + g)
    h7 = bit.tobit(h7 + h)
  end

  return bit.tohex(h0) .. bit.tohex(h1) .. bit.tohex(h2) .. bit.tohex(h3) ..
         bit.tohex(h4) .. bit.tohex(h5) .. bit.tohex(h6) .. bit.tohex(h7)
end

local function ci_now_ms()
  local t = redis.call('TIME')
  return string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
end

-- gate_receipt_write (3.7): writes the write-once single or tip gid receipt.
-- ci:<repo>:<head>:<gid> and ci:<repo>:<base>:tip:<tip>:<gid>.
function gate_receipt_write(repo, head, gid, verdict, kind, base, base_sha, required_set_id, policy_id, runner_id, receipt, bench, pkg, test, at)
  local ckey = 'ci:' .. repo .. ':' .. head .. ':' .. gid
  if redis.call('EXISTS', ckey) == 1 then
    return 'ALREADY'
  end
  at = at or ci_now_ms()
  redis.call('HSET', ckey,
    'verdict', verdict,
    'gid', gid,
    'head', head,
    'repo', repo,
    'kind', kind,
    'base', base,
    'base_sha', base_sha,
    'required_set_id', required_set_id,
    'policy_id', policy_id,
    'runner_id', runner_id,
    'receipt', receipt,
    'bench', bench or '',
    'pkg', pkg or '',
    'test', test or '',
    'at', tostring(at)
  )
  redis.call('SADD', 'ci:' .. repo .. ':' .. head .. ':gids', gid)
  if kind == 'tip' then
    local tip_key = 'ci:' .. repo .. ':' .. base .. ':tip:' .. base_sha .. ':' .. gid
    redis.call('HSET', tip_key,
      'verdict', verdict,
      'kind', kind,
      'base', base,
      'base_sha', base_sha,
      'required_set_id', required_set_id,
      'policy_id', policy_id,
      'runner_id', runner_id,
      'receipt', receipt,
      'bench', bench or '',
      'pkg', pkg or '',
      'test', test or '',
      'at', tostring(at)
    )
  end
  return 'OK'
end

local function ci_hget(key, field)
  local v = redis.call('HGET', key, field)
  if not v then return '' end
  return tostring(v)
end

local function ci_receipt(S, kind, id, from_state, to_state, attempt, token_sha, actor, reason, evidence, idem, at)
  return redis.call('XADD', 's:' .. S .. ':log', '*',
    'kind', kind, 'id', id, 'from', from_state, 'to', to_state,
    'attempt', tostring(attempt or 0), 'token_sha', token_sha or '',
    'actor', actor or '', 'reason', reason or '', 'evidence', evidence or '',
    'idem', idem or '', 'at', tostring(at))
end

-- Every ci card state write is NS.card (02_card_move.lua): cut creates the
-- record and moves it to ready, end is working -> done (ok only for DONE with
-- verdict OK), rerun and dispose are done/fail -> ready on the new bench.
local CARD = NS.card

-- A leg list is the desired hash's `legs` field, space or comma separated,
-- written by `capacity bench --legs` (#3349). THE ONE RULE for an empty or
-- absent list, the same in deal.lua's deal_runs: the bench runs every leg
-- (one bench standard: every bench runs any card). A bench that declares a
-- list carries only the legs it names.
-- A bench whose registry role is friends (#3634) carries no leg at all.
local function ci_carries(bench, leg)
  if ci_hget('bench:' .. bench .. ':desired', 'role') == 'friends' then return false end
  local legs = ci_hget('bench:' .. bench .. ':desired', 'legs')
  if legs == '' then return true end
  for l in string.gmatch(legs, '[^%s,]+') do
    if l == leg then return true end
  end
  return false
end

local function ci_healthy(bench, leg)
  if redis.call('HGET', 'bench:' .. bench .. ':state', 'state') ~= 'UP' then return false end
  local paused = ci_hget('bench:' .. bench .. ':desired', 'paused')
  if paused == '1' or paused == 'true' then return false end
  return ci_carries(bench, leg)
end

-- ci_settle_waiting opens (OK) or parks (FAIL) every task waiting on
-- ci:<repo>:<head>, in whichever sprint it lives, with one receipt each in
-- that sprint's log. A member whose task is gone, has moved on or names
-- another head is dropped. Released members leave the set; parked ones stay,
-- so an OK at this exact head still reaches them.
local function ci_settle_waiting(repo, head, final, pkg, actor, at)
  if final ~= 'OK' and final ~= 'FAIL' then return end
  local wkey = 'ci:' .. repo .. ':' .. head .. ':waiting'
  for _, member in ipairs(redis.call('SMEMBERS', wkey)) do
    local T, id = string.match(member, '^([^/]+)/(.+)$')
    local key = T and ('s:' .. T .. ':task:' .. id) or ''
    local row = T and redis.call('HMGET', key, 'state', 'repo', 'head', 'to', 'priority', 'reason', 'wait_score') or {}
    local state = row[1]
    local live = row[2] == repo and row[3] == head and
      (state == 'waiting-ci' or (state == 'parked' and row[6] == 'ci-fail'))
    if not live then
      redis.call('SREM', wkey, member)
    elseif final == 'OK' then
      local to = row[4] or ''
      -- The score it had: wait_score when the parker kept one (a front push
      -- is -priority), else its stored priority unchanged, as ns_task_push
      -- scores a plain push and task_beat requeues. Never negated here.
      local score = tonumber(row[7]) or tonumber(row[5]) or 0
      redis.call('HSET', key, 'state', 'open', 'reason', '', 'enqueued_at', at)
      redis.call('HDEL', key, 'wait_score')
      redis.call('SREM', 's:' .. T .. ':idx:task:' .. state, id)
      redis.call('SADD', 's:' .. T .. ':idx:task:open', id)
      if to ~= '' then
        redis.call('ZADD', 's:' .. T .. ':open:' .. to, score, id)
      else
        redis.call('ZADD', 's:' .. T .. ':ready', score, id)
      end
      redis.call('SREM', wkey, member)
      ci_receipt(T, 'task', id, state, 'open', 0, '', actor, 'ci-ok',
        'ci:' .. repo .. ':' .. head .. ' OK', 'ci-ok:' .. head .. ':' .. id, at)
    elseif state == 'waiting-ci' then
      redis.call('HSET', key, 'state', 'parked', 'reason', 'ci-fail')
      redis.call('SREM', 's:' .. T .. ':idx:task:waiting-ci', id)
      redis.call('SADD', 's:' .. T .. ':idx:task:parked', id)
      ci_receipt(T, 'task', id, 'waiting-ci', 'parked', 0, '', actor, 'ci-fail',
        'ci:' .. repo .. ':' .. head .. ' FAIL pkg=' .. pkg, 'ci-fail:' .. head .. ':' .. id, at)
    end
  end
end

local function ci_sorted_benches()
  local benches = redis.call('SMEMBERS', 'benches')
  table.sort(benches)
  return benches
end

-- ns_ci_cut: cut ci-<pr>-<head8>-<base8> for one PR head at one tested base
-- tip (4.8, 10.2, nova-tools #3148). The card waits in pool at the front tier;
-- PENDING lives only on the card in this same call. Each (head, base_sha) is
-- its own unit: the same pair again is EXISTS; a card at the label for another
-- full head or base_sha (an 8-hex prefix clash) is CONFLICT and writes
-- nothing. The cut writes no ci: key (#3139 rev 7 3.7): the gids set that
-- ns_ci_end writes is the head's index of tested bases.
local function ci_cut(keys, args)
  local S, label, repo, pr, head, base = args[1], args[2], args[3], args[4], args[5], args[6]
  local base_sha, leg, paths, actor, idem = args[7], args[8], args[9], args[10], args[11]
  local card_key = 's:' .. S .. ':card:' .. label

  if ci_hget('s:' .. S, 'status') ~= 'open' then return { 'CLOSED' } end
  if redis.call('EXISTS', card_key) == 1 then
    if ci_hget(card_key, 'ci_head') == head and ci_hget(card_key, 'base_sha') == base_sha then
      return { 'EXISTS', ci_hget(card_key, 'attempt') }
    end
    return { 'CONFLICT', label }
  end

  local carried = false
  for _, b in ipairs(ci_sorted_benches()) do
    if ci_carries(b, leg) then carried = true break end
  end
  if not carried then return { 'RUNNER-ONLY', leg } end

  local at = ci_now_ms()
  local cut_fields = {
    'kind', 'script', 'leg', leg, 'tier', 'front', 'repo', repo,
    'base', base, 'base_sha', base_sha, 'paths', paths, 'depends_on', 'none',
    'priority', tostring(CI_FRONT), 'attempt', '1',
    'identity', '', 'token', '', 'token_sha', '', 'avoid', '',
    'ci_for', repo .. ' ' .. pr .. ' ' .. head .. ' ' .. base,
    'ci_repo', repo, 'ci_pr', pr, 'ci_head', head,
    'cut_at', at, 'outcome', '', 'reason', '', 'reruns', '0', 'prev', '',
    'verdict', 'PENDING', 'disp', '', 'blocked', '' }
  local err = CARD.create(card_key, cut_fields, { by = actor })
  if not err then err = CARD.move(card_key, 'ready', { by = actor, why = 'ci cut', priority = CI_FRONT }) end
  if err then return redis.error_reply('ns_ci_cut: ' .. err) end
  ci_receipt(S, 'ci cut', label, '', 'queued', 1, '', actor, 'cut', repo .. '@' .. head, idem, at)
  return { 'CREATED', '1' }
end

-- ns_ci_end: the ci card's end record ends the card and writes a final verdict
-- receipt via gate_receipt_write (10.3, 10.5 item 5, 3.7). Token first: a mismatch
-- is FENCED and writes nothing. A record naming another attempt resolves NOTHING.
-- DONE carries OK or FAIL; FAILED or BLOCKED carries no verdict.
local function ci_end(keys, args)
  local S, label, token, identity = args[1], args[2], args[3], args[4]
  local outcome, reason, verdict = args[5], args[6], args[7]
  local pkg, test, wall_s, log, tree, actor = args[8], args[9], args[10], args[11], args[12], args[13]
  local card_key = 's:' .. S .. ':card:' .. label

  local state = ci_hget(card_key, 'state')
  if state == '' then return { 'NOTFOUND' } end
  if ci_hget(card_key, 'kind') ~= 'script' or ci_hget(card_key, 'ci_head') == '' then return { 'NOTCI' } end
  local attempt = ci_hget(card_key, 'attempt')
  if token == '' or token ~= ci_hget(card_key, 'token') then return { 'FENCED', attempt } end
  if identity ~= ci_hget(card_key, 'identity') then return { 'NOTHING', attempt } end
  if state == 'ended' then
    if ci_hget(card_key, 'outcome') == outcome and ci_hget(card_key, 'reason') == reason then
      return { 'ENDED', attempt }
    end
    return { 'CONFLICT', attempt }
  end
  if state ~= 'dealt' and state ~= 'launched' and state ~= 'running' then return { 'STATE', attempt } end

  if outcome == 'DONE' then
    if reason ~= 'done' or (verdict ~= 'OK' and verdict ~= 'FAIL') then return { 'RECORD', attempt } end
    if verdict == 'FAIL' and pkg == '' then return { 'RECORD', attempt } end
  elseif outcome == 'FAILED' then
    -- tests-red never ends a ci card: red tests are DONE with verdict FAIL.
    if verdict ~= '' or (reason ~= 'crash' and reason ~= 'timeout' and reason ~= 'idle-killed') then
      return { 'RECORD', attempt }
    end
  elseif outcome == 'BLOCKED' then
    if verdict ~= '' or reason ~= 'env' then return { 'RECORD', attempt } end
  else
    return { 'RECORD', attempt }
  end

  local at = ci_now_ms()
  local repo, pr, head = ci_hget(card_key, 'ci_repo'), ci_hget(card_key, 'ci_pr'), ci_hget(card_key, 'ci_head')
  local bench = ci_hget(card_key, 'bench')
  local member = S .. '/' .. label .. '/' .. attempt

  local prev = ci_hget(card_key, 'prev')
  local disp = ci_hget(card_key, 'disp')
  local reruns = tonumber(ci_hget(card_key, 'reruns')) or 0
  local budget = tonumber(ci_hget('s:' .. S .. ':policy', 'ci_reruns')) or 1

  local final = ''
  local pbench, pverdict, ppkg, ptest = '', '', '', ''
  if prev ~= '' then
    pbench, pverdict, ppkg, ptest = string.match(prev, '^([^|]*)|([^|]*)|([^|]*)|([^|]*)$')
  end

  if verdict == '' then
    final = ''
  elseif pverdict == 'FAIL' and verdict == 'OK' then
    if disp == '1' then
      final = 'OK'
    else
      final = 'FLAKY'
    end
  elseif pverdict == 'FAIL' and verdict == 'FAIL' and (ppkg ~= pkg or ptest ~= test) then
    if disp == '1' then
      final = 'FAIL'
    else
      final = 'FLAKY'
    end
  else
    final = verdict
  end

  local ok = 'fail'
  if outcome == 'DONE' and final == 'OK' then ok = 'ok' end
  local refused = CARD.move(card_key, 'done', { state = 'ended', ok = ok, by = actor, why = reason })
  if refused then return { 'STATE', attempt } end
  redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
  redis.call('ZREM', 'bench:' .. bench .. ':living', member)
  redis.call('SADD', 's:' .. S .. ':bench:' .. bench .. ':ended', label)

  local flaky = ''
  if final == 'FLAKY' then
    flaky = pbench .. ':FAIL,' .. bench .. ':' .. verdict
    local fpkg = ppkg
    if fpkg == '' then fpkg = pkg end
    redis.call('HSETNX', 's:' .. S .. ':unresolved', pr .. ':' .. head .. ':flaky:' .. fpkg,
      'flaky ' .. repo .. ' ' .. head .. ' ' .. flaky .. ' ' .. S .. '/' .. label)
  end

  if final == 'FAIL' and (reruns >= budget or disp == '1') then
    redis.call('HSETNX', 's:' .. S .. ':unresolved', pr .. ':' .. head .. ':ci-fail:' .. pkg,
      'ci-fail ' .. repo .. ' ' .. head .. ' ' .. pkg .. ' ' .. test .. ' ' .. S .. '/' .. label)
  end

  redis.call('HSET', card_key, 'outcome', outcome, 'reason', reason,
    'ended_at', at, 'results', log, 'tree', tree, 'verdict', (final ~= '' and final or 'MISSING'),
    'pkg', pkg, 'test', test, 'wall_s', tostring(wall_s or ''), 'flaky', flaky)

  ci_settle_waiting(repo, head, final, pkg, actor, at)

  local should_write = false
  if final == 'OK' and (prev == '' or disp == '1') then
    should_write = true
  elseif final == 'FAIL' and (reruns >= budget or disp == '1') then
    should_write = true
  end

  if should_write then
    local base = ci_hget(card_key, 'base')
    local base_sha = ci_hget(card_key, 'base_sha')
    local pol_key = 'land:' .. repo .. ':' .. base .. ':policy'
    local pol = redis.call('HMGET', pol_key, 'policy_id', 'required_set_id', 'runner_id')
    local policy_id, required_set_id, runner_id = pol[1], pol[2], pol[3]
    if not policy_id or policy_id == '' or not required_set_id or required_set_id == '' or not runner_id or runner_id == '' then
      ci_receipt(S, 'ci end', label, state, 'ended', attempt, ci_hget(card_key, 'token_sha'), actor,
        outcome .. ' ' .. reason, 'NOPOLICY ' .. pol_key, 'ci-end:' .. identity, at)
      return { 'NOPOLICY', attempt }
    end

    local raw = 'kind=single,' .. base .. ',' .. base_sha .. ',' .. required_set_id .. ',' .. policy_id .. ',' .. runner_id
    local gid = string.sub(ci_sha256_hex(raw), 1, 16)
    local rkey = 's:' .. S .. ':card:' .. label
    local res = gate_receipt_write(repo, head, gid, final, 'single', base, base_sha, required_set_id, policy_id, runner_id, rkey, bench, pkg, test, at)
    if res == 'ALREADY' then
      ci_receipt(S, 'ci end', label, state, 'ended', attempt, ci_hget(card_key, 'token_sha'), actor,
        outcome .. ' ' .. reason, 'ALREADY ' .. gid, 'ci-end:' .. identity, at)
      return { 'ALREADY', attempt }
    end
    local ckey = 'ci:' .. repo .. ':' .. head .. ':' .. gid
    redis.call('HSET', ckey, 'card', S .. '/' .. label, 'attempt', tostring(attempt or '1'), 'log', log, 'tree', tree, 'wall_s', tostring(wall_s or ''), 'pr', tostring(pr or ''), 'source', 'card', 'cut_at', ci_hget(card_key, 'cut_at'), 'end_at', tostring(at))
  end

  local evidence = repo .. '@' .. head .. ' ' .. (final ~= '' and final or (verdict ~= '' and verdict or 'MISSING')) ..
    ' bench=' .. bench .. ' pkg=' .. pkg .. ' test=' .. test .. ' wall_s=' .. wall_s ..
    ' log=' .. log
  ci_receipt(S, 'ci end', label, state, 'ended', attempt, ci_hget(card_key, 'token_sha'), actor,
    outcome .. ' ' .. reason, evidence, 'ci-end:' .. identity, at)
  return { 'ENDED', attempt, final }
end

-- ns_ci_rerun: exactly one rerun per head (or up to policy budget), on another healthy bench (10.5
-- item 2). The reconciler calls it from policy; a hand call carries a reason.
-- The rerun is a new attempt of the same label pinned to the other bench, and
-- the card goes PENDING in this same call. No alternate bench is a visible blocked
-- state on the card, never a silent wait.
local function ci_rerun(keys, args)
  local S, label, actor, reason, idem = args[1], args[2], args[3], args[4], args[5]
  local card_key = 's:' .. S .. ':card:' .. label
  local state = ci_hget(card_key, 'state')
  if state == '' then return { 'NOTFOUND' } end
  if ci_hget(card_key, 'kind') ~= 'script' or ci_hget(card_key, 'ci_head') == '' then return { 'NOTCI' } end
  local repo, head = ci_hget(card_key, 'ci_repo'), ci_hget(card_key, 'ci_head')
  local attempt = tonumber(ci_hget(card_key, 'attempt'))
  if state ~= 'ended' then return { 'STATE', tostring(attempt) } end

  local verdict = ci_hget(card_key, 'verdict')
  if verdict ~= 'FAIL' and verdict ~= '' and verdict ~= 'MISSING' then return { 'STATE', tostring(attempt) } end

  local budget = tonumber(ci_hget('s:' .. S .. ':policy', 'ci_reruns')) or 1
  local reruns = tonumber(ci_hget(card_key, 'reruns')) or 0
  if reruns >= budget then return { 'SPENT', tostring(attempt) } end

  local from_bench = ci_hget(card_key, 'bench')
  local leg = ci_hget(card_key, 'leg')
  local to_bench = ''
  for _, b in ipairs(ci_sorted_benches()) do
    if b ~= from_bench and ci_healthy(b, leg) then to_bench = b break end
  end
  local at = ci_now_ms()
  if to_bench == '' then
    redis.call('HSET', card_key, 'blocked', 'blocked: no alternate bench')
    return { 'BLOCKED', tostring(attempt) }
  end

  local next_attempt = attempt + 1
  local prev = from_bench .. '|' .. (verdict == '' and 'MISSING' or verdict) .. '|' ..
    ci_hget(card_key, 'pkg') .. '|' .. ci_hget(card_key, 'test')
  local refused = CARD.move(card_key, 'ready', { state = 'queued', bench = to_bench, by = actor, why = 'ci rerun',
    priority = CI_FRONT,
    fields = { 'attempt', tostring(next_attempt),
      'identity', '', 'token', '', 'token_sha', '', 'avoid', from_bench,
      'outcome', '', 'reason', '', 'reruns', tostring(reruns + 1), 'prev', prev, 'blocked', '',
      'verdict', 'PENDING' } })
  if refused then return { 'STATE', tostring(attempt) } end
  redis.call('ZADD', 's:' .. S .. ':bench:' .. to_bench .. ':queue', CI_FRONT, label)
  ci_receipt(S, 'ci rerun', label, 'ended', 'queued', next_attempt, '', actor, reason,
    repo .. '@' .. head .. ' ' .. from_bench .. '->' .. to_bench, idem, at)
  return { 'RERUN', tostring(next_attempt), to_bench }
end

-- ns_ci_dispose: the typed disposition on a FLAKY head (10.5 item 3). APPROVE
-- queues one rerun outside the budget (card disp=1) whose end writes its verdict,
-- OK or FAIL; HOLD leaves FLAKY and cuts one fix item. Either closes the item.
local function ci_dispose(keys, args)
  local S, repo, head, disposition, friend, url = args[1], args[2], args[3], args[4], args[5], args[6]
  local unresolved = 's:' .. S .. ':unresolved'
  local item = ''
  local pr, pkg = '', ''
  local fields = redis.call('HKEYS', unresolved)
  local match_pattern = '^(%d+):' .. head .. ':flaky:(.+)$'
  for _, f in ipairs(fields) do
    local p, k = string.match(f, match_pattern)
    if p and k then
      item = f
      pr = p
      pkg = k
      break
    end
  end
  if item == '' then return { 'STATE' } end

  -- The item names its card (<S>/<label>, written by ns_ci_end), so the
  -- disposition lands on the (head, base) unit that went FLAKY (#3148).
  local named = string.match(ci_hget(unresolved, item), '(%S+)$') or ''
  if string.sub(named, 1, #S + 1) ~= S .. '/' then return { 'STATE' } end
  local label = string.sub(named, #S + 2)
  local card_key = 's:' .. S .. ':card:' .. label
  if redis.call('EXISTS', card_key) == 0 then return { 'STATE' } end
  if ci_hget(card_key, 'verdict') ~= 'FLAKY' then return { 'STATE' } end

  local at = ci_now_ms()
  if disposition == 'APPROVE' then
    local attempt = tonumber(ci_hget(card_key, 'attempt')) or 1
    local next_attempt = attempt + 1
    local from_bench = ci_hget(card_key, 'bench')
    local leg = ci_hget(card_key, 'leg')
    local to_bench = ''
    for _, b in ipairs(ci_sorted_benches()) do
      if b ~= from_bench and ci_healthy(b, leg) then to_bench = b break end
    end
    if to_bench == '' and ci_healthy(from_bench, leg) then
      to_bench = from_bench
    end
    local refused = CARD.move(card_key, 'ready', { state = 'queued', bench = to_bench, by = friend, why = 'ci dispose APPROVE',
      priority = CI_FRONT,
      fields = { 'attempt', tostring(next_attempt),
        'identity', '', 'token', '', 'token_sha', '', 'avoid', from_bench,
        'outcome', '', 'reason', '', 'verdict', 'PENDING', 'disp', '1',
        'disposition', 'APPROVE ' .. friend .. ' ' .. url, 'log', url, 'blocked', '' } })
    if refused then return { 'STATE' } end
    if to_bench ~= '' then
      redis.call('ZADD', 's:' .. S .. ':bench:' .. to_bench .. ':queue', CI_FRONT, label)
    end
    redis.call('HDEL', unresolved, item)
    ci_receipt(S, 'ci dispose', label, 'FLAKY', 'PENDING', attempt,
      '', friend, disposition, url, 'ci-dispose:' .. item, at)
    return { disposition }
  elseif disposition == 'HOLD' then
    local attempt = ci_hget(card_key, 'attempt')
    redis.call('HSET', card_key, 'disposition', 'HOLD ' .. friend .. ' ' .. url, 'disp', 'hold')
    redis.call('HSETNX', unresolved, pr .. ':' .. head .. ':flaky-hold:' .. pkg,
      'flaky-hold ' .. repo .. ' ' .. head .. ' ' .. pkg .. ' ' .. S .. '/' .. label)
    redis.call('HDEL', unresolved, item)
    ci_receipt(S, 'ci dispose', label, 'FLAKY', 'FLAKY', attempt,
      '', friend, disposition, url, 'ci-dispose:' .. item, at)
    return { disposition }
  else
    return { 'RECORD' }
  end
end

redis.register_function('ns_ci_cut', ci_cut)
redis.register_function('ns_ci_end', ci_end)
redis.register_function('ns_ci_rerun', ci_rerun)
redis.register_function('ns_ci_dispose', ci_dispose)
end

-- The cross-file surface (loader.go: every file is its own do-block, and NS
-- is the one chunk-level local). land.lua sorts after this file.
NS.ci = { sha256_hex = ci_sha256_hex, gate_receipt_write = gate_receipt_write }
