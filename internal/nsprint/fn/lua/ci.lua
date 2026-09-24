-- CI passes are cards (#2756 section 10, nova-tools #2936). One function per
-- transition; each checks its guard, writes the ci card hash, the global
-- verdict record ci:<repo>:<sha> and one receipt in the sprint log in one
-- atomic call (spec 2.1 rule 2). No shebang: loader.go prepends the header.
--
-- The verdict record has two writers (10.3): ns_ci_cut and ns_ci_rerun set
-- verdict PENDING with the new attempt in the same call that cuts the card,
-- and ns_ci_end writes the terminal fields in the same call that ends the
-- card. ns_ci_dispose is the typed disposition on a FLAKY head (10.5 item 3).
--
-- Execution state and verdict are distinct (10.5 item 5): a script that ran
-- to its end ends DONE with verdict OK or FAIL; a wrapper failure ends FAILED
-- or BLOCKED and writes no verdict, so the head reads MISSING.
--
-- Tasks wait on a head (nova-tools #3382, the waiting-ci step of the closed
-- #3128 rebuilt here per ci card): a task parked on ci:<repo>:<sha> is state
-- waiting-ci in its sprint's idx:task:waiting-ci, on no open queue, with its
-- queue in `to` ('' is the ready pool), and <sprint>/<id> is a member of the
-- head's set ci:<repo>:<sha>:waiting. Its queue score is kept: `wait_score`
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

local CI_FRONT = -1000000000

local function ci_now_ms()
  local t = redis.call('TIME')
  return string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
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

local function ci_move(S, label, from_state, to_state)
  if from_state ~= '' then
    redis.call('SREM', 's:' .. S .. ':idx:card:' .. from_state, label)
  end
  redis.call('SADD', 's:' .. S .. ':idx:card:' .. to_state, label)
end

-- A leg list is the desired hash's `legs` field, space or comma separated.
-- A bench that declares no legs carries none: no evidence is not a leg.
local function ci_carries(bench, leg)
  local legs = ci_hget('bench:' .. bench .. ':desired', 'legs')
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

-- ns_ci_cut: cut ci-<pr>-<sha8> for one PR head (4.8, 10.2). The card waits
-- in pool at the front tier; the record goes PENDING with attempt 1 in this
-- same call. An existing card for the label is EXISTS and writes nothing.
local function ci_cut(keys, args)
  local S, label, repo, pr, head, base = args[1], args[2], args[3], args[4], args[5], args[6]
  local leg, paths, actor, idem = args[7], args[8], args[9], args[10]
  local card_key = 's:' .. S .. ':card:' .. label
  local rec_key = 'ci:' .. repo .. ':' .. head

  if ci_hget('s:' .. S, 'status') ~= 'open' then return { 'CLOSED' } end
  if redis.call('EXISTS', card_key) == 1 then return { 'EXISTS', ci_hget(card_key, 'attempt') } end

  local carried = false
  for _, b in ipairs(ci_sorted_benches()) do
    if ci_carries(b, leg) then carried = true break end
  end
  if not carried then return { 'RUNNER-ONLY', leg } end

  local at = ci_now_ms()
  local base8 = string.sub(base, 1, 8)
  redis.call('HSET', card_key,
    'kind', 'script', 'leg', leg, 'tier', 'front', 'repo', repo,
    'base', base, 'base_sha', base8, 'paths', paths, 'depends_on', 'none',
    'priority', tostring(CI_FRONT), 'state', 'queued', 'attempt', '1',
    'identity', '', 'token', '', 'token_sha', '', 'bench', '', 'avoid', '',
    'ci_for', repo .. ' ' .. pr .. ' ' .. head .. ' ' .. base,
    'ci_repo', repo, 'ci_pr', pr, 'ci_head', head,
    'cut_at', at, 'outcome', '', 'reason', '', 'reruns', '0', 'prev', '')
  redis.call('ZADD', 's:' .. S .. ':pool', CI_FRONT, label)
  ci_move(S, label, '', 'queued')
  redis.call('DEL', rec_key)
  redis.call('HSET', rec_key,
    'verdict', 'PENDING', 'attempt', '1', 'card', S .. '/' .. label,
    'head', head, 'base', base, 'pr', pr, 'repo', repo, 'tree', '',
    'pkg', '', 'test', '', 'wall_s', '', 'bench', '', 'log', '',
    'cut_at', at, 'end_at', '', 'flaky', '', 'source', 'card',
    'reruns', '0', 'rerun', '', 'why', '', 'at', at)
  ci_receipt(S, 'ci cut', label, '', 'queued', 1, '', actor, 'cut', rec_key, idem, at)
  return { 'CREATED', '1' }
end

-- ns_ci_end: the ci card's end record ends the card and writes the verdict
-- in one call (10.3, 10.5 item 5). Token first: a mismatch is FENCED and
-- writes nothing. A record naming another attempt resolves NOTHING (control
-- 26). DONE carries OK or FAIL; FAILED or BLOCKED carries no verdict.
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
  redis.call('ZREM', 'bench:' .. bench .. ':starting', member)
  redis.call('ZREM', 'bench:' .. bench .. ':living', member)
  redis.call('SADD', 's:' .. S .. ':bench:' .. bench .. ':ended', label)
  redis.call('HSET', card_key, 'state', 'ended', 'outcome', outcome, 'reason', reason,
    'ended_at', at, 'results', log, 'tree', tree)
  ci_move(S, label, state, 'ended')

  local rec_key = 'ci:' .. repo .. ':' .. head
  -- The key is the latest pointer: only the latest attempt of this card writes it.
  local owns = ci_hget(rec_key, 'card') == S .. '/' .. label and ci_hget(rec_key, 'attempt') == attempt
  local final = ''
  if owns then
    local prev = ci_hget(card_key, 'prev')
    local pbench, pverdict, ppkg, ptest = string.match(prev, '^([^|]*)|([^|]*)|([^|]*)|([^|]*)$')
    if verdict == '' then
      final = ''
      redis.call('HDEL', rec_key, 'verdict')
    elseif pverdict == 'FAIL' and verdict == 'OK' then
      final = 'FLAKY'
    elseif pverdict == 'FAIL' and verdict == 'FAIL' and (ppkg ~= pkg or ptest ~= test) then
      -- A different failure on the rerun is discordant (10.5 item 4).
      final = 'FLAKY'
    else
      final = verdict
    end
    local flaky = ''
    if final == 'FLAKY' then
      flaky = pbench .. ':FAIL,' .. bench .. ':' .. verdict
      local fpkg = ppkg
      if fpkg == '' then fpkg = pkg end
      redis.call('HSETNX', 's:' .. S .. ':unresolved', pr .. ':' .. head .. ':flaky:' .. fpkg,
        'flaky ' .. repo .. ' ' .. head .. ' ' .. flaky .. ' ' .. S .. '/' .. label)
    end
    local budget = tonumber(ci_hget('s:' .. S .. ':policy', 'ci_reruns')) or 1
    if final == 'FAIL' and (tonumber(ci_hget(card_key, 'reruns')) or 0) >= budget then
      -- The rerun budget is spent and the failure stands: one fix item for
      -- the author, deduplicated by <pr>:<head>:ci-fail:<pkg> (3.3). This
      -- is the item's one writer (#3382).
      redis.call('HSETNX', 's:' .. S .. ':unresolved', pr .. ':' .. head .. ':ci-fail:' .. pkg,
        'ci-fail ' .. repo .. ' ' .. head .. ' ' .. pkg .. ' ' .. test .. ' ' .. S .. '/' .. label)
    end
    if final ~= '' then
      redis.call('HSET', rec_key, 'verdict', final)
    end
    redis.call('HSET', rec_key, 'pkg', pkg, 'test', test, 'wall_s', wall_s, 'bench', bench,
      'log', log, 'tree', tree, 'end_at', at, 'flaky', flaky, 'why', (final == '' and reason or ''),
      'at', at)
    ci_settle_waiting(repo, head, final, pkg, actor, at)
  end
  local evidence = rec_key .. ' ' .. (verdict ~= '' and verdict or 'MISSING') ..
    ' bench=' .. bench .. ' pkg=' .. pkg .. ' test=' .. test .. ' wall_s=' .. wall_s ..
    ' log=' .. log
  ci_receipt(S, 'ci end', label, state, 'ended', attempt, ci_hget(card_key, 'token_sha'), actor,
    outcome .. ' ' .. reason, evidence, 'ci-end:' .. identity, at)
  return { 'ENDED', attempt, final }
end

-- ns_ci_rerun: exactly one rerun per head, on another healthy bench (10.5
-- item 2). The reconciler calls it from policy; a hand call carries a reason.
-- The rerun is a new attempt of the same label pinned to the other bench, and
-- the record goes PENDING in this same call, so the earlier verdict can never
-- land while the rerun is pending. No alternate bench is a visible blocked
-- state on the record and the card, never a silent wait.
local function ci_rerun(keys, args)
  local S, label, actor, reason, idem = args[1], args[2], args[3], args[4], args[5]
  local card_key = 's:' .. S .. ':card:' .. label
  local state = ci_hget(card_key, 'state')
  if state == '' then return { 'NOTFOUND' } end
  if ci_hget(card_key, 'kind') ~= 'script' or ci_hget(card_key, 'ci_head') == '' then return { 'NOTCI' } end
  local repo, head = ci_hget(card_key, 'ci_repo'), ci_hget(card_key, 'ci_head')
  local rec_key = 'ci:' .. repo .. ':' .. head
  local attempt = tonumber(ci_hget(card_key, 'attempt'))
  if state ~= 'ended' then return { 'STATE', tostring(attempt) } end
  if ci_hget(rec_key, 'card') ~= S .. '/' .. label or ci_hget(rec_key, 'attempt') ~= tostring(attempt) then
    return { 'STATE', tostring(attempt) }
  end
  local verdict = ci_hget(rec_key, 'verdict')
  if verdict ~= 'FAIL' and verdict ~= '' then return { 'STATE', tostring(attempt) } end

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
    redis.call('HSET', rec_key, 'rerun', 'blocked: no alternate bench', 'at', at)
    redis.call('HSET', card_key, 'blocked', 'no alternate bench')
    return { 'BLOCKED', tostring(attempt) }
  end

  local next_attempt = attempt + 1
  local prev = from_bench .. '|' .. (verdict == '' and 'MISSING' or verdict) .. '|' ..
    ci_hget(rec_key, 'pkg') .. '|' .. ci_hget(rec_key, 'test')
  redis.call('HSET', card_key, 'state', 'queued', 'attempt', tostring(next_attempt),
    'identity', '', 'token', '', 'token_sha', '', 'bench', to_bench, 'avoid', from_bench,
    'outcome', '', 'reason', '', 'reruns', tostring(reruns + 1), 'prev', prev, 'blocked', '')
  redis.call('ZADD', 's:' .. S .. ':pool', CI_FRONT, label)
  redis.call('ZADD', 's:' .. S .. ':bench:' .. to_bench .. ':queue', CI_FRONT, label)
  ci_move(S, label, 'ended', 'queued')
  redis.call('HSET', rec_key, 'verdict', 'PENDING', 'attempt', tostring(next_attempt),
    'reruns', tostring(reruns + 1), 'rerun', 'on ' .. to_bench, 'at', at)
  ci_receipt(S, 'ci rerun', label, 'ended', 'queued', next_attempt, '', actor, reason,
    rec_key .. ' ' .. from_bench .. '->' .. to_bench, idem, at)
  return { 'RERUN', tostring(next_attempt), to_bench }
end

-- ns_ci_dispose: the typed disposition on a FLAKY head (10.5 item 3). APPROVE
-- names a known flaky test and turns the verdict OK with the disposition url
-- in log; HOLD leaves FLAKY and cuts one fix item. Either closes the item.
local function ci_dispose(keys, args)
  local S, repo, head, disposition, friend, url = args[1], args[2], args[3], args[4], args[5], args[6]
  local rec_key = 'ci:' .. repo .. ':' .. head
  if redis.call('EXISTS', rec_key) == 0 then return { 'MISSING' } end
  if ci_hget(rec_key, 'verdict') ~= 'FLAKY' then return { 'STATE' } end
  local card = ci_hget(rec_key, 'card')
  if string.sub(card, 1, #S + 1) ~= S .. '/' then return { 'STATE' } end
  local label = string.sub(card, #S + 2)
  local pr = ci_hget(rec_key, 'pr')
  local unresolved = 's:' .. S .. ':unresolved'
  local item = ''
  local fields = redis.call('HKEYS', unresolved)
  local prefix = pr .. ':' .. head .. ':flaky:'
  for _, f in ipairs(fields) do
    if string.sub(f, 1, #prefix) == prefix then item = f break end
  end
  if item == '' then return { 'STATE' } end
  local at = ci_now_ms()
  local pkg = string.sub(item, #prefix + 1)
  if disposition == 'APPROVE' then
    redis.call('HSET', rec_key, 'verdict', 'OK', 'log', url, 'disposition', 'APPROVE ' .. friend .. ' ' .. url, 'at', at)
  elseif disposition == 'HOLD' then
    redis.call('HSET', rec_key, 'disposition', 'HOLD ' .. friend .. ' ' .. url, 'at', at)
    -- Its own key: ns_ci_end is the one writer of the ci-fail item (#3382).
    redis.call('HSETNX', unresolved, pr .. ':' .. head .. ':flaky-hold:' .. pkg,
      'flaky-hold ' .. repo .. ' ' .. head .. ' ' .. pkg .. ' ' .. card)
  else
    return { 'RECORD' }
  end
  redis.call('HDEL', unresolved, item)
  ci_receipt(S, 'ci dispose', label, 'FLAKY', disposition == 'APPROVE' and 'OK' or 'FLAKY', ci_hget(rec_key, 'attempt'),
    '', friend, disposition, url, 'ci-dispose:' .. item, at)
  return { disposition }
end

redis.register_function('ns_ci_cut', ci_cut)
redis.register_function('ns_ci_end', ci_end)
redis.register_function('ns_ci_rerun', ci_rerun)
redis.register_function('ns_ci_dispose', ci_dispose)
