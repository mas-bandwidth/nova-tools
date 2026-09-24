-- Card harvest for #2932 (spec #2756 3.2 row "ended(DONE) -> harvested",
-- 4.3 `card harvest`, 5.4 rows "branch push", "PR open" and "harvest worker").
-- No shebang: loader.go prepends the one library header. The file is one
-- do-block so its locals never add to the shared chunk's local count.
--
-- One harvest worker per bench holds lease:harvest:<b>; every write below
-- checks that lease, so two workers for one bench can never both harvest.
-- The PR idempotency key is pr:<repo>:<branch> in s:<S>:idem; a re-run reads
-- it back and never opens a second PR. The harvested transition requires the
-- PR head, read back by REST, to equal the card's pushed_sha.
do
  local function hv_now_ms()
    local t = redis.call('TIME')
    return string.format('%.0f', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
  end

  local function hv_hget(key, field)
    local v = redis.call('HGET', key, field)
    if not v then return '' end
    return tostring(v)
  end

  local function hv_branch(S, label, attempt)
    return 'nova/' .. S .. '/' .. label .. '-a' .. attempt
  end

  local function hv_lease_ok(bench, instance, token)
    local key = 'lease:harvest:' .. bench
    return instance ~= '' and token ~= ''
      and hv_hget(key, 'instance') == instance and hv_hget(key, 'token') == token
  end

  -- The durable harvest steps (#2932 rev 2): a card on its way to harvested
  -- moves forward only, absent -> pushed -> intent -> published -> harvested.
  -- pushed and intent are ns_harvest_step's; published is ns_harvest_pr's
  -- (with the verified reservation, same call); harvested is
  -- ns_card_harvested's (with the receipt).
  local hv_rank = { [''] = 0, pushed = 1, intent = 2, published = 3, harvested = 4 }

  -- ns_harvest_lease bench instance token ttl_ms
  -- TAKEN on a free lease, RENEWED for the holder, HELD|<instance> otherwise.
  redis.register_function('ns_harvest_lease', function(keys, args)
    local bench, instance, token, ttl = args[1] or '', args[2] or '', args[3] or '', tonumber(args[4] or '6000')
    if bench == '' or instance == '' or token == '' or not ttl or ttl <= 0 then
      return 'USAGE'
    end
    local key = 'lease:harvest:' .. bench
    local at = hv_now_ms()
    if redis.call('EXISTS', key) == 1 then
      if not hv_lease_ok(bench, instance, token) then
        return 'HELD|' .. hv_hget(key, 'instance')
      end
      redis.call('HSET', key, 'at', at)
      redis.call('PEXPIRE', key, ttl)
      return 'RENEWED'
    end
    redis.call('HSET', key, 'instance', instance, 'token', token, 'at', at)
    redis.call('PEXPIRE', key, ttl)
    return 'TAKEN'
  end)

  -- ns_harvest_pass bench instance token took_ms n err
  -- The worker's pass line (proc:harvest:<b>, written by that worker only)
  -- and the lease release, in one call. A fenced worker writes nothing.
  redis.register_function('ns_harvest_pass', function(keys, args)
    local bench, instance, token = args[1] or '', args[2] or '', args[3] or ''
    local took, n, err = args[4] or '0', args[5] or '0', args[6] or ''
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED'
    end
    local at = hv_now_ms()
    redis.call('HSET', 'proc:harvest:' .. bench, 'pass_at', at, 'took_ms', took, 'n', n, 'err', err, 'at', at)
    redis.call('DEL', 'lease:harvest:' .. bench)
    return 'OK'
  end)

  -- ns_harvest_due S bench limit (read only)
  -- The bench's ended(DONE) cards with a commit, oldest label order, as rows
  -- of 10: label repo base attempt pushed_sha identity results branch pr_idem
  -- harvest_step.
  -- A ci card (kind script) and a card with no pushed_sha have nothing to
  -- harvest; ok-to-friend classifies them (3.2). The bench's host and user
  -- from its beat come first so the worker needs no second read.
  redis.register_function{
    function_name = 'ns_harvest_due',
    flags = { 'no-writes' },
    callback = function(keys, args)
      local S, bench, limit = args[1] or '', args[2] or '', tonumber(args[3] or '256')
      if S == '' or bench == '' or not limit or limit <= 0 then
        return { 'USAGE' }
      end
      local bstate = redis.call('HGET', 'bench:' .. bench .. ':state', 'state')
      if not bstate or bstate == '' then
        bstate = 'down'
      else
        bstate = string.lower(bstate)
      end
      if bstate ~= 'up' then
        return { 'NONE', bstate }
      end
      local beat = 'bench:' .. bench .. ':beat'
      local out = { 'OK', hv_hget(beat, 'host'), hv_hget(beat, 'user') }
      local labels = redis.call('SINTER', 's:' .. S .. ':bench:' .. bench .. ':ended', 's:' .. S .. ':idx:card:ended')
      table.sort(labels)
      local rows = 0
      for _, label in ipairs(labels) do
        local key = 's:' .. S .. ':card:' .. label
        local f = redis.call('HMGET', key, 'state', 'outcome', 'kind', 'bench', 'repo', 'base',
          'attempt', 'pushed_sha', 'identity', 'results', 'harvest_step')
        local state, outcome, kind, cbench = f[1] or '', f[2] or '', f[3] or '', f[4] or ''
        local pushed = f[8] or ''
        -- pushed_sha '-' is a card that committed nothing (a read card, a
        -- probe, NO-COMMIT or OVERSIZE): it never belongs to harvest; its
        -- friend read is #3036's report rule (#2932 rev 4).
        if state == 'ended' and outcome == 'DONE' and kind ~= 'script' and cbench == bench
          and pushed ~= '' and pushed ~= '-' then
          if rows >= limit then break end
          local repo, attempt = f[5] or '', f[7] or ''
          local branch = hv_branch(S, label, attempt)
          out[#out + 1] = label
          out[#out + 1] = repo
          out[#out + 1] = f[6] or ''
          out[#out + 1] = attempt
          out[#out + 1] = pushed
          out[#out + 1] = f[9] or ''
          out[#out + 1] = f[10] or ''
          out[#out + 1] = branch
          out[#out + 1] = hv_hget('s:' .. S .. ':idem', 'pr:' .. repo .. ':' .. branch)
          out[#out + 1] = f[11] or ''
          rows = rows + 1
        end
      end
      return out
    end,
  }

  -- ns_harvest_step S bench instance token label step
  -- The lease holder's durable step before the PR exists: pushed (once
  -- ls-remote on the bench shows the branch tip == pushed_sha) and intent
  -- (right before the REST create POST). Forward only: the same step again is
  -- a no-op (OK, nothing written), a step behind the card's is BACKWARD, and
  -- published and harvested are REFUSED here (they have their own writers).
  redis.register_function('ns_harvest_step', function(keys, args)
    local S, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
    local label, step = args[5] or '', args[6] or ''
    if S == '' or bench == '' or label == '' or step == '' then
      return 'USAGE|'
    end
    if step ~= 'pushed' and step ~= 'intent' then
      return 'REFUSED|' .. step
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED|'
    end
    local key = 's:' .. S .. ':card:' .. label
    local f = redis.call('HMGET', key, 'state', 'outcome', 'bench', 'harvest_step')
    local state, outcome, cbench, cur = f[1] or '', f[2] or '', f[3] or '', f[4] or ''
    if state == '' then return 'NOTFOUND|' end
    if state ~= 'ended' or outcome ~= 'DONE' then return 'STATE|' .. state end
    if cbench ~= bench then return 'CONFLICT|' .. cbench end
    local have, want = hv_rank[cur] or 0, hv_rank[step]
    if want < have then return 'BACKWARD|' .. cur end
    if want == have then return 'OK|' .. cur end
    local at = hv_now_ms()
    if step == 'intent' then
      redis.call('HSET', key, 'harvest_step', step, 'harvest_step_at', at, 'harvest_intent_at', at)
    else
      redis.call('HSET', key, 'harvest_step', step, 'harvest_step_at', at)
    end
    return 'OK|' .. step
  end)

  -- ns_harvest_pr S bench instance token label repo branch pr
  -- The reservation comes after the PR (#2932 rule 1): the caller passes only
  -- a number GitHub returned whose head.ref and head.sha a REST response
  -- verified. HSETNX pr:<repo>:<branch>; when the key then names this PR the
  -- card moves to published in the same call. A key that already names
  -- another PR is returned unchanged and nothing is written (the caller
  -- reports it and opens nothing). A card with no harvest_step (never pushed)
  -- or one past published is refused (STEP), and a fenced caller writes
  -- nothing.
  redis.register_function('ns_harvest_pr', function(keys, args)
    local S, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
    local label, repo, branch, pr = args[5] or '', args[6] or '', args[7] or '', args[8] or ''
    if S == '' or label == '' or repo == '' or branch == '' or not string.match(pr, '^[1-9][0-9]*$') then
      return 'USAGE|'
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED|'
    end
    local key = 's:' .. S .. ':card:' .. label
    local f = redis.call('HMGET', key, 'state', 'bench', 'attempt', 'repo', 'harvest_step')
    local state, cbench, attempt, crepo, cur = f[1] or '', f[2] or '', f[3] or '', f[4] or '', f[5] or ''
    if state == '' then return 'NOTFOUND|' end
    if cbench ~= bench or crepo ~= repo or hv_branch(S, label, attempt) ~= branch then
      return 'CONFLICT|'
    end
    if cur ~= 'pushed' and cur ~= 'intent' and cur ~= 'published' then
      return 'STEP|' .. cur
    end
    local idem = 's:' .. S .. ':idem'
    local ikey = 'pr:' .. repo .. ':' .. branch
    redis.call('HSETNX', idem, ikey, pr)
    local stored = hv_hget(idem, ikey)
    if stored == pr and cur ~= 'published' then
      local at = hv_now_ms()
      redis.call('HSET', key, 'harvest_step', 'published', 'harvest_step_at', at)
    end
    return 'PR|' .. stored
  end)

  -- ns_card_harvested S label bench instance token pr head actor
  -- ended(DONE) -> harvested (3.2): the bench's lease holder only, the card
  -- ended DONE on this bench, the PR recorded under the card's idem key, and
  -- the head read back by REST equal to pushed_sha. A repeat with the same PR
  -- and head returns the stored receipt and writes nothing.
  redis.register_function('ns_card_harvested', function(keys, args)
    local S, label, bench, instance, token = args[1] or '', args[2] or '', args[3] or '', args[4] or '', args[5] or ''
    local pr, head, actor = args[6] or '', args[7] or '', args[8] or 'card-harvest'
    if S == '' or label == '' or bench == '' or pr == '' or head == '' then
      return 'USAGE|'
    end
    if not hv_lease_ok(bench, instance, token) then
      return 'FENCED|'
    end
    local key = 's:' .. S .. ':card:' .. label
    local state = hv_hget(key, 'state')
    if state == '' then return 'NOTFOUND|' end
    local attempt = hv_hget(key, 'attempt')
    local identity = hv_hget(key, 'identity')
    local idem_key = 's:' .. S .. ':idem'
    local idem = 'harvest:' .. identity
    if state == 'harvested' then
      if hv_hget(key, 'pr') == pr and hv_hget(key, 'head') == head then
        return 'OK|' .. hv_hget(idem_key, idem)
      end
      return 'CONFLICT|'
    end
    if state ~= 'ended' or hv_hget(key, 'outcome') ~= 'DONE' then return 'STATE|' end
    if hv_hget(key, 'bench') ~= bench then return 'CONFLICT|' end
    if head ~= hv_hget(key, 'pushed_sha') then return 'HEAD|' end
    local repo = hv_hget(key, 'repo')
    local branch = hv_branch(S, label, attempt)
    if hv_hget(idem_key, 'pr:' .. repo .. ':' .. branch) ~= pr then return 'IDEM|' end

    local at = hv_now_ms()
    local receipt = redis.call('XADD', 's:' .. S .. ':log', '*',
      'kind', 'card', 'id', label, 'from', 'ended', 'to', 'harvested',
      'attempt', attempt, 'token_sha', hv_hget(key, 'token_sha'),
      'actor', actor, 'reason', 'harvested',
      'evidence', 'pr=' .. pr .. ' head=' .. head .. ' branch=' .. branch,
      'idem', idem, 'at', at)
    redis.call('HSET', key, 'state', 'harvested', 'pr', pr, 'head', head,
      'harvested_at', at, 'harvest_receipt', receipt,
      'harvest_step', 'harvested', 'harvest_step_at', at)
    redis.call('SREM', 's:' .. S .. ':idx:card:ended', label)
    redis.call('SADD', 's:' .. S .. ':idx:card:harvested', label)
    redis.call('HSET', 's:' .. S .. ':prcard', repo .. '#' .. pr, label) -- pr-to-read skips card PRs (#3040)
    redis.call('HSET', idem_key, idem, receipt)
    return 'OK|' .. receipt
  end)
end
